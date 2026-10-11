package store

// Settings are runtime-tunable options editable from the admin UI.
type Settings struct {
	Cache     CacheSettings     `json:"cache"`
	Transcode TranscodeSettings `json:"transcode"`
	Search    SearchSettings    `json:"search"`
	Services  ServiceSettings   `json:"services"`
}

type CacheSettings struct {
	ThumbTTLHours          int `json:"thumb_ttl_hours"`
	ThumbMaxMB             int `json:"thumb_max_mb"`
	TranscodeTTLHours      int `json:"transcode_ttl_hours"`
	TranscodeMaxMB         int `json:"transcode_max_mb"`
	CleanupIntervalMinutes int `json:"cleanup_interval_minutes"`
}

type TranscodeSettings struct {
	Encoder        string `json:"encoder"`
	Preset         string `json:"preset"`
	MaxSessions    int    `json:"max_sessions"`
	SegmentSeconds int    `json:"segment_seconds"`
}

type SearchSettings struct {
	ReindexIntervalMinutes int  `json:"reindex_interval_minutes"`
	Watch                  bool `json:"watch"`
}

type ServiceSettings struct {
	WebDAVEnabled bool   `json:"webdav_enabled"`
	S3Enabled     bool   `json:"s3_enabled"`
	S3Port        int    `json:"s3_port"`
	SMBEnabled    bool   `json:"smb_enabled"`
	SMBPort       int    `json:"smb_port"`
	SMBWorkgroup  string `json:"smb_workgroup"`
}

func DefaultSettings() Settings {
	return Settings{
		Cache: CacheSettings{
			ThumbTTLHours:          24 * 30,
			ThumbMaxMB:             2048,
			TranscodeTTLHours:      24,
			TranscodeMaxMB:         20480,
			CleanupIntervalMinutes: 30,
		},
		Transcode: TranscodeSettings{
			Encoder:        "libx264",
			Preset:         "veryfast",
			MaxSessions:    2,
			SegmentSeconds: 6,
		},
		Search: SearchSettings{ReindexIntervalMinutes: 60, Watch: true},
		Services: ServiceSettings{
			WebDAVEnabled: true,
			S3Enabled:     true,
			S3Port:        9000,
			SMBEnabled:    false,
			SMBPort:       445,
			SMBWorkgroup:  "WORKGROUP",
		},
	}
}

var validEncoders = map[string]bool{"libx264": true, "h264_videotoolbox": true, "h264_nvenc": true, "h264_qsv": true, "h264_vaapi": true}
var validPresets = map[string]bool{"ultrafast": true, "superfast": true, "veryfast": true, "faster": true, "fast": true, "medium": true}

// Normalize clamps values into sane ranges, falling back to defaults.
func (s *Settings) Normalize() {
	d := DefaultSettings()
	clamp := func(v *int, lo, hi, def int) {
		if *v < lo || *v > hi {
			*v = def
		}
	}
	clamp(&s.Cache.ThumbTTLHours, 1, 24*3650, d.Cache.ThumbTTLHours)
	clamp(&s.Cache.ThumbMaxMB, 16, 1<<22, d.Cache.ThumbMaxMB)
	clamp(&s.Cache.TranscodeTTLHours, 1, 24*3650, d.Cache.TranscodeTTLHours)
	clamp(&s.Cache.TranscodeMaxMB, 256, 1<<24, d.Cache.TranscodeMaxMB)
	clamp(&s.Cache.CleanupIntervalMinutes, 1, 24*60, d.Cache.CleanupIntervalMinutes)
	if !validEncoders[s.Transcode.Encoder] {
		s.Transcode.Encoder = d.Transcode.Encoder
	}
	if !validPresets[s.Transcode.Preset] {
		s.Transcode.Preset = d.Transcode.Preset
	}
	clamp(&s.Transcode.MaxSessions, 1, 32, d.Transcode.MaxSessions)
	clamp(&s.Transcode.SegmentSeconds, 2, 20, d.Transcode.SegmentSeconds)
	clamp(&s.Search.ReindexIntervalMinutes, 0, 7*24*60, d.Search.ReindexIntervalMinutes)
	clamp(&s.Services.S3Port, 1, 65535, d.Services.S3Port)
	clamp(&s.Services.SMBPort, 1, 65535, d.Services.SMBPort)
	if s.Services.SMBWorkgroup == "" {
		s.Services.SMBWorkgroup = d.Services.SMBWorkgroup
	}
}
