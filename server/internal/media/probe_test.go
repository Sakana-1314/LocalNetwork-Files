package media

import (
	"context"
	"testing"
)

func TestProbeTracks(t *testing.T) {
	env := newEnv(t, nil)
	p := movieMKV(t)
	fi := stat(t, p)
	mi, err := env.svc.Probe(context.Background(), p, fi.ModTime(), fi.Size())
	if err != nil {
		t.Fatal(err)
	}
	if mi.Video == nil || mi.Video.Codec != "h264" || mi.Video.Width != 1280 || mi.Video.Height != 720 || mi.Video.FPS != 23.98 {
		t.Fatalf("video %+v", mi.Video)
	}
	if mi.Duration < 59.9 || mi.Duration > 60.2 {
		t.Fatalf("duration %v", mi.Duration)
	}
	if len(mi.Audio) != 2 || mi.Audio[0].Codec != "ac3" || mi.Audio[0].Language != "eng" ||
		mi.Audio[1].Language != "jpn" || mi.Audio[1].Title != "Silent" || mi.Audio[1].Channels != 6 || mi.Audio[1].Index != 1 {
		t.Fatalf("audio %+v", mi.Audio)
	}
	if len(mi.Subtitles) != 1 || mi.Subtitles[0].Codec != "subrip" || mi.Subtitles[0].Language != "chi" {
		t.Fatalf("subs %+v", mi.Subtitles)
	}
	if mi.DirectPlay {
		t.Fatal("mkv/ac3 must not be direct play")
	}
	// Cached: same pointer for the same path+mtime+size.
	mi2, _ := env.svc.Probe(context.Background(), p, fi.ModTime(), fi.Size())
	if mi2 != mi {
		t.Fatal("probe result not cached")
	}
}

func TestProbeDirectPlay(t *testing.T) {
	env := newEnv(t, nil)
	v := []string{"-f", "lavfi", "-i", "testsrc2=size=320x240:duration=2:rate=25"}
	a := []string{"-f", "lavfi", "-i", "sine=duration=2"}
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"h264aac.mp4", append(append(append([]string{}, v...), a...), "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac"), true},
		{"h264aac.mkv", append(append(append([]string{}, v...), a...), "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac"), false},
		{"hevc.mp4", append(append(append([]string{}, v...), a...), "-c:v", "libx265", "-preset", "ultrafast", "-c:a", "aac", "-x265-params", "log-level=error"), false},
		{"h264ac3.mp4", append(append(append([]string{}, v...), a...), "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "ac3"), false},
		{"h264-10bit.mp4", append(append([]string{}, v...), "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p10le"), false},
		{"audio.mp3", append(append([]string{}, a...), "-c:a", "libmp3lame"), true},
		{"audio.flac", append(append([]string{}, a...), "-c:a", "flac"), true},
		{"audio.wma", append(append([]string{}, a...), "-c:a", "wmav2"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := fixture(t, "dp-"+c.name, c.args...)
			fi := stat(t, p)
			mi, err := env.svc.Probe(context.Background(), p, fi.ModTime(), fi.Size())
			if err != nil {
				t.Fatal(err)
			}
			if mi.DirectPlay != c.want {
				t.Fatalf("direct_play=%v want %v (format %s, video %+v, audio %+v)", mi.DirectPlay, c.want, mi.Format, mi.Video, mi.Audio)
			}
		})
	}
}
