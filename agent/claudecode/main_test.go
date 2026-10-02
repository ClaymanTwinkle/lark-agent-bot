package claudecode

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(runTestsWithIsolatedHome(m))
}

// Constructors discover plugins and skills. Never traverse the developer's
// real plugin cache (which may contain large node_modules trees) in unit tests.
func runTestsWithIsolatedHome(m *testing.M) (code int) {
	home, err := os.MkdirTemp("", "lark-claude-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() {
		if err := os.RemoveAll(home); err != nil {
			fmt.Fprintln(os.Stderr, "clean test home:", err)
			code = 1
		}
	}()
	for key, value := range map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": ""} {
		if err := os.Setenv(key, value); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	return m.Run()
}

// fakeClaudeCLI returns the path of an empty file New's CLI lookup accepts,
// for tests that construct an agent on hosts without claude. New only
// looks the CLI up, it never runs it. Tests used to set run_as_user to skip
// the lookup, but on Windows run_as_user is not supported and the lookup
// always happens.
func fakeClaudeCLI(t *testing.T) string {
	t.Helper()
	name := "claude"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
