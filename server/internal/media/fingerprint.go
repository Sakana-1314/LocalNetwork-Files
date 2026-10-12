package media

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Media caches (thumbnails, converted subtitles, HLS segments, ffprobe
// results) are keyed by the *content* of the source file, never by its path.
// Moving, renaming or re-mounting a file therefore keeps its cache, and two
// identical files share one entry.
//
// Hashing a large video in full would cost as much as transcoding it, so the
// fingerprint only reads the head and the tail of the file (plus its size).
// Collisions require two files of exactly the same size whose first and last
// 64 KiB are identical: negligible for a personal library.
const (
	// fingerprintSmall reads the whole file up to this size.
	fingerprintSmall = 1 << 20 // 1 MiB
	// fingerprintChunk is how much of the head and tail a large file contributes.
	fingerprintChunk = 64 << 10 // 64 KiB
	// fingerprintMemoMax bounds the in-memory fingerprint cache.
	fingerprintMemoMax = 512
)

func hashChunks(h io.Writer, f *os.File, size int64) error {
	buf := make([]byte, fingerprintChunk)
	// Head.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := io.CopyBuffer(h, io.LimitReader(f, fingerprintChunk), buf); err != nil {
		return err
	}
	if size > fingerprintChunk {
		// Tail (may overlap the head for files just above the chunk size).
		start := size - fingerprintChunk
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return err
		}
		if _, err := io.CopyBuffer(h, io.LimitReader(f, fingerprintChunk), buf); err != nil {
			return err
		}
	}
	return nil
}

// fileFingerprint returns a stable content hash of the file at real.
func fileFingerprint(real string, size int64) (string, error) {
	f, err := os.Open(real)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	fmt.Fprintf(h, "%d\x00", size)
	if size <= fingerprintSmall {
		if _, err := io.Copy(h, f); err != nil {
			return "", err
		}
	} else if err := hashChunks(h, f, size); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Service fingerprint memo. It only avoids re-reading file heads; it never
// changes the cache identity, so entries stay valid across restarts and copies.
type fingerprintMemo struct {
	mu    sync.Mutex
	items map[string]string
	order []string
}

func newFingerprintMemo() *fingerprintMemo {
	return &fingerprintMemo{items: map[string]string{}}
}

func (m *fingerprintMemo) get(key string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.items[key]
	return v, ok
}

func (m *fingerprintMemo) put(key, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[key]; ok {
		return
	}
	m.items[key] = value
	m.order = append(m.order, key)
	if len(m.order) > fingerprintMemoMax {
		delete(m.items, m.order[0])
		m.order = m.order[1:]
	}
}

// Fingerprint returns the content fingerprint of a source file. size and
// mtime come from the caller's stat call; mtime only keys the in-memory memo,
// never the cache key itself.
func (s *Service) Fingerprint(real string, size int64, mtime time.Time) (string, error) {
	memoKey := fmt.Sprintf("%s|%d|%d", real, size, mtime.UnixNano())
	if v, ok := s.fpMemo.get(memoKey); ok {
		return v, nil
	}
	v, err := fileFingerprint(real, size)
	if err != nil {
		return "", err
	}
	s.fpMemo.put(memoKey, v)
	return v, nil
}
