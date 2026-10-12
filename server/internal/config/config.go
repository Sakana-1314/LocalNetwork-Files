// Package config loads the server configuration file. Everything is static:
// options that used to be editable at runtime in the admin UI (services,
// transcoding, caches, search) live here too and need a restart to change.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	// Listen is the HTTP listen address (web UI, REST API, WebDAV).
	Listen string `yaml:"listen"`
	// DataDir holds the database, secret key and samba state.
	DataDir string `yaml:"data_dir"`
	// CacheDir holds thumbnails and transcoded segments. Defaults to <data_dir>/cache.
	CacheDir string `yaml:"cache_dir"`

	FFmpeg  string `yaml:"ffmpeg"`
	FFprobe string `yaml:"ffprobe"`

	// SessionTTL is the lifetime of web login sessions.
	SessionTTL time.Duration `yaml:"session_ttl"`

	// Admin is the bootstrap account, created only when the database has no users.
	Admin struct {
		Username string `yaml:"username"`
		Password string `yaml:"password"`
	} `yaml:"admin"`

	SMB SMBConfig `yaml:"smb"`

	// Services, Transcode, Cache and Search are the runtime options.
	Services  ServiceSettings   `yaml:"services"`
	Transcode TranscodeSettings `yaml:"transcode"`
	Cache     CacheSettings     `yaml:"cache"`
	Search    SearchSettings    `yaml:"search"`

	LogLevel string `yaml:"log_level"`
}

// SMBConfig controls the managed Samba process.
type SMBConfig struct {
	Smbd      string `yaml:"smbd"`
	Smbpasswd string `yaml:"smbpasswd"`
	// CreateSystemUsers creates a nologin system account for every user (Linux, requires root).
	// Samba can only authenticate users that exist as system accounts.
	CreateSystemUsers bool `yaml:"create_system_users"`
	// UserPrefix is prepended to usernames to form the samba/system account name.
	UserPrefix string `yaml:"user_prefix"`
	// ForceUser makes samba access files as this system user, so ownership matches the web server.
	ForceUser string `yaml:"force_user"`
}

func Default() *Config {
	c := &Config{
		Listen:     ":8080",
		DataDir:    "./data",
		FFmpeg:     "ffmpeg",
		FFprobe:    "ffprobe",
		SessionTTL: 7 * 24 * time.Hour,
		LogLevel:   "info",
	}
	c.Admin.Username = "admin"
	c.Admin.Password = "admin123"
	c.SMB = SMBConfig{Smbd: "smbd", Smbpasswd: "smbpasswd", UserPrefix: ""}
	c.Services = defaultServiceSettings()
	c.Transcode = defaultTranscodeSettings()
	c.Cache = defaultCacheSettings()
	c.Search = defaultSearchSettings()
	return c
}

// Load reads path (if it exists) on top of the defaults.
func Load(path string) (*Config, error) {
	c := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := yaml.Unmarshal(b, c); err != nil {
				return nil, fmt.Errorf("parse %s: %w", path, err)
			}
		case !os.IsNotExist(err):
			return nil, err
		}
	}
	if v := os.Getenv("LNF_LISTEN"); v != "" {
		c.Listen = v
	}
	if v := os.Getenv("LNF_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	c.Services.Normalize()
	c.Transcode.Normalize()
	c.Cache.Normalize()
	c.Search.Normalize()
	var err error
	if c.DataDir, err = filepath.Abs(c.DataDir); err != nil {
		return nil, err
	}
	if c.CacheDir == "" {
		c.CacheDir = filepath.Join(c.DataDir, "cache")
	}
	if c.CacheDir, err = filepath.Abs(c.CacheDir); err != nil {
		return nil, err
	}
	if c.SessionTTL <= 0 {
		c.SessionTTL = 7 * 24 * time.Hour
	}
	for _, d := range []string{c.DataDir, c.CacheDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	return c, nil
}
