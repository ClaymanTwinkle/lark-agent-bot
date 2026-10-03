package config

import (
	"bytes"
	"log"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"
)

// captureSlog sends slog output to the returned buffer until the test ends.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev, prevOut, prevFlags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	// slog.SetDefault also redirected the log package; undo that too.
	t.Cleanup(func() {
		slog.SetDefault(prev)
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	return &buf
}

const projectTOML = `
[[projects]]
name = "demo"

[projects.agent]
type = "claudecode"

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "id"
app_secret = "secret"
`

// A top-level key written below [log], the way config.example.toml used to
// invite, belongs to [log] and was dropped without a word (#12).
func TestLoadWarnsAboutKeyUnderWrongTable(t *testing.T) {
	path := writeConfigFixture(t, `
[log]
level = "info"
idle_timeout_mins = 5
`+projectTOML)
	logs := captureSlog(t)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v (an unknown key must not stop the bot starting)", err)
	}
	if cfg.IdleTimeoutMins != nil {
		t.Fatalf("IdleTimeoutMins = %d, want nil: the key belongs to [log]", *cfg.IdleTimeoutMins)
	}
	out := logs.String()
	for _, want := range []string{
		"config: unknown key ignored",
		"key=log.idle_timeout_mins",
		"file=" + path,
		"idle_timeout_mins is a top-level key",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
}

// Every key here is used, including tables nested in the map[string]any
// option tables, which the decoder does not mark as decoded.
func TestLoadValidConfigDoesNotWarn(t *testing.T) {
	path := writeConfigFixture(t, `
language = "zh-TW"
idle_timeout_mins = 60
workspace_idle_timeout_mins = 0
provider_presets_url = "https://example.com/presets.json"
banned_words = ["x"]

[log]
level = "debug"

[[providers]]
name = "relay"
api_key = "k"
agent_types = ["claudecode"]
endpoints = { codex = "https://x/v1" }
env = { A = "1" }

[providers.codex]
env_key = "RELAY_KEY"
http_headers = { X-Org = "o" }

[display]
mode = "compact"

[outgoing_rate_limit.platforms.feishu]
max_per_second = 5

[tts.agents.demo]
voice_id = "v"

[speech.openai]
api_key = "k"

[[projects]]
name = "demo"
reset_on_idle_mins = 30

[projects.users]
default_role = "member"

[projects.users.roles.member]
user_ids = ["*"]
rate_limit = { max_messages = 10, window_secs = 60 }

[projects.display]
tool_messages = false

[projects.auto_compress]
enabled = true

[projects.agent]
type = "claudecode"
provider_refs = ["relay"]

[projects.agent.options]
cmd = ["claude", "--verbose"]
work_dir = "/tmp/demo"

[projects.agent.options.env]
ANTHROPIC_BASE_URL = "https://example.com"

[projects.agent.options.nested.deeper]
x = 1

[[projects.agent.providers]]
name = "inline"
api_key = "k"

[[projects.agent.providers.models]]
model = "m"
alias = "a"

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "id"
app_secret = "secret"
require_mention = false
mention_map = { BOT-A = "ou_a" }
peer_bots = { cli_b = "BOT-B" }
`)
	logs := captureSlog(t)

	if _, err := Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if strings.Contains(logs.String(), "unknown key") {
		t.Fatalf("valid config logged an unknown key:\n%s", logs.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if unknown, err := decodeConfig(data, &Config{}); err != nil || len(unknown) > 0 {
		t.Fatalf("decodeConfig = %v, %v; want no unknown keys", unknown, err)
	}
}

func TestUnknownKeysListsEachKeyOnce(t *testing.T) {
	data := []byte(`
idle_timout_mins = 5

[auto_compress]
enabled = true
max_tokens = 1

[[projects]]
name = "a"
stray = 1

[projects.agent]
type = "codex"
reset_on_idle_mins = 30

[[projects]]
name = "b"
stray = 2
`)
	got, err := decodeConfig(data, &Config{})
	if err != nil {
		t.Fatalf("decodeConfig: %v", err)
	}
	want := []string{"idle_timout_mins", "auto_compress", "projects.stray", "projects.agent.reset_on_idle_mins"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unknown keys = %q, want %q", got, want)
	}
}

func TestUnknownKeyHint(t *testing.T) {
	cases := map[string]string{
		"log.idle_timeout_mins":                "idle_timeout_mins is a top-level key: put it above the first [table] header",
		"log.banned_words":                     "banned_words is a top-level key: put it above the first [table] header",
		"projects.agent.reset_on_idle_mins":    "did you mean projects.reset_on_idle_mins?",
		"projects.display.reset_on_idle_mins":  "did you mean projects.reset_on_idle_mins?",
		"projects.platforms.admin_from":        "did you mean projects.admin_from?",
		"projects.agent.providers.thinkingg":   "",
		"tts.agents.x.unknown":                 "",
		"idle_timout_mins":                     "",
		"log.no_such_key":                      "",
		"projects.agent.options.anything_else": "",
	}
	for key, want := range cases {
		if got := unknownKeyHint(key); got != want {
			t.Errorf("unknownKeyHint(%q) = %q, want %q", key, got, want)
		}
	}
}

// Saving from the management API, chat commands or the provider CLI rewrites
// the whole file from the Config struct; it must not delete keys the struct
// does not have.
func TestSaveConfigRefusesToDeleteUnknownKeys(t *testing.T) {
	content := `
[log]
level = "info"
idle_timeout_mins = 5
` + projectTOML
	writeTestConfig(t, content)

	err := AddCommand(CommandConfig{Name: "hello", Prompt: "say hello"})
	if err == nil {
		t.Fatal("AddCommand succeeded, want it to refuse to delete log.idle_timeout_mins")
	}
	if !strings.Contains(err.Error(), "log.idle_timeout_mins") {
		t.Fatalf("error = %q, want it to name log.idle_timeout_mins", err)
	}
	data, readErr := os.ReadFile(ConfigPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != content {
		t.Fatalf("config changed although the save was refused:\n%s", data)
	}
}

func TestSaveConfigStillSavesAConfigWithoutUnknownKeys(t *testing.T) {
	writeTestConfig(t, projectTOML)

	if err := AddCommand(CommandConfig{Name: "hello", Prompt: "say hello"}); err != nil {
		t.Fatalf("AddCommand: %v", err)
	}
	cfg := readTestConfig(t)
	if len(cfg.Commands) != 1 || cfg.Commands[0].Name != "hello" {
		t.Fatalf("commands = %+v, want the new command", cfg.Commands)
	}
}

// `config format` works on the text, so it keeps unknown keys; it now also
// reports them.
func TestFormatConfigFileKeepsAndReportsUnknownKeys(t *testing.T) {
	path := writeConfigFixture(t, "[log]\nlevel = \"info\"\nidle_timeout_mins = 5\n\n\n"+projectTOML)
	logs := captureSlog(t)

	if err := FormatConfigFile(path); err != nil {
		t.Fatalf("FormatConfigFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "idle_timeout_mins = 5") {
		t.Fatalf("formatting deleted the unknown key:\n%s", data)
	}
	if !strings.Contains(logs.String(), "key=log.idle_timeout_mins") {
		t.Fatalf("format did not report the unknown key:\n%s", logs.String())
	}
}
