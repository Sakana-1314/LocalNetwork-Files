package media

import (
	"bytes"
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

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"

	"github.com/Sakana-1314/MList/server/internal/cache"
	"github.com/Sakana-1314/MList/server/internal/vfs"
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
	case "hdmv_pgs_subtitle", "dvd_subtitle", "dvb_subtitle", "xsub", "dvb_teletext", "arib_caption":
		return "", ErrUnsupportedSubtitle
	}
	fp, err := s.Fingerprint(real, fi.Size(), fi.ModTime())
	if err != nil {
		return "", err
	}
	key := cacheKey("sub", fp, index)
	return s.convertSubtitle(ctx, key, func(string) ([]string, error) {
		return []string{"-i", real, "-map", "0:s:" + strconv.Itoa(index)}, nil
	})
}

// ExternalSubtitle converts a subtitle file to WebVTT (cached). UTF-16 (with
// BOM) is converted in-process; other non UTF-8 input is decoded as GB18030,
// the most common legacy encoding for Chinese subtitles (a superset of GBK).
func (s *Service) ExternalSubtitle(ctx context.Context, real string, fi os.FileInfo) (string, error) {
	if vfs.Ext(fi.Name()) == "vtt" {
		return real, nil
	}
	fp, err := s.Fingerprint(real, fi.Size(), fi.ModTime())
	if err != nil {
		return "", err
	}
	key := cacheKey("extsub", fp)
	return s.convertSubtitle(ctx, key, func(tmpBase string) ([]string, error) {
		f, err := os.Open(real)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(f, maxSubtitleBytes+1))
		f.Close()
		if err != nil {
			return nil, err
		}
		if len(data) > maxSubtitleBytes {
			return nil, errors.New("字幕文件过大")
		}
		conv, changed, err := subtitleToUTF8(data)
		if err != nil {
			return nil, fmt.Errorf("字幕编码转换失败: %w", err)
		}
		if !changed {
			return []string{"-i", real}, nil
		}
		src := tmpBase + ".src." + vfs.Ext(fi.Name())
		if err := os.WriteFile(src, conv, 0o644); err != nil {
			return nil, err
		}
		return []string{"-i", src}, nil
	})
}

const maxSubtitleBytes = 32 << 20

// subtitleToUTF8 returns data as UTF-8 and whether it had to be converted.
func subtitleToUTF8(data []byte) ([]byte, bool, error) {
	var dec *encoding.Decoder
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
		dec = unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM).NewDecoder()
	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		dec = unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM).NewDecoder()
	case utf8.Valid(data):
		return data, false, nil
	default:
		dec = simplifiedchinese.GB18030.NewDecoder()
	}
	out, err := dec.Bytes(data)
	return out, true, err
}

// convertSubtitle converts to WebVTT once per key. input returns the ffmpeg
// input arguments; it may create helper files named tmpBase+"*", which are
// removed afterwards.
func (s *Service) convertSubtitle(ctx context.Context, key string, input func(tmpBase string) ([]string, error)) (string, error) {
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
		tmpBase := out + ".tmp"
		defer func() {
			olds, _ := filepath.Glob(tmpBase + ".src.*")
			for _, o := range olds {
				os.Remove(o)
			}
		}()
		in, err := input(tmpBase)
		if err != nil {
			return nil, err
		}
		args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}, in...)
		args = append(args, "-f", "webvtt", tmp)
		if b, err := exec.CommandContext(cctx, s.ffmpeg, args...).CombinedOutput(); err != nil {
			os.Remove(tmp)
			return nil, fmt.Errorf("字幕转换失败: %s", lastLine(b))
		}
		if err := os.Rename(tmp, out); err != nil {
			return nil, err
		}
		if st, err := os.Stat(out); err == nil {
			s.cache.Added(cache.Thumb, st.Size())
		}
		return nil, nil
	})
	if err != nil {
		return "", err
	}
	return out, nil
}
