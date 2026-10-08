package main

import (
	"bytes"
	"fmt"
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

// Regression: on a new install `web` stopped at "Config file not found", and
// the config a plain `lark-agent-bot` run creates kept a placeholder work_dir
// that keeps the bot, and so the web admin, from starting.
func TestWebCommand_FirstRunCreatesStartableConfig(t *testing.T) {
	home := isolateHome(t)
	stubWeb(t, true)
	t.Chdir(t.TempDir())
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runWebCommand(t, "--no-browser")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr %q", code, stderr)
	}
	path := filepath.Join(home, ".lark-agent-bot", "config.toml")
	// config.Load is what lark-agent-bot itself starts with.
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("lark-agent-bot cannot load the config web created: %v", err)
	}
	if cfg.Management.Enabled == nil || !*cfg.Management.Enabled {
		t.Error("web did not enable the web admin in the new config")
	}
	if got := cfg.Projects[0].Agent.Options["work_dir"]; got != cwd {
		t.Errorf("work_dir = %v, want the current folder %s", got, cwd)
	}
	for _, want := range []string{path, cwd, "Token: " + cfg.Management.Token} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout %q lacks %q", stdout, want)
		}
	}
	// The starter config's comments are its only guide to the file.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# lark-agent-bot configuration") {
		t.Errorf("the starter config lost its comments:\n%s", data)
	}
}

func TestWebCommand_FillsOnlyStarterWorkDir(t *testing.T) {
	isolateHome(t)
	stubWeb(t, true)
	t.Chdir(t.TempDir())
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	ownDir := t.TempDir()
	project := func(name, workDir string) string {
		return fmt.Sprintf(`
[[projects]]
name = %q

[projects.agent]
type = "claudecode"

[projects.agent.options]
work_dir = %q

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "cli_%s"
app_secret = "secret"
`, name, workDir, name)
	}
	path := filepath.Join(t.TempDir(), "custom.toml")
	content := "language = \"en\"\n" + project("starter", config.StarterWorkDir) + project("own", ownDir)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runWebCommand(t, "--config", path, "--no-browser")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr %q", code, stderr)
	}
	cfg, err := config.LoadPermissive(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"starter": cwd, "own": ownDir}
	for _, proj := range cfg.Projects {
		if got := proj.Agent.Options["work_dir"]; got != want[proj.Name] {
			t.Errorf("project %s: work_dir = %v, want %s", proj.Name, got, want[proj.Name])
		}
	}
	if msg := core.NewI18n(core.LangEnglish).Tf(core.MsgSetupWorkDirFilled, "starter", cwd); !strings.Contains(stdout, msg) {
		t.Errorf("stdout %q lacks %q", stdout, msg)
	}
}

func TestWebCommand_NotBuiltCreatesNoConfig(t *testing.T) {
	home := isolateHome(t)
	stubWeb(t, false)
	t.Chdir(t.TempDir())

	if code, _, _ := runWebCommand(t, "--no-browser"); code == 0 {
		t.Fatal("web succeeded without the web admin in the build")
	}
	if _, err := os.Stat(filepath.Join(home, ".lark-agent-bot", "config.toml")); !os.IsNotExist(err) {
		t.Errorf("web created a config it cannot use: %v", err)
	}
}
