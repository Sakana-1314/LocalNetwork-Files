package store

import (
	"os"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// User 是登录用户。
// @Description 用户
type User struct {
	ID           uint   `gorm:"primaryKey" json:"id"`                         // 用户 ID
	Username     string `gorm:"uniqueIndex;size:64;not null" json:"username"` // 用户名
	PasswordHash string `gorm:"not null" json:"-"`
	// PasswordEnc 是密码的可逆密文（AES-GCM，密钥为 data 目录下的 secret.key），
	// 供管理员在用户管理里查看当前密码。登录校验始终用 PasswordHash。
	// 升级前创建的用户没有这一列，取值为空串，接口会提示无法查看。
	PasswordEnc string `gorm:"size:512" json:"-"`
	Role        string `gorm:"size:16;not null;default:user" json:"role" enums:"admin,user" extensions:"x-apifox-mock=admin"` // 角色，admin 为管理员
	Disabled    bool   `gorm:"not null;default:false" json:"disabled" extensions:"x-apifox-mock=false"`                       // 是否已禁用（禁用后无法登录，令牌也失效）
	// TokenVersion 递增即可让该用户的所有登录会话失效。
	TokenVersion uint      `gorm:"not null;default:0" json:"-"`
	Grants       []Grant   `gorm:"constraint:OnDelete:CASCADE" json:"grants"` // 该用户被授权的桶
	CreatedAt    time.Time `json:"created_at"`                                // 创建时间
	UpdatedAt    time.Time `json:"-"`

	// Mounts 由 Grants 与其桶解析而来，是文件系统、搜索与 SMB 使用的运行时视图。
	Mounts []Mount `gorm:"-" json:"-"`
}

func (u *User) IsAdmin() bool { return u.Role == RoleAdmin }

// Bucket 是对服务器目录的抽象：一个桶对应一个或多个本地目录，多个目录互为备份，
// 内容保持一致（复制而非软链接）。第一个可用目录是读写的主目录，其余为副本。
type Bucket struct {
	ID        uint      `gorm:"primaryKey" json:"id"`                      // 桶 ID
	Name      string    `gorm:"uniqueIndex;size:128;not null" json:"name"` // 桶名称，即用户虚拟根目录下显示的名称
	PathsRaw  string    `gorm:"column:paths;not null" json:"-"`
	CreatedAt time.Time `json:"created_at"` // 创建时间

	Paths []string `gorm:"-" json:"paths"` // 桶对应的服务器本地绝对路径，按优先级排列；多个目录互为备份
}

func (b *Bucket) AfterFind(*gorm.DB) error { b.Paths = splitPaths(b.PathsRaw); return nil }

func (b *Bucket) BeforeSave(*gorm.DB) error { b.PathsRaw = strings.Join(b.Paths, "\n"); return nil }

func splitPaths(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, "\n") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Grant 把一个桶授权给用户；Writable=false 表示只读。
//
// @Description 把一个桶授权给用户
type Grant struct {
	ID       uint    `gorm:"primaryKey" json:"-"`
	UserID   uint    `gorm:"uniqueIndex:idx_grant_user_bucket;not null" json:"-"`
	BucketID uint    `gorm:"uniqueIndex:idx_grant_user_bucket;index;not null" json:"bucket_id"` // 桶 ID
	Writable bool    `gorm:"not null;default:false" json:"writable"`                            // false 为只读，true 为可写
	Bucket   *Bucket `gorm:"constraint:OnDelete:CASCADE" json:"-"`

	Name string `gorm:"-" json:"name"` // 桶名称（只读，由服务端填充）
}

// Mount 是用户可访问的一个桶的运行时视图（不落库）。Path 是首选目录，
// Mirrors 是其余互为备份的目录；实际读写哪个目录由 vfs 在可用目录中选择。
// @Description 授权给用户的服务器目录
type Mount struct {
	BucketID uint
	Name     string
	Path     string
	Mirrors  []string
	Writable bool
}

// Dirs returns every configured directory of the mount, preferred first.
func (m Mount) Dirs() []string { return append([]string{m.Path}, m.Mirrors...) }

// Active returns the directory that serves reads and writes — the first one
// that currently exists as a directory — and the other available directories
// that receive copies. When none is available the preferred path is returned
// so that callers report "not found" instead of silently switching.
func (m Mount) Active() (string, []string) {
	var avail []string
	for _, d := range m.Dirs() {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			avail = append(avail, d)
		}
	}
	if len(avail) == 0 {
		return m.Path, nil
	}
	return avail[0], avail[1:]
}

const (
	ScopeRead  = "read"
	ScopeWrite = "write"
)

// AccessToken 是 API 访问令牌。
// @Description API 访问令牌
type AccessToken struct {
	ID     uint   `gorm:"primaryKey" json:"id"` // 令牌 ID
	UserID uint   `gorm:"index;not null" json:"-"`
	Name   string `gorm:"size:128" json:"name"` // 令牌名称
	// Prefix 是令牌开头若干位，用于辨认；完整明文可通过 /tokens/{id}/secret 读取。
	Prefix string `gorm:"size:16;index" json:"prefix"`
	// TokenEnc 保存整条令牌的密文（AES-GCM）；令牌可随时读回，不存哈希。
	TokenEnc   string     `gorm:"not null;default:''" json:"-"`
	ScopesRaw  string     `gorm:"column:scopes;size:64" json:"-"`
	ExpiresAt  *time.Time `json:"expires_at"`   // 过期时间；null 表示永不过期
	LastUsedAt *time.Time `json:"last_used_at"` // 最近使用时间；null 表示从未使用
	CreatedAt  time.Time  `json:"created_at"`   // 创建时间

	Scopes []string `gorm:"-" json:"scopes"` // 权限范围
}

func (t *AccessToken) AfterFind(*gorm.DB) error { t.Scopes = splitScopes(t.ScopesRaw); return nil }

func (t *AccessToken) HasScope(s string) bool {
	for _, x := range splitScopes(t.ScopesRaw) {
		if x == s {
			return true
		}
	}
	return false
}

func splitScopes(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// S3Key 是 S3 访问密钥。
// @Description S3 访问密钥
type S3Key struct {
	AccessKey string    `gorm:"primaryKey;size:32" json:"access_key"` // S3 Access Key（访问密钥 ID）
	UserID    uint      `gorm:"index;not null" json:"-"`
	Name      string    `gorm:"size:128" json:"name"` // 备注名称
	SecretEnc string    `gorm:"not null" json:"-"`
	ReadOnly  bool      `gorm:"not null;default:false" json:"read_only"` // 是否只读
	CreatedAt time.Time `json:"created_at"`                              // 创建时间
}
