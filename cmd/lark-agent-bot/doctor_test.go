package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ClaymanTwinkle/lark-agent-bot/config"
	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// doctorCLIAgent is a stub agent that names its CLI binary.
type doctorCLIAgent struct {
	stubMainAgent
	bin string
}

func (a *doctorCLIAgent) CLIBinaryName() string  { return a.bin }
func (a *doctorCLIAgent) CLIDisplayName() string { return "Stub" }

// testPreflight checks the config at path with agent creation and the CLI
// lookup replaced, so no real agent CLI runs.
func testPreflight(path, configFlag string) *preflight {
	return &preflight{
		configPath: path,
		configFlag: configFlag,
		i18n:       core.NewI18n(core.LangEnglish),
		createAgent: func(string, map[string]any) (core.Agent, error) {
			return &stubMainAgent{}, nil
		},
		checkCLI: func(context.Context, core.Agent) core.DoctorCheckResult {
			return core.DoctorCheckResult{Status: core.DoctorPass, Detail: "stub 1.0"}
		},
	}
}

func writeDoctorConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// doctorConfig is a config with one project in workDir.
func doctorConfig(agentType, workDir, appID, appSecret string) string {
	return `[[projects]]
name = "demo"

[projects.agent]
type = "` + agentType + `"

[projects.agent.options]
work_dir = '` + workDir + `'

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "` + appID + `"
app_secret = "` + appSecret + `"
`
}

func findCheck(t *testing.T, checks []preflightCheck, project, name string) preflightCheck {
	t.Helper()
	for _, c := range checks {
		if c.Project == project && c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s check for project %q in %+v", name, project, checks)
	return preflightCheck{}
}

func failedChecks(checks []preflightCheck) []string {
	var names []string
	for _, c := range checks {
		if c.Status == core.DoctorFail {
			names = append(names, c.Name)
		}
	}
	return names
}

func TestPreflight_ReadyProject(t *testing.T) {
	path := writeDoctorConfig(t, doctorConfig("claudecode", t.TempDir(), "cli_real", "real-secret"))
	p := testPreflight(path, path)
	p.createAgent = func(string, map[string]any) (core.Agent, error) {
		return &doctorCLIAgent{bin: "stub-cli"}, nil
	}

	checks := p.run(context.Background())
	if failed := failedChecks(checks); len(failed) > 0 {
		t.Fatalf("failed checks %v in %+v", failed, checks)
	}
	if c := findCheck(t, checks, "demo", "agent_cli"); !strings.Contains(c.Message, "stub-cli") || !strings.Contains(c.Message, "stub 1.0") {
		t.Errorf("agent CLI message = %q", c.Message)
	}
	platform := findCheck(t, checks, "demo", "platform")
	if want := "lark-agent-bot feishu check --project demo --config " + commandArg(path); !strings.Contains(platform.Message, want) {
		t.Errorf("platform message %q does not point at %q", platform.Message, want)
	}
}

// The starter config written on first run must not pass: its work_dir and
// credentials are placeholders.
func TestPreflight_StarterConfigFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := bootstrapConfig(path); err != nil {
		t.Fatal(err)
	}
	checks := testPreflight(path, "").run(context.Background())

	if c := findCheck(t, checks, "my-project", "work_dir"); c.Status != core.DoctorFail || !strings.Contains(c.Message, config.StarterWorkDir) {
		t.Errorf("work_dir check = %+v, want a placeholder failure", c)
	}
	if c := findCheck(t, checks, "my-project", "platform"); c.Status != core.DoctorFail || !strings.Contains(c.Message, "placeholder") {
		t.Errorf("platform check = %+v, want a placeholder failure", c)
	}
	if c := findCheck(t, checks, "my-project", "agent"); c.Status != core.DoctorPass {
		t.Errorf("agent check = %+v", c)
	}
}

func TestPreflight_ConfigProblems(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "none.toml")
	checks := testPreflight(missing, "").run(context.Background())
	if len(checks) != 1 || checks[0].Name != "config" || checks[0].Status != core.DoctorFail {
		t.Errorf("missing config: %+v", checks)
	}

	empty := writeDoctorConfig(t, "[log]\nlevel = \"info\"\n")
	checks = testPreflight(empty, "").run(context.Background())
	if c := findCheck(t, checks, "", "projects"); c.Status != core.DoctorFail || !strings.Contains(c.Message, "feishu setup") {
		t.Errorf("no projects: %+v", c)
	}

	broken := writeDoctorConfig(t, "[[projects]\nname = \n")
	checks = testPreflight(broken, "").run(context.Background())
	if len(checks) != 1 || checks[0].Status != core.DoctorFail {
		t.Errorf("broken config: %+v", checks)
	}
}

func TestPreflight_AgentProblems(t *testing.T) {
	path := writeDoctorConfig(t, doctorConfig("no-such-agent", t.TempDir(), "cli_real", "secret"))
	checks := testPreflight(path, "").run(context.Background())
	if c := findCheck(t, checks, "demo", "agent"); c.Status != core.DoctorFail || !strings.Contains(c.Message, "no-such-agent") {
		t.Errorf("unknown agent type: %+v", c)
	}
	for _, c := range checks {
		if c.Name == "agent_cli" {
			t.Errorf("CLI checked for an unknown agent type: %+v", c)
		}
	}

	// A missing work_dir fails its own check and does not hide the CLI result.
	missingDir := filepath.Join(t.TempDir(), "gone")
	path = writeDoctorConfig(t, doctorConfig("claudecode", missingDir, "cli_real", "secret"))
	p := testPreflight(path, "")
	var gotWorkDir any
	p.createAgent = func(_ string, opts map[string]any) (core.Agent, error) {
		gotWorkDir = opts["work_dir"]
		return nil, errors.New(`claudecode: "claude" CLI not found in PATH, please install it first`)
	}
	checks = p.run(context.Background())
	if c := findCheck(t, checks, "demo", "work_dir"); c.Status != core.DoctorFail || !strings.Contains(c.Message, missingDir) {
		t.Errorf("missing work_dir: %+v", c)
	}
	if gotWorkDir != "." {
		t.Errorf("agent created with work_dir %v, want .", gotWorkDir)
	}
	if c := findCheck(t, checks, "demo", "agent_cli"); c.Status != core.DoctorFail || !strings.Contains(c.Message, "not found in PATH") {
		t.Errorf("agent creation failure: %+v", c)
	}

	// The CLI the agent names is not installed.
	path = writeDoctorConfig(t, doctorConfig("claudecode", t.TempDir(), "cli_real", "secret"))
	p = testPreflight(path, "")
	p.createAgent = func(string, map[string]any) (core.Agent, error) {
		return &doctorCLIAgent{bin: "my-claude"}, nil
	}
	p.checkCLI = func(context.Context, core.Agent) core.DoctorCheckResult {
		return core.DoctorCheckResult{Status: core.DoctorFail, Detail: "not found in PATH"}
	}
	checks = p.run(context.Background())
	if c := findCheck(t, checks, "demo", "agent_cli"); c.Status != core.DoctorFail || !strings.Contains(c.Message, "my-claude") {
		t.Errorf("missing CLI: %+v", c)
	}
}

func TestPreflight_UsesCLICheckOfCore(t *testing.T) {
	path := writeDoctorConfig(t, doctorConfig("claudecode", t.TempDir(), "cli_real", "secret"))
	p := testPreflight(path, "")
	p.createAgent = func(string, map[string]any) (core.Agent, error) {
		return &doctorCLIAgent{bin: "lark-agent-bot-doctor-test-missing-cli"}, nil
	}
	p.checkCLI = core.CheckAgentCLI
	checks := p.run(context.Background())
	if c := findCheck(t, checks, "demo", "agent_cli"); c.Status != core.DoctorFail || !strings.Contains(c.Message, "lark-agent-bot-doctor-test-missing-cli") {
		t.Errorf("agent_cli = %+v, want the CLI reported missing", c)
	}
}

func TestPreflight_EmptyCredentials(t *testing.T) {
	path := writeDoctorConfig(t, doctorConfig("claudecode", t.TempDir(), "cli_real", ""))
	checks := testPreflight(path, "").run(context.Background())
	if c := findCheck(t, checks, "demo", "platform"); c.Status != core.DoctorFail || !strings.Contains(c.Message, "feishu setup --project demo") {
		t.Errorf("empty app_secret: %+v", c)
	}
}

func TestPreflight_UnsetWorkDirWarns(t *testing.T) {
	path := writeDoctorConfig(t, strings.Replace(doctorConfig("claudecode", "x", "cli_real", "secret"), "work_dir = 'x'\n", "", 1))
	checks := testPreflight(path, "").run(context.Background())
	if c := findCheck(t, checks, "demo", "work_dir"); c.Status != core.DoctorWarn {
		t.Errorf("unset work_dir: %+v", c)
	}
	if failed := failedChecks(checks); len(failed) > 0 {
		t.Errorf("failed checks %v", failed)
	}
}

func TestPreflight_RunAsUserSkipsCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("run_as_user is not supported on Windows")
	}
	content := strings.Replace(doctorConfig("claudecode", t.TempDir(), "cli_real", "secret"), "[projects.agent]", "run_as_user = \"coder\"\n\n[projects.agent]", 1)
	p := testPreflight(writeDoctorConfig(t, content), "")
	p.createAgent = func(string, map[string]any) (core.Agent, error) {
		t.Error("agent created for a run_as_user project")
		return &stubMainAgent{}, nil
	}
	checks := p.run(context.Background())
	if c := findCheck(t, checks, "demo", "agent_cli"); c.Status != core.DoctorWarn || !strings.Contains(c.Message, "doctor user-isolation") {
		t.Errorf("run_as_user: %+v", c)
	}
}

// runDoctorCommand runs doctorCommand and returns its exit code and stdout.
func runDoctorCommand(t *testing.T, args ...string) (code int, stdout string) {
	t.Helper()
	stdout = captureStdout(t, func() {
		captureStderr(t, func() { code = doctorCommand(args) })
	})
	return code, stdout
}

func TestDoctorCommand(t *testing.T) {
	isolateHome(t)
	for _, args := range [][]string{{"--help"}, {"-h"}, {"--config", "x.toml", "--help"}} {
		if code, out := runDoctorCommand(t, args...); code != 0 || !strings.Contains(out, "lark-agent-bot doctor user-isolation") {
			t.Errorf("doctor %q: code %d, stdout %q", args, code, out)
		}
	}
	if code, _ := runDoctorCommand(t, "--bogus"); code != 2 {
		t.Errorf("doctor --bogus exit code = %d, want 2", code)
	}

	missing := filepath.Join(t.TempDir(), "none.toml")
	code, out := runDoctorCommand(t, "--config", missing)
	if code != 1 {
		t.Errorf("doctor on a missing config exit code = %d, want 1", code)
	}
	if !strings.HasPrefix(out, "FAIL") || !strings.Contains(out, missing) {
		t.Errorf("doctor output = %q", out)
	}
}

func TestFormatPreflight(t *testing.T) {
	out, failed := formatPreflight([]preflightCheck{
		{Name: "config", Status: core.DoctorPass, Message: "Config file: c.toml"},
		{Project: "demo", Name: "work_dir", Status: core.DoctorWarn, Message: "work_dir is not set"},
		{Project: "demo", Name: "platform", Status: core.DoctorFail, Message: "feishu app_id is empty"},
	}, core.NewI18n(core.LangEnglish))
	if failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}
	for _, want := range []string{"ok    Config file: c.toml\n", "WARN  [demo] work_dir is not set\n", "FAIL  [demo] feishu app_id is empty\n", "1 passed", "1 warnings", "1 failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q lacks %q", out, want)
		}
	}
}
