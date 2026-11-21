// Package antigravity runs Google's Antigravity CLI (agy) as a lark-agent-bot
// agent through its Agent Client Protocol server, `agy acp`: JSON-RPC over
// stdio, one process per bot session. ACP gives the bot streamed replies,
// tool calls, permission requests it can show as buttons, and conversations
// that can be resumed by id.
package antigravity

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

func init() {
	core.RegisterAgent("antigravity", New)
}

// The optional capabilities the engine looks for.
var (
	_ core.Agent                           = (*Agent)(nil)
	_ core.AgentDoctorInfo                 = (*Agent)(nil)
	_ core.SystemPromptSupporter           = (*Agent)(nil)
	_ core.PlatformPromptInjector          = (*Agent)(nil)
	_ core.SessionEnvInjector              = (*Agent)(nil)
	_ core.WorkDirSwitcher                 = (*Agent)(nil)
	_ core.MemoryFileProvider              = (*Agent)(nil)
	_ core.ModeSwitcher                    = (*Agent)(nil)
	_ core.ModeNormalizer                  = (*Agent)(nil)
	_ core.ModelSwitcher                   = (*Agent)(nil)
	_ core.UsageReporter                   = (*Agent)(nil)
	_ core.ReasoningEffortSwitcher         = (*Agent)(nil)
	_ core.WorkspaceAgentOptionSnapshotter = (*Agent)(nil)
	_ core.ContextCompressor               = (*Agent)(nil)
	_ core.SkillProvider                   = (*Agent)(nil)
	_ core.CommandProvider                 = (*Agent)(nil)
	_ core.AgentSession                    = (*session)(nil)
	_ core.LiveModeSwitcher                = (*session)(nil)
	_ core.StartupWarner                   = (*session)(nil)
	_ core.ContextUsageReporter            = (*session)(nil)
	_ core.BackgroundTaskReporter          = (*session)(nil)
)

// Permission mode keys. All but yolo map to agy's own ACP modes; yolo starts
// agy with --dangerously-skip-permissions.
const (
	modeDefault     = "default"
	modeAcceptEdits = "accept-edits"
	modeAuto        = "auto"
	modePlan        = "plan"
	modeYolo        = "yolo"
)

// Agent is the Antigravity CLI agent.
type Agent struct {
	mu             sync.RWMutex
	workDir        string
	model          string
	mode           string // canonical mode key; "" keeps agy's default
	effort         string // reasoning effort (agy's thought_level); "" keeps agy's default
	lang           core.Language
	cmd            string
	cliExtraArgs   []string
	configEnv      []string
	sessionEnv     []string
	instructions   string // lark-agent-bot's own instructions, in the bot's language
	platformPrompt string

	// Learned from the latest session, for /mode and /model.
	stateMu      sync.RWMutex
	agyModes     []acpMode
	agyMode      string
	agyModels    []acpModel
	agyModelName string
	agyEfforts   []string
	agyEffort    string
}

// New creates the agent from [projects.agent.options].
func New(opts map[string]any) (core.Agent, error) {
	workDir, _ := opts["work_dir"].(string)
	if workDir == "" {
		workDir = "."
	}
	model, _ := opts["model"].(string)
	mode, _ := opts["mode"].(string)
	effort, _ := opts["reasoning_effort"].(string)
	lang, _ := opts["language"].(string)
	cmd, extraArgs := core.ParseCmdOpts(opts, "agy")
	if _, err := exec.LookPath(cmd); err != nil {
		return nil, fmt.Errorf("antigravity: %q CLI not found in PATH; install it from https://antigravity.google/docs/install or set cmd to its full path", cmd)
	}
	language := core.NormalizeLanguageString(lang)
	return &Agent{
		workDir:      workDir,
		model:        strings.TrimSpace(model),
		mode:         normalizeMode(mode),
		effort:       normalizeEffort(effort),
		lang:         language,
		cmd:          cmd,
		cliExtraArgs: extraArgs,
		configEnv:    core.ParseConfigEnv(opts),
		instructions: core.AgentSystemPromptForLang(language),
	}, nil
}

func (a *Agent) Name() string { return "antigravity" }

// StartSession starts `agy acp` and opens (or resumes) a conversation.
func (a *Agent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	a.mu.RLock()
	cfg := sessionConfig{
		command:      a.cmd,
		args:         a.acpArgsLocked(),
		env:          append(append([]string(nil), a.configEnv...), a.sessionEnv...),
		workDir:      a.workDir,
		resumeID:     sessionID,
		mode:         a.mode,
		model:        a.model,
		effort:       a.effort,
		systemPrompt: joinPrompts(a.instructions, a.platformPrompt),
		lang:         a.lang,
		reporter:     a,
	}
	a.mu.RUnlock()
	return startSession(ctx, cfg)
}

// acpArgsLocked returns the arguments for `agy acp`: extra args from cmd,
// the subcommand, and the yolo flag when that mode is selected.
func (a *Agent) acpArgsLocked() []string {
	args := append(append([]string(nil), a.cliExtraArgs...), "acp")
	if a.mode == modeYolo {
		args = append(args, "--dangerously-skip-permissions")
	}
	return args
}

func joinPrompts(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n\n")
}

func (a *Agent) Stop() error { return nil }

// CLIBinaryName implements core.AgentDoctorInfo.
func (a *Agent) CLIBinaryName() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cmd
}

// CLIDisplayName implements core.AgentDoctorInfo.
func (a *Agent) CLIDisplayName() string { return "Antigravity" }

// HasSystemPromptSupport reports that the bot's instructions reach agy
// natively, through the systemPrompt.append field of session/new.
func (a *Agent) HasSystemPromptSupport() bool { return true }

// SetPlatformPrompt implements core.PlatformPromptInjector.
func (a *Agent) SetPlatformPrompt(prompt string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.platformPrompt = prompt
}

// SetSessionEnv implements core.SessionEnvInjector.
func (a *Agent) SetSessionEnv(env []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessionEnv = env
}

func (a *Agent) SetWorkDir(dir string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.workDir = dir
}

func (a *Agent) GetWorkDir() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.workDir
}

// ProjectMemoryFile is the project instruction file agy reads. agy also
// reads GEMINI.md; AGENTS.md is the name it shares with other agents.
func (a *Agent) ProjectMemoryFile() string {
	dir, err := filepath.Abs(a.GetWorkDir())
	if err != nil {
		dir = a.GetWorkDir()
	}
	return filepath.Join(dir, "AGENTS.md")
}

// GlobalMemoryFile is agy's user-level instruction file.
func (a *Agent) GlobalMemoryFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".gemini", "AGENTS.md")
}

// ── Modes ────────────────────────────────────────────────────────────

// normalizeMode maps user input to a mode key; "" means agy's default.
func normalizeMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return ""
	case "default", "manual", "ask":
		return modeDefault
	case "accept-edits", "accept_edits", "acceptedits", "edit", "edits":
		return modeAcceptEdits
	case "auto":
		return modeAuto
	case "plan":
		return modePlan
	case "yolo", "bypasspermissions", "bypass", "dangerously-skip-permissions":
		return modeYolo
	default:
		return strings.TrimSpace(raw) // an agy mode id the bot doesn't know by name
	}
}

// matchMode finds agy's mode id for a mode key, comparing ids and names
// without case, spaces, dashes or underscores ("accept-edits" matches
// "Accept Edits").
func matchMode(key string, modes []acpMode) string {
	want := squash(key)
	for _, m := range modes {
		if squash(m.ID) == want || squash(m.Name) == want {
			return m.ID
		}
	}
	return ""
}

func squash(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '_':
			return -1
		}
		return r
	}, strings.ToLower(strings.TrimSpace(s)))
}

func (a *Agent) SetMode(mode string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.mode = normalizeMode(mode)
	slog.Info("antigravity: mode changed", "mode", a.mode)
}

// NormalizeMode implements core.ModeNormalizer.
func (a *Agent) NormalizeMode(mode string) string { return normalizeMode(mode) }

// GetMode returns the selected mode, or agy's current mode when none is set.
func (a *Agent) GetMode() string {
	a.mu.RLock()
	mode := a.mode
	a.mu.RUnlock()
	if mode != "" {
		return mode
	}
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	for _, key := range []string{modeDefault, modeAcceptEdits, modeAuto, modePlan} {
		if matchMode(key, []acpMode{{ID: a.agyMode}}) != "" {
			return key
		}
	}
	return a.agyMode
}

// PermissionModes lists the modes for /mode. The four agy modes are always
// offered; agy decides at run time whether one (such as auto) is available.
func (a *Agent) PermissionModes() []core.PermissionModeInfo {
	return []core.PermissionModeInfo{
		{Key: modeDefault, Name: "Default", NameZh: "默认", Desc: "Ask before editing files or running commands", DescZh: "修改文件和运行命令前先确认"},
		{Key: modeAcceptEdits, Name: "Accept Edits", NameZh: "自动编辑", Desc: "Edit workspace files freely, ask before running commands", DescZh: "工作区内改文件不确认，运行命令前确认"},
		{Key: modeAuto, Name: "Auto", NameZh: "自动", Desc: "agy edits files, sandboxes commands and reviews the rest itself", DescZh: "agy 自动改文件、在沙箱里跑命令，其余由它自己评估"},
		{Key: modePlan, Name: "Plan", NameZh: "规划", Desc: "Plan first, outlining steps before writing code", DescZh: "先规划步骤，再写代码"},
		{Key: modeYolo, Name: "YOLO", NameZh: "全自动", Desc: "Approve every tool call without asking", DescZh: "所有工具调用都不确认", Privileged: true},
	}
}

// ── Models ───────────────────────────────────────────────────────────

func (a *Agent) SetModel(model string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.model = strings.TrimSpace(model)
	slog.Info("antigravity: model changed", "model", a.model)
}

// GetModel returns the selected model, or agy's current one when none is set.
func (a *Agent) GetModel() string {
	a.mu.RLock()
	model := a.model
	a.mu.RUnlock()
	if model != "" {
		return model
	}
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return a.agyModelName
}

// AvailableModels returns the models agy offered in the latest session;
// it is empty until a session has started.
func (a *Agent) AvailableModels(_ context.Context) []core.ModelOption {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	out := make([]core.ModelOption, 0, len(a.agyModels))
	for _, m := range a.agyModels {
		desc := m.Name
		if desc == "" {
			desc = m.Description
		}
		out = append(out, core.ModelOption{Name: m.ModelID, Desc: desc})
	}
	return out
}

// reportModes implements stateReporter.
func (a *Agent) reportModes(modes []acpMode, current string) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	if len(modes) > 0 {
		a.agyModes = modes
	}
	a.agyMode = current
}

// reportModels implements stateReporter.
func (a *Agent) reportModels(models []acpModel, current string) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	if len(models) > 0 {
		a.agyModels = models
	}
	if current != "" {
		a.agyModelName = current
	}
}

// reportEfforts implements stateReporter.
func (a *Agent) reportEfforts(efforts []string, current string) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	if len(efforts) > 0 {
		a.agyEfforts = efforts
	}
	if current != "" {
		a.agyEffort = current
	}
}

// ── Reasoning effort ─────────────────────────────────────────────────

// normalizeEffort lowercases an effort and maps common aliases to agy's
// values (low, medium, high, xhigh, max).
func normalizeEffort(raw string) string {
	switch e := strings.ToLower(strings.TrimSpace(raw)); e {
	case "med":
		return "medium"
	case "x-high", "extra-high", "extra_high":
		return "xhigh"
	case "maximum":
		return "max"
	default:
		return e
	}
}

func (a *Agent) SetReasoningEffort(effort string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.effort = normalizeEffort(effort)
	slog.Info("antigravity: reasoning effort changed", "effort", a.effort)
}

// GetReasoningEffort returns the selected effort, or agy's current one.
func (a *Agent) GetReasoningEffort() string {
	a.mu.RLock()
	effort := a.effort
	a.mu.RUnlock()
	if effort != "" {
		return effort
	}
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return a.agyEffort
}

// AvailableReasoningEfforts lists agy's thought_level values from the latest
// session; before any session it lists the levels `agy --effort` accepts.
func (a *Agent) AvailableReasoningEfforts() []string {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	if len(a.agyEfforts) > 0 {
		return append([]string(nil), a.agyEfforts...)
	}
	return []string{"low", "medium", "high", "xhigh", "max"}
}

// ── Workspaces, compaction, skills ───────────────────────────────────

// WorkspaceAgentOptions implements core.WorkspaceAgentOptionSnapshotter, so
// an agent created for another workspace keeps this one's settings.
func (a *Agent) WorkspaceAgentOptions() map[string]any {
	a.mu.RLock()
	defer a.mu.RUnlock()
	opts := map[string]any{
		"cmd": append([]string{a.cmd}, a.cliExtraArgs...),
	}
	if a.mode != "" {
		opts["mode"] = a.mode
	}
	if a.model != "" {
		opts["model"] = a.model
	}
	if a.effort != "" {
		opts["reasoning_effort"] = a.effort
	}
	if len(a.configEnv) > 0 {
		env := make(map[string]any, len(a.configEnv))
		for _, kv := range a.configEnv {
			if k, v, ok := strings.Cut(kv, "="); ok {
				env[k] = v
			}
		}
		opts["env"] = env
	}
	return opts
}

// CompressCommand implements core.ContextCompressor with agy's /compact.
func (a *Agent) CompressCommand() string { return "/compact" }

// SkillDirs returns agy's workspace and global skill folders.
func (a *Agent) SkillDirs() []string {
	return a.agyDirs(filepath.Join(".agents", "skills"), filepath.Join(".gemini", "config", "skills"))
}

// CommandDirs returns agy's workflow folders, whose .md files are its
// custom slash commands.
func (a *Agent) CommandDirs() []string {
	return a.agyDirs(filepath.Join(".agents", "workflows"), filepath.Join(".gemini", "config", "workflows"))
}

// agyDirs returns <work_dir>/<workspaceRel> and ~/<homeRel>.
func (a *Agent) agyDirs(workspaceRel, homeRel string) []string {
	dir, err := filepath.Abs(a.GetWorkDir())
	if err != nil {
		dir = a.GetWorkDir()
	}
	dirs := []string{filepath.Join(dir, workspaceRel)}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, homeRel))
	}
	return dirs
}
