package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setTestHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	return dir
}

func TestSanitizeInstance(t *testing.T) {
	tests := []struct {
		raw, want string
		wantErr   bool
	}{
		{raw: "", want: ""},
		{raw: "claude", want: "claude"},
		{raw: "Codex_Bot-2", want: "Codex_Bot-2"},
		{raw: "claude-bot.local", want: "claude-bot-local"},
		{raw: "  my bot  ", want: "my-bot"},
		{raw: "a//b..c", want: "a-b-c"},
		{raw: "--x--", want: "x"},
		{raw: "机器人", wantErr: true},
		{raw: "...", wantErr: true},
		{raw: strings.Repeat("a", 60), want: strings.Repeat("a", maxInstanceLen)},
	}
	for _, tt := range tests {
		got, err := SanitizeInstance(tt.raw)
		if tt.wantErr {
			if err == nil {
				t.Errorf("SanitizeInstance(%q) = %q, want an error", tt.raw, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("SanitizeInstance(%q) = %q, %v; want %q", tt.raw, got, err, tt.want)
		}
	}
}

func TestInstanceFromConfigPath(t *testing.T) {
	tests := map[string]string{
		filepath.Join("bots", "config.toml"):           "",
		filepath.Join("bots", "Config.TOML"):           "",
		filepath.Join("bots", "claude-bot.toml"):       "claude-bot",
		filepath.Join("bots", "claude-bot.local.toml"): "claude-bot-local",
		filepath.Join("bots", "机器人.toml"):              "",
	}
	for path, want := range tests {
		if got := InstanceFromConfigPath(path); got != want {
			t.Errorf("InstanceFromConfigPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestInstanceNamesKeepDefaultsForDefaultInstance(t *testing.T) {
	home := setTestHome(t)
	if got := ServiceNameFor(""); got != "lark-agent-bot" {
		t.Errorf("ServiceNameFor(\"\") = %q", got)
	}
	if got := ServiceNameFor("claude"); got != "lark-agent-bot-claude" {
		t.Errorf("ServiceNameFor(claude) = %q", got)
	}
	logs := filepath.Join(home, ".lark-agent-bot", "logs")
	if got := DefaultLogFileFor(""); got != filepath.Join(logs, "lark-agent-bot.log") {
		t.Errorf("DefaultLogFileFor(\"\") = %q", got)
	}
	if got := DefaultLogFileFor("claude"); got != filepath.Join(logs, "lark-agent-bot-claude.log") {
		t.Errorf("DefaultLogFileFor(claude) = %q", got)
	}
	if got := metaPathFor(""); filepath.Base(got) != "daemon.json" {
		t.Errorf("metaPathFor(\"\") = %q", got)
	}
	if got := metaPathFor("claude"); filepath.Base(got) != "daemon-claude.json" {
		t.Errorf("metaPathFor(claude) = %q", got)
	}
}

func TestMetaIsKeptPerInstance(t *testing.T) {
	setTestHome(t)
	for _, m := range []*Meta{
		{Instance: "", ConfigPath: "default.toml", LogFile: "default.log"},
		{Instance: "codex", ConfigPath: "codex.toml", LogFile: "codex.log"},
		{Instance: "claude", ConfigPath: "claude.toml", LogFile: "claude.log"},
	} {
		if err := SaveMeta(m); err != nil {
			t.Fatalf("SaveMeta(%q): %v", m.Instance, err)
		}
	}

	got, err := LoadMeta("claude")
	if err != nil || got.ConfigPath != "claude.toml" || got.Instance != "claude" {
		t.Fatalf("LoadMeta(claude) = %+v, %v", got, err)
	}

	var order []string
	for _, m := range ListMeta() {
		order = append(order, m.Instance+"="+m.ConfigPath)
	}
	if want := "=default.toml,claude=claude.toml,codex=codex.toml"; strings.Join(order, ",") != want {
		t.Fatalf("ListMeta() = %v, want %s", order, want)
	}

	RemoveMeta("claude")
	if _, err := LoadMeta("claude"); err == nil {
		t.Fatal("LoadMeta(claude) after RemoveMeta succeeded")
	}
	if _, err := LoadMeta(""); err != nil {
		t.Fatalf("RemoveMeta(claude) removed the default instance: %v", err)
	}
}

func TestResolveUsesConfigPathAndInstanceLog(t *testing.T) {
	home := setTestHome(t)
	workDir := t.TempDir()
	configPath := filepath.Join(workDir, "claude.toml")
	if err := os.WriteFile(configPath, []byte(`app_secret = "${RESOLVE_INSTANCE_SECRET}"`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RESOLVE_INSTANCE_SECRET", "s3cret")

	cfg := Config{BinaryPath: "/bin/true", WorkDir: workDir, ConfigPath: configPath, Instance: "claude"}
	if err := Resolve(&cfg); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cfg.EnvExtra["RESOLVE_INSTANCE_SECRET"] != "s3cret" {
		t.Errorf("placeholder in %s not captured; EnvExtra=%v", configPath, cfg.EnvExtra)
	}
	if want := filepath.Join(home, ".lark-agent-bot", "logs", "lark-agent-bot-claude.log"); cfg.LogFile != want {
		t.Errorf("LogFile = %q, want %q", cfg.LogFile, want)
	}

	cfg = Config{BinaryPath: "/bin/true", WorkDir: workDir}
	if err := Resolve(&cfg); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := filepath.Join(workDir, "config.toml"); cfg.ConfigPath != want {
		t.Errorf("default ConfigPath = %q, want %q", cfg.ConfigPath, want)
	}

	// `daemon install --config bot.toml` gives a relative work dir; the
	// service must not depend on the directory it is started in.
	t.Chdir(workDir)
	cfg = Config{BinaryPath: "/bin/true", WorkDir: ".", LogFile: filepath.Join("logs", "bot.log"), ConfigPath: configPath}
	if err := Resolve(&cfg); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cfg.WorkDir != workDir {
		t.Errorf("relative WorkDir resolved to %q, want %q", cfg.WorkDir, workDir)
	}
	if want := filepath.Join(workDir, "logs", "bot.log"); cfg.LogFile != want {
		t.Errorf("relative LogFile resolved to %q, want %q", cfg.LogFile, want)
	}
}
