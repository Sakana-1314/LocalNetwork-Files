// Package auth implements password hashing, login sessions (JWT), API access
// tokens and S3 credential encryption.
package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/store"
)

var (
	ErrInvalidCredentials = errors.New("用户名或密码错误")
	ErrUnauthorized       = errors.New("未登录或登录已过期")
	ErrDisabled           = errors.New("账户已被禁用")
)

const TokenPrefix = "lnf_"

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-password"), bcrypt.DefaultCost)

// Principal is the authenticated identity of a request.
type Principal struct {
	User *store.User
	// ReadOnly is set when authenticating with a token lacking the write scope.
	ReadOnly bool
	// Via is "session", "token", "password" or "s3".
	Via string
}

type Service struct {
	store  *store.Store
	secret []byte
	ttl    time.Duration

	// OnPassword is called whenever a user's plaintext password is known to be
	// valid (successful login or password change), e.g. to sync it to Samba.
	OnPassword func(u *store.User, password string)
}

func (s *Service) passwordSeen(u *store.User, pw string) {
	if s.OnPassword != nil {
		go s.OnPassword(u, pw)
	}
}

// SetPassword changes a user's password, invalidating existing login sessions.
func (s *Service) SetPassword(u *store.User, pw string) error {
	h, err := HashPassword(pw)
	if err != nil {
		return err
	}
	u.PasswordHash = h
	u.TokenVersion++
	if err := s.store.UpdateUser(u, nil); err != nil {
		return err
	}
	s.passwordSeen(u, pw)
	return nil
}

func New(st *store.Store, dataDir string, ttl time.Duration) (*Service, error) {
	secret, err := loadOrCreateSecret(filepath.Join(dataDir, "secret.key"))
	if err != nil {
		return nil, err
	}
	return &Service{store: st, secret: secret, ttl: ttl}, nil
}

func loadOrCreateSecret(path string) ([]byte, error) {
	if b, err := os.ReadFile(path); err == nil && len(b) >= 32 {
		return b, nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return nil, fmt.Errorf("write secret: %w", err)
	}
	return b, nil
}

// hashCost is the bcrypt cost for new password hashes (lowered in tests).
var hashCost = bcrypt.DefaultCost

func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), hashCost)
	return string(b), err
}

func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// EnsureAdmin creates the bootstrap admin when no user exists.
func (s *Service) EnsureAdmin(username, password string) (bool, error) {
	n, err := s.store.CountUsers()
	if err != nil || n > 0 {
		return false, err
	}
	h, err := HashPassword(password)
	if err != nil {
		return false, err
	}
	return true, s.store.CreateUser(&store.User{Username: username, PasswordHash: h, Role: store.RoleAdmin})
}

// Authenticate verifies a username/password pair.
func (s *Service) Authenticate(username, password string) (*store.User, error) {
	u, err := s.store.GetUserByName(username)
	if err != nil {
		// Keep timing roughly constant for unknown users.
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return nil, ErrInvalidCredentials
	}
	if !CheckPassword(u.PasswordHash, password) {
		return nil, ErrInvalidCredentials
	}
	if u.Disabled {
		return nil, ErrDisabled
	}
	s.passwordSeen(u, password)
	return u, nil
}

type sessionClaims struct {
	UID uint `json:"uid"`
	Ver uint `json:"ver"`
	jwt.RegisteredClaims
}

// IssueSession creates a signed login session token.
func (s *Service) IssueSession(u *store.User) (string, time.Time, error) {
	exp := time.Now().Add(s.ttl)
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, sessionClaims{
		UID: u.ID, Ver: u.TokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(exp), IssuedAt: jwt.NewNumericDate(time.Now())},
	})
	str, err := tok.SignedString(s.secret)
	return str, exp, err
}

// Resolve authenticates a bearer value which may be a session JWT or an API token.
func (s *Service) Resolve(token string) (*Principal, error) {
	if token == "" {
		return nil, ErrUnauthorized
	}
	if strings.HasPrefix(token, TokenPrefix) {
		return s.resolveAPIToken(token)
	}
	var c sessionClaims
	_, err := jwt.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return s.secret, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil {
		return nil, ErrUnauthorized
	}
	u, err := s.store.GetUser(c.UID)
	if err != nil || u.TokenVersion != c.Ver {
		return nil, ErrUnauthorized
	}
	if u.Disabled {
		return nil, ErrDisabled
	}
	return &Principal{User: u, Via: "session"}, nil
}

// resolveAPIToken finds the token by its clear-text prefix and confirms it by
// decrypting the stored copy. Tokens are never hashed, so they can be shown to
// their owner again at any time.
func (s *Service) resolveAPIToken(token string) (*Principal, error) {
	if len(token) < len(TokenPrefix)+6 {
		return nil, ErrUnauthorized
	}
	cands, err := s.store.GetTokensByPrefix(token[:len(TokenPrefix)+6])
	if err != nil || len(cands) == 0 {
		return nil, ErrUnauthorized
	}
	var t *store.AccessToken
	for i := range cands {
		plain, err := s.decrypt(cands[i].TokenEnc)
		if err != nil {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(plain), []byte(token)) == 1 {
			t = &cands[i]
			break
		}
	}
	if t == nil {
		return nil, ErrUnauthorized
	}
	if t.ExpiresAt != nil && time.Now().After(*t.ExpiresAt) {
		return nil, ErrUnauthorized
	}
	u, err := s.store.GetUser(t.UserID)
	if err != nil {
		return nil, ErrUnauthorized
	}
	if u.Disabled {
		return nil, ErrDisabled
	}
	if t.LastUsedAt == nil || time.Since(*t.LastUsedAt) > time.Minute {
		s.store.TouchToken(t.ID)
	}
	return &Principal{User: u, ReadOnly: !t.HasScope(store.ScopeWrite), Via: "token"}, nil
}

// CreateToken generates a new API token and stores it encrypted.
func (s *Service) CreateToken(userID uint, name string, scopes []string, expiresInDays int) (*store.AccessToken, string, error) {
	plain := TokenPrefix + randomString(40)
	valid := []string{}
	for _, sc := range scopes {
		if (sc == store.ScopeRead || sc == store.ScopeWrite) && !contains(valid, sc) {
			valid = append(valid, sc)
		}
	}
	if !contains(valid, store.ScopeRead) {
		valid = append([]string{store.ScopeRead}, valid...)
	}
	enc, err := s.encrypt(plain)
	if err != nil {
		return nil, "", err
	}
	t := &store.AccessToken{
		UserID: userID, Name: strings.TrimSpace(name), Prefix: plain[:len(TokenPrefix)+6],
		TokenEnc: enc, Scopes: valid,
	}
	if expiresInDays > 0 {
		exp := time.Now().Add(time.Duration(expiresInDays) * 24 * time.Hour)
		t.ExpiresAt = &exp
	}
	if err := s.store.CreateToken(t); err != nil {
		return nil, "", err
	}
	return t, plain, nil
}

// TokenSecret returns the plaintext of one of the user's own tokens.
func (s *Service) TokenSecret(userID, id uint) (string, error) {
	t, err := s.store.GetToken(userID, id)
	if err != nil {
		return "", err
	}
	return s.decrypt(t.TokenEnc)
}

// S3KeySecret returns the secret of one of the user's own S3 keys.
func (s *Service) S3KeySecret(userID uint, accessKey string) (string, error) {
	k, err := s.store.GetS3Key(accessKey)
	if err != nil {
		return "", err
	}
	if k.UserID != userID {
		return "", store.ErrNotFound
	}
	return s.decrypt(k.SecretEnc)
}

// CreateS3Key generates an S3 access key pair, storing the secret encrypted.
func (s *Service) CreateS3Key(userID uint, name string, readOnly bool) (*store.S3Key, string, error) {
	access := "LNF" + strings.ToUpper(randomString(17))
	secret := randomString(40)
	enc, err := s.encrypt(secret)
	if err != nil {
		return nil, "", err
	}
	k := &store.S3Key{AccessKey: access, UserID: userID, Name: strings.TrimSpace(name), SecretEnc: enc, ReadOnly: readOnly}
	if err := s.store.CreateS3Key(k); err != nil {
		return nil, "", err
	}
	return k, secret, nil
}

// LookupS3Key returns the key owner and decrypted secret.
func (s *Service) LookupS3Key(accessKey string) (*store.S3Key, *store.User, string, error) {
	k, err := s.store.GetS3Key(accessKey)
	if err != nil {
		return nil, nil, "", err
	}
	secret, err := s.decrypt(k.SecretEnc)
	if err != nil {
		return nil, nil, "", err
	}
	u, err := s.store.GetUser(k.UserID)
	if err != nil {
		return nil, nil, "", err
	}
	if u.Disabled {
		return nil, nil, "", ErrDisabled
	}
	return k, u, secret, nil
}

func (s *Service) aead() (cipher.AEAD, error) {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte("lnf-s3-secret"))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *Service) encrypt(plain string) (string, error) {
	g, err := s.aead()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, g.NonceSize())
	rand.Read(nonce)
	return base64.StdEncoding.EncodeToString(g.Seal(nonce, nonce, []byte(plain), nil)), nil
}

func (s *Service) decrypt(enc string) (string, error) {
	g, err := s.aead()
	if err != nil {
		return "", err
	}
	b, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(b) < g.NonceSize() {
		return "", errors.New("corrupt secret")
	}
	out, err := g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], nil)
	return string(out), err
}

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func randomString(n int) string {
	b := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range b {
		v, _ := rand.Int(rand.Reader, max)
		b[i] = alphabet[v.Int64()]
	}
	return string(b)
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
