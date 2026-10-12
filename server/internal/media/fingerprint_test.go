package media

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeBytes(t *testing.T, p string, data []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// A fingerprint must describe the content only: the same bytes at another path,
// under another name, with another mtime, must hash the same.
func TestFingerprintIsLocationIndependent(t *testing.T) {
	env := newEnv(t, nil)
	dir := t.TempDir()
	data := make([]byte, 300)
	for i := range data {
		data[i] = byte(i)
	}
	a := writeBytes(t, filepath.Join(dir, "a", "clip.mp4"), data)
	b := writeBytes(t, filepath.Join(dir, "b", "renamed.mp4"), data)

	fiA, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	fiB, err := os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	fpA, err := env.svc.Fingerprint(a, fiA.Size(), fiA.ModTime())
	if err != nil {
		t.Fatal(err)
	}
	// A different mtime must not change the fingerprint.
	fpB, err := env.svc.Fingerprint(b, fiB.Size(), fiB.ModTime().Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if fpA != fpB {
		t.Fatalf("copies differ: %s vs %s", fpA, fpB)
	}

	// Same size, different content -> different fingerprint.
	data[0] ^= 0xff
	c := writeBytes(t, filepath.Join(dir, "c.mp4"), data)
	fiC, _ := os.Stat(c)
	fpC, err := env.svc.Fingerprint(c, fiC.Size(), fiC.ModTime())
	if err != nil {
		t.Fatal(err)
	}
	if fpC == fpA {
		t.Fatal("different content hashed the same")
	}
}

// Large files are fingerprinted from head + tail only, so a change in the
// middle is not detected; the size must still be part of the hash.
func TestFingerprintLargeFile(t *testing.T) {
	env := newEnv(t, nil)
	dir := t.TempDir()
	data := make([]byte, 3*fingerprintSmall)
	for i := range data {
		data[i] = byte(i % 251)
	}
	p := writeBytes(t, filepath.Join(dir, "big.mp4"), data)
	fi, _ := os.Stat(p)
	fp, err := env.svc.Fingerprint(p, fi.Size(), fi.ModTime())
	if err != nil {
		t.Fatal(err)
	}
	if len(fp) != 64 {
		t.Fatalf("fingerprint %q", fp)
	}
	// Appending changes both the size and the tail.
	if err := os.WriteFile(p, append(data, 0x01), 0o644); err != nil {
		t.Fatal(err)
	}
	fi2, _ := os.Stat(p)
	fp2, err := env.svc.Fingerprint(p, fi2.Size(), fi2.ModTime())
	if err != nil {
		t.Fatal(err)
	}
	if fp2 == fp {
		t.Fatal("appending did not change the fingerprint")
	}
}

func TestFingerprintMemoAndErrors(t *testing.T) {
	env := newEnv(t, nil)
	dir := t.TempDir()
	p := writeBytes(t, filepath.Join(dir, "m.bin"), []byte("hello"))
	fi, _ := os.Stat(p)
	fp, err := env.svc.Fingerprint(p, fi.Size(), fi.ModTime())
	if err != nil {
		t.Fatal(err)
	}
	// The memo answers without touching the file.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	again, err := env.svc.Fingerprint(p, fi.Size(), fi.ModTime())
	if err != nil || again != fp {
		t.Fatalf("memo miss: %q %v", again, err)
	}
	// A path that never existed is an error, not a panic.
	if _, err := env.svc.Fingerprint(filepath.Join(dir, "nope"), 1, time.Now()); err == nil {
		t.Fatal("missing file must fail")
	}
}

// Moving a file must not invalidate its cached preview.
func TestThumbnailSurvivesMove(t *testing.T) {
	env := newEnv(t, nil)
	dir := t.TempDir()
	src := fixture(t, "move-src.png", "-f", "lavfi", "-i", "color=c=red:size=64x64:duration=1", "-frames:v", "1")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	before := writeBytes(t, filepath.Join(dir, "before", "red.png"), data)
	after := writeBytes(t, filepath.Join(dir, "moved", "pink.png"), data)

	ctx := context.Background()
	out1, err := env.svc.Thumbnail(ctx, before, stat(t, before), 128)
	if err != nil {
		t.Fatal(err)
	}
	out2, err := env.svc.Thumbnail(ctx, after, stat(t, after), 128)
	if err != nil {
		t.Fatal(err)
	}
	if out1 != out2 {
		t.Fatalf("moving the file lost the cached preview:\n%s\n%s", out1, out2)
	}
}
