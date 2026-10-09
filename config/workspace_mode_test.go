package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const workspaceModeFixture = `# top comment

[[projects]]
name = "alpha"
# alpha keeps this comment
admin_from = "boss"

# agent settings
[projects.agent]
type = "claudecode"

[projects.agent.options]
work_dir = %q # where alpha works
mode = "default"

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "cli_x"
app_secret = "secret"

[[projects]]
name = "beta"

[projects.agent]
type = "codex"

[projects.agent.options]
work_dir = %q

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "cli_y"
app_secret = "secret"
`

func TestSetProjectWorkspaceMode_RoundTrip(t *testing.T) {
	workDir := filepath.ToSlash(t.TempDir())
	baseDir := filepath.ToSlash(t.TempDir())
	configPath := writeConfigFixture(t, fmt.Sprintf(workspaceModeFixture, workDir, workDir))
	patchConfigPath(t, configPath)

	if err := SetProjectWorkspaceMode("alpha", true, baseDir); err != nil {
		t.Fatalf("switch to multi-workspace: %v", err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("config must still load after switching to multi-workspace: %v", err)
	}
	alpha := cfg.Projects[0]
	if alpha.Mode != "multi-workspace" || filepath.ToSlash(alpha.BaseDir) != baseDir {
		t.Fatalf("mode = %q, base_dir = %q", alpha.Mode, alpha.BaseDir)
	}
	if _, ok := alpha.Agent.Options["work_dir"]; ok {
		t.Fatal("work_dir must be removed in multi-workspace mode")
	}
	if alpha.Agent.Options["mode"] != "default" {
		t.Fatalf("the agent's permission mode must be left alone, got %v", alpha.Agent.Options["mode"])
	}
	if beta := cfg.Projects[1]; beta.Mode != "" || stringMapValue(beta.Agent.Options, "work_dir") != workDir {
		t.Fatalf("another project changed: %+v", beta)
	}
	raw, _ := os.ReadFile(configPath)
	for _, comment := range []string{"# top comment", "# alpha keeps this comment", "# agent settings"} {
		if !strings.Contains(string(raw), comment) {
			t.Fatalf("comment %q was lost:\n%s", comment, raw)
		}
	}

	newWorkDir := filepath.ToSlash(t.TempDir())
	if err := SetProjectWorkspaceMode("alpha", false, newWorkDir); err != nil {
		t.Fatalf("switch back to single workspace: %v", err)
	}
	cfg, err = Load(configPath)
	if err != nil {
		t.Fatalf("config must still load after switching back: %v", err)
	}
	alpha = cfg.Projects[0]
	if alpha.Mode != "" || stringMapValue(alpha.Agent.Options, "work_dir") != newWorkDir {
		t.Fatalf("mode = %q, work_dir = %v", alpha.Mode, alpha.Agent.Options["work_dir"])
	}
	if filepath.ToSlash(alpha.BaseDir) != baseDir {
		t.Fatalf("base_dir = %q, want it kept for switching back", alpha.BaseDir)
	}
	if alpha.Agent.Options["mode"] != "default" {
		t.Fatalf("the agent's permission mode must be left alone, got %v", alpha.Agent.Options["mode"])
	}
}

func TestSetProjectWorkspaceMode_CreatesAgentOptions(t *testing.T) {
	baseDir := filepath.ToSlash(t.TempDir())
	configPath := writeConfigFixture(t, fmt.Sprintf(`[[projects]]
name = "alpha"
mode = "multi-workspace"
base_dir = %q

[projects.agent]
type = "claudecode"

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "cli_x"
app_secret = "secret"
`, baseDir))
	patchConfigPath(t, configPath)

	workDir := filepath.ToSlash(t.TempDir())
	if err := SetProjectWorkspaceMode("alpha", false, workDir); err != nil {
		t.Fatalf("switch to single workspace: %v", err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("config must load: %v", err)
	}
	if p := cfg.Projects[0]; p.Mode != "" || stringMapValue(p.Agent.Options, "work_dir") != workDir {
		t.Fatalf("mode = %q, work_dir = %v", p.Mode, p.Agent.Options["work_dir"])
	}
}

func TestSetProjectWorkspaceMode_RefusesWithoutWriting(t *testing.T) {
	workDir := filepath.ToSlash(t.TempDir())
	content := fmt.Sprintf(workspaceModeFixture, workDir, workDir)
	configPath := writeConfigFixture(t, content)
	patchConfigPath(t, configPath)

	for name, call := range map[string]func() error{
		"relative path":   func() error { return SetProjectWorkspaceMode("alpha", true, "projects") },
		"unknown project": func() error { return SetProjectWorkspaceMode("gamma", true, workDir) },
	} {
		if err := call(); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		if raw, _ := os.ReadFile(configPath); string(raw) != content {
			t.Fatalf("%s: the config file changed", name)
		}
	}
}
