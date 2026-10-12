package config

// Runtime options used to live in the database and were edited from the admin
// UI. They are rarely changed, so they are part of the configuration file now
// and take effect after a restart.

// ServiceSettings controls the protocol services.
type ServiceSettings struct {
	WebDAVEnabled bool `yaml:"webdav_enabled"`
	// S3Enabled serves the S3-compatible API under /s3 on the main HTTP port.
	S3Enabled    bool   `yaml:"s3_enabled"`
	SMBEnabled   bool   `yaml:"smb_enabled"`
	SMBPort      int    `yaml:"smb_port"`
	SMBWorkgroup string `yaml:"smb_workgroup"`
}

// TranscodeSettings controls ffmpeg based HLS transcoding.
type TranscodeSettings struct {
	Encoder        string `yaml:"encoder"`
	Preset         string `yaml:"preset"`
	MaxSessions    int    `yaml:"max_sessions"`
	SegmentSeconds int    `yaml:"segment_seconds"`
}

// CacheSettings controls the preview / transcode caches. A TTL of 0 hours
// means "never expires": entries only go away when a bucket outgrows its cap.
type CacheSettings struct {
	ThumbTTLHours          int `yaml:"thumb_ttl_hours"`
	ThumbMaxMB             int `yaml:"thumb_max_mb"`
	TranscodeTTLHours      int `yaml:"transcode_ttl_hours"`
	TranscodeMaxMB         int `yaml:"transcode_max_mb"`
	CleanupIntervalMinutes int `yaml:"cleanup_interval_minutes"`
}

// SearchSettings controls the filename index.
type SearchSettings struct {
	ReindexIntervalMinutes int  `yaml:"reindex_interval_minutes"`
	Watch                  bool `yaml:"watch"`
}

func defaultServiceSettings() ServiceSettings {
	return ServiceSettings{WebDAVEnabled: true, S3Enabled: true, SMBEnabled: false, SMBPort: 445, SMBWorkgroup: "WORKGROUP"}
}

func defaultTranscodeSettings() TranscodeSettings {
	return TranscodeSettings{Encoder: "libx264", Preset: "veryfast", MaxSessions: 2, SegmentSeconds: 6}
}

func defaultCacheSettings() CacheSettings {
	return CacheSettings{ThumbTTLHours: 0, ThumbMaxMB: 2048, TranscodeTTLHours: 0, TranscodeMaxMB: 20480, CleanupIntervalMinutes: 30}
}

func defaultSearchSettings() SearchSettings {
	return SearchSettings{ReindexIntervalMinutes: 60, Watch: true}
}

var validEncoders = map[string]bool{"libx264": true, "h264_videotoolbox": true, "h264_nvenc": true, "h264_qsv": true, "h264_vaapi": true}
var validPresets = map[string]bool{"ultrafast": true, "superfast": true, "veryfast": true, "faster": true, "fast": true, "medium": true}

func clamp(v *int, lo, hi, def int) {
	if *v < lo || *v > hi {
		*v = def
	}
}

// Normalize clamps every runtime option into a sane range, falling back to the
// default when a value is missing (zero) or out of range. It is applied after
// loading the file, so a partial configuration file keeps working defaults.
func (s *ServiceSettings) Normalize() {
	d := defaultServiceSettings()
	clamp(&s.SMBPort, 1, 65535, d.SMBPort)
	if s.SMBWorkgroup == "" {
		s.SMBWorkgroup = d.SMBWorkgroup
	}
}

func (t *TranscodeSettings) Normalize() {
	d := defaultTranscodeSettings()
	if !validEncoders[t.Encoder] {
		t.Encoder = d.Encoder
	}
	if !validPresets[t.Preset] {
		t.Preset = d.Preset
	}
	clamp(&t.MaxSessions, 1, 32, d.MaxSessions)
	clamp(&t.SegmentSeconds, 2, 20, d.SegmentSeconds)
}

func (c *CacheSettings) Normalize() {
	d := defaultCacheSettings()
	// 0 is a valid TTL: keep the cache forever.
	clamp(&c.ThumbTTLHours, 0, 24*3650, d.ThumbTTLHours)
	clamp(&c.ThumbMaxMB, 16, 1<<22, d.ThumbMaxMB)
	clamp(&c.TranscodeTTLHours, 0, 24*3650, d.TranscodeTTLHours)
	clamp(&c.TranscodeMaxMB, 256, 1<<24, d.TranscodeMaxMB)
	clamp(&c.CleanupIntervalMinutes, 1, 24*60, d.CleanupIntervalMinutes)
}

func (s *SearchSettings) Normalize() {
	clamp(&s.ReindexIntervalMinutes, 0, 7*24*60, defaultSearchSettings().ReindexIntervalMinutes)
}
