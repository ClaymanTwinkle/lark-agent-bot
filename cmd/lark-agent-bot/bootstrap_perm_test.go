//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ClaymanTwinkle/lark-agent-bot/config"
)

// The starter config and the setup config receive the app secret: owner only.
func TestNewConfigFilesAreOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bootstrap", "config.toml")
	if err := bootstrapConfig(path); err != nil {
		t.Fatalf("bootstrapConfig: %v", err)
	}
	assertMode0600(t, path)

	old := config.ConfigPath
	t.Cleanup(func() { config.ConfigPath = old })
	config.ConfigPath = filepath.Join(t.TempDir(), "setup", "config.toml")
	if err := ensureSetupConfig(); err != nil {
		t.Fatalf("ensureSetupConfig: %v", err)
	}
	assertMode0600(t, config.ConfigPath)
}

func assertMode0600(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("%s mode = %o, want 600", path, perm)
	}
}
