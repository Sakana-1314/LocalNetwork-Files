package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

// 40x30 lossless WebP: left half red, right half blue (no WebP encoder in Go/ffmpeg here).
const webpB64 = "UklGRigAAABXRUJQVlA4TBsAAAAvJ0AHAA8Q87//8x8OhASEB/6v9UCO6H+UMAQA"

func halfImage(w, h int, left, right color.Color) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x < w/2 {
				img.Set(x, y, left)
			} else {
				img.Set(x, y, right)
			}
		}
	}
	return img
}

func decodeJPEG(t *testing.T, p string) image.Image {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatalf("thumbnail %s is not a JPEG: %v", p, err)
	}
	return img
}

func near(c color.Color, r, g, b uint8) bool {
	cr, cg, cb, _ := c.RGBA()
	d := func(a uint32, b uint8) bool { x := int(a>>8) - int(b); return x > -40 && x < 40 }
	return d(cr, r) && d(cg, g) && d(cb, b)
}

func TestThumbnailImages(t *testing.T) {
	env := newEnv(t, nil)
	dir := t.TempDir()
	red, blue := color.NRGBA{255, 0, 0, 255}, color.NRGBA{0, 0, 255, 255}
	src := halfImage(800, 400, red, blue)

	write := func(name string, enc func(*os.File) error) string {
		p := filepath.Join(dir, name)
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := enc(f); err != nil {
			t.Fatal(err)
		}
		f.Close()
		return p
	}
	files := map[string]string{
		"a.jpg":  write("a.jpg", func(f *os.File) error { return jpeg.Encode(f, src, nil) }),
		"b.png":  write("b.png", func(f *os.File) error { return png.Encode(f, src) }),
		"c.gif":  write("c.gif", func(f *os.File) error { return gif.Encode(f, src, nil) }),
		"d.bmp":  write("d.bmp", func(f *os.File) error { return bmp.Encode(f, src) }),
		"e.tiff": write("e.tiff", func(f *os.File) error { return tiff.Encode(f, src, nil) }),
	}
	wb, _ := base64.StdEncoding.DecodeString(webpB64)
	files["f.webp"] = filepath.Join(dir, "f.webp")
	os.WriteFile(files["f.webp"], wb, 0o644)

	for name, p := range files {
		t.Run(name, func(t *testing.T) {
			out, err := env.svc.Thumbnail(context.Background(), p, stat(t, p), 256)
			if err != nil {
				t.Fatal(err)
			}
			img := decodeJPEG(t, out)
			b := img.Bounds()
			if name == "f.webp" {
				if b.Dx() != 40 || b.Dy() != 30 { // never upscaled
					t.Fatalf("webp thumb %v", b)
				}
			} else if b.Dx() != 256 || b.Dy() != 128 {
				t.Fatalf("thumb size %v, want 256x128", b)
			}
			if !near(img.At(2, b.Dy()/2), 255, 0, 0) || !near(img.At(b.Dx()-3, b.Dy()/2), 0, 0, 255) {
				t.Fatalf("unexpected colors %v %v", img.At(2, b.Dy()/2), img.At(b.Dx()-3, b.Dy()/2))
			}
		})
	}
}

func TestThumbnailPNGAlphaFlattenedWhite(t *testing.T) {
	env := newEnv(t, nil)
	p := filepath.Join(t.TempDir(), "alpha.png")
	img := halfImage(300, 300, color.NRGBA{0, 0, 0, 0}, color.NRGBA{0, 200, 0, 255})
	f, _ := os.Create(p)
	png.Encode(f, img)
	f.Close()
	out, err := env.svc.Thumbnail(context.Background(), p, stat(t, p), 128)
	if err != nil {
		t.Fatal(err)
	}
	th := decodeJPEG(t, out)
	if !near(th.At(5, 64), 255, 255, 255) {
		t.Fatalf("transparent area should be white, got %v", th.At(5, 64))
	}
	if !near(th.At(120, 64), 0, 200, 0) {
		t.Fatalf("opaque area color %v", th.At(120, 64))
	}
}

// exifJPEG builds a JPEG with an APP1 EXIF segment carrying Orientation=6 (rotate 90° CW).
func exifJPEG(t *testing.T, img image.Image) []byte {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	tiffHdr := []byte{'M', 'M', 0, 42, 0, 0, 0, 8, // big endian, IFD at 8
		0, 1, // 1 entry
		0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, 6, 0, 0, // Orientation SHORT 1 = 6
		0, 0, 0, 0} // next IFD
	payload := append([]byte("Exif\x00\x00"), tiffHdr...)
	seg := []byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	seg = append(seg, payload...)
	out := append([]byte{}, raw[:2]...) // SOI
	out = append(out, seg...)
	return append(out, raw[2:]...)
}

func TestThumbnailEXIFRotation(t *testing.T) {
	env := newEnv(t, nil)
	// 400x200 stored: left red, right blue. Orientation 6 => displayed 200x400, red on top.
	p := filepath.Join(t.TempDir(), "rot.jpg")
	os.WriteFile(p, exifJPEG(t, halfImage(400, 200, color.NRGBA{255, 0, 0, 255}, color.NRGBA{0, 0, 255, 255})), 0o644)
	out, err := env.svc.Thumbnail(context.Background(), p, stat(t, p), 128)
	if err != nil {
		t.Fatal(err)
	}
	img := decodeJPEG(t, out)
	b := img.Bounds()
	if b.Dx() != 64 || b.Dy() != 128 {
		t.Fatalf("rotated thumb %v, want 64x128", b)
	}
	if !near(img.At(32, 5), 255, 0, 0) || !near(img.At(32, 122), 0, 0, 255) {
		t.Fatalf("rotation wrong: top %v bottom %v", img.At(32, 5), img.At(32, 122))
	}
}

func TestThumbnailHEIC(t *testing.T) {
	needFFmpeg(t)
	if _, err := exec.LookPath("sips"); err != nil {
		t.Skip("no HEIC encoder (sips) available")
	}
	env := newEnv(t, nil)
	jpg := fixture(t, "heic-src.jpg", "-f", "lavfi", "-i", "testsrc2=size=640x480", "-frames:v", "1")
	heic := filepath.Join(t.TempDir(), "photo.heic")
	if out, err := exec.Command("sips", "-s", "format", "heic", jpg, "--out", heic).CombinedOutput(); err != nil {
		t.Skipf("sips cannot write HEIC: %s", out)
	}
	out, err := env.svc.Thumbnail(context.Background(), heic, stat(t, heic), 256)
	if err != nil {
		t.Fatalf("HEIC thumbnail: %v", err)
	}
	b := decodeJPEG(t, out).Bounds()
	if b.Dx() != 256 || b.Dy() != 192 {
		t.Fatalf("heic thumb %v", b)
	}
}

func TestThumbnailVideoSeeks10Percent(t *testing.T) {
	env := newEnv(t, nil)
	// Black for the first 5 s, then white: at 10% of 20 s (= 2 s) the frame is black;
	// a thumbnail from 0 would also be black, so use a 60 s clip with white from 4 s
	// instead: 10% = 6 s => white.
	p := fixture(t, "bw.mp4", "-f", "lavfi", "-i", "color=black:size=320x240:duration=4:rate=25",
		"-f", "lavfi", "-i", "color=white:size=320x240:duration=56:rate=25",
		"-filter_complex", "[0][1]concat=n=2:v=1:a=0", "-c:v", "libx264", "-preset", "ultrafast")
	out, err := env.svc.Thumbnail(context.Background(), p, stat(t, p), 128)
	if err != nil {
		t.Fatal(err)
	}
	img := decodeJPEG(t, out)
	if b := img.Bounds(); b.Dx() != 128 || b.Dy() != 96 {
		t.Fatalf("video thumb %v", b)
	}
	if !near(img.At(64, 48), 255, 255, 255) {
		t.Fatalf("expected frame at ~10%% (white), got %v", img.At(64, 48))
	}
}

func TestThumbnailAudioCover(t *testing.T) {
	env := newEnv(t, nil)
	cover := fixture(t, "cover.png", "-f", "lavfi", "-i", "color=red:size=300x300", "-frames:v", "1")
	withCover := fixture(t, "cover.mp3", "-f", "lavfi", "-i", "sine=duration=3", "-i", cover,
		"-map", "0:a", "-map", "1:v", "-c:a", "libmp3lame", "-c:v", "png", "-disposition:v", "attached_pic", "-id3v2_version", "3")
	noCover := fixture(t, "nocover.mp3", "-f", "lavfi", "-i", "sine=duration=3", "-c:a", "libmp3lame")

	out, err := env.svc.Thumbnail(context.Background(), withCover, stat(t, withCover), 128)
	if err != nil {
		t.Fatalf("cover thumb: %v", err)
	}
	img := decodeJPEG(t, out)
	if !near(img.At(64, 64), 255, 0, 0) {
		t.Fatalf("cover color %v", img.At(64, 64))
	}

	fi := stat(t, noCover)
	if _, err := env.svc.Thumbnail(context.Background(), noCover, fi, 128); !errors.Is(err, ErrNoThumb) {
		t.Fatalf("no cover: got %v, want ErrNoThumb", err)
	}
	fp, err := env.svc.Fingerprint(noCover, fi.Size(), fi.ModTime())
	if err != nil {
		t.Fatal(err)
	}
	key := cacheKey("thumb", fp, 128)
	mark := env.cache.ThumbPath(key, ".jpg") + ".fail"
	if _, err := os.Stat(mark); err != nil {
		t.Fatalf("failure marker missing: %v", err)
	}
	// The marker short-circuits: no probe/ffmpeg even when the file is unreadable now.
	os.Chmod(noCover, 0)
	defer os.Chmod(noCover, 0o644)
	start := time.Now()
	if _, err := env.svc.Thumbnail(context.Background(), noCover, fi, 100); !errors.Is(err, ErrNoThumb) {
		t.Fatalf("second call: %v", err)
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Fatalf("failure marker not used (took %v)", time.Since(start))
	}
}

func TestThumbnailSizeNormalization(t *testing.T) {
	cases := map[int]int{-5: 128, 0: 128, 1: 128, 128: 128, 129: 256, 300: 512, 512: 512, 1000: 1024, 5000: 1024}
	for in, want := range cases {
		if got := NormalizeThumbSize(in); got != want {
			t.Errorf("NormalizeThumbSize(%d)=%d want %d", in, got, want)
		}
	}
	env := newEnv(t, nil)
	p := filepath.Join(t.TempDir(), "big.png")
	f, _ := os.Create(p)
	png.Encode(f, halfImage(2000, 1000, color.Black, color.White))
	f.Close()
	a, err := env.svc.Thumbnail(context.Background(), p, stat(t, p), 300)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := env.svc.Thumbnail(context.Background(), p, stat(t, p), 512)
	if a != b {
		t.Fatalf("300 and 512 should share the cache entry: %s vs %s", a, b)
	}
	if w := decodeJPEG(t, a).Bounds().Dx(); w != 512 {
		t.Fatalf("width %d", w)
	}
}

func TestThumbnailSingleflightAndTouch(t *testing.T) {
	env := newEnv(t, nil)
	p := fixture(t, "sf.mp4", "-f", "lavfi", "-i", "testsrc2=size=640x360:duration=5:rate=25", "-c:v", "libx264", "-preset", "ultrafast")
	fi := stat(t, p)

	const n = 16
	var wg sync.WaitGroup
	paths := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			paths[i], errs[i] = env.svc.Thumbnail(context.Background(), p, fi, 256)
		}(i)
	}
	wg.Wait()
	for i := range paths {
		if errs[i] != nil || paths[i] != paths[0] {
			t.Fatalf("req %d: %q %v", i, paths[i], errs[i])
		}
	}
	// Only one file was produced (no leftover temp files from duplicate work).
	entries, _ := os.ReadDir(filepath.Dir(paths[0]))
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected exactly one cache file, got %v", names)
	}

	// Cache hit path bumps mtime (access tracking for the LRU/TTL).
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(paths[0], old, old)
	if _, err := env.svc.Thumbnail(context.Background(), p, fi, 256); err != nil {
		t.Fatal(err)
	}
	if mt := stat(t, paths[0]).ModTime(); time.Since(mt) > time.Minute {
		t.Fatalf("mtime not touched: %v", mt)
	}

	// A cancelled caller doesn't poison the shared flight or leave a failure marker.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p2 := fixture(t, "sf2.mp4", "-f", "lavfi", "-i", "testsrc2=size=320x240:duration=3:rate=25", "-c:v", "libx264", "-preset", "ultrafast")
	env.svc.Thumbnail(ctx, p2, stat(t, p2), 128)
	if _, err := env.svc.Thumbnail(context.Background(), p2, stat(t, p2), 128); err != nil {
		t.Fatalf("after cancelled request: %v", err)
	}
}
