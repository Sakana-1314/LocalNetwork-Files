package auth

import (
	"crypto/sha256"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Basic authenticates protocol clients (WebDAV) that send HTTP Basic
// credentials on every request. The password may be the login password or an
// API access token. Successful password checks are cached briefly because
// bcrypt is deliberately slow and clients issue many requests.
type Basic struct {
	svc   *Service
	mu    sync.Mutex
	cache map[[32]byte]basicEntry
}

type basicEntry struct {
	userID  uint
	pwHash  string
	expires time.Time
}

const basicCacheTTL = 5 * time.Minute

func NewBasic(svc *Service) *Basic {
	return &Basic{svc: svc, cache: map[[32]byte]basicEntry{}}
}

// FromRequest authenticates Basic or Bearer credentials.
func (b *Basic) FromRequest(r *http.Request) (*Principal, error) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return b.svc.Resolve(strings.TrimSpace(h[7:]))
	}
	user, pass, ok := r.BasicAuth()
	if !ok {
		return nil, ErrUnauthorized
	}
	return b.Check(user, pass)
}

// Check verifies a username with a password or access token.
func (b *Basic) Check(user, pass string) (*Principal, error) {
	if strings.HasPrefix(pass, TokenPrefix) {
		p, err := b.svc.Resolve(pass)
		if err != nil {
			return nil, err
		}
		if user != "" && !strings.EqualFold(user, p.User.Username) {
			return nil, ErrUnauthorized
		}
		return p, nil
	}
	key := sha256.Sum256(append(append([]byte(user), 0), append([]byte(pass), b.svc.secret...)...))
	b.mu.Lock()
	e, hit := b.cache[key]
	b.mu.Unlock()
	if hit && time.Now().Before(e.expires) {
		u, err := b.svc.store.GetUser(e.userID)
		if err == nil && !u.Disabled && u.PasswordHash == e.pwHash {
			return &Principal{User: u, Via: "password"}, nil
		}
	}
	u, err := b.svc.Authenticate(user, pass)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	if len(b.cache) > 1024 {
		now := time.Now()
		for k, v := range b.cache {
			if now.After(v.expires) {
				delete(b.cache, k)
			}
		}
	}
	b.cache[key] = basicEntry{userID: u.ID, pwHash: u.PasswordHash, expires: time.Now().Add(basicCacheTTL)}
	b.mu.Unlock()
	return &Principal{User: u, Via: "password"}, nil
}
