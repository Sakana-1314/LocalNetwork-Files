package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/cache"
)

// Real-time HLS transcoding.
//
// A session publishes a complete VOD playlist up front (segment count derived
// from the duration) so the player can seek anywhere. Segments are produced on
// demand: a request for a segment that is neither on disk nor about to be
// produced by the running ffmpeg restarts ffmpeg at that segment's timestamp.
// Keyframes are forced on exact segment boundaries so segments produced by
// different ffmpeg runs line up. Finished segments stay in the transcode cache
// and are subject to its TTL/LRU lifecycle.

var (
	ErrSessionNotFound = errors.New("转码会话不存在或已过期")
	ErrNoVideo         = errors.New("文件不包含视频流")
)

var Qualities = map[string]struct {
	Height  int
	Bitrate int // kbit/s
}{
	"original": {0, 8000},
	"1080p":    {1080, 6000},
	"720p":     {720, 3000},
	"480p":     {480, 1200},
}

const (
	jobIdleTimeout = 45 * time.Second // kill ffmpeg when no segment was requested for this long
	sessionActive  = 3 * time.Minute  // sessions accessed this recently are never evicted
	segmentWait    = 90 * time.Second // max time a segment request waits for ffmpeg
	lookahead      = 4                // a running job "covers" segments up to this far beyond its progress
)

// Lock order: jobMu -> mu -> hlsSession.mu. jobMu guards every hlsSession.job.
type hlsManager struct {
	svc      *Service
	jobMu    sync.Mutex
	mu       sync.Mutex
	sessions map[string]*hlsSession
	stop     chan struct{}
}

type hlsSession struct {
	ID       string
	Real     string
	VPath    string
	Quality  string
	Audio    int
	Duration float64
	SegDur   int
	NSeg     int
	Dir      string
	info     *MediaInfo

	job *hlsJob // guarded by hlsManager.jobMu

	mu         sync.Mutex
	users      map[uint]string
	lastAccess time.Time
	forceSW    bool
}

type hlsJob struct {
	start   int
	next    int // first segment index not yet known to exist
	cmd     *exec.Cmd
	done    chan struct{}
	stderr  *bytes.Buffer
	err     error
	encoder string
	started time.Time
}

func newHLSManager(s *Service) *hlsManager {
	m := &hlsManager{svc: s, sessions: map[string]*hlsSession{}, stop: make(chan struct{})}
	go m.janitor()
	return m
}

type SessionInfo struct {
	ID         string    `json:"id"`
	Path       string    `json:"path"`
	Quality    string    `json:"quality"`
	User       string    `json:"user"`
	Running    bool      `json:"running"`
	LastAccess time.Time `json:"last_access"`
}

// Sessions lists known transcode sessions.
func (s *Service) Sessions() []SessionInfo {
	m := s.hls
	m.jobMu.Lock()
	defer m.jobMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []SessionInfo{}
	for _, se := range m.sessions {
		se.mu.Lock()
		names := []string{}
		for _, n := range se.users {
			names = append(names, n)
		}
		out = append(out, SessionInfo{ID: se.ID, Path: se.VPath, Quality: se.Quality, User: strings.Join(names, ","),
			Running: se.job != nil && !se.job.finished(), LastAccess: se.lastAccess})
		se.mu.Unlock()
	}
	return out
}

// CreateSession registers (or reuses) a transcode session for a user.
func (s *Service) CreateSession(ctx context.Context, userID uint, username, real, vpath string, fi os.FileInfo, quality string, audio int) (*hlsSession, error) {
	if !s.hasFFmpeg {
		return nil, ErrNoFFmpeg
	}
	if _, ok := Qualities[quality]; !ok {
		quality = "720p"
	}
	mi, err := s.Probe(ctx, real, fi.ModTime(), fi.Size())
	if err != nil {
		return nil, err
	}
	if mi.Video == nil || mi.Duration <= 0 {
		return nil, ErrNoVideo
	}
	if audio < 0 || audio >= len(mi.Audio) {
		audio = 0
	}
	ts := s.store.Settings().Transcode
	id := cacheKey("hls", real, fi.ModTime().UnixNano(), fi.Size(), quality, audio, ts.SegmentSeconds)[:24]

	m := s.hls
	m.mu.Lock()
	defer m.mu.Unlock()
	se := m.sessions[id]
	if se == nil {
		se = &hlsSession{
			ID: id, Real: real, VPath: vpath, Quality: quality, Audio: audio, Duration: mi.Duration,
			SegDur: ts.SegmentSeconds, Dir: s.cache.TranscodeDir(id), info: mi, users: map[uint]string{},
		}
		se.NSeg = int(math.Ceil(mi.Duration / float64(se.SegDur)))
		if err := os.MkdirAll(se.Dir, 0o755); err != nil {
			return nil, err
		}
		m.sessions[id] = se
	}
	se.mu.Lock()
	se.users[userID] = username
	se.lastAccess = time.Now()
	se.mu.Unlock()
	return se, nil
}

func (m *hlsManager) get(id string, userID uint) (*hlsSession, error) {
	m.mu.Lock()
	se := m.sessions[id]
	m.mu.Unlock()
	if se == nil {
		return nil, ErrSessionNotFound
	}
	se.mu.Lock()
	_, ok := se.users[userID]
	se.mu.Unlock()
	if !ok {
		return nil, ErrSessionNotFound
	}
	return se, nil
}

// Playlist renders the VOD playlist. query is appended to segment URIs (e.g. access_token).
func (s *Service) Playlist(id string, userID uint, query string) (string, error) {
	se, err := s.hls.get(id, userID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n", se.SegDur)
	if query != "" {
		query = "?" + query
	}
	for i := 0; i < se.NSeg; i++ {
		d := math.Min(float64(se.SegDur), se.Duration-float64(i*se.SegDur))
		fmt.Fprintf(&b, "#EXTINF:%.6f,\n%d.ts%s\n", d, i, query)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String(), nil
}

// StopSession stops ffmpeg for the session (cached segments are kept).
func (s *Service) StopSession(id string, userID uint) error {
	se, err := s.hls.get(id, userID)
	if err != nil {
		return err
	}
	s.hls.jobMu.Lock()
	defer s.hls.jobMu.Unlock()
	se.killJob()
	return nil
}

func (se *hlsSession) segPath(n int) string { return filepath.Join(se.Dir, strconv.Itoa(n)+".ts") }

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// Segment returns the local path of segment n, transcoding it if necessary.
func (s *Service) Segment(ctx context.Context, id string, userID uint, n int) (string, error) {
	se, err := s.hls.get(id, userID)
	if err != nil {
		return "", err
	}
	if n < 0 || n >= se.NSeg {
		return "", ErrSessionNotFound
	}
	se.mu.Lock()
	se.lastAccess = time.Now()
	se.mu.Unlock()
	cache.Touch(se.Dir)

	p := se.segPath(n)
	if exists(p) {
		return p, nil
	}
	deadline := time.Now().Add(segmentWait)
	restarts := 0
	for {
		s.hls.jobMu.Lock()
		job := se.job
		covered := job != nil && !job.finished() && n >= job.start && n <= job.progress(se)+lookahead
		if !covered {
			// A job started for this request already ended without producing n: give up after a retry.
			if restarts >= 2 && job != nil {
				s.hls.jobMu.Unlock()
				return "", fmt.Errorf("转码失败: %s", job.errText())
			}
			restarts++
			if err := s.hls.startJob(se, n); err != nil {
				s.hls.jobMu.Unlock()
				return "", err
			}
		}
		s.hls.jobMu.Unlock()

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		if exists(p) {
			return p, nil
		}
		if time.Now().After(deadline) {
			return "", errors.New("转码超时")
		}
	}
}

func (j *hlsJob) finished() bool {
	select {
	case <-j.done:
		return true
	default:
		return false
	}
}

// progress advances j.next past segments that already exist and returns the last produced index.
func (j *hlsJob) progress(se *hlsSession) int {
	for j.next < se.NSeg && exists(se.segPath(j.next)) {
		j.next++
	}
	return j.next - 1
}

func (j *hlsJob) errText() string {
	if j.stderr != nil && j.stderr.Len() > 0 {
		return lastLine(j.stderr.Bytes())
	}
	if j.err != nil {
		return j.err.Error()
	}
	return "unknown"
}

// killJob must be called with hlsManager.jobMu held.
func (se *hlsSession) killJob() {
	if se.job != nil && !se.job.finished() {
		se.job.cmd.Process.Kill()
		<-se.job.done
	}
}

func (m *hlsManager) runningJobs() int {
	n := 0
	for _, se := range m.sessions {
		if se.job != nil && !se.job.finished() {
			n++
		}
	}
	return n
}

// startJob (re)starts ffmpeg for se at segment n. Caller holds m.jobMu.
func (m *hlsManager) startJob(se *hlsSession, n int) error {
	se.killJob()
	ts := m.svc.store.Settings().Transcode

	// Enforce the global concurrency limit by stopping the least recently used other job.
	m.mu.Lock()
	for m.runningJobs() >= ts.MaxSessions {
		var victim *hlsSession
		var victimAccess time.Time
		for _, o := range m.sessions {
			if o == se || o.job == nil || o.job.finished() {
				continue
			}
			o.mu.Lock()
			la := o.lastAccess
			o.mu.Unlock()
			if victim == nil || la.Before(victimAccess) {
				victim, victimAccess = o, la
			}
		}
		if victim == nil {
			break
		}
		victim.killJob()
	}
	m.mu.Unlock()

	// Remove partial outputs from previous runs.
	tmps, _ := filepath.Glob(filepath.Join(se.Dir, "*.tmp"))
	for _, t := range tmps {
		os.Remove(t)
	}

	encoder := ts.Encoder
	se.mu.Lock()
	if se.forceSW {
		encoder = "libx264"
	}
	se.mu.Unlock()
	args := m.svc.hlsArgs(se, n, encoder, ts.Preset)
	cmd := exec.Command(m.svc.ffmpeg, args...)
	stderr := &bytes.Buffer{}
	cmd.Stderr = &limitedWriter{buf: stderr, max: 8192}
	if err := cmd.Start(); err != nil {
		return err
	}
	job := &hlsJob{start: n, next: n, cmd: cmd, done: make(chan struct{}), stderr: stderr, encoder: encoder, started: time.Now()}
	se.job = job
	slog.Debug("hls job start", "session", se.ID, "segment", n, "encoder", encoder)
	go func() {
		job.err = cmd.Wait()
		close(job.done)
		if job.err != nil && encoder != "libx264" && time.Since(job.started) < 10*time.Second && !exists(se.segPath(n)) {
			slog.Warn("hardware encoder failed, falling back to libx264", "encoder", encoder, "err", job.errText())
			se.mu.Lock()
			se.forceSW = true
			se.mu.Unlock()
		}
	}()
	return nil
}

func (s *Service) hlsArgs(se *hlsSession, n int, encoder, preset string) []string {
	q := Qualities[se.Quality]
	start := float64(n * se.SegDur)
	seg := strconv.Itoa(se.SegDur)
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}
	if encoder == "h264_vaapi" {
		args = append(args, "-vaapi_device", "/dev/dri/renderD128")
	}
	if start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(start, 'f', 3, 64))
	}
	args = append(args, "-i", se.Real, "-map", "0:v:0")
	if len(se.info.Audio) > 0 {
		args = append(args, "-map", "0:a:"+strconv.Itoa(se.Audio))
	}

	var filters []string
	if q.Height > 0 && se.info.Video != nil && se.info.Video.Height > q.Height {
		filters = append(filters, "scale=-2:"+strconv.Itoa(q.Height))
	} else {
		filters = append(filters, "scale=trunc(iw/2)*2:trunc(ih/2)*2")
	}
	bitrate := q.Bitrate
	if se.info.BitRate > 0 && int(se.info.BitRate/1000) < bitrate {
		bitrate = max(int(se.info.BitRate/1000), 500)
	}
	br := strconv.Itoa(bitrate) + "k"
	buf := strconv.Itoa(bitrate*2) + "k"
	switch encoder {
	case "h264_videotoolbox":
		filters = append(filters, "format=yuv420p")
		args = append(args, "-c:v", encoder, "-b:v", br, "-maxrate", br, "-bufsize", buf, "-allow_sw", "1", "-profile:v", "high")
	case "h264_nvenc":
		filters = append(filters, "format=yuv420p")
		args = append(args, "-c:v", encoder, "-preset", "p4", "-b:v", br, "-maxrate", br, "-bufsize", buf, "-profile:v", "high")
	case "h264_qsv":
		filters = append(filters, "format=nv12")
		args = append(args, "-c:v", encoder, "-preset", "veryfast", "-b:v", br, "-maxrate", br, "-bufsize", buf)
	case "h264_vaapi":
		filters = append(filters, "format=nv12", "hwupload")
		args = append(args, "-c:v", encoder, "-b:v", br, "-maxrate", br, "-bufsize", buf)
	default:
		filters = append(filters, "format=yuv420p")
		args = append(args, "-c:v", "libx264", "-preset", preset, "-crf", "23", "-maxrate", br, "-bufsize", buf,
			"-profile:v", "high", "-sc_threshold", "0")
	}
	args = append(args, "-vf", strings.Join(filters, ","),
		"-force_key_frames", "expr:gte(t,n_forced*"+seg+")")
	if len(se.info.Audio) > 0 {
		args = append(args, "-c:a", "aac", "-ac", "2", "-b:a", "160k")
	}
	args = append(args, "-sn", "-dn", "-max_muxing_queue_size", "2048",
		"-output_ts_offset", strconv.FormatFloat(start, 'f', 3, 64),
		"-f", "hls", "-hls_time", seg, "-hls_list_size", "0", "-hls_segment_type", "mpegts",
		"-start_number", strconv.Itoa(n), "-hls_flags", "temp_file",
		"-hls_segment_filename", filepath.Join(se.Dir, "%d.ts"),
		filepath.Join(se.Dir, "ffmpeg.m3u8"))
	return args
}

func (m *hlsManager) janitor() {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
		}
		m.jobMu.Lock()
		m.mu.Lock()
		for _, se := range m.sessions {
			se.mu.Lock()
			idle := time.Since(se.lastAccess) > jobIdleTimeout
			se.mu.Unlock()
			if idle && se.job != nil && !se.job.finished() {
				slog.Debug("hls job idle, stopping", "session", se.ID)
				se.killJob()
			}
		}
		m.mu.Unlock()
		m.jobMu.Unlock()
	}
}

func (m *hlsManager) isActive(id string) bool {
	m.jobMu.Lock()
	defer m.jobMu.Unlock()
	m.mu.Lock()
	se := m.sessions[id]
	m.mu.Unlock()
	if se == nil {
		return false
	}
	if se.job != nil && !se.job.finished() {
		return true
	}
	se.mu.Lock()
	defer se.mu.Unlock()
	return time.Since(se.lastAccess) < sessionActive
}

func (m *hlsManager) forget(id string) {
	m.jobMu.Lock()
	defer m.jobMu.Unlock()
	m.mu.Lock()
	se := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if se != nil {
		se.killJob()
	}
}

func (m *hlsManager) closeAll() {
	close(m.stop)
	m.jobMu.Lock()
	defer m.jobMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, se := range m.sessions {
		se.killJob()
	}
}

type limitedWriter struct {
	mu  sync.Mutex
	buf *bytes.Buffer
	max int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len() < w.max {
		w.buf.Write(p[:min(len(p), w.max-w.buf.Len())])
	}
	return len(p), nil
}
