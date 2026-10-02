package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ClaymanTwinkle/lark-agent-bot/daemon"
)

func TestParseDaemonInstallArgs_ConfigSetsWorkDir(t *testing.T) {
	cfg, force, err := parseDaemonInstallArgs([]string{"--config", "/tmp/example/config.toml"})
	if err != nil {
		t.Fatalf("parseDaemonInstallArgs returned error: %v", err)
	}
	if force {
		t.Fatalf("force = true, want false")
	}

	want := filepath.Clean("/tmp/example")
	if cfg.WorkDir != want {
		t.Fatalf("cfg.WorkDir = %q, want %q", cfg.WorkDir, want)
	}
}

func TestParseDaemonInstallArgs_ConfigEqualsFormSetsWorkDir(t *testing.T) {
	cfg, _, err := parseDaemonInstallArgs([]string{"--config=/tmp/example/config.toml"})
	if err != nil {
		t.Fatalf("parseDaemonInstallArgs returned error: %v", err)
	}

	want := filepath.Clean("/tmp/example")
	if cfg.WorkDir != want {
		t.Fatalf("cfg.WorkDir = %q, want %q", cfg.WorkDir, want)
	}
}

func TestParseDaemonInstallArgs_NoCaptureSecretsFlag(t *testing.T) {
	os.Unsetenv("CC_DAEMON_NO_CAPTURE_SECRETS")

	cfg, _, err := parseDaemonInstallArgs([]string{"--no-capture-secrets"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !cfg.NoCaptureSecrets {
		t.Fatal("flag should set NoCaptureSecrets=true")
	}

	cfg2, _, err := parseDaemonInstallArgs(nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg2.NoCaptureSecrets {
		t.Fatal("default must be false when flag and env are unset")
	}
}

func TestParseDaemonInstallArgs_NoCaptureSecretsEnv(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "yes", "on"} {
		t.Run("truthy="+v, func(t *testing.T) {
			t.Setenv("CC_DAEMON_NO_CAPTURE_SECRETS", v)
			cfg, _, err := parseDaemonInstallArgs(nil)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if !cfg.NoCaptureSecrets {
				t.Fatalf("env=%q should opt out", v)
			}
		})
	}
	for _, v := range []string{"0", "false", "", "no", "off"} {
		t.Run("falsy="+v, func(t *testing.T) {
			t.Setenv("CC_DAEMON_NO_CAPTURE_SECRETS", v)
			cfg, _, err := parseDaemonInstallArgs(nil)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if cfg.NoCaptureSecrets {
				t.Fatalf("env=%q should NOT opt out", v)
			}
		})
	}
}

func TestParseDaemonInstallArgs_NoCaptureSecretsFlagAndEnvCombine(t *testing.T) {
	// OR semantics: env=truthy + flag=present → still true.
	t.Setenv("CC_DAEMON_NO_CAPTURE_SECRETS", "1")
	cfg, _, err := parseDaemonInstallArgs([]string{"--no-capture-secrets", "--force"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !cfg.NoCaptureSecrets {
		t.Fatal("flag+env both should leave NoCaptureSecrets=true")
	}
	// env=truthy without flag → still true.
	cfg2, _, err := parseDaemonInstallArgs([]string{"--force"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !cfg2.NoCaptureSecrets {
		t.Fatal("env=1 alone should opt out")
	}
}

func TestParseDaemonInstallArgs_WorkDirOverridesConfig(t *testing.T) {
	overrideDir := t.TempDir()
	cfg, force, err := parseDaemonInstallArgs([]string{
		"--config", "/tmp/example/config.toml",
		"--work-dir", overrideDir,
		"--force",
	})
	if err != nil {
		t.Fatalf("parseDaemonInstallArgs returned error: %v", err)
	}
	if !force {
		t.Fatalf("force = false, want true")
	}

	want := overrideDir
	if cfg.WorkDir != want {
		t.Fatalf("cfg.WorkDir = %q, want %q", cfg.WorkDir, want)
	}
}

func TestParseDaemonInstallArgs_ConfigAndName(t *testing.T) {
	overrideDir := t.TempDir()
	// --work-dir wins over the config's directory whichever comes first.
	cfg, _, err := parseDaemonInstallArgs([]string{
		"--work-dir", overrideDir,
		"--config=/tmp/bots/claude.local.toml",
		"--name", "Claude Bot",
	})
	if err != nil {
		t.Fatalf("parseDaemonInstallArgs: %v", err)
	}
	if cfg.ConfigPath != "/tmp/bots/claude.local.toml" {
		t.Errorf("ConfigPath = %q", cfg.ConfigPath)
	}
	if cfg.WorkDir != overrideDir {
		t.Errorf("WorkDir = %q, want %q", cfg.WorkDir, overrideDir)
	}
	if cfg.Instance != "Claude-Bot" {
		t.Errorf("Instance = %q, want the sanitized --name", cfg.Instance)
	}

	if _, _, err := parseDaemonInstallArgs([]string{"--name", "..."}); err == nil {
		t.Error("an unusable --name should be rejected")
	}
}

func TestParseInstanceSelector(t *testing.T) {
	setDaemonTestHome(t)
	tests := []struct {
		args         []string
		wantInstance string
		wantRest     []string
	}{
		{args: nil, wantInstance: ""},
		{args: []string{"--force"}, wantInstance: "", wantRest: []string{"--force"}},
		{args: []string{"--name", "claude", "--force"}, wantInstance: "claude", wantRest: []string{"--force"}},
		{args: []string{"-f", "--name=codex"}, wantInstance: "codex", wantRest: []string{"-f"}},
		{args: []string{"--config", filepath.Join("bots", "claude.toml")}, wantInstance: "claude"},
		{args: []string{"--config=" + filepath.Join("bots", "config.toml")}, wantInstance: ""},
	}
	for _, tt := range tests {
		instance, rest, err := parseInstanceSelector(tt.args)
		if err != nil {
			t.Errorf("parseInstanceSelector(%q): %v", tt.args, err)
			continue
		}
		if instance != tt.wantInstance || strings.Join(rest, " ") != strings.Join(tt.wantRest, " ") {
			t.Errorf("parseInstanceSelector(%q) = %q, %q; want %q, %q", tt.args, instance, rest, tt.wantInstance, tt.wantRest)
		}
	}
	if _, _, err := parseInstanceSelector([]string{"--name"}); err == nil {
		t.Error("--name without a value should be rejected")
	}
}

func TestParseInstanceSelector_ConfigFindsInstalledInstance(t *testing.T) {
	setDaemonTestHome(t)
	configPath := filepath.Join(t.TempDir(), "claude.toml")
	if err := daemon.SaveMeta(&daemon.Meta{Instance: "work", ConfigPath: configPath}); err != nil {
		t.Fatal(err)
	}
	// Installed with --name work: --config must find that instance, not
	// the "claude" its file name would give.
	instance, _, err := parseInstanceSelector([]string{"--config", configPath})
	if err != nil || instance != "work" {
		t.Fatalf("parseInstanceSelector(--config) = %q, %v; want work", instance, err)
	}
}

func TestResolveDaemonConfigPath(t *testing.T) {
	setDaemonTestHome(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "claude.toml")
	if err := os.WriteFile(configPath, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := daemon.Config{ConfigPath: configPath}
	if err := resolveDaemonConfigPath(&cfg); err != nil || cfg.ConfigPath != configPath {
		t.Fatalf("explicit config: %q, %v", cfg.ConfigPath, err)
	}

	cfg = daemon.Config{ConfigPath: filepath.Join(dir, "missing.toml")}
	if err := resolveDaemonConfigPath(&cfg); err == nil {
		t.Fatal("a missing --config should be an error")
	}

	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg = daemon.Config{WorkDir: dir}
	if err := resolveDaemonConfigPath(&cfg); err != nil || cfg.ConfigPath != filepath.Join(dir, "config.toml") {
		t.Fatalf("work dir config: %q, %v", cfg.ConfigPath, err)
	}

	cfg = daemon.Config{WorkDir: t.TempDir()}
	if err := resolveDaemonConfigPath(&cfg); err == nil {
		t.Fatal("no config.toml in the work dir or home should be an error")
	}
}

func TestInstanceUsingConfig(t *testing.T) {
	metas := []*daemon.Meta{
		{Instance: "", ConfigPath: filepath.Join("bots", "config.toml")},
		{Instance: "claude", ConfigPath: filepath.Join("bots", "claude.toml")},
	}
	if got := instanceUsingConfig(metas, filepath.Join("bots", "claude.toml"), "work"); got == nil || got.Instance != "claude" {
		t.Fatalf("instanceUsingConfig() = %+v, want the claude instance", got)
	}
	if got := instanceUsingConfig(metas, filepath.Join("bots", "claude.toml"), "claude"); got != nil {
		t.Fatalf("reinstalling the same instance is not a conflict, got %+v", got)
	}
	if got := instanceUsingConfig(metas, filepath.Join("bots", "codex.toml"), "codex"); got != nil {
		t.Fatalf("a new config is not a conflict, got %+v", got)
	}
}

func setDaemonTestHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}
