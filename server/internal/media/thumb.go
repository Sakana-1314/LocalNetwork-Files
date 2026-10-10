package media

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/disintegration/imaging"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/cache"
	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/vfs"
)

var ErrNoThumb = errors.New("无法生成预览图")

// ThumbSizes are the supported bounding box sizes.
var ThumbSizes = []int{128, 256, 512, 1024}

func NormalizeThumbSize(n int) int {
	for _, s := range ThumbSizes {
		if n <= s {
			return s
		}
	}
	return ThumbSizes[len(ThumbSizes)-1]
}

func cacheKey(parts ...any) string {
	h := sha1.New()
	for _, p := range parts {
		fmt.Fprint(h, p, "\x00")
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Thumbnail returns the path of a cached JPEG preview, generating it when needed.
func (s *Service) Thumbnail(ctx context.Context, real string, fi os.FileInfo, size int) (string, error) {
	size = NormalizeThumbSize(size)
	name := fi.Name()
	if !vfs.Thumbable(name, fi.IsDir(), s.hasFFmpeg) {
		return "", ErrNoThumb
	}
	key := cacheKey("thumb", real, fi.ModTime().UnixNano(), fi.Size(), size)
	out := s.cache.ThumbPath(key, ".jpg")
	failMark := out + ".fail"
	if _, err := os.Stat(out); err == nil {
		cache.Touch(out)
		return out, nil
	}
	if _, err := os.Stat(failMark); err == nil {
		return "", ErrNoThumb
	}
	_, err, _ := s.sf.Do("thumb:"+key, func() (any, error) {
		// Detached from the first caller: concurrent requests share this work.
		gctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		defer cancel()
		select {
		case s.thumbSem <- struct{}{}:
		case <-gctx.Done():
			return nil, gctx.Err()
		}
		defer func() { <-s.thumbSem }()
		if _, err := os.Stat(out); err == nil {
			return nil, nil // produced by a previous flight while we queued
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return nil, err
		}
		err := s.generateThumb(gctx, real, fi, size, out)
		if err == nil {
			if st, e := os.Stat(out); e == nil {
				s.cache.Added(cache.Thumb, st.Size())
			}
		} else if errors.Is(err, ErrNoThumb) && gctx.Err() == nil {
			// Remember permanent failures so we don't retry on every request.
			// Timeouts/cancellations are transient and not remembered.
			os.WriteFile(failMark, nil, 0o644)
		}
		return nil, err
	})
	if err != nil {
		if errors.Is(err, ErrNoThumb) {
			return "", ErrNoThumb
		}
		return "", err
	}
	return out, nil
}

func (s *Service) generateThumb(ctx context.Context, real string, fi os.FileInfo, size int, out string) error {
	switch vfs.KindOf(fi.Name(), false) {
	case vfs.KindImage:
		switch vfs.Ext(fi.Name()) {
		case "jpg", "jpeg", "jfif", "png", "gif", "webp", "bmp", "tif", "tiff":
			if err := imageThumb(real, size, out); err == nil {
				return nil
			}
		}
		err := ErrNoThumb
		if s.hasFFmpeg {
			if err = s.ffmpegThumb(ctx, real, 0, size, out, false); err == nil {
				return nil
			}
		}
		switch vfs.Ext(fi.Name()) {
		case "heic", "heif", "avif":
			// ffmpeg < 7 cannot decode HEIF (e.g. Ubuntu 24.04 ships 6.1); try dedicated tools.
			if herr := heifThumb(ctx, real, size, out); herr == nil {
				return nil
			}
		}
		return err
	case vfs.KindVideo:
		mi, err := s.Probe(ctx, real, fi.ModTime(), fi.Size())
		if err != nil || mi.Video == nil {
			return ErrNoThumb
		}
		at := 3.0
		if mi.Duration > 0 {
			at = mi.Duration * 0.1
			if at > 120 {
				at = 120
			}
		}
		if err := s.ffmpegThumb(ctx, real, at, size, out, false); err != nil {
			return s.ffmpegThumb(ctx, real, 0, size, out, false)
		}
		return nil
	case vfs.KindAudio:
		mi, err := s.Probe(ctx, real, fi.ModTime(), fi.Size())
		if err != nil || !mi.HasCover {
			return ErrNoThumb
		}
		return s.ffmpegThumb(ctx, real, 0, size, out, true)
	}
	return ErrNoThumb
}

// maxDecodePixels bounds the memory used to decode one image in-process
// (~4 bytes/pixel); larger images go through ffmpeg.
const maxDecodePixels = 64 << 20

func imageThumb(real string, size int, out string) error {
	f, err := os.Open(real)
	if err != nil {
		return err
	}
	cfg, _, err := image.DecodeConfig(f)
	f.Close()
	if err != nil {
		return err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxDecodePixels {
		return fmt.Errorf("image too large to decode: %dx%d", cfg.Width, cfg.Height)
	}
	img, err := imaging.Open(real, imaging.AutoOrientation(true))
	if err != nil {
		return err
	}
	img = imaging.Fit(img, size, size, imaging.Lanczos)
	// Flatten transparency onto white; JPEG has no alpha channel.
	bg := image.NewRGBA(img.Bounds())
	draw.Draw(bg, bg.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(bg, bg.Bounds(), img, img.Bounds().Min, draw.Over)
	return writeAtomic(out, func(f *os.File) error { return jpeg.Encode(f, bg, &jpeg.Options{Quality: 82}) })
}

func (s *Service) ffmpegThumb(ctx context.Context, real string, at float64, size int, out string, cover bool) error {
	tmp := out + ".tmp.jpg"
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}
	if at > 0 {
		args = append(args, "-ss", strconv.FormatFloat(at, 'f', 2, 64))
	}
	args = append(args, "-i", real)
	if cover {
		args = append(args, "-an", "-map", "0:v:0")
	}
	sz := strconv.Itoa(size)
	args = append(args, "-frames:v", "1",
		"-vf", "scale="+sz+":"+sz+":force_original_aspect_ratio=decrease",
		"-q:v", "4", "-f", "image2", "-c:v", "mjpeg", tmp)
	cmd := exec.CommandContext(ctx, s.ffmpeg, args...)
	if b, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("%w: %s", ErrNoThumb, lastLine(b))
	}
	if fi, err := os.Stat(tmp); err != nil || fi.Size() == 0 {
		os.Remove(tmp)
		return ErrNoThumb
	}
	return os.Rename(tmp, out)
}

func writeAtomic(out string, fn func(*os.File) error) error {
	f, err := os.CreateTemp(filepath.Dir(out), ".tmp-*")
	if err != nil {
		return err
	}
	if err := fn(f); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return err
	}
	return os.Rename(f.Name(), out)
}

func lastLine(b []byte) string {
	s := string(b)
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\n' {
			return s[i+1:]
		}
	}
	return s
}

// heifThumb converts HEIF/AVIF images with whichever external tool is
// installed (libheif's heif-convert, libvips' vipsthumbnail, or macOS sips)
// and then thumbnails the intermediate JPEG.
func heifThumb(ctx context.Context, real string, size int, out string) error {
	dir, err := os.MkdirTemp(filepath.Dir(out), ".heif-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	tmp := filepath.Join(dir, "img.jpg")
	sz := strconv.Itoa(size)
	tools := [][]string{
		{"vipsthumbnail", real, "-s", sz, "-o", tmp + "[Q=90]"},
		{"heif-convert", "-q", "90", real, tmp},
		{"sips", "-s", "format", "jpeg", "-Z", sz, real, "--out", tmp},
	}
	for _, args := range tools {
		if _, err := exec.LookPath(args[0]); err != nil {
			continue
		}
		if exec.CommandContext(ctx, args[0], args[1:]...).Run() != nil {
			continue
		}
		if _, err := os.Stat(tmp); err == nil {
			return imageThumb(tmp, size, out)
		}
	}
	return ErrNoThumb
}
