// Package app wires all subsystems together and runs the servers.
package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Sakana-1314/MList/server/internal/api"
	"github.com/Sakana-1314/MList/server/internal/auth"
	"github.com/Sakana-1314/MList/server/internal/cache"
	"github.com/Sakana-1314/MList/server/internal/config"
	"github.com/Sakana-1314/MList/server/internal/media"
	"github.com/Sakana-1314/MList/server/internal/search"
	"github.com/Sakana-1314/MList/server/internal/services/s3"
	"github.com/Sakana-1314/MList/server/internal/services/smb"
	"github.com/Sakana-1314/MList/server/internal/services/webdav"
	"github.com/Sakana-1314/MList/server/internal/store"
	"github.com/Sakana-1314/MList/server/internal/vfs"
	"github.com/Sakana-1314/MList/server/internal/webui"
)

type App struct {
	cfg     *config.Config
	version string
	store   *store.Store
	auth    *auth.Service
	cache   *cache.Manager
	media   *media.Service
	search  *search.Indexer
	dav     *webdav.Server
	s3      *s3.Server
	smb     *smb.Manager
	handler http.Handler

	// bg tracks goroutines started from store events so Close can wait for them.
	bg        sync.WaitGroup
	stop      chan struct{}
	closeOnce sync.Once
}

// bucketSyncInterval is how often the directories of every bucket are merged.
// Changes made through MList are copied right away; this pass repairs changes
// made over SMB or directly on disk, and directories that were offline.
const bucketSyncInterval = 30 * time.Minute

func New(cfg *config.Config, version string) (*App, error) {
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	au, err := auth.New(st, cfg.DataDir, cfg.SessionTTL)
	if err != nil {
		return nil, err
	}
	if created, err := au.EnsureAdmin(cfg.Admin.Username, cfg.Admin.Password); err != nil {
		return nil, err
	} else if created {
		slog.Warn("created bootstrap admin account; change its password after first login", "username", cfg.Admin.Username)
	}
	cm, err := cache.New(cfg.CacheDir, cfg.Cache)
	if err != nil {
		return nil, err
	}
	a := &App{cfg: cfg, version: version, store: st, auth: au, cache: cm, stop: make(chan struct{})}
	a.media = media.New(cfg.FFmpeg, cfg.FFprobe, cm, cfg.Transcode)
	vfs.FFmpegAvailable = a.media.HasFFmpeg()
	// Directory listings and search results carry a content digest instead of a
	// path, so previews never have to expose where a file lives.
	vfs.Fingerprint = a.media.Fingerprint
	if !a.media.HasFFmpeg() {
		slog.Warn("ffmpeg/ffprobe not found: video thumbnails and transcoding are disabled")
	}
	a.search = search.New(st, cfg.Search)
	a.dav = webdav.New(auth.NewBasic(au), a.newFS, func() bool { return cfg.Services.WebDAVEnabled })
	a.s3 = s3.New(au, a.newFS, cfg.DataDir)
	a.smb = smb.New(cfg.SMB, cfg.Services, cfg.DataDir, st)
	au.OnPassword = a.smb.SyncPassword

	st.Subscribe(func(e store.Event) {
		switch e {
		case store.EventUsersChanged:
			a.bg.Go(a.search.SyncRoots)
			a.bg.Go(a.smb.Apply)
		}
	})
	a.handler = a.buildHandler()
	return a, nil
}

func (a *App) newFS(p *auth.Principal) *vfs.FS {
	return vfs.New(p.User.Mounts, p.ReadOnly, a.search)
}

// syncBuckets reconciles the directories of every bucket that has backups.
func (a *App) syncBuckets() {
	bs, err := a.store.ListBuckets()
	if err != nil {
		slog.Error("bucket sync: list buckets", "err", err)
		return
	}
	for _, b := range bs {
		select {
		case <-a.stop:
			return
		default:
		}
		if len(b.Paths) < 2 {
			continue
		}
		res, err := vfs.Reconcile(b.Paths, a.search)
		if err != nil {
			slog.Warn("bucket sync failed", "bucket", b.Name, "err", err)
		}
		if res.Copied > 0 || len(res.Conflicts) > 0 {
			slog.Info("bucket synced", "bucket", b.Name, "copied", res.Copied, "bytes", res.Bytes, "conflicts", len(res.Conflicts))
		}
	}
}

func (a *App) syncLoop() {
	t := time.NewTicker(bucketSyncInterval)
	defer t.Stop()
	a.syncBuckets()
	for {
		select {
		case <-a.stop:
			return
		case <-t.C:
			a.syncBuckets()
		}
	}
}

func (a *App) applyServices() {
	s := a.cfg.Services
	a.s3.SetEnabled(s.S3Enabled)
	a.smb.Apply()
}

// Status implements api.Services.
func (a *App) Status(userID uint) map[string]api.ServiceStatus {
	s := a.cfg.Services
	out := map[string]api.ServiceStatus{}
	_, port := splitPort(a.cfg.Listen)
	out["webdav"] = api.ServiceStatus{Enabled: s.WebDAVEnabled, Running: s.WebDAVEnabled, Port: port, Path: webdav.Prefix}
	// S3 shares the main listener, so its port and status follow it.
	out["s3"] = api.ServiceStatus{
		Enabled: s.S3Enabled, Running: s.S3Enabled && a.s3.Enabled(),
		Port: port, Path: s3.Prefix,
	}
	running, smbPort, err := a.smb.Status()
	st := api.ServiceStatus{Enabled: s.SMBEnabled, Running: running, Port: s.SMBPort}
	if running {
		st.Port = smbPort
	}
	if err != nil {
		st.Error = err.Error()
	}
	if s.SMBEnabled {
		st.Shares = a.smb.SharesFor(userID)
		if u, err := a.store.GetUser(userID); err == nil {
			st.Account = a.smb.Account(u.Username)
		}
	}
	out["smb"] = st
	// Start errors may reveal local paths and binaries; only admins see them.
	if u, err := a.store.GetUser(userID); err != nil || !u.IsAdmin() {
		for k, v := range out {
			if v.Error != "" {
				v.Error = "服务异常，请联系管理员"
				out[k] = v
			}
		}
	}
	return out
}

func (a *App) buildHandler() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	// Only trust X-Forwarded-For / X-Real-IP from a reverse proxy on this host;
	// otherwise any LAN client could spoof its address and evade the login rate limit.
	if err := r.SetTrustedProxies([]string{"127.0.0.1", "::1"}); err != nil {
		panic(err)
	}
	r.Use(gin.Recovery(), requestLogger())
	api.Register(r, api.Deps{
		Config: a.cfg, Store: a.store, Auth: a.auth, Media: a.media, Cache: a.cache, Search: a.search,
		NewFS: a.newFS, Services: a, Version: a.version,
	})
	ui := webui.Handler()
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "接口不存在"})
			return
		}
		// gin presets 404 for NoRoute; client-side routes must load the SPA with 200.
		c.Status(http.StatusOK)
		ui.ServeHTTP(c.Writer, c.Request)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Windows' WebDAV client probes OPTIONS at the server root before mapping
		// http://host:port/dav/ as a drive; advertise DAV support there.
		if req.Method == http.MethodOptions && req.URL.Path == "/" && a.cfg.Services.WebDAVEnabled {
			w.Header().Set("DAV", "1, 2")
			w.Header().Set("MS-Author-Via", "DAV")
			w.Header().Set("Allow", "OPTIONS, GET, HEAD, PROPFIND")
			w.WriteHeader(http.StatusOK)
			return
		}
		if req.URL.Path == webdav.Prefix || strings.HasPrefix(req.URL.Path, webdav.Prefix+"/") {
			a.dav.ServeHTTP(w, req)
			return
		}
		// S3 lives on the same port as the web UI; the handler strips the prefix
		// itself, after verifying the signature against the signed path.
		if req.URL.Path == s3.Prefix || strings.HasPrefix(req.URL.Path, s3.Prefix+"/") {
			a.s3.ServeHTTP(w, req)
			return
		}
		r.ServeHTTP(w, req)
	})
}

func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			slog.Debug("http", "method", c.Request.Method, "path", c.Request.URL.Path, "status", c.Writer.Status(),
				"took", time.Since(start).Round(time.Millisecond))
		}
	}
}

// Handler exposes the root HTTP handler (used by tests).
func (a *App) Handler() http.Handler { return a.handler }

// Run starts background workers and serves until ctx is cancelled.
func (a *App) Run(ctx context.Context) error {
	a.cache.Start()
	a.search.Start()
	a.applyServices()
	a.bg.Go(a.syncLoop)

	srv := &http.Server{Addr: a.cfg.Listen, Handler: a.handler, ReadHeaderTimeout: 30 * time.Second}
	errc := make(chan error, 1)
	go func() {
		slog.Info("http server listening", "addr", a.cfg.Listen, "version", a.version)
		errc <- srv.ListenAndServe()
	}()
	var err error
	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	slog.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(sctx)
	a.Close()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Close stops background workers and protocol services and closes the
// database. It is safe to call more than once.
func (a *App) Close() {
	a.closeOnce.Do(func() {
		close(a.stop)
		a.bg.Wait()
		a.s3.Close()
		a.smb.Close()
		a.media.Close()
		a.search.Stop()
		a.cache.Stop()
		a.store.Close()
	})
}
