//go:build windows

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/ClaymanTwinkle/lark-agent-bot/config"
)

// The starter config and the setup config receive the app secret: their DACL
// must be protected (not inherited from the directory) and owner-only.
func TestNewConfigFilesAreOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bootstrap", "config.toml")
	if err := bootstrapConfig(path); err != nil {
		t.Fatalf("bootstrapConfig: %v", err)
	}
	assertProtectedDACL(t, path)

	old := config.ConfigPath
	t.Cleanup(func() { config.ConfigPath = old })
	config.ConfigPath = filepath.Join(t.TempDir(), "setup", "config.toml")
	if err := ensureSetupConfig(); err != nil {
		t.Fatalf("ensureSetupConfig: %v", err)
	}
	assertProtectedDACL(t, config.ConfigPath)
}

func assertProtectedDACL(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo(%s): %v", path, err)
	}
	got := sd.String()
	if !strings.HasPrefix(got, "D:P") {
		t.Errorf("%s: DACL is not protected: %s", path, got)
	}
	for _, broad := range []string{";;;AU)", ";;;BU)", ";;;WD)"} {
		if strings.Contains(got, broad) {
			t.Errorf("%s: DACL grants a broad group: %s", path, got)
		}
	}
}
