package antigravity

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

func TestMirrorDirectoryEntries_PreservesConfigLinks(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	path := filepath.Join(source, "keep.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Windows requires Developer Mode or SeCreateSymbolicLinkPrivilege.
	probe := filepath.Join(t.TempDir(), "probe")
	if err := os.Symlink(path, probe); err != nil {
		if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
			t.Skip("symbolic link privilege is unavailable")
		}
		t.Fatal(err)
	}
	if err := mirrorDirectoryEntries(source, target, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(filepath.Join(target, "keep.json")); err != nil || got != path {
		t.Fatalf("preserved config link = %q, %v; want %q", got, err, path)
	}
}
