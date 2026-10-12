package api

import (
	"math"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Sakana-1314/MList/server/internal/media"
	"github.com/Sakana-1314/MList/server/internal/vfs"
)

func (a *API) mediaThumb(c *gin.Context) {
	loc, fi, err := a.fs(c).LocalFile(c.Query("path"))
	if err != nil {
		fail(c, err)
		return
	}
	size, _ := strconv.Atoi(c.DefaultQuery("size", "256"))
	p, err := a.Media.Thumbnail(c.Request.Context(), loc.Real, fi, size)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "private, max-age=86400")
	c.Header("Content-Type", "image/jpeg")
	http.ServeFile(c.Writer, c.Request, p)
}

// probeError maps an unexpected ffprobe failure (the file is not a parsable
// media file) to 415 instead of a 500 server error.
func probeError(c *gin.Context, err error) error {
	if statusOf(err) == http.StatusInternalServerError && c.Request.Context().Err() == nil {
		return &httpError{http.StatusUnsupportedMediaType, "无法识别的媒体文件"}
	}
	return err
}

func (a *API) mediaInfo(c *gin.Context) {
	vp := vfs.CleanPath(c.Query("path"))
	loc, fi, err := a.fs(c).LocalFile(vp)
	if err != nil {
		fail(c, err)
		return
	}
	mi, err := a.Media.Probe(c.Request.Context(), loc.Real, fi.ModTime(), fi.Size())
	if err != nil {
		fail(c, probeError(c, err))
		return
	}
	out := *mi
	out.Subtitles = append([]media.SubtitleStream{}, mi.Subtitles...)
	if mi.Video != nil {
		for _, name := range media.SidecarSubtitles(loc.Real) {
			out.Subtitles = append(out.Subtitles, media.SubtitleStream{
				Index: len(out.Subtitles), Codec: vfs.Ext(name), Title: name, External: path.Join(path.Dir(vp), name),
			})
		}
	}
	c.JSON(http.StatusOK, out)
}

func (a *API) mediaTranscode(c *gin.Context) {
	var req struct {
		Path       string `json:"path" binding:"required"`
		Quality    string `json:"quality"`
		AudioTrack int    `json:"audio_track"`
	}
	if !bind(c, &req) {
		return
	}
	vp := vfs.CleanPath(req.Path)
	loc, fi, err := a.fs(c).LocalFile(vp)
	if err != nil {
		fail(c, err)
		return
	}
	u := principal(c).User
	se, err := a.Media.CreateSession(c.Request.Context(), u.ID, u.Username, loc.Real, vp, fi, req.Quality, req.AudioTrack)
	if err != nil {
		fail(c, probeError(c, err))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id": se.ID, "playlist": "/api/v1/media/hls/" + se.ID + "/index.m3u8", "duration": se.Duration,
		"quality": se.Quality,
	})
}

func (a *API) mediaHLS(c *gin.Context) {
	id, file := c.Param("id"), c.Param("file")
	uid := principal(c).User.ID
	if file == "index.m3u8" {
		query := ""
		if t := c.Query("access_token"); t != "" {
			query = "access_token=" + url.QueryEscape(t)
		}
		pl, err := a.Media.Playlist(id, uid, query)
		if err != nil {
			fail(c, err)
			return
		}
		c.Header("Cache-Control", "no-cache")
		c.Data(http.StatusOK, "application/vnd.apple.mpegurl", []byte(pl))
		return
	}
	n, err := strconv.Atoi(strings.TrimSuffix(file, ".ts"))
	if err != nil || !strings.HasSuffix(file, ".ts") {
		fail(c, media.ErrSessionNotFound)
		return
	}
	p, err := a.Media.Segment(c.Request.Context(), id, uid, n)
	if err != nil {
		if c.Request.Context().Err() != nil {
			return
		}
		fail(c, err)
		return
	}
	c.Header("Content-Type", "video/mp2t")
	c.Header("Cache-Control", "private, max-age=3600")
	http.ServeFile(c.Writer, c.Request, p)
}

func (a *API) mediaHLSStop(c *gin.Context) {
	if err := a.Media.StopSession(c.Param("id"), principal(c).User.ID); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (a *API) mediaAudio(c *gin.Context) {
	loc, _, err := a.fs(c).LocalFile(c.Query("path"))
	if err != nil {
		fail(c, err)
		return
	}
	if !a.Media.HasFFmpeg() {
		fail(c, media.ErrNoFFmpeg)
		return
	}
	start, err := strconv.ParseFloat(c.DefaultQuery("start", "0"), 64)
	if err != nil || math.IsNaN(start) || math.IsInf(start, 0) || start < 0 {
		fail(c, badRequest("start 无效"))
		return
	}
	c.Header("Content-Type", "audio/mpeg")
	c.Header("Cache-Control", "no-store")
	c.Status(http.StatusOK)
	a.Media.StreamAudio(c.Request.Context(), loc.Real, start, c.Writer)
}

func (a *API) mediaSubtitle(c *gin.Context) {
	loc, fi, err := a.fs(c).LocalFile(c.Query("path"))
	if err != nil {
		fail(c, err)
		return
	}
	var p string
	if idx := c.Query("index"); idx != "" && !media.IsSubtitleFile(fi.Name()) {
		n, err := strconv.Atoi(idx)
		if err != nil {
			fail(c, badRequest("index 无效"))
			return
		}
		p, err = a.Media.EmbeddedSubtitle(c.Request.Context(), loc.Real, fi, n)
		if err != nil {
			fail(c, err)
			return
		}
	} else {
		if !media.IsSubtitleFile(fi.Name()) {
			fail(c, badRequest("不是字幕文件"))
			return
		}
		if p, err = a.Media.ExternalSubtitle(c.Request.Context(), loc.Real, fi); err != nil {
			fail(c, err)
			return
		}
	}
	c.Header("Content-Type", "text/vtt; charset=utf-8")
	c.Header("Cache-Control", "private, max-age=3600")
	http.ServeFile(c.Writer, c.Request, p)
}
