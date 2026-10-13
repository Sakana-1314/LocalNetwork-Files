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
	ErrInvalid  = errors.New("invalid")
)

// invalidError carries a user-facing message and matches ErrInvalid.
type invalidError string

func (e invalidError) Error() string        { return string(e) }
func (e invalidError) Is(target error) bool { return target == ErrInvalid }

func invalidf(format string, args ...any) error { return invalidError(fmt.Sprintf(format, args...)) }

// Event is published after data that other subsystems depend on changes.
type Event int

const (
	// EventUsersChanged covers users, their grants and buckets: anything that
	// changes which directories are exposed to whom.
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
	if err := db.AutoMigrate(&User{}, &Bucket{}, &Grant{}, &AccessToken{}, &S3Key{}); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	s := &Store{db: db}
	if err := s.dropHashedTokens(); err != nil {
		return nil, err
	}
	if err := s.migrateMounts(); err != nil {
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

func (s *Store) usersQuery() *gorm.DB {
	return s.db.Preload("Grants", func(db *gorm.DB) *gorm.DB { return db.Order("id") }).Preload("Grants.Bucket")
}

// resolve fills the derived grant names and the runtime mounts of a loaded user.
func resolve(u *User) {
	u.Mounts = nil
	grants := u.Grants[:0]
	for _, g := range u.Grants {
		if g.Bucket == nil {
			continue
		}
		g.Name = g.Bucket.Name
		grants = append(grants, g)
		ps := g.Bucket.Paths
		if len(ps) == 0 {
			continue
		}
		u.Mounts = append(u.Mounts, Mount{
			BucketID: g.BucketID, Name: g.Bucket.Name, Path: ps[0],
			Mirrors: append([]string(nil), ps[1:]...), Writable: g.Writable,
		})
	}
	for i := range grants {
		grants[i].Bucket = nil
	}
	u.Grants = grants
}

func (s *Store) ListUsers() ([]User, error) {
	var users []User
	err := s.usersQuery().Order("id").Find(&users).Error
	for i := range users {
		resolve(&users[i])
	}
	return users, err
}

func (s *Store) GetUser(id uint) (*User, error) {
	var u User
	if err := s.usersQuery().First(&u, id).Error; err != nil {
		return nil, notFound(err)
	}
	resolve(&u)
	return &u, nil
}

func (s *Store) GetUserByName(name string) (*User, error) {
	var u User
	if err := s.usersQuery().Where("username = ? COLLATE NOCASE", name).First(&u).Error; err != nil {
		return nil, notFound(err)
	}
	resolve(&u)
	return &u, nil
}

// validateGrants checks that every grant names an existing bucket at most once.
func validateGrants(tx *gorm.DB, gs []Grant) error {
	seen := map[uint]bool{}
	for i := range gs {
		g := &gs[i]
		if seen[g.BucketID] {
			return invalidf("桶重复授权: %d", g.BucketID)
		}
		seen[g.BucketID] = true
		var n int64
		if err := tx.Model(&Bucket{}).Where("id = ?", g.BucketID).Count(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			return invalidf("桶 %d 不存在", g.BucketID)
		}
		g.ID, g.Bucket, g.Name = 0, nil, ""
	}
	return nil
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}

func (s *Store) CreateUser(u *User) error {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := validateGrants(tx, u.Grants); err != nil {
			return err
		}
		return tx.Create(u).Error
	})
	if isUnique(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if fresh, err := s.GetUser(u.ID); err == nil {
		*u = *fresh
	}
	s.publish(EventUsersChanged)
	return nil
}

// UpdateUser saves the user's scalar fields and, when grants != nil, replaces its grants.
func (s *Store) UpdateUser(u *User, grants []Grant) error {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(u).Select("Username", "PasswordHash", "Role", "Disabled", "TokenVersion").Updates(u).Error; err != nil {
			return err
		}
		if grants == nil {
			return nil
		}
		if err := validateGrants(tx, grants); err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", u.ID).Delete(&Grant{}).Error; err != nil {
			return err
		}
		for i := range grants {
			grants[i].UserID = u.ID
		}
		if len(grants) > 0 {
			return tx.Create(&grants).Error
		}
		return nil
	})
	if isUnique(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if fresh, err := s.GetUser(u.ID); err == nil {
		*u = *fresh
	}
	s.publish(EventUsersChanged)
	return nil
}

func (s *Store) DeleteUser(id uint) error {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		for _, m := range []any{&Grant{}, &AccessToken{}, &S3Key{}} {
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

// AllMountPaths returns the distinct active directories of every bucket
// granted to an enabled user.
func (s *Store) AllMountPaths() ([]string, error) {
	var bs []Bucket
	err := s.db.Model(&Bucket{}).
		Where("id IN (?)", s.db.Model(&Grant{}).Select("grants.bucket_id").
			Joins("JOIN users ON users.id = grants.user_id AND users.disabled = ?", false)).
		Order("id").Find(&bs).Error
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var paths []string
	for _, b := range bs {
		if len(b.Paths) == 0 {
			continue
		}
		p, _ := Mount{Path: b.Paths[0], Mirrors: b.Paths[1:]}.Active()
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// ---------- buckets ----------

// ValidateBucket normalizes a bucket definition in place.
func ValidateBucket(b *Bucket) error {
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" || b.Name == "." || b.Name == ".." || strings.ContainsAny(b.Name, "/\\\x00") ||
		utf8.RuneCountInString(b.Name) > 128 {
		return invalidf("无效的桶名称 %q", b.Name)
	}
	if len(b.Paths) == 0 {
		return invalidf("桶至少需要一个目录")
	}
	seen := map[string]bool{}
	for i, p := range b.Paths {
		if !filepath.IsAbs(p) || strings.ContainsRune(p, 0) {
			return invalidf("目录必须是绝对路径: %s", p)
		}
		p = filepath.Clean(p)
		if seen[p] {
			return invalidf("目录重复: %s", p)
		}
		seen[p] = true
		b.Paths[i] = p
	}
	for _, p := range b.Paths {
		for _, q := range b.Paths {
			if p != q && within(p, q) {
				return invalidf("目录 %s 位于 %s 之内，不能互为备份", q, p)
			}
		}
	}
	return nil
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (s *Store) ListBuckets() ([]Bucket, error) {
	var bs []Bucket
	err := s.db.Order("id").Find(&bs).Error
	return bs, err
}

func (s *Store) GetBucket(id uint) (*Bucket, error) {
	var b Bucket
	if err := s.db.First(&b, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &b, nil
}

// BucketUsers returns how many users each bucket is granted to.
func (s *Store) BucketUsers() (map[uint]int, error) {
	var rows []struct {
		BucketID uint
		N        int
	}
	err := s.db.Model(&Grant{}).Select("bucket_id, count(*) AS n").Group("bucket_id").Scan(&rows).Error
	out := map[uint]int{}
	for _, r := range rows {
		out[r.BucketID] = r.N
	}
	return out, err
}

// bucketNameTaken compares names case-insensitively: they become directory
// names under the user's virtual root, SMB share names and S3 bucket names.
func bucketNameTaken(tx *gorm.DB, name string, exclude uint) (bool, error) {
	var n int64
	err := tx.Model(&Bucket{}).Where("name = ? COLLATE NOCASE AND id <> ?", name, exclude).Count(&n).Error
	return n > 0, err
}

func (s *Store) CreateBucket(b *Bucket) error {
	if err := ValidateBucket(b); err != nil {
		return err
	}
	b.ID = 0
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if taken, err := bucketNameTaken(tx, b.Name, 0); err != nil {
			return err
		} else if taken {
			return ErrConflict
		}
		return tx.Create(b).Error
	})
	if isUnique(err) {
		return ErrConflict
	}
	if err == nil {
		s.publish(EventUsersChanged)
	}
	return err
}

func (s *Store) UpdateBucket(b *Bucket) error {
	if err := ValidateBucket(b); err != nil {
		return err
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if taken, err := bucketNameTaken(tx, b.Name, b.ID); err != nil {
			return err
		} else if taken {
			return ErrConflict
		}
		res := tx.Model(b).Select("Name", "PathsRaw").Updates(b)
		if res.Error == nil && res.RowsAffected == 0 {
			return ErrNotFound
		}
		return res.Error
	})
	if isUnique(err) {
		return ErrConflict
	}
	if err == nil {
		s.publish(EventUsersChanged)
	}
	return err
}

// DeleteBucket removes the bucket and its grants. Files on disk are untouched.
func (s *Store) DeleteBucket(id uint) error {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("bucket_id = ?", id).Delete(&Grant{}).Error; err != nil {
			return err
		}
		res := tx.Delete(&Bucket{}, id)
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

// legacyMount is the per-user authorized directory used before buckets existed.
type legacyMount struct {
	UserID   uint
	Name     string
	Path     string
	Writable bool
}

// migrateMounts turns the legacy per-user mounts table into buckets and grants:
// every distinct directory becomes a bucket (named after the first mount that
// used it, de-duplicated) and every mount a grant. The old table is dropped
// afterwards so this runs once.
func (s *Store) migrateMounts() error {
	if !s.db.Migrator().HasTable("mounts") {
		return nil
	}
	var ms []legacyMount
	if err := s.db.Raw("SELECT user_id, name, path, writable FROM mounts ORDER BY id").Scan(&ms).Error; err != nil {
		return fmt.Errorf("read legacy mounts: %w", err)
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		byPath := map[string]uint{}
		var existing []Bucket
		if err := tx.Find(&existing).Error; err != nil {
			return err
		}
		for _, b := range existing {
			if len(b.Paths) == 1 {
				byPath[b.Paths[0]] = b.ID
			}
		}
		for _, m := range ms {
			p := filepath.Clean(m.Path)
			id, ok := byPath[p]
			if !ok {
				name := strings.TrimSpace(m.Name)
				for i := 2; ; i++ {
					taken, err := bucketNameTaken(tx, name, 0)
					if err != nil {
						return err
					}
					if !taken {
						break
					}
					name = fmt.Sprintf("%s-%d", strings.TrimSpace(m.Name), i)
				}
				b := &Bucket{Name: name, Paths: []string{p}}
				if err := tx.Create(b).Error; err != nil {
					return err
				}
				id, byPath[p] = b.ID, b.ID
			}
			g := &Grant{UserID: m.UserID, BucketID: id, Writable: m.Writable}
			if err := tx.Where("user_id = ? AND bucket_id = ?", m.UserID, id).
				Assign(Grant{Writable: m.Writable}).FirstOrCreate(g).Error; err != nil {
				return err
			}
		}
		return tx.Exec("DROP TABLE mounts").Error
	})
	if err != nil {
		return fmt.Errorf("migrate legacy mounts: %w", err)
	}
	if len(ms) > 0 {
		slog.Info("store: converted authorized directories to buckets", "mounts", len(ms))
	}
	return nil
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
