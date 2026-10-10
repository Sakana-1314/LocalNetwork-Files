package media

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/cache"
	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/store"
)

// Fixtures are generated once per test binary run with lavfi sources.
var (
	fixtureDir  string
	fixtureMu   sync.Mutex
	fixtureDone = map[string]error{}
)

func TestMain(m *testing.M) {
	d, err := os.MkdirTemp("", "media-fixtures-")
	if err != nil {
		panic(err)
	}
	fixtureDir = d
	code := m.Run()
	os.RemoveAll(d)
	os.Exit(code)
}

func needFFmpeg(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
}

// fixture returns the path of a generated file, running ffmpeg with args
// (output path appended) the first time.
func fixture(t testing.TB, name string, args ...string) string {
	t.Helper()
	needFFmpeg(t)
	p := filepath.Join(fixtureDir, name)
	fixtureMu.Lock()
	defer fixtureMu.Unlock()
	err, ok := fixtureDone[name]
	if !ok {
		full := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}, args...)
		full = append(full, p)
		if out, e := exec.Command("ffmpeg", full...).CombinedOutput(); e != nil {
			err = &fixtureErr{name, string(out)}
		}
		fixtureDone[name] = err
	}
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return p
}

type fixtureErr struct{ name, out string }

func (e *fixtureErr) Error() string { return e.name + ": " + e.out }

// srtFile writes a small SRT subtitle used for embedding.
func srtFile(t testing.TB) string {
	p := filepath.Join(fixtureDir, "embed.srt")
	fixtureMu.Lock()
	defer fixtureMu.Unlock()
	os.WriteFile(p, []byte("1\n00:00:01,000 --> 00:00:03,000\n你好 world\n\n2\n00:00:05,000 --> 00:00:07,000\nsecond line\n"), 0o644)
	return p
}

// movieMKV: 60 s 720p 23.976 fps H.264 + two AC-3 tracks (sine / silence) + SRT stream.
// AC-3 audio in Matroska forces a full transcode for browsers.
func movieMKV(t testing.TB) string {
	srt := srtFile(t)
	return fixture(t, "movie.mkv",
		"-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=24000/1001:duration=60",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=60:sample_rate=48000",
		"-f", "lavfi", "-i", "anullsrc=channel_layout=5.1:sample_rate=48000",
		"-i", srt,
		"-map", "0:v", "-map", "1:a", "-map", "2:a", "-map", "3:s", "-t", "60",
		"-c:v", "libx264", "-preset", "ultrafast", "-g", "240", "-c:a", "ac3", "-c:s", "srt",
		"-metadata:s:a:0", "language=eng", "-metadata:s:a:1", "language=jpn", "-metadata:s:a:1", "title=Silent",
		"-metadata:s:s:0", "language=chi")
}

type testEnv struct {
	svc   *Service
	cache *cache.Manager
	store *store.Store
	dir   string
}

func newEnv(t testing.TB, mutate func(*store.Settings)) *testEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if mutate != nil {
		s := st.Settings()
		mutate(&s)
		if _, err := st.SaveSettings(s); err != nil {
			t.Fatal(err)
		}
	}
	cm, err := cache.New(filepath.Join(dir, "cache"), st)
	if err != nil {
		t.Fatal(err)
	}
	svc := New("ffmpeg", "ffprobe", cm, st)
	t.Cleanup(func() {
		svc.Close()
		cm.Stop()
		assertNoFFmpeg(t, dir)
	})
	return &testEnv{svc: svc, cache: cm, store: st, dir: dir}
}

// assertNoFFmpeg fails when an ffmpeg process still references dir.
func assertNoFFmpeg(t testing.TB, dir string) {
	t.Helper()
	out, _ := exec.Command("pgrep", "-fl", "ffmpeg.*"+dir).Output()
	if s := strings.TrimSpace(string(out)); s != "" {
		t.Errorf("leftover ffmpeg processes:\n%s", s)
	}
}

func stat(t testing.TB, p string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

type probeStream struct {
	Index         int               `json:"index"`
	CodecType     string            `json:"codec_type"`
	CodecName     string            `json:"codec_name"`
	Profile       string            `json:"profile"`
	Width         int               `json:"width"`
	Height        int               `json:"height"`
	PixFmt        string            `json:"pix_fmt"`
	Channels      int               `json:"channels"`
	SampleRate    string            `json:"sample_rate"`
	StartTime     string            `json:"start_time"`
	Tags          map[string]string `json:"tags"`
	CodecLongName string            `json:"codec_long_name"`
}

type probePacket struct {
	StreamIndex int    `json:"stream_index"`
	PtsTime     string `json:"pts_time"`
	DtsTime     string `json:"dts_time"`
	Duration    string `json:"duration_time"`
	Flags       string `json:"flags"`
}

type probeResult struct {
	Format struct {
		FormatName string `json:"format_name"`
		StartTime  string `json:"start_time"`
		Duration   string `json:"duration"`
	} `json:"format"`
	Streams []probeStream `json:"streams"`
	Packets []probePacket `json:"packets"`
}

func ffprobeJSON(t testing.TB, target string, extra ...string) probeResult {
	t.Helper()
	args := append([]string{"-v", "error", "-print_format", "json", "-show_format", "-show_streams"}, extra...)
	args = append(args, target)
	out, err := exec.Command("ffprobe", args...).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", target, err)
	}
	var r probeResult
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func f64(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }

func (r probeResult) stream(kind string) *probeStream {
	for i := range r.Streams {
		if r.Streams[i].CodecType == kind {
			return &r.Streams[i]
		}
	}
	return nil
}
