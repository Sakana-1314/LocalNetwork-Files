package api

import (
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/Sakana-1314/MList/server/internal/store"
	"github.com/Sakana-1314/MList/server/internal/vfs"
)

// bucketMu serializes bucket changes: creating or extending a bucket merges
// directories on disk before the record is saved.
var bucketMu sync.Mutex

func (a *API) bucketView(b *store.Bucket, users map[uint]int) BucketView {
	v := BucketView{Bucket: *b, Users: users[b.ID], Dirs: make([]BucketDir, 0, len(b.Paths))}
	if len(b.Paths) > 0 {
		active, _ := store.Mount{Path: b.Paths[0], Mirrors: b.Paths[1:]}.Active()
		for _, p := range b.Paths {
			fi, err := os.Stat(p)
			v.Dirs = append(v.Dirs, BucketDir{Path: p, Available: err == nil && fi.IsDir(), Active: p == active})
		}
	}
	return v
}

func bucketID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, badRequest("id 无效"))
		return 0, false
	}
	return uint(id), true
}

// checkDirs verifies that every path of b is an existing directory.
func checkDirs(paths []string) error {
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil || !fi.IsDir() {
			return badRequest("目录不存在: " + p)
		}
	}
	return nil
}

// merge makes every directory in paths hold the union of their content. It
// refuses (409 with the list) when an entry exists in several directories with
// different content, and changes nothing in that case.
func (a *API) merge(c *gin.Context, paths []string) (*vfs.SyncResult, bool) {
	conflicts, err := vfs.FindConflicts(paths)
	if err != nil {
		fail(c, err)
		return nil, false
	}
	if len(conflicts) > 0 {
		c.AbortWithStatusJSON(http.StatusConflict, BucketConflictResponse{
			Error:     "目录中存在同名但内容不同的条目，已拒绝合并，请先处理冲突",
			Conflicts: conflicts,
		})
		return nil, false
	}
	res, err := vfs.Reconcile(paths, a.Search)
	if err != nil {
		fail(c, err)
		return nil, false
	}
	return &res, true
}

// @Summary     列出桶
// @Description 返回全部桶、各桶的目录及其可用状态，以及被授权的用户数。需要管理员权限。
// @Tags     管理
// @Produce  json
// @Success  200 {array} BucketView "全部桶"
// @Failure  403 {object} ErrorResponse "需要管理员权限"
// @Security bearerAuth
// @Router   /admin/buckets [get]
func (a *API) adminListBuckets(c *gin.Context) {
	bs, err := a.Store.ListBuckets()
	if err != nil {
		fail(c, err)
		return
	}
	users, err := a.Store.BucketUsers()
	if err != nil {
		fail(c, err)
		return
	}
	out := make([]BucketView, 0, len(bs))
	for i := range bs {
		out = append(out, a.bucketView(&bs[i], users))
	}
	c.JSON(http.StatusOK, out)
}

// @Summary     创建桶
// @Description 创建一个桶并指定其对应的服务器目录。多个目录互为备份，内容通过复制保持一致。
// @Description 若多个目录都不为空，会自动合并，使每个文件在每个目录中都有一份；
// @Description 同一路径在不同目录中内容不同（或一边是文件一边是目录）时拒绝创建，并返回冲突列表。需要管理员权限。
// @Tags     管理
// @Accept   json
// @Produce  json
// @Param    请求体 body BucketInput true "桶名称与目录"
// @Success  201 {object} BucketCreated "桶已创建，目录已合并"
// @Failure  400 {object} ErrorResponse "名称或目录无效"
// @Failure  403 {object} ErrorResponse "需要管理员权限"
// @Failure  409 {object} BucketConflictResponse "名称已被使用，或目录间存在同名冲突"
// @Security bearerAuth
// @Router   /admin/buckets [post]
func (a *API) adminCreateBucket(c *gin.Context) {
	var in BucketInput
	if !bind(c, &in) {
		return
	}
	b := &store.Bucket{Name: in.Name, Paths: in.Paths}
	if err := store.ValidateBucket(b); err != nil {
		fail(c, err)
		return
	}
	if err := checkDirs(b.Paths); err != nil {
		fail(c, err)
		return
	}
	bucketMu.Lock()
	defer bucketMu.Unlock()
	if a.bucketNameTaken(c, b.Name, 0) {
		return
	}
	res, ok := a.merge(c, b.Paths)
	if !ok {
		return
	}
	if err := a.Store.CreateBucket(b); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, BucketCreated{Bucket: a.bucketView(b, nil), Sync: *res})
}

func (a *API) bucketNameTaken(c *gin.Context, name string, exclude uint) bool {
	bs, err := a.Store.ListBuckets()
	if err != nil {
		fail(c, err)
		return true
	}
	for _, o := range bs {
		if o.ID != exclude && strings.EqualFold(o.Name, name) {
			fail(c, store.ErrConflict)
			return true
		}
	}
	return false
}

// @Summary     更新桶
// @Description 修改桶名称或目录列表（整表替换，第一个可用目录为主目录）。
// @Description 新增的目录会与现有目录合并，存在同名冲突时拒绝修改；移出的目录不会被删除，只是不再同步。需要管理员权限。
// @Tags     管理
// @Accept   json
// @Produce  json
// @Param    id path integer true "桶 ID"
// @Param    请求体 body BucketInput true "桶名称与目录"
// @Success  200 {object} BucketCreated "桶已更新，新增目录已合并"
// @Failure  400 {object} ErrorResponse "名称或目录无效"
// @Failure  403 {object} ErrorResponse "需要管理员权限"
// @Failure  404 {object} ErrorResponse "桶不存在"
// @Failure  409 {object} BucketConflictResponse "名称已被使用，或目录间存在同名冲突"
// @Security bearerAuth
// @Router   /admin/buckets/{id} [put]
func (a *API) adminUpdateBucket(c *gin.Context) {
	id, ok := bucketID(c)
	if !ok {
		return
	}
	var in BucketInput
	if !bind(c, &in) {
		return
	}
	bucketMu.Lock()
	defer bucketMu.Unlock()
	old, err := a.Store.GetBucket(id)
	if err != nil {
		fail(c, err)
		return
	}
	b := &store.Bucket{ID: id, Name: in.Name, Paths: in.Paths, CreatedAt: old.CreatedAt}
	if err := store.ValidateBucket(b); err != nil {
		fail(c, err)
		return
	}
	var added []string
	for _, p := range b.Paths {
		if !slices.Contains(old.Paths, p) {
			added = append(added, p)
		}
	}
	if err := checkDirs(added); err != nil {
		fail(c, err)
		return
	}
	if a.bucketNameTaken(c, b.Name, id) {
		return
	}
	res := &vfs.SyncResult{Conflicts: []vfs.Conflict{}}
	if len(added) > 0 {
		if res, ok = a.merge(c, b.Paths); !ok {
			return
		}
	}
	if err := a.Store.UpdateBucket(b); err != nil {
		fail(c, err)
		return
	}
	users, _ := a.Store.BucketUsers()
	c.JSON(http.StatusOK, BucketCreated{Bucket: a.bucketView(b, users), Sync: *res})
}

// @Summary     删除桶
// @Description 删除桶及其全部用户授权。服务器上的目录与文件不会被删除。需要管理员权限。
// @Tags     管理
// @Param    id path integer true "桶 ID"
// @Success  204 "桶已删除，无响应体"
// @Failure  403 {object} ErrorResponse "需要管理员权限"
// @Failure  404 {object} ErrorResponse "桶不存在"
// @Security bearerAuth
// @Router   /admin/buckets/{id} [delete]
func (a *API) adminDeleteBucket(c *gin.Context) {
	id, ok := bucketID(c)
	if !ok {
		return
	}
	bucketMu.Lock()
	defer bucketMu.Unlock()
	if err := a.Store.DeleteBucket(id); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// @Summary     同步桶
// @Description 立即在桶的各目录之间补齐缺失或较旧的文件（以最新修改的副本为准），不会删除任何文件。
// @Description 通过 SMB 或直接在服务器上做的修改，以及目录离线期间的变化，都靠同步补齐；服务也会定期自动同步。需要管理员权限。
// @Tags     管理
// @Produce  json
// @Param    id path integer true "桶 ID"
// @Success  200 {object} vfs.SyncResult "同步完成"
// @Failure  403 {object} ErrorResponse "需要管理员权限"
// @Failure  404 {object} ErrorResponse "桶不存在"
// @Security bearerAuth
// @Router   /admin/buckets/{id}/sync [post]
func (a *API) adminSyncBucket(c *gin.Context) {
	id, ok := bucketID(c)
	if !ok {
		return
	}
	b, err := a.Store.GetBucket(id)
	if err != nil {
		fail(c, err)
		return
	}
	res, err := vfs.Reconcile(b.Paths, a.Search)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}
