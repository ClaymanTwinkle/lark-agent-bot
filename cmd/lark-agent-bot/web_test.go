package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ClaymanTwinkle/lark-agent-bot/config"
	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

const webTestConfig = `language = "en"

[[projects]]
name = "demo"

[projects.agent]
type = "claudecode"

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "cli_test"
app_secret = "secret"
`

// stubWeb makes the web admin look built in or not, and fails the test if
// the command tries to open a browser.
func stubWeb(t *testing.T, available bool) {
	t.Helper()
	oldAvailable, oldOpen := webAssetsAvailable, openWebBrowser
	webAssetsAvailable = func() bool { return available }
	openWebBrowser = func(string) error {
		t.Error("web opened a browser")
		return nil
	}
	t.Cleanup(func() { webAssetsAvailable, openWebBrowser = oldAvailable, oldOpen })
}

func writeWebTestConfig(t *testing.T) (path string, content []byte) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "custom.toml")
	content = []byte(webTestConfig)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, content
}

// runWebCommand runs webCommand and returns its exit code and output.
func runWebCommand(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	stdout = captureStdout(t, func() {
		stderr = captureStderr(t, func() { code = webCommand(args) })
	})
	return code, stdout, stderr
}

func TestParseWebArgs(t *testing.T) {
	opts, err := parseWebArgs([]string{"--config", "a.toml", "-n"})
	if err != nil || opts.configPath != "a.toml" || !opts.noBrowser {
		t.Fatalf("parseWebArgs = %+v, %v", opts, err)
	}
	opts, err = parseWebArgs([]string{"--no-browser"})
	if err != nil || !opts.noBrowser {
		t.Fatalf("parseWebArgs(--no-browser) = %+v, %v", opts, err)
	}
	if _, err := parseWebArgs([]string{"--bogus"}); err == nil {
		t.Fatal("parseWebArgs(--bogus) succeeded")
	}
}

// Regression: `web --help` turned the web admin on, rewrote the config and
// opened a browser.
func TestWebCommand_HelpDoesNotTouchConfig(t *testing.T) {
	isolateHome(t)
	stubWeb(t, true)
	path, before := writeWebTestConfig(t)

	for _, args := range [][]string{{"--help"}, {"-h"}, {"--config", path, "--no-browser", "--help"}} {
		code, stdout, stderr := runWebCommand(t, args...)
		if code != 0 {
			t.Errorf("web %q exit code = %d, stderr %q", args, code, stderr)
		}
		if !strings.Contains(stdout, "Usage: lark-agent-bot web") {
			t.Errorf("web %q printed %q, want the usage", args, stdout)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("web --help changed the config:\n%s", after)
	}
}

func TestWebCommand_UsesConfigFlag(t *testing.T) {
	home := isolateHome(t)
	stubWeb(t, true)
	path, _ := writeWebTestConfig(t)

	code, stdout, stderr := runWebCommand(t, "--config", path, "--no-browser")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr %q", code, stderr)
	}
	cfg, err := config.LoadPermissive(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Management.Enabled == nil || !*cfg.Management.Enabled {
		t.Fatalf("web did not enable the web admin in %s", path)
	}
	if !strings.Contains(stdout, "Token: "+cfg.Management.Token) {
		t.Errorf("stdout %q lacks the token", stdout)
	}
	if _, err := os.Stat(filepath.Join(home, ".lark-agent-bot", "config.toml")); !os.IsNotExist(err) {
		t.Errorf("web touched the default config: %v", err)
	}
}

func TestWebCommand_NotBuiltLeavesConfigAlone(t *testing.T) {
	isolateHome(t)
	stubWeb(t, false)
	path, before := writeWebTestConfig(t)

	code, _, stderr := runWebCommand(t, "--config", path)
	if code == 0 {
		t.Fatal("web succeeded without the web admin in the build")
	}
	if want := core.NewI18n(core.LangEnglish).T(core.MsgCLIWebNotBuilt); !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("web changed the config:\n%s", after)
	}
}
