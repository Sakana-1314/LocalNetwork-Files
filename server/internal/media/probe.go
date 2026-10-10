// Package media implements preview images, media probing, real-time HLS
// transcoding, audio transcoding and subtitle conversion on top of ffmpeg.
package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/cache"
	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/store"
)

var ErrNoFFmpeg = errors.New("服务器未安装 ffmpeg")

type Service struct {
	ffmpeg, ffprobe string
	hasFFmpeg       bool
	cache           *cache.Manager
	store           *store.Store

	sf       singleflight.Group
	thumbSem chan struct{}

	probeMu    sync.Mutex
	probeCache map[string]*MediaInfo
	probeOrder []string

	hls *hlsManager
}

func New(ffmpegBin, ffprobeBin string, c *cache.Manager, st *store.Store) *Service {
	s := &Service{cache: c, store: st, thumbSem: make(chan struct{}, 4), probeCache: map[string]*MediaInfo{}}
	if p, err := exec.LookPath(ffmpegBin); err == nil {
		s.ffmpeg = p
	}
	if p, err := exec.LookPath(ffprobeBin); err == nil {
		s.ffprobe = p
	}
	s.hasFFmpeg = s.ffmpeg != "" && s.ffprobe != ""
	s.hls = newHLSManager(s)
	c.IsActive = s.hls.isActive
	c.OnEvict = s.hls.forget
	return s
}

func (s *Service) HasFFmpeg() bool { return s.hasFFmpeg }

// Close stops all running transcoders.
func (s *Service) Close() { s.hls.closeAll() }

type VideoStream struct {
	Codec  string  `json:"codec"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
	FPS    float64 `json:"fps"`

	index   int    // absolute stream index (for -map 0:<index>)
	pixFmt  string // e.g. yuv420p, yuv420p10le
	profile string // e.g. High, High 10, Main 10
}

type AudioStream struct {
	Index    int    `json:"index"`
	Codec    string `json:"codec"`
	Channels int    `json:"channels"`
	Language string `json:"language"`
	Title    string `json:"title"`

	startTime float64 // stream start (source timeline, seconds)
}

type SubtitleStream struct {
	Index    int    `json:"index"`
	Codec    string `json:"codec"`
	Language string `json:"language"`
	Title    string `json:"title"`
	External string `json:"external,omitempty"`
}

type MediaInfo struct {
	Duration   float64          `json:"duration"`
	Format     string           `json:"format"`
	BitRate    int64            `json:"bit_rate"`
	DirectPlay bool             `json:"direct_play"`
	Video      *VideoStream     `json:"video"`
	Audio      []AudioStream    `json:"audio"`
	Subtitles  []SubtitleStream `json:"subtitles"`
	// HasCover is true for audio files with embedded artwork (attached_pic).
	HasCover bool `json:"-"`

	startTime float64 // container start time (source timeline, seconds)
}

type ffprobeOut struct {
	Format struct {
		FormatName string `json:"format_name"`
		StartTime  string `json:"start_time"`
		Duration   string `json:"duration"`
		BitRate    string `json:"bit_rate"`
	} `json:"format"`
	Streams []struct {
		Index        int               `json:"index"`
		CodecType    string            `json:"codec_type"`
		Profile      string            `json:"profile"`
		PixFmt       string            `json:"pix_fmt"`
		Duration     string            `json:"duration"`
		StartTime    string            `json:"start_time"`
		CodecName    string            `json:"codec_name"`
		Width        int               `json:"width"`
		Height       int               `json:"height"`
		Channels     int               `json:"channels"`
		AvgFrameRate string            `json:"avg_frame_rate"`
		Tags         map[string]string `json:"tags"`
		Disposition  map[string]int    `json:"disposition"`
	} `json:"streams"`
}

// Probe runs ffprobe (cached by path+mtime+size).
func (s *Service) Probe(ctx context.Context, real string, mtime time.Time, size int64) (*MediaInfo, error) {
	if !s.hasFFmpeg {
		return nil, ErrNoFFmpeg
	}
	key := fmt.Sprintf("%s|%d|%d", real, mtime.UnixNano(), size)
	s.probeMu.Lock()
	if mi, ok := s.probeCache[key]; ok {
		s.probeMu.Unlock()
		return mi, nil
	}
	s.probeMu.Unlock()

	v, err, _ := s.sf.Do("probe:"+key, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, s.ffprobe, "-v", "error", "-print_format", "json",
			"-show_format", "-show_streams", "--", real).Output()
		if err != nil {
			return nil, fmt.Errorf("ffprobe: %w", err)
		}
		var po ffprobeOut
		if err := json.Unmarshal(out, &po); err != nil {
			return nil, err
		}
		mi := &MediaInfo{Format: po.Format.FormatName, Audio: []AudioStream{}, Subtitles: []SubtitleStream{}}
		mi.Duration, _ = strconv.ParseFloat(po.Format.Duration, 64)
		mi.startTime, _ = strconv.ParseFloat(po.Format.StartTime, 64)
		mi.BitRate, _ = strconv.ParseInt(po.Format.BitRate, 10, 64)
		for _, st := range po.Streams {
			switch st.CodecType {
			case "video":
				if st.Disposition["attached_pic"] == 1 {
					mi.HasCover = true
					continue
				}
				if mi.Video == nil {
					mi.Video = &VideoStream{Codec: st.CodecName, Width: st.Width, Height: st.Height, FPS: parseRate(st.AvgFrameRate),
						index: st.Index, pixFmt: st.PixFmt, profile: st.Profile}
				}
			case "audio":
				ast, err := strconv.ParseFloat(st.StartTime, 64)
				if err != nil {
					ast = math.NaN()
				}
				mi.Audio = append(mi.Audio, AudioStream{Index: len(mi.Audio), Codec: st.CodecName, Channels: st.Channels,
					Language: st.Tags["language"], Title: st.Tags["title"], startTime: ast})
			case "subtitle":
				mi.Subtitles = append(mi.Subtitles, SubtitleStream{Index: len(mi.Subtitles), Codec: st.CodecName,
					Language: st.Tags["language"], Title: st.Tags["title"]})
			}
		}
		if mi.Duration <= 0 {
			// Some containers (raw streams, broken indexes) only report per-stream durations.
			for _, st := range po.Streams {
				if d, err := strconv.ParseFloat(st.Duration, 64); err == nil && d > mi.Duration {
					mi.Duration = d
				}
			}
		}
		mi.DirectPlay = directPlayable(mi)
		return mi, nil
	})
	if err != nil {
		return nil, err
	}
	mi := v.(*MediaInfo)
	s.probeMu.Lock()
	if _, ok := s.probeCache[key]; !ok {
		s.probeCache[key] = mi
		s.probeOrder = append(s.probeOrder, key)
		if len(s.probeOrder) > 512 {
			delete(s.probeCache, s.probeOrder[0])
			s.probeOrder = s.probeOrder[1:]
		}
	}
	s.probeMu.Unlock()
	return mi, nil
}

func parseRate(r string) float64 {
	a, b, ok := strings.Cut(r, "/")
	if !ok {
		f, _ := strconv.ParseFloat(r, 64)
		return f
	}
	n, _ := strconv.ParseFloat(a, 64)
	d, _ := strconv.ParseFloat(b, 64)
	if d == 0 {
		return 0
	}
	return float64(int(n/d*100+0.5)) / 100
}

// directPlayable approximates what mainstream browsers (Chrome, Firefox,
// Safari) all decode natively through a plain <video>/<audio> element.
func directPlayable(mi *MediaInfo) bool {
	var mp4, matroska, other bool
	for _, f := range strings.Split(mi.Format, ",") {
		switch f {
		case "mp4", "mov", "m4a":
			mp4 = true
		case "matroska", "webm":
			// ffprobe reports "matroska,webm" for both .mkv and .webm; only
			// WebM-legal codecs (checked below) make it playable.
			matroska = true
		case "mp3", "ogg", "flac", "wav", "aac":
			other = true
		}
	}
	if !mp4 && !matroska && !other {
		return false
	}
	if mi.Video != nil {
		switch mi.Video.Codec {
		case "h264":
			// 10-bit / 4:2:2 / 4:4:4 H.264 is not decodable by browsers.
			if matroska || !browserPixFmt(mi.Video.pixFmt) || strings.Contains(mi.Video.profile, "10") ||
				strings.Contains(mi.Video.profile, "4:4") {
				return false
			}
		case "vp8", "vp9", "av1":
			if mi.Video.codecPixFmtTooDeep() {
				return false
			}
		default:
			return false
		}
	}
	if len(mi.Audio) > 0 {
		switch mi.Audio[0].Codec {
		case "aac", "mp3":
			if matroska {
				return false
			}
		case "opus", "vorbis":
			// Safari plays Opus/Vorbis in WebM/Ogg but not in MP4; treat as playable.
		case "flac", "pcm_s16le", "pcm_f32le":
			if matroska {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func browserPixFmt(f string) bool { return f == "" || f == "yuv420p" || f == "yuvj420p" }

// codecPixFmtTooDeep reports pixel formats browsers won't decode for VP9/AV1 (4:4:4, 12-bit).
func (v *VideoStream) codecPixFmtTooDeep() bool {
	return strings.Contains(v.pixFmt, "444") || strings.Contains(v.pixFmt, "12")
}
