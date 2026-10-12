// Package store is the persistence layer (SQLite via GORM, pure Go driver).
package store

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("already exists")
)

// Event is published after data that other subsystems depend on changes.
type Event int

const (
	EventUsersChanged Event = iota + 1
)

type Store struct {
	db *gorm.DB

	mu   sync.RWMutex
	subs []func(Event)
}

// dbFile is the SQLite database name. It was renamed from lnfiles.db when the
// project became MList; migrateDBFile brings an existing database along.
const dbFile = "mlist.db"

// legacyDBFile is the database name used before the rename.
const legacyDBFile = "lnfiles.db"

func Open(dataDir string) (*Store, error) {
	if err := migrateDBFile(dataDir); err != nil {
		return nil, err
	}
	dsn := filepath.Join(dataDir, dbFile) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(&User{}, &Mount{}, &AccessToken{}, &S3Key{}); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	s := &Store{db: db}
	if err := s.dropHashedTokens(); err != nil {
		return nil, err
	}
	return s, nil
}

// migrateDBFile renames lnfiles.db (and its WAL sidecars) to mlist.db so that
// upgrading an existing deployment keeps users, mounts and credentials. It is
// a no-op when the new file already exists, so it is safe to run on every
// start and cannot clobber a database that was already migrated.
func migrateDBFile(dataDir string) error {
	newPath := filepath.Join(dataDir, dbFile)
	if _, err := os.Stat(newPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	oldPath := filepath.Join(dataDir, legacyDBFile)
	if _, err := os.Stat(oldPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // fresh install
		}
		return err
	}
	// Rename the WAL sidecars first: renaming the main file beforehand would
	// make the database look migrated while its WAL is still under the old name.
	for _, suffix := range []string{"-wal", "-shm"} {
		src := oldPath + suffix
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := os.Rename(src, newPath+suffix); err != nil {
			return fmt.Errorf("rename %s: %w", filepath.Base(src), err)
		}
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("rename %s: %w", legacyDBFile, err)
	}
	slog.Info("store: renamed the legacy database file", "from", legacyDBFile, "to", dbFile)
	return nil
}

// dropHashedTokens removes access tokens created before the token was stored
// encrypted. Their plaintext only ever existed in the response of the creating
// request, so they cannot be migrated and have to be recreated; the leftover
// hash column is dropped as well.
func (s *Store) dropHashedTokens() error {
	if !s.db.Migrator().HasColumn(&AccessToken{}, "hash") {
		return nil
	}
	var n int64
	s.db.Model(&AccessToken{}).Count(&n)
	if err := s.db.Exec("DELETE FROM access_tokens").Error; err != nil {
		return fmt.Errorf("drop legacy tokens: %w", err)
	}
	// SQLite refuses DROP COLUMN while any index still references it, and the
	// old unique index on hash does. Dropping the index first is what makes the
	// column go away; if it stays behind as NOT NULL the next token INSERT fails
	// with a 500, so this must not be best-effort.
	if err := s.dropIndexesOn("access_tokens", "hash"); err != nil {
		return fmt.Errorf("drop legacy token index: %w", err)
	}
	// GORM's DropColumn is a no-op for SQLite, so issue the statement directly.
	if err := s.db.Exec("ALTER TABLE access_tokens DROP COLUMN hash").Error; err != nil {
		if s.db.Migrator().HasColumn(&AccessToken{}, "hash") {
			return fmt.Errorf("drop legacy token hash column: %w", err)
		}
	}
	if n > 0 {
		slog.Warn("store: cleared access tokens created by an older version; create new ones in 设置 → 访问令牌", "removed", n)
	}
	return nil
}

// dropIndexesOn drops every index of table that references any of cols.
func (s *Store) dropIndexesOn(table string, cols ...string) error {
	want := make(map[string]bool, len(cols))
	for _, c := range cols {
		want[c] = true
	}
	var idx []struct {
		Name string `gorm:"column:name"`
	}
	if err := s.db.Raw(fmt.Sprintf("PRAGMA index_list(%q)", table)).Scan(&idx).Error; err != nil {
		return err
	}
	for _, ix := range idx {
		var info []struct {
			Name string `gorm:"column:name"`
		}
		if err := s.db.Raw(fmt.Sprintf("PRAGMA index_info(%q)", ix.Name)).Scan(&info).Error; err != nil {
			return err
		}
		for _, c := range info {
			if !want[c.Name] {
				continue
			}
			if err := s.db.Exec(fmt.Sprintf("DROP INDEX IF EXISTS %q", ix.Name)).Error; err != nil {
				return err
			}
			break
		}
	}
	return nil
}

func (s *Store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// Subscribe registers fn to be called (synchronously) after changes.
func (s *Store) Subscribe(fn func(Event)) {
	s.mu.Lock()
	s.subs = append(s.subs, fn)
	s.mu.Unlock()
}

func (s *Store) publish(e Event) {
	s.mu.RLock()
	subs := append([]func(Event){}, s.subs...)
	s.mu.RUnlock()
	for _, fn := range subs {
		fn(e)
	}
}

func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}

// ---------- users ----------

func (s *Store) CountUsers() (int64, error) {
	var n int64
	err := s.db.Model(&User{}).Count(&n).Error
	return n, err
}

func (s *Store) ListUsers() ([]User, error) {
	var users []User
	err := s.db.Preload("Mounts", func(db *gorm.DB) *gorm.DB { return db.Order("id") }).Order("id").Find(&users).Error
	return users, err
}

func (s *Store) GetUser(id uint) (*User, error) {
	var u User
	if err := s.db.Preload("Mounts", func(db *gorm.DB) *gorm.DB { return db.Order("id") }).First(&u, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &u, nil
}

func (s *Store) GetUserByName(name string) (*User, error) {
	var u User
	if err := s.db.Preload("Mounts", func(db *gorm.DB) *gorm.DB { return db.Order("id") }).Where("username = ? COLLATE NOCASE", name).First(&u).Error; err != nil {
		return nil, notFound(err)
	}
	return &u, nil
}

// ValidateMounts normalizes mount definitions in place.
func ValidateMounts(ms []Mount) error {
	seen := map[string]bool{}
	for i := range ms {
		m := &ms[i]
		m.Name = strings.TrimSpace(m.Name)
		if m.Name == "" || m.Name == "." || m.Name == ".." || strings.ContainsAny(m.Name, "/\\\x00") ||
			utf8.RuneCountInString(m.Name) > 128 {
			return fmt.Errorf("无效的目录名称 %q", m.Name)
		}
		if seen[strings.ToLower(m.Name)] {
			return fmt.Errorf("目录名称重复: %s", m.Name)
		}
		seen[strings.ToLower(m.Name)] = true
		if !filepath.IsAbs(m.Path) || strings.ContainsRune(m.Path, 0) {
			return fmt.Errorf("目录 %s 的路径必须是绝对路径", m.Name)
		}
		m.Path = filepath.Clean(m.Path)
		m.ID = 0
	}
	return nil
}

func (s *Store) CreateUser(u *User) error {
	if err := ValidateMounts(u.Mounts); err != nil {
		return err
	}
	err := s.db.Create(u).Error
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		return ErrConflict
	}
	if err == nil {
		s.publish(EventUsersChanged)
	}
	return err
}

// UpdateUser saves the user's scalar fields and, when mounts != nil, replaces its mounts.
func (s *Store) UpdateUser(u *User, mounts []Mount) error {
	if mounts != nil {
		if err := ValidateMounts(mounts); err != nil {
			return err
		}
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(u).Select("Username", "PasswordHash", "Role", "Disabled", "TokenVersion").Updates(u).Error; err != nil {
			return err
		}
		if mounts == nil {
			return nil
		}
		if err := tx.Where("user_id = ?", u.ID).Delete(&Mount{}).Error; err != nil {
			return err
		}
		for i := range mounts {
			mounts[i].UserID = u.ID
		}
		if len(mounts) > 0 {
			if err := tx.Create(&mounts).Error; err != nil {
				return err
			}
		}
		u.Mounts = mounts
		return nil
	})
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		return ErrConflict
	}
	if err == nil {
		s.publish(EventUsersChanged)
	}
	return err
}

func (s *Store) DeleteUser(id uint) error {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		for _, m := range []any{&Mount{}, &AccessToken{}, &S3Key{}} {
			if err := tx.Where("user_id = ?", id).Delete(m).Error; err != nil {
				return err
			}
		}
		res := tx.Delete(&User{}, id)
		if res.Error == nil && res.RowsAffected == 0 {
			return ErrNotFound
		}
		return res.Error
	})
	if err == nil {
		s.publish(EventUsersChanged)
	}
	return err
}

// AllMountPaths returns the distinct local directories granted to any enabled user.
func (s *Store) AllMountPaths() ([]string, error) {
	var paths []string
	err := s.db.Model(&Mount{}).
		Joins("JOIN users ON users.id = mounts.user_id AND users.disabled = ?", false).
		Distinct().Pluck("mounts.path", &paths).Error
	return paths, err
}

// ---------- access tokens ----------

func (s *Store) CreateToken(t *AccessToken) error {
	t.ScopesRaw = strings.Join(t.Scopes, ",")
	return s.db.Create(t).Error
}

func (s *Store) ListTokens(userID uint) ([]AccessToken, error) {
	var ts []AccessToken
	err := s.db.Where("user_id = ?", userID).Order("id desc").Find(&ts).Error
	return ts, err
}

// GetToken loads one token of a user (owner-scoped, for revealing it).
func (s *Store) GetToken(userID, id uint) (*AccessToken, error) {
	var t AccessToken
	if err := s.db.Where("user_id = ? AND id = ?", userID, id).First(&t).Error; err != nil {
		return nil, notFound(err)
	}
	return &t, nil
}

// GetTokensByPrefix returns every token starting with prefix. The caller
// decodes the stored ciphertext and compares it in full.
func (s *Store) GetTokensByPrefix(prefix string) ([]AccessToken, error) {
	var ts []AccessToken
	err := s.db.Where("prefix = ?", prefix).Find(&ts).Error
	return ts, err
}

func (s *Store) TouchToken(id uint) {
	now := time.Now()
	s.db.Model(&AccessToken{}).Where("id = ?", id).Update("last_used_at", &now)
}

func (s *Store) DeleteToken(userID, id uint) error {
	res := s.db.Where("user_id = ? AND id = ?", userID, id).Delete(&AccessToken{})
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

// ---------- S3 keys ----------

func (s *Store) CreateS3Key(k *S3Key) error { return s.db.Create(k).Error }

func (s *Store) ListS3Keys(userID uint) ([]S3Key, error) {
	var ks []S3Key
	err := s.db.Where("user_id = ?", userID).Order("created_at desc").Find(&ks).Error
	return ks, err
}

func (s *Store) GetS3Key(accessKey string) (*S3Key, error) {
	var k S3Key
	if err := s.db.Where("access_key = ?", accessKey).First(&k).Error; err != nil {
		return nil, notFound(err)
	}
	return &k, nil
}

func (s *Store) DeleteS3Key(userID uint, accessKey string) error {
	res := s.db.Where("user_id = ? AND access_key = ?", userID, accessKey).Delete(&S3Key{})
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}
