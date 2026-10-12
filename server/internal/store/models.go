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
	ID           uint   `gorm:"primaryKey" json:"id"`
	Username     string `gorm:"uniqueIndex;size:64;not null" json:"username"`
	PasswordHash string `gorm:"not null" json:"-"`
	Role         string `gorm:"size:16;not null;default:user" json:"role"`
	Disabled     bool   `gorm:"not null;default:false" json:"disabled"`
	// TokenVersion is bumped to invalidate all login sessions of the user.
	TokenVersion uint      `gorm:"not null;default:0" json:"-"`
	Mounts       []Mount   `gorm:"constraint:OnDelete:CASCADE" json:"mounts"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"-"`
}

func (u *User) IsAdmin() bool { return u.Role == RoleAdmin }

// Mount grants a user access to a local directory. Writable=false means read only.
type Mount struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	UserID   uint   `gorm:"index;not null" json:"-"`
	Name     string `gorm:"size:128;not null" json:"name"`
	Path     string `gorm:"not null" json:"path,omitempty"`
	Writable bool   `gorm:"not null;default:false" json:"writable"`
}

const (
	ScopeRead  = "read"
	ScopeWrite = "write"
)

type AccessToken struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	UserID     uint       `gorm:"index;not null" json:"-"`
	Name       string     `gorm:"size:128" json:"name"`
	Prefix     string     `gorm:"size:16" json:"prefix"`
	Hash       string     `gorm:"uniqueIndex;size:64;not null" json:"-"`
	ScopesRaw  string     `gorm:"column:scopes;size:64" json:"-"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`

	Scopes []string `gorm:"-" json:"scopes"`
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
	AccessKey string    `gorm:"primaryKey;size:32" json:"access_key"`
	UserID    uint      `gorm:"index;not null" json:"-"`
	Name      string    `gorm:"size:128" json:"name"`
	SecretEnc string    `gorm:"not null" json:"-"`
	ReadOnly  bool      `gorm:"not null;default:false" json:"read_only"`
	CreatedAt time.Time `json:"created_at"`
}
