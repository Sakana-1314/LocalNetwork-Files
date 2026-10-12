package config

import (
	"path/filepath"
	"testing"
)

func TestLegacyEnvNamesStillWork(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MLIST_LISTEN", "")
	t.Setenv("MLIST_DATA_DIR", "")
	t.Setenv("LNF_LISTEN", ":19999")
	t.Setenv("LNF_DATA_DIR", filepath.Join(dir, "legacy"))

	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":19999" {
		t.Fatalf("listen = %q", c.Listen)
	}
	if c.DataDir != filepath.Join(dir, "legacy") {
		t.Fatalf("data dir = %q", c.DataDir)
	}

	// The new names win when both are set.
	t.Setenv("MLIST_LISTEN", ":18888")
	c2, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c2.Listen != ":18888" {
		t.Fatalf("listen = %q", c2.Listen)
	}
}
