//go:build windows

package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPermissionHookShell_FindsGitBashOutsidePATH(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"cmd/git.exe", "bin/bash.exe"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", filepath.Join(root, "cmd"))
	t.Setenv("CLAUDE_CODE_GIT_BASH_PATH", "")
	got, err := permissionHookShell()
	want := filepath.Join(root, "bin", "bash.exe")
	if err != nil || got != want {
		t.Fatalf("shell = %q, %v; want %q", got, err, want)
	}
	t.Setenv("CLAUDE_CODE_GIT_BASH_PATH", filepath.Join(root, "missing.exe"))
	if _, err := permissionHookShell(); err == nil {
		t.Fatal("invalid explicit shell must return an error")
	}
	t.Setenv("CLAUDE_CODE_GIT_BASH_PATH", want)
	t.Setenv("PATH", "")
	if got, err := permissionHookShell(); err != nil || got != want {
		t.Fatalf("explicit shell = %q, %v", got, err)
	}
}
