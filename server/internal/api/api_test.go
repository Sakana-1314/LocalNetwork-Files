package api

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"syscall"
	"testing"

	"github.com/Sakana-1314/MList/server/internal/auth"
	"github.com/Sakana-1314/MList/server/internal/media"
	"github.com/Sakana-1314/MList/server/internal/store"
	"github.com/Sakana-1314/MList/server/internal/vfs"
)

func TestStatusAndPublicMessage(t *testing.T) {
	internal := &fs.PathError{Op: "write", Path: "/srv/secret/dir/file", Err: syscall.ENOSPC}
	cases := []struct {
		err    error
		status int
		msg    string
	}{
		{vfs.ErrNotFound, 404, vfs.ErrNotFound.Error()},
		{vfs.ErrReadOnly, 403, vfs.ErrReadOnly.Error()},
		{vfs.ErrPermission, 403, vfs.ErrPermission.Error()},
		{fmt.Errorf("%w: detail", vfs.ErrInvalid), 400, "无效的路径或名称: detail"},
		{vfs.ErrExists, 409, vfs.ErrExists.Error()},
		{vfs.ErrIsDir, 400, vfs.ErrIsDir.Error()},
		{store.ErrNotFound, 404, "记录不存在"},
		{store.ErrConflict, 409, "名称已被使用"},
		{auth.ErrUnauthorized, 401, auth.ErrUnauthorized.Error()},
		{auth.ErrDisabled, 403, auth.ErrDisabled.Error()},
		{media.ErrNoFFmpeg, 501, media.ErrNoFFmpeg.Error()},
		{media.ErrSessionNotFound, 404, media.ErrSessionNotFound.Error()},
		{badRequest("bad"), 400, "bad"},
		{internal, 500, "服务器内部错误"},
		{fmt.Errorf("ffprobe: %w", internal), 500, "服务器内部错误"},
	}
	for _, c := range cases {
		if st := statusOf(c.err); st != c.status {
			t.Errorf("statusOf(%v) = %d, want %d", c.err, st, c.status)
		}
		msg := publicMessage(c.err)
		if msg != c.msg {
			t.Errorf("publicMessage(%v) = %q, want %q", c.err, msg, c.msg)
		}
		if strings.Contains(msg, "/srv/secret") {
			t.Errorf("message leaks local path: %q", msg)
		}
	}
	var br batchResult
	br.add("/a", internal)
	if br.Failed[0].Error != "服务器内部错误" {
		t.Errorf("batch failure leaks internals: %q", br.Failed[0].Error)
	}
	if http.StatusText(statusOf(&httpError{http.StatusUnsupportedMediaType, "x"})) == "" {
		t.Error("httpError status")
	}
}

func TestLoginLimiter(t *testing.T) {
	l := newLoginLimiter()
	for i := 0; i < loginMaxFails-1; i++ {
		l.fail("a")
	}
	if l.blocked("a") {
		t.Fatal("blocked too early")
	}
	l.fail("a")
	if !l.blocked("a") || l.blocked("b") {
		t.Fatal("limit not per IP")
	}
	l.reset("a")
	if l.blocked("a") {
		t.Fatal("reset failed")
	}
	for i := 0; i < 3000; i++ {
		l.fail(fmt.Sprint("ip", i))
	}
	if len(l.fails) != 3000 {
		t.Fatalf("recent failures must be kept, have %d", len(l.fails))
	}
}
