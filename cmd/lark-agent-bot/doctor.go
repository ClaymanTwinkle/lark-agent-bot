package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/ClaymanTwinkle/lark-agent-bot/config"
	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// starterAppSecret is the app_secret placeholder in the starter config that
// bootstrapConfig writes; config.StarterAppID is its app_id.
const starterAppSecret = "your-feishu-app-secret"

// runDoctor dispatches `lark-agent-bot doctor`: without a subcommand it checks
// the setup before the bot starts.
func runDoctor(args []string) {
	if len(args) > 0 {
		switch args[0] {
		case "user-isolation":
			runDoctorUserIsolation(args[1:])
			return
		case "help":
			fmt.Print(doctorUsage)
			return
		}
	}
	exitWith(doctorCommand(args))
}

const doctorUsage = `Usage:
  lark-agent-bot doctor [--config <path>]
  lark-agent-bot doctor user-isolation [flags]

doctor checks the setup before you start the bot: the config file loads and
has projects, and for each project the agent type is in this build, the agent
CLI is installed, work_dir exists and the Feishu/Lark app_id / app_secret are
filled in. It exits 1 when a check fails. It does not call Feishu/Lark; check
an app's permissions with: lark-agent-bot feishu check --project <name>

Flags:
  --config <path>    Config file (default: ./config.toml, then
                     ~/.lark-agent-bot/config.toml)
  -h, --help         Show this help

user-isolation audits the projects that set run_as_user (Linux / macOS):
  --config <path>    Config file
  --project <name>   Audit only this project
  --out <path>       Write the JSON report here (default:
                     ~/.lark-agent-bot/audits/<time>-<project>.json)
  --print-script     Print the embedded probe script and exit
`

// doctorCommand runs the checks of `lark-agent-bot doctor` and returns the
// exit code: 1 when a check fails.
func doctorCommand(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	configFlag := fs.String("config", "", "config file")
	err := parseCommandFlags(fs, args)
	if code, done := flagParseExit(err, doctorUsage); done {
		return code
	}

	p := newPreflight(*configFlag)
	report, failed := formatPreflight(p.run(context.Background()), p.i18n)
	fmt.Print(report)
	if failed > 0 {
		return 1
	}
	return 0
}

// preflightCheck is one line of the doctor report.
type preflightCheck struct {
	Project string // "" for checks of the config file itself
	Name    string // what was checked: config, projects, work_dir, agent, agent_cli, platform
	Status  core.DoctorStatus
	Message string // localized; says how to fix a failure
}

// preflight checks a config file before the bot starts. createAgent and
// checkCLI are replaced in tests.
type preflight struct {
	configPath  string // the config file checked
	configFlag  string // --config as given, repeated in the commands doctor suggests
	i18n        *core.I18n
	createAgent func(name string, opts map[string]any) (core.Agent, error)
	checkCLI    func(ctx context.Context, agent core.Agent) core.DoctorCheckResult
}

func newPreflight(configFlag string) *preflight {
	path := resolveConfigPath(configFlag)
	return &preflight{
		configPath:  path,
		configFlag:  configFlag,
		i18n:        core.NewI18n(cliLanguage(path)),
		createAgent: core.CreateAgent,
		checkCLI:    core.CheckAgentCLI,
	}
}

func (p *preflight) run(ctx context.Context) []preflightCheck {
	var checks []preflightCheck
	add := func(name string, status core.DoctorStatus, key core.MsgKey, args ...any) {
		checks = append(checks, preflightCheck{Name: name, Status: status, Message: p.i18n.Tf(key, args...)})
	}

	if _, err := os.Stat(p.configPath); errors.Is(err, os.ErrNotExist) {
		add("config", core.DoctorFail, core.MsgCLIDoctorConfigMissing, p.configPath, p.command("feishu setup", "my-project"))
		return checks
	}
	cfg, err := config.Load(p.configPath)
	if errors.Is(err, config.ErrNoProjects) {
		add("config", core.DoctorPass, core.MsgCLIDoctorConfigOK, p.configPath)
		add("projects", core.DoctorFail, core.MsgCLIDoctorNoProjects, p.command("feishu setup", "my-project"))
		return checks
	}
	if err != nil {
		add("config", core.DoctorFail, core.MsgCLIDoctorConfigInvalid, p.configPath, err)
		return checks
	}
	add("config", core.DoctorPass, core.MsgCLIDoctorConfigOK, p.configPath)
	names := make([]string, 0, len(cfg.Projects))
	for _, proj := range cfg.Projects {
		names = append(names, proj.Name)
	}
	add("projects", core.DoctorPass, core.MsgCLIDoctorProjects, len(names), strings.Join(names, ", "))

	for _, proj := range cfg.Projects {
		checks = append(checks, p.checkProject(ctx, proj)...)
	}
	return checks
}

func (p *preflight) checkProject(ctx context.Context, proj config.ProjectConfig) []preflightCheck {
	var checks []preflightCheck
	add := func(name string, status core.DoctorStatus, key core.MsgKey, args ...any) {
		checks = append(checks, preflightCheck{Project: proj.Name, Name: name, Status: status, Message: p.i18n.Tf(key, args...)})
	}

	workDirOK := p.checkWorkDir(proj, add)

	if !slices.Contains(core.ListRegisteredAgents(), proj.Agent.Type) {
		add("agent", core.DoctorFail, core.MsgCLIDoctorAgentUnknown, proj.Agent.Type, strings.Join(sortedAgentNames(), ", "))
	} else {
		add("agent", core.DoctorPass, core.MsgCLIDoctorAgentOK, proj.Agent.Type)
		p.checkAgentCLI(ctx, proj, workDirOK, add)
	}

	p.checkPlatforms(proj, add)
	return checks
}

type preflightAdd func(name string, status core.DoctorStatus, key core.MsgKey, args ...any)

// checkWorkDir reports whether the agent can start in the project's work_dir.
func (p *preflight) checkWorkDir(proj config.ProjectConfig, add preflightAdd) bool {
	if proj.Mode == "multi-workspace" {
		return true // no work_dir: the bot creates base_dir when it starts
	}
	workDir, _ := proj.Agent.Options["work_dir"].(string)
	switch {
	case strings.TrimSpace(workDir) == "":
		add("work_dir", core.DoctorWarn, core.MsgCLIDoctorWorkDirUnset)
		return true
	case workDir == config.StarterWorkDir:
		add("work_dir", core.DoctorFail, core.MsgCLIDoctorWorkDirPlaceholder, workDir)
		return false
	}
	if info, err := os.Stat(workDir); err != nil || !info.IsDir() {
		add("work_dir", core.DoctorFail, core.MsgCLIDoctorWorkDirMissing, workDir)
		return false
	}
	add("work_dir", core.DoctorPass, core.MsgCLIDoctorWorkDirOK, workDir)
	return true
}

// checkAgentCLI creates the project's agent, which finds its CLI the way the
// bot will (honouring the cmd option), then runs the CLI check of /doctor.
func (p *preflight) checkAgentCLI(ctx context.Context, proj config.ProjectConfig, workDirOK bool, add preflightAdd) {
	if proj.RunAsUser != "" {
		// The CLI must be on the target user's PATH, not this one.
		add("agent_cli", core.DoctorWarn, core.MsgCLIDoctorAgentCLIRunAs, proj.RunAsUser, p.command("doctor user-isolation", proj.Name))
		return
	}
	// Without the bot's data dir: this agent only answers where its CLI is.
	opts := maps.Clone(proj.Agent.Options)
	if opts == nil {
		opts = map[string]any{}
	}
	if !workDirOK {
		// work_dir has its own check; do not let it hide the CLI result.
		opts["work_dir"] = "."
	}
	agent, err := p.createAgent(proj.Agent.Type, opts)
	if err != nil {
		add("agent_cli", core.DoctorFail, core.MsgCLIDoctorAgentFailed, proj.Agent.Type, err)
		return
	}
	defer func() {
		if err := agent.Stop(); err != nil {
			slog.Warn("doctor: stop agent", "project", proj.Name, "error", err)
		}
	}()

	info, ok := agent.(core.AgentDoctorInfo)
	if !ok {
		// The agent names no CLI binary; creating it already looked the CLI up.
		add("agent_cli", core.DoctorPass, core.MsgCLIDoctorAgentCLIFound, proj.Agent.Type)
		return
	}
	result := p.checkCLI(ctx, agent)
	if result.Status == core.DoctorFail {
		add("agent_cli", core.DoctorFail, core.MsgCLIDoctorAgentCLIMissing, info.CLIBinaryName())
		return
	}
	add("agent_cli", core.DoctorPass, core.MsgCLIDoctorAgentCLIOK, info.CLIBinaryName(), result.Detail)
}

// checkPlatforms checks that each platform is in this build and that the
// Feishu/Lark ones have their app credentials. It does not call Feishu/Lark:
// the messages point at `feishu check`, which checks the app's permissions.
func (p *preflight) checkPlatforms(proj config.ProjectConfig, add preflightAdd) {
	registered := core.ListRegisteredPlatforms()
	sort.Strings(registered)
	for _, pc := range proj.Platforms {
		if !slices.Contains(registered, pc.Type) {
			add("platform", core.DoctorFail, core.MsgCLIDoctorPlatformUnknown, pc.Type, strings.Join(registered, ", "))
			continue
		}
		if pc.Type != "feishu" && pc.Type != "lark" {
			continue
		}
		appID, _ := pc.Options["app_id"].(string)
		appSecret, _ := pc.Options["app_secret"].(string)
		appID, appSecret = strings.TrimSpace(appID), strings.TrimSpace(appSecret)
		switch {
		case appID == "" || appSecret == "":
			add("platform", core.DoctorFail, core.MsgCLIDoctorCredentialsEmpty, pc.Type, p.command("feishu setup", proj.Name))
		case appID == config.StarterAppID || appSecret == starterAppSecret:
			add("platform", core.DoctorFail, core.MsgCLIDoctorCredentialsPlaceholder, pc.Type, p.command("feishu setup", proj.Name))
		default:
			add("platform", core.DoctorPass, core.MsgCLIDoctorCredentialsOK, pc.Type, appID, p.command("feishu check", proj.Name))
		}
	}
}

// command is a lark-agent-bot command line for the user to run next, on the
// config doctor checked.
func (p *preflight) command(sub, project string) string {
	cmd := "lark-agent-bot " + sub + " --project " + commandArg(project)
	if p.configFlag != "" {
		cmd += " --config " + commandArg(p.configFlag)
	}
	return cmd
}

// formatPreflight renders one line per check and a summary, and counts the
// checks that failed.
func formatPreflight(checks []preflightCheck, i18n *core.I18n) (report string, failed int) {
	var b strings.Builder
	var passed, warned int
	for _, c := range checks {
		tag := "ok"
		switch c.Status {
		case core.DoctorFail:
			tag = "FAIL"
			failed++
		case core.DoctorWarn:
			tag = "WARN"
			warned++
		default:
			passed++
		}
		prefix := ""
		if c.Project != "" {
			prefix = "[" + c.Project + "] "
		}
		fmt.Fprintf(&b, "%-4s  %s%s\n", tag, prefix, c.Message)
	}
	fmt.Fprintln(&b, i18n.Tf(core.MsgDoctorSummary, passed, warned, failed))
	return b.String(), failed
}
