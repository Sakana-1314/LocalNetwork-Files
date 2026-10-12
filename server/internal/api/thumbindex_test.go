package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestThumbIndexResolvesAndExpires(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "a.jpg")
	if err := os.WriteFile(real, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(real)
	if err != nil {
		t.Fatal(err)
	}

	idx := newThumbIndex(4)
	idx.Put("d1", real, fi.Size(), fi.ModTime())
	if got, ok := idx.Lookup("d1"); !ok || got != real {
		t.Fatalf("lookup = %q %v", got, ok)
	}

	// An unknown digest is simply a miss.
	if _, ok := idx.Lookup("nope"); ok {
		t.Fatal("unknown digest resolved")
	}

	// Changing the file on disk invalidates the mapping, so a stale digest
	// cannot serve a preview of replaced content.
	if err := os.WriteFile(real, []byte("hello world"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := idx.Lookup("d1"); ok {
		t.Fatal("stale digest resolved after the file changed")
	}
	if idx.len() != 0 {
		t.Fatalf("stale entry kept: %d", idx.len())
	}
}

func TestThumbIndexEvictsLeastRecentlyUsed(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "a.jpg")
	if err := os.WriteFile(real, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(real)

	idx := newThumbIndex(2)
	idx.Put("old", real, fi.Size(), fi.ModTime())
	idx.Put("hot", real, fi.Size(), fi.ModTime())
	if _, ok := idx.Lookup("old"); !ok { // "old" becomes most recently used
		t.Fatal("old missing")
	}
	idx.Put("new", real, fi.Size(), fi.ModTime()) // evicts "hot", not "old"

	if _, ok := idx.Lookup("hot"); ok {
		t.Fatal("least recently used entry survived")
	}
	if _, ok := idx.Lookup("old"); !ok {
		t.Fatal("recently used entry was evicted")
	}
	if idx.len() != 2 {
		t.Fatalf("size = %d", idx.len())
	}
}

func TestThumbIndexIgnoresEmptyKeys(t *testing.T) {
	idx := newThumbIndex(2)
	idx.Put("", "/tmp/x", 1, time.Now())
	idx.Put("d", "", 1, time.Now())
	if idx.len() != 0 {
		t.Fatalf("empty keys stored: %d", idx.len())
	}
}
