package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/cache"
	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/vfs"
)

var ErrUnsupportedSubtitle = errors.New("该字幕为图形字幕，无法转换为 WebVTT")

// StreamAudio transcodes the first audio stream to MP3 and writes it to w until
// the file ends or ctx is cancelled (client disconnected).
func (s *Service) StreamAudio(ctx context.Context, real string, start float64, w io.Writer) error {
	if !s.hasFFmpeg {
		return ErrNoFFmpeg
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	if start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(start, 'f', 3, 64))
	}
	args = append(args, "-i", real, "-vn", "-map", "0:a:0", "-c:a", "libmp3lame", "-b:a", "320k",
		"-ar", "44100", "-f", "mp3", "pipe:1")
	cmd := exec.CommandContext(ctx, s.ffmpeg, args...)
	cmd.Stdout = w
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

var subtitleExts = map[string]bool{"srt": true, "ass": true, "ssa": true, "vtt": true}

// SidecarSubtitles lists subtitle files next to a video that share its base name,
// e.g. movie.mkv -> movie.srt, movie.zh-CN.ass. Returns file names.
func SidecarSubtitles(real string) []string {
	dir := filepath.Dir(real)
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(real), filepath.Ext(real)))
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, de := range des {
		name := de.Name()
		if de.IsDir() || !subtitleExts[vfs.Ext(name)] {
			continue
		}
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, base+".") {
			out = append(out, name)
		}
	}
	return out
}

func IsSubtitleFile(name string) bool { return subtitleExts[vfs.Ext(name)] }

// EmbeddedSubtitle extracts embedded subtitle stream #index as WebVTT (cached).
func (s *Service) EmbeddedSubtitle(ctx context.Context, real string, fi os.FileInfo, index int) (string, error) {
	mi, err := s.Probe(ctx, real, fi.ModTime(), fi.Size())
	if err != nil {
		return "", err
	}
	if index < 0 || index >= len(mi.Subtitles) {
		return "", fmt.Errorf("字幕轨道 %d 不存在", index)
	}
	switch mi.Subtitles[index].Codec {
	case "hdmv_pgs_subtitle", "dvd_subtitle", "dvb_subtitle", "xsub":
		return "", ErrUnsupportedSubtitle
	}
	key := cacheKey("sub", real, fi.ModTime().UnixNano(), fi.Size(), index)
	return s.convertSubtitle(ctx, key, []string{"-i", real, "-map", "0:s:" + strconv.Itoa(index)})
}

// ExternalSubtitle converts a subtitle file to WebVTT (cached). Non UTF-8 input is
// decoded as GB18030, the most common legacy encoding for Chinese subtitles.
func (s *Service) ExternalSubtitle(ctx context.Context, real string, fi os.FileInfo) (string, error) {
	if vfs.Ext(fi.Name()) == "vtt" {
		return real, nil
	}
	head := make([]byte, 64<<10)
	f, err := os.Open(real)
	if err != nil {
		return "", err
	}
	n, _ := io.ReadFull(f, head)
	f.Close()
	head = head[:n]
	// Avoid flagging a multi-byte rune cut at the buffer end as invalid.
	for i := 0; i < 3 && len(head) > 0 && !utf8.Valid(head); i++ {
		head = head[:len(head)-1]
	}
	args := []string{}
	if !utf8.Valid(head) {
		args = append(args, "-sub_charenc", "GB18030")
	}
	args = append(args, "-i", real)
	key := cacheKey("extsub", real, fi.ModTime().UnixNano(), fi.Size())
	return s.convertSubtitle(ctx, key, args)
}

func (s *Service) convertSubtitle(ctx context.Context, key string, input []string) (string, error) {
	if !s.hasFFmpeg {
		return "", ErrNoFFmpeg
	}
	out := s.cache.ThumbPath(key, ".vtt")
	if _, err := os.Stat(out); err == nil {
		cache.Touch(out)
		return out, nil
	}
	_, err, _ := s.sf.Do("sub:"+key, func() (any, error) {
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return nil, err
		}
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		tmp := out + ".tmp.vtt"
		args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}, input...)
		args = append(args, "-f", "webvtt", tmp)
		if b, err := exec.CommandContext(cctx, s.ffmpeg, args...).CombinedOutput(); err != nil {
			os.Remove(tmp)
			return nil, fmt.Errorf("字幕转换失败: %s", lastLine(b))
		}
		return nil, os.Rename(tmp, out)
	})
	if err != nil {
		return "", err
	}
	return out, nil
}
