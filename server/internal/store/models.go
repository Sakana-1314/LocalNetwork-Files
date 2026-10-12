package store

import (
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

type User struct {
	ID           uint   `gorm:"primaryKey" json:"id"`                         // 用户 ID
	Username     string `gorm:"uniqueIndex;size:64;not null" json:"username"` // 用户名
	PasswordHash string `gorm:"not null" json:"-"`
	Role         string `gorm:"size:16;not null;default:user" json:"role" enums:"admin,user"` // 角色，admin 为管理员
	Disabled     bool   `gorm:"not null;default:false" json:"disabled"`                       // 是否已禁用（禁用后无法登录，令牌也失效）
	// TokenVersion 递增即可让该用户的所有登录会话失效。
	TokenVersion uint      `gorm:"not null;default:0" json:"-"`
	Mounts       []Mount   `gorm:"constraint:OnDelete:CASCADE" json:"mounts"` // 该用户被授权的目录
	CreatedAt    time.Time `json:"created_at"`                                // 创建时间
	UpdatedAt    time.Time `json:"-"`
}

func (u *User) IsAdmin() bool { return u.Role == RoleAdmin }

// Mount 是授权给用户的服务器目录；Writable=false 表示只读。
type Mount struct {
	ID       uint   `gorm:"primaryKey" json:"id"` // 授权目录 ID
	UserID   uint   `gorm:"index;not null" json:"-"`
	Name     string `gorm:"size:128;not null" json:"name"`          // 在虚拟根目录下显示的名称
	Path     string `gorm:"not null" json:"path,omitempty"`         // 服务器本地绝对路径（仅管理员可见）
	Writable bool   `gorm:"not null;default:false" json:"writable"` // false 为只读目录，true 为可写目录
}

const (
	ScopeRead  = "read"
	ScopeWrite = "write"
)

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

type S3Key struct {
	AccessKey string    `gorm:"primaryKey;size:32" json:"access_key"` // S3 Access Key（访问密钥 ID）
	UserID    uint      `gorm:"index;not null" json:"-"`
	Name      string    `gorm:"size:128" json:"name"` // 备注名称
	SecretEnc string    `gorm:"not null" json:"-"`
	ReadOnly  bool      `gorm:"not null;default:false" json:"read_only"` // 是否只读
	CreatedAt time.Time `json:"created_at"`                              // 创建时间
}
