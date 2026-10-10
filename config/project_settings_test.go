package config

import (
	"os"
	"strings"
	"testing"
)

const projectSettingsFixture = `# top comment
language = "en" # interface language

# global providers
[[providers]]
name = "relay"
agent_types = ["claudecode"]

[[projects]]
name = "alpha"
# alpha keeps this comment
admin_from = "boss" # who may run admin commands
disabled_commands = [
  "shell", # risky
  "upgrade",
]

# agent settings
[projects.agent]
type = "claudecode"
provider_refs = ["relay"]

[projects.agent.options]
work_dir = "/srv/alpha" # where alpha works
mode = "default"
provider = "relay"

# feishu bot
[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "cli_x" # app
allow_from = "boss"

[[projects]]
name = "beta"
# beta comment

[projects.agent]
type = "codex"

[projects.agent.options]
work_dir = "/srv/beta"

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "cli_y"
`

func betaBlock(t *testing.T, raw string) string {
	t.Helper()
	i := strings.Index(raw, "name = \"beta\"")
	if i < 0 {
		t.Fatalf("beta project missing:\n%s", raw)
	}
	return raw[i:]
}

func TestSaveProjectSettings_KeepsComments(t *testing.T) {
	configPath := writeConfigFixture(t, projectSettingsFixture)
	patchConfigPath(t, configPath)
	beta := betaBlock(t, projectSettingsFixture)

	lang, admins, workDir, mode := "zh", "boss,ou_ann", "/srv/alpha2", "plan"
	yes, no := true, false
	err := SaveProjectSettings("alpha", ProjectSettingsUpdate{
		Language:             &lang,
		AdminFrom:            &admins,
		DisabledCommands:     []string{"restart"},
		WorkDir:              &workDir,
		Mode:                 &mode,
		ShowContextIndicator: &no,
		ShowWorkdirIndicator: &yes,
		ReplyFooter:          &no,
		InjectSender:         &yes,
		PlatformAllowFrom:    map[string]string{"Feishu": "boss,ou_ann"},
	})
	if err != nil {
		t.Fatalf("SaveProjectSettings: %v", err)
	}

	raw, _ := os.ReadFile(configPath)
	for _, comment := range []string{
		"# top comment", "# interface language", "# global providers", "# alpha keeps this comment",
		"# who may run admin commands", "# agent settings", "# where alpha works", "# feishu bot", "# app", "# beta comment",
	} {
		if !strings.Contains(string(raw), comment) {
			t.Fatalf("comment %q was lost:\n%s", comment, raw)
		}
	}
	if got := betaBlock(t, string(raw)); got != beta {
		t.Fatalf("another project changed:\n%s", got)
	}

	cfg := readConfigFixture(t, configPath)
	p := cfg.Projects[0]
	switch {
	case cfg.Language != "zh", p.AdminFrom != "boss,ou_ann":
		t.Fatalf("language %q, admin_from %q", cfg.Language, p.AdminFrom)
	case len(p.DisabledCommands) != 1 || p.DisabledCommands[0] != "restart":
		t.Fatalf("the multi-line disabled_commands must be replaced, got %v", p.DisabledCommands)
	case stringMapValue(p.Agent.Options, "work_dir") != workDir, stringMapValue(p.Agent.Options, "mode") != mode:
		t.Fatalf("options = %v", p.Agent.Options)
	case *p.ShowContextIndicator || !*p.ShowWorkdirIndicator || *p.ReplyFooter || !*p.InjectSender:
		t.Fatalf("indicators = %v %v %v %v", *p.ShowContextIndicator, *p.ShowWorkdirIndicator, *p.ReplyFooter, *p.InjectSender)
	case stringMapValue(p.Platforms[0].Options, "allow_from") != "boss,ou_ann":
		t.Fatalf("allow_from = %v", p.Platforms[0].Options["allow_from"])
	}
}

func TestSaveProjectSettings_RemovesAndCreatesKeys(t *testing.T) {
	configPath := writeConfigFixture(t, `[[projects]]
name = "alpha"

[projects.agent]
type = "claudecode"

[projects.agent.options]
work_dir = "/srv/alpha" # only option

[[projects.platforms]]
type = "feishu"
`)
	patchConfigPath(t, configPath)

	empty := ""
	err := SaveProjectSettings("alpha", ProjectSettingsUpdate{
		WorkDir:           &empty,
		DisabledCommands:  []string{},
		PlatformAllowFrom: map[string]string{"feishu": "boss"},
	})
	if err != nil {
		t.Fatalf("SaveProjectSettings: %v", err)
	}
	raw, _ := os.ReadFile(configPath)
	cfg := readConfigFixture(t, configPath)
	p := cfg.Projects[0]
	if _, ok := p.Agent.Options["work_dir"]; ok {
		t.Fatalf("an empty work_dir must remove the key:\n%s", raw)
	}
	// The options table is left empty; writing drops the empty header.
	if strings.Contains(string(raw), "[projects.agent.options]") {
		t.Fatalf("the emptied options table should be dropped:\n%s", raw)
	}
	if !strings.Contains(string(raw), "disabled_commands = []") {
		t.Fatalf("an empty list must be written as []:\n%s", raw)
	}
	if stringMapValue(p.Platforms[0].Options, "allow_from") != "boss" {
		t.Fatalf("a missing platform options table must be created for allow_from:\n%s", raw)
	}
}

func TestSaveProjectSettings_AgentTypeDropsIncompatibleProvider(t *testing.T) {
	configPath := writeConfigFixture(t, projectSettingsFixture)
	patchConfigPath(t, configPath)

	codex := "codex"
	if err := SaveProjectSettings("alpha", ProjectSettingsUpdate{AgentType: &codex}); err != nil {
		t.Fatalf("SaveProjectSettings: %v", err)
	}
	raw, _ := os.ReadFile(configPath)
	p := readConfigFixture(t, configPath).Projects[0]
	if p.Agent.Type != "codex" || len(p.Agent.ProviderRefs) != 0 {
		t.Fatalf("type %q, provider_refs %v; want codex without the claudecode-only relay", p.Agent.Type, p.Agent.ProviderRefs)
	}
	if _, ok := p.Agent.Options["provider"]; ok {
		t.Fatal("the active provider was removed with its ref and must be cleared")
	}
	if !strings.Contains(string(raw), "# agent settings") || !strings.Contains(string(raw), "# where alpha works") {
		t.Fatalf("comments were lost:\n%s", raw)
	}
}

func TestSaveProjectSettings_KeepsUnknownKeys(t *testing.T) {
	// saveConfig refuses to rewrite a file with keys this version does not
	// know; editing lines keeps them and needs no refusal.
	configPath := writeConfigFixture(t, `[[projects]]
name = "alpha"
future_option = 1

[projects.agent]
type = "claudecode"

[[projects.platforms]]
type = "feishu"
`)
	patchConfigPath(t, configPath)

	admins := "boss"
	if err := SaveProjectSettings("alpha", ProjectSettingsUpdate{AdminFrom: &admins}); err != nil {
		t.Fatalf("SaveProjectSettings with an unknown key: %v", err)
	}
	raw, _ := os.ReadFile(configPath)
	if !strings.Contains(string(raw), "future_option = 1") || readConfigFixture(t, configPath).Projects[0].AdminFrom != "boss" {
		t.Fatalf("unknown key kept and admin_from saved:\n%s", raw)
	}
}

func TestSaveProjectSettings_FallsBackForInlineTables(t *testing.T) {
	// An inline agent table cannot take a new [projects.agent.options]
	// header; the line edits fail the check and the file is rewritten.
	configPath := writeConfigFixture(t, `[[projects]]
name = "alpha"
agent = { type = "claudecode", options = { work_dir = "/srv/alpha" } }

[[projects.platforms]]
type = "feishu"
`)
	patchConfigPath(t, configPath)

	workDir := "/srv/other"
	if err := SaveProjectSettings("alpha", ProjectSettingsUpdate{WorkDir: &workDir}); err != nil {
		t.Fatalf("SaveProjectSettings: %v", err)
	}
	if got := stringMapValue(readConfigFixture(t, configPath).Projects[0].Agent.Options, "work_dir"); got != workDir {
		t.Fatalf("work_dir = %q, want %q", got, workDir)
	}
}

func TestTomlValueEnd(t *testing.T) {
	lines := strings.Split(`a = [
  "x", # has ] in comment
  "y]",
]
b = "[not an array"
c = """
text
"""
d = ["one", "two"]`, "\n")
	for _, tc := range []struct{ line, want int }{{0, 3}, {4, 4}, {5, 7}, {8, 8}} {
		if got := tomlValueEnd(lines, tc.line); got != tc.want {
			t.Errorf("tomlValueEnd(line %d) = %d, want %d", tc.line, got, tc.want)
		}
	}
}

func TestSaveProviderRefs_KeepsComments(t *testing.T) {
	configPath := writeConfigFixture(t, projectSettingsFixture)
	patchConfigPath(t, configPath)

	if err := SaveProviderRefs("alpha", []string{"relay", "backup"}); err != nil {
		t.Fatalf("SaveProviderRefs: %v", err)
	}
	if refs := readConfigFixture(t, configPath).Projects[0].Agent.ProviderRefs; len(refs) != 2 || refs[1] != "backup" {
		t.Fatalf("provider_refs = %v", refs)
	}
	if err := SaveProviderRefs("alpha", nil); err != nil {
		t.Fatalf("SaveProviderRefs(nil): %v", err)
	}
	raw, _ := os.ReadFile(configPath)
	if strings.Contains(string(raw), "provider_refs") {
		t.Fatalf("no refs must remove the key:\n%s", raw)
	}
	if !strings.Contains(string(raw), "# agent settings") || !strings.Contains(string(raw), "# top comment") {
		t.Fatalf("comments were lost:\n%s", raw)
	}
}

func TestSaveGlobalSettings_KeepsComments(t *testing.T) {
	configPath := writeConfigFixture(t, `# top comment
language = "en"

# logging
[log]
level = "info" # default

[display]
thinking_messages = true # show thinking

`+projectSettingsFixture[strings.Index(projectSettingsFixture, "[[projects]]"):])
	patchConfigPath(t, configPath)

	lang, attach, level := "zh", "off", "debug"
	idle, maxLen, interval, maxMsgs, depth := 30, 200, 900, 10, 3
	on := true
	err := SaveGlobalSettings(GlobalSettingsUpdate{
		Language:           &lang,
		AttachmentSend:     &attach,
		LogLevel:           &level,
		IdleTimeoutMins:    &idle,
		ThinkingMaxLen:     &maxLen,
		StreamPreviewOn:    &on,
		StreamPreviewIntMs: &interval,
		RateLimitMax:       &maxMsgs,
		QueueMaxDepth:      &depth,
	})
	if err != nil {
		t.Fatalf("SaveGlobalSettings: %v", err)
	}
	raw, _ := os.ReadFile(configPath)
	for _, comment := range []string{"# top comment", "# logging", "# default", "# show thinking", "# alpha keeps this comment"} {
		if !strings.Contains(string(raw), comment) {
			t.Fatalf("comment %q was lost:\n%s", comment, raw)
		}
	}
	cfg := readConfigFixture(t, configPath)
	switch {
	case cfg.Language != lang, cfg.AttachmentSend != attach, cfg.Log.Level != level, *cfg.IdleTimeoutMins != idle:
		t.Fatalf("top-level settings: %q %q %q %d", cfg.Language, cfg.AttachmentSend, cfg.Log.Level, *cfg.IdleTimeoutMins)
	case *cfg.Display.ThinkingMaxLen != maxLen || !*cfg.Display.ThinkingMessages:
		t.Fatalf("display = %+v", cfg.Display)
	case !*cfg.StreamPreview.Enabled || *cfg.StreamPreview.IntervalMs != interval:
		t.Fatalf("a missing [stream_preview] must be created: %+v", cfg.StreamPreview)
	case *cfg.RateLimit.MaxMessages != maxMsgs || *cfg.Queue.MaxDepth != depth:
		t.Fatalf("rate_limit %+v, queue %+v", cfg.RateLimit, cfg.Queue)
	case len(cfg.Projects) != 2:
		t.Fatalf("projects = %d", len(cfg.Projects))
	}
}
