package claudecode

import (
	"fmt"
	"os"
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
