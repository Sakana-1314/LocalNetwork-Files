package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestStreamAudio(t *testing.T) {
	env := newEnv(t, nil)
	for _, name := range []string{"stream.flac", "stream.wma"} {
		t.Run(name, func(t *testing.T) {
			codec := map[string]string{"stream.flac": "flac", "stream.wma": "wmav2"}[name]
			p := fixture(t, name, "-f", "lavfi", "-i", "sine=duration=10:sample_rate=48000", "-c:a", codec)
			var buf bytes.Buffer
			if err := env.svc.StreamAudio(context.Background(), p, 4, &buf); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(t.TempDir(), "o.mp3")
			os.WriteFile(out, buf.Bytes(), 0o644)
			r := ffprobeJSON(t, out)
			a := r.stream("audio")
			if a == nil || a.CodecName != "mp3" || a.SampleRate != "44100" {
				t.Fatalf("stream %+v", a)
			}
			if d := f64(r.Format.Duration); d < 5.5 || d > 6.5 {
				t.Fatalf("duration after seek to 4 s: %v", d)
			}
		})
	}
	// Cancelling the request stops ffmpeg and is not an error.
	p := fixture(t, "long.flac", "-f", "lavfi", "-i", "sine=duration=600", "-c:a", "flac")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := env.svc.StreamAudio(ctx, p, 0, &slowWriter{}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
}

type slowWriter struct{}

func (slowWriter) Write(p []byte) (int, error) { time.Sleep(10 * time.Millisecond); return len(p), nil }

func readFile(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEmbeddedSubtitles(t *testing.T) {
	env := newEnv(t, nil)
	p := movieMKV(t)
	out, err := env.svc.EmbeddedSubtitle(context.Background(), p, stat(t, p), 0)
	if err != nil {
		t.Fatal(err)
	}
	vtt := readFile(t, out)
	if !strings.HasPrefix(vtt, "WEBVTT") || !strings.Contains(vtt, "你好 world") || !subTimingRe.MatchString(vtt) {
		t.Fatalf("vtt:\n%s", vtt)
	}
	if _, err := env.svc.EmbeddedSubtitle(context.Background(), p, stat(t, p), 3); err == nil {
		t.Fatal("missing track should fail")
	}

	// ASS stream -> WebVTT (styling stripped).
	ass := filepath.Join(fixtureDir, "s.ass")
	os.WriteFile(ass, []byte("[Script Info]\nScriptType: v4.00+\n\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\nStyle: Default,Arial,20,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,2,2,10,10,10,1\n\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:01.00,0:00:02.50,Default,,0,0,0,,{\\b1}粗体{\\b0} text\n"), 0o644)
	mkv := fixture(t, "ass.mkv", "-f", "lavfi", "-i", "testsrc2=size=320x240:duration=3:rate=25", "-i", ass,
		"-map", "0", "-map", "1", "-c:v", "libx264", "-preset", "ultrafast", "-c:s", "ass")
	out, err = env.svc.EmbeddedSubtitle(context.Background(), mkv, stat(t, mkv), 0)
	if err != nil {
		t.Fatal(err)
	}
	if vtt := readFile(t, out); !strings.Contains(vtt, "粗体") || !strings.Contains(vtt, "00:01.000 --> 00:02.500") || strings.Contains(vtt, "\\b1") {
		t.Fatalf("ass vtt:\n%s", vtt)
	}

	// Bitmap subtitles cannot become WebVTT. ffmpeg can't render a bitmap
	// subtitle fixture from text, so fake the probe result for a real file.
	for _, codec := range []string{"hdmv_pgs_subtitle", "dvd_subtitle", "dvb_subtitle"} {
		fi := stat(t, mkv)
		key := fmt.Sprintf("%s|%d|%d", mkv, fi.ModTime().UnixNano(), fi.Size())
		env.svc.probeMu.Lock()
		env.svc.probeCache[key] = &MediaInfo{Duration: 3, Subtitles: []SubtitleStream{{Codec: codec}}}
		env.svc.probeMu.Unlock()
		if _, err := env.svc.EmbeddedSubtitle(context.Background(), mkv, fi, 0); !errors.Is(err, ErrUnsupportedSubtitle) {
			t.Fatalf("%s: %v", codec, err)
		}
	}
}

func TestExternalSubtitleEncodings(t *testing.T) {
	env := newEnv(t, nil)
	dir := t.TempDir()
	text := "1\n00:00:01,000 --> 00:00:02,000\n中文字幕测试，简体。\n\n2\n00:00:03,000 --> 00:00:04,000\n第二行\n"

	gbk, _ := simplifiedchinese.GBK.NewEncoder().String(text)
	utf16le := []byte{0xFF, 0xFE}
	for _, r := range text { // BMP only
		utf16le = append(utf16le, byte(r), byte(r>>8))
	}
	files := map[string][]byte{
		"gbk.srt":     []byte(gbk),
		"utf8.srt":    []byte(text),
		"utf8bom.srt": append([]byte("\xEF\xBB\xBF"), text...),
		"utf16.srt":   utf16le,
	}
	for name, data := range files {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(dir, name)
			os.WriteFile(p, data, 0o644)
			out, err := env.svc.ExternalSubtitle(context.Background(), p, stat(t, p))
			if err != nil {
				t.Fatal(err)
			}
			vtt := readFile(t, out)
			if !strings.HasPrefix(vtt, "WEBVTT") || !strings.Contains(vtt, "中文字幕测试，简体。") || !strings.Contains(vtt, "第二行") {
				t.Fatalf("vtt:\n%q", vtt)
			}
		})
	}
	// .vtt is served as is.
	p := filepath.Join(dir, "x.vtt")
	os.WriteFile(p, []byte("WEBVTT\n"), 0o644)
	if out, err := env.svc.ExternalSubtitle(context.Background(), p, stat(t, p)); err != nil || out != p {
		t.Fatalf("vtt passthrough: %q %v", out, err)
	}
}

func TestSidecarSubtitles(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"movie.mkv", "movie.zh.srt", "Movie.SRT", "movie.en-US.ass", "movie.vtt", "movie.txt",
		"movie2.srt", "other.srt", "moviezh.srt", "movie.nfo"} {
		os.WriteFile(filepath.Join(dir, n), nil, 0o644)
	}
	os.Mkdir(filepath.Join(dir, "movie.srt"), 0o755) // directories are ignored
	got := SidecarSubtitles(filepath.Join(dir, "movie.mkv"))
	sort.Strings(got)
	want := []string{"Movie.SRT", "movie.en-US.ass", "movie.vtt", "movie.zh.srt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}
	if !IsSubtitleFile("A.SSA") || IsSubtitleFile("a.txt") {
		t.Fatal("IsSubtitleFile")
	}
}

// ffmpeg 6 shifts extracted cues by the container start offset (a few ms); newer versions don't.
var subTimingRe = regexp.MustCompile(`00:05\.00\d --> 00:07\.00\d`)
