package codex

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func codexExeName() string {
	if runtime.GOOS == "windows" {
		return "codex.exe"
	}
	return "codex"
}

// fakeDesktopCodex creates root/<dir>/codex[.exe] with the given mtime.
func fakeDesktopCodex(t *testing.T, root, dir string, mtime time.Time) string {
	t.Helper()
	path := filepath.Join(root, dir, codexExeName())
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNewestDesktopCodexPicksLatestVersionDirectory(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	fakeDesktopCodex(t, root, "old1111", now.Add(-48*time.Hour))
	want := fakeDesktopCodex(t, root, "new2222", now)
	// A version directory holding other tools but no codex is skipped.
	if err := os.MkdirAll(filepath.Join(root, "tools3333"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := newestDesktopCodex(root); got != want {
		t.Fatalf("newestDesktopCodex() = %q, want %q", got, want)
	}
}

func TestNewestDesktopCodexWithoutInstall(t *testing.T) {
	if got := newestDesktopCodex(filepath.Join(t.TempDir(), "missing")); got != "" {
		t.Fatalf("newestDesktopCodex() = %q, want empty", got)
	}
	if got := newestDesktopCodex(""); got != "" {
		t.Fatalf("newestDesktopCodex(\"\") = %q, want empty", got)
	}
}

func TestResolveCodexBinFallsBackToDesktopApp(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the Codex desktop app layout is only known on Windows")
	}
	localAppData := t.TempDir()
	want := fakeDesktopCodex(t, filepath.Join(localAppData, "OpenAI", "Codex", "bin"), "abc123", time.Now())
	t.Setenv("LOCALAPPDATA", localAppData)
	t.Setenv("PATH", t.TempDir()) // no codex on PATH

	bin, fromDesktop, err := resolveCodexBin("codex")
	if err != nil {
		t.Fatalf("resolveCodexBin() error = %v", err)
	}
	if bin != want || !fromDesktop {
		t.Fatalf("resolveCodexBin() = %q, %v; want %q from the desktop app", bin, fromDesktop, want)
	}
}

func TestResolveCodexBinPrefersPath(t *testing.T) {
	pathDir := t.TempDir()
	onPath := fakeDesktopCodex(t, pathDir, ".", time.Now())
	localAppData := t.TempDir()
	fakeDesktopCodex(t, filepath.Join(localAppData, "OpenAI", "Codex", "bin"), "abc123", time.Now())
	t.Setenv("LOCALAPPDATA", localAppData)
	t.Setenv("PATH", pathDir)

	bin, fromDesktop, err := resolveCodexBin("codex")
	if err != nil {
		t.Fatalf("resolveCodexBin() error = %v", err)
	}
	if fromDesktop || !strings.EqualFold(filepath.Clean(bin), filepath.Clean(onPath)) {
		t.Fatalf("resolveCodexBin() = %q, %v; want %q from PATH", bin, fromDesktop, onPath)
	}
}

func TestResolveCodexBinCustomCommandHasNoFallback(t *testing.T) {
	localAppData := t.TempDir()
	fakeDesktopCodex(t, filepath.Join(localAppData, "OpenAI", "Codex", "bin"), "abc123", time.Now())
	t.Setenv("LOCALAPPDATA", localAppData)
	t.Setenv("PATH", t.TempDir())

	if _, _, err := resolveCodexBin("my-codex-wrapper"); err == nil || !strings.Contains(err.Error(), "my-codex-wrapper") {
		t.Fatalf("resolveCodexBin() error = %v, want not found for a custom command", err)
	}
}

func TestWithBinDirOnPathPrependsToSessionPath(t *testing.T) {
	bin := filepath.Join("C:", "codex", "v2", codexExeName())
	sep := string(os.PathListSeparator)

	env := withBinDirOnPath([]string{"FOO=1", "PATH=/session/bin"}, bin)
	if got := env[len(env)-1]; got != "PATH="+filepath.Dir(bin)+sep+"/session/bin" {
		t.Fatalf("last env entry = %q, want the codex dir before the session PATH", got)
	}

	t.Setenv("PATH", "/process/bin")
	env = withBinDirOnPath(nil, bin)
	if got := env[len(env)-1]; got != "PATH="+filepath.Dir(bin)+sep+"/process/bin" {
		t.Fatalf("last env entry = %q, want the codex dir before the process PATH", got)
	}
}
