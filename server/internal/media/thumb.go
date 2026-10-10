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
		select {
		case s.thumbSem <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer func() { <-s.thumbSem }()
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return nil, err
		}
		gctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		defer cancel()
		err := s.generateThumb(gctx, real, fi, size, out)
		if errors.Is(err, ErrNoThumb) {
			// Remember permanent failures so we don't retry on every request.
			os.WriteFile(failMark, nil, 0o644)
		}
		return nil, err
	})
	if err != nil {
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
		if !s.hasFFmpeg {
			return ErrNoThumb
		}
		return s.ffmpegThumb(ctx, real, 0, size, out, false)
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

func imageThumb(real string, size int, out string) error {
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
