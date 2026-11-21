package antigravity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// closeGrace is how long Close waits for agy to exit after its stdin is
// closed (so it can finish saving the conversation) before killing it.
const closeGrace = 5 * time.Second

// rpcAuthRequired is the JSON-RPC error code agy returns when nobody is signed in.
const rpcAuthRequired = -32000

// sessionConfig is everything one `agy acp` session needs at start.
type sessionConfig struct {
	command      string
	args         []string
	env          []string
	workDir      string
	resumeID     string
	mode         string // canonical mode key; "" keeps agy's default
	model        string // model id; "" keeps agy's default
	effort       string // thought_level value; "" keeps agy's default
	systemPrompt string // appended to agy's system prompt via _meta
	lang         core.Language
	reporter     stateReporter
}

// stateReporter receives the modes, models and reasoning efforts a session
// learns from agy, so the Agent can show them in /mode, /model and /reasoning.
type stateReporter interface {
	reportModes(modes []acpMode, current string)
	reportModels(models []acpModel, current string)
	reportEfforts(efforts []string, current string)
}

// ACP session config options agy offers. "model" lists the base models
// (gemini-4-neon, claude-opus-5-5, ...); "thought_level" is the reasoning
// effort (low, medium, high, ...). agy's legacy models list instead combines
// the two (gemini-4-neon-high), so the bot uses these options when present.
const (
	modelConfigID  = "model"
	effortConfigID = "thought_level"
)

// acpConfigOption is one entry of an ACP session's configOptions.
type acpConfigOption struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Category     string `json:"category"`
	CurrentValue string `json:"currentValue"`
	Options      []struct {
		Value string `json:"value"`
		Name  string `json:"name"`
	} `json:"options"`
}

// findOption returns the config option with the given id or category.
func findOption(opts []acpConfigOption, id string) *acpConfigOption {
	for i := range opts {
		if opts[i].ID == id || opts[i].Category == id {
			return &opts[i]
		}
	}
	return nil
}

func (o *acpConfigOption) has(value string) bool {
	for _, v := range o.Options {
		if v.Value == value {
			return true
		}
	}
	return false
}

type acpMode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type acpModes struct {
	CurrentModeID  string    `json:"currentModeId"`
	AvailableModes []acpMode `json:"availableModes"`
}

type acpModel struct {
	ModelID     string `json:"modelId"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type acpModels struct {
	CurrentModelID  string     `json:"currentModelId"`
	AvailableModels []acpModel `json:"availableModels"`
}

// sessionSetup is the part of a session/new, session/load or session/resume
// response the bot uses.
type sessionSetup struct {
	SessionID     string            `json:"sessionId"`
	Modes         *acpModes         `json:"modes"`
	Models        *acpModels        `json:"models"`
	ConfigOptions []acpConfigOption `json:"configOptions"`
}

type initializeResult struct {
	AgentCapabilities struct {
		LoadSession         bool `json:"loadSession"`
		SessionCapabilities struct {
			Resume json.RawMessage `json:"resume"`
			List   json.RawMessage `json:"list"`
		} `json:"sessionCapabilities"`
	} `json:"agentCapabilities"`
}

type pendingPermission struct {
	rpcID   json.RawMessage
	options []permissionOption
}

// session is one running `agy acp` process serving one ACP session.
//
// Events is owned by the session: it is closed exactly once, by Close.
type session struct {
	workDir string
	yolo    bool // started with --dangerously-skip-permissions
	events  chan core.Event
	ctx     context.Context // canceled by Close; stops emits and RPC waits
	cancel  context.CancelFunc
	wg      sync.WaitGroup // read loop and turn goroutines
	alive   atomic.Bool

	cmd    *exec.Cmd
	stdin  io.Closer
	stderr *tailBuffer
	tr     *transport
	exited chan struct{} // closed when the process has been reaped

	idMu sync.RWMutex
	id   string

	mapper    *updateMapper // read-loop only
	replaying atomic.Bool   // drop updates while session/load replays history

	turnActive atomic.Bool

	permMu sync.Mutex
	perms  map[string]pendingPermission

	modeMu sync.RWMutex
	modes  []acpMode
	mode   string

	usageMu sync.RWMutex
	usage   *core.ContextUsage

	agentTurn    agentTurnTracker
	tools        toolTracker
	exitReported atomic.Bool // agy's exit was reported, by a turn or the exit watcher

	warnings []string // set during setup only, read after

	reporter  stateReporter
	closeOnce sync.Once
}

func startSession(ctx context.Context, cfg sessionConfig) (*session, error) {
	workDir, err := filepath.Abs(cfg.workDir)
	if err != nil {
		workDir = cfg.workDir
	}
	sctx, cancel := context.WithCancel(ctx)
	s := &session{
		workDir:  workDir,
		yolo:     cfg.mode == modeYolo,
		events:   make(chan core.Event, 128),
		ctx:      sctx,
		cancel:   cancel,
		stderr:   newTailBuffer(8 << 10),
		exited:   make(chan struct{}),
		mapper:   newUpdateMapper(),
		perms:    make(map[string]pendingPermission),
		reporter: cfg.reporter,
	}

	// Not exec.CommandContext: Close stops agy itself, giving it time to
	// save the conversation instead of killing it outright.
	cmd := exec.Command(cfg.command, cfg.args...)
	cmd.Dir = workDir
	cmd.Env = core.MergeEnv(os.Environ(), cfg.env)
	cmd.Stderr = s.stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("antigravity: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("antigravity: stdout pipe: %w", err)
	}
	s.cmd, s.stdin = cmd, stdin
	s.tr = newTransport(stdout, stdin, s.onNotification, s.onServerRequest)
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("antigravity: start %s: %w", cfg.command, err)
	}
	s.alive.Store(true)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.tr.readLoop()
		waitErr := cmd.Wait()
		close(s.exited)
		// An unexpected exit is reported once: here, or by the turn whose
		// session/prompt call it broke.
		if s.alive.Swap(false) && s.exitReported.CompareAndSwap(false, true) {
			s.emit(core.Event{Type: core.EventError, Error: s.withStderr(fmt.Errorf("antigravity: agy exited: %v", waitErr))})
		}
	}()

	s.wg.Add(1)
	go s.watchAgentTurns()

	if err := s.setup(cfg); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// setup runs initialize, then resumes or creates the ACP session and applies
// the configured model and mode.
func (s *session) setup(cfg sessionConfig) error {
	raw, err := s.tr.call(s.ctx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
		"clientInfo": map[string]any{"name": "lark-agent-bot", "version": "1"},
	})
	if err != nil {
		return s.withStderr(fmt.Errorf("antigravity: initialize: %w", err))
	}
	var init initializeResult
	if err := json.Unmarshal(raw, &init); err != nil {
		return fmt.Errorf("antigravity: parse initialize: %w", err)
	}

	sessionParams := map[string]any{"cwd": s.workDir, "mcpServers": []any{}}
	if strings.TrimSpace(cfg.systemPrompt) != "" {
		sessionParams["_meta"] = map[string]any{
			"antigravity": map[string]any{"systemPrompt": map[string]any{"append": cfg.systemPrompt}},
		}
	}

	var setup *sessionSetup
	if cfg.resumeID != "" && cfg.resumeID != core.ContinueSession {
		setup = s.reopen(init, cfg.resumeID, sessionParams)
	}
	if setup == nil {
		setup, err = s.callSetup("session/new", sessionParams)
		if err != nil {
			return err
		}
	}
	s.setID(setup.SessionID)
	s.absorbModes(setup.Modes)
	if findOption(setup.ConfigOptions, modelConfigID) == nil {
		s.absorbModels(setup.Models)
	}
	s.absorbConfigOptions(setup.ConfigOptions)

	i18n := core.NewI18n(cfg.lang)
	opts := setup.ConfigOptions
	if cfg.model != "" {
		updated, err := s.setModel(cfg.model, opts, setup.Models)
		if err != nil {
			slog.Warn("antigravity: could not set model", "model", cfg.model, "error", err)
			s.warnings = append(s.warnings, i18n.Tf(core.MsgAgentModelNotApplied, cfg.model))
		} else if updated != nil {
			opts = updated
		}
	}
	if cfg.effort != "" {
		if err := s.setEffort(cfg.effort, opts); err != nil {
			slog.Warn("antigravity: could not set reasoning effort", "effort", cfg.effort, "error", err)
			s.warnings = append(s.warnings, i18n.Tf(core.MsgAgentEffortNotApplied, cfg.effort))
		}
	}
	if cfg.mode != "" && cfg.mode != modeYolo && !s.SetLiveMode(cfg.mode) {
		slog.Warn("antigravity: could not apply mode", "mode", cfg.mode)
		s.warnings = append(s.warnings, i18n.Tf(core.MsgAgentModeNotApplied, cfg.mode))
	}
	return nil
}

// setModel selects a model: through the "model" config option when it lists
// the id (a base model such as gemini-4-neon), else through session/set_model
// when agy's legacy models list has it (an id with the effort built in, such
// as gemini-4-neon-high). It returns the config options agy sent back, if any.
func (s *session) setModel(model string, opts []acpConfigOption, models *acpModels) ([]acpConfigOption, error) {
	if opt := findOption(opts, modelConfigID); opt != nil && opt.has(model) {
		if opt.CurrentValue == model {
			return nil, nil
		}
		return s.setConfigOption(opt.ID, model)
	}
	if models != nil {
		for _, m := range models.AvailableModels {
			if m.ModelID != model {
				continue
			}
			if models.CurrentModelID == model {
				return nil, nil
			}
			if _, err := s.tr.call(s.ctx, "session/set_model", map[string]any{"sessionId": s.ID(), "modelId": model}); err != nil {
				return nil, err
			}
			if s.reporter != nil {
				s.reporter.reportModels(nil, model)
			}
			return nil, nil
		}
	}
	return nil, fmt.Errorf("agy offers no model %q", model)
}

// setEffort sets agy's thought_level config option, unless it already has
// that value.
func (s *session) setEffort(effort string, opts []acpConfigOption) error {
	opt := findOption(opts, effortConfigID)
	if opt == nil {
		return fmt.Errorf("agy offers no %s option", effortConfigID)
	}
	if opt.CurrentValue == effort {
		return nil
	}
	if _, err := s.setConfigOption(opt.ID, effort); err != nil {
		return err
	}
	if s.reporter != nil {
		s.reporter.reportEfforts(nil, effort)
	}
	return nil
}

// setConfigOption calls session/set_config_option and absorbs the updated
// options agy returns.
func (s *session) setConfigOption(id, value string) ([]acpConfigOption, error) {
	raw, err := s.tr.call(s.ctx, "session/set_config_option", map[string]any{
		"sessionId": s.ID(), "configId": id, "value": value,
	})
	if err != nil {
		return nil, err
	}
	var res struct {
		ConfigOptions []acpConfigOption `json:"configOptions"`
	}
	if json.Unmarshal(raw, &res) == nil && len(res.ConfigOptions) > 0 {
		s.absorbConfigOptions(res.ConfigOptions)
		return res.ConfigOptions, nil
	}
	return nil, nil
}

// absorbConfigOptions reports the model and reasoning-effort choices and
// their current values.
func (s *session) absorbConfigOptions(opts []acpConfigOption) {
	if s.reporter == nil {
		return
	}
	if opt := findOption(opts, modelConfigID); opt != nil {
		models := make([]acpModel, 0, len(opt.Options))
		for _, o := range opt.Options {
			models = append(models, acpModel{ModelID: o.Value, Name: o.Name})
		}
		s.reporter.reportModels(models, opt.CurrentValue)
	}
	if opt := findOption(opts, effortConfigID); opt != nil {
		values := make([]string, 0, len(opt.Options))
		for _, o := range opt.Options {
			values = append(values, o.Value)
		}
		s.reporter.reportEfforts(values, opt.CurrentValue)
	}
}

// StartupWarning implements core.StartupWarner: settings from the config
// that agy did not accept, so the user knows the defaults are in effect.
func (s *session) StartupWarning() string { return strings.Join(s.warnings, "\n") }

// GetContextUsage implements core.ContextUsageReporter from agy's latest
// usage_update. It returns nil until agy has sent one.
func (s *session) GetContextUsage() *core.ContextUsage {
	s.usageMu.RLock()
	defer s.usageMu.RUnlock()
	if s.usage == nil {
		return nil
	}
	u := *s.usage
	return &u
}

// absorbUsage records an ACP usage_update: "used" tokens of a "size"-token
// context window.
func (s *session) absorbUsage(update json.RawMessage) {
	var u struct {
		Used int `json:"used"`
		Size int `json:"size"`
	}
	if json.Unmarshal(update, &u) != nil || u.Size <= 0 {
		return
	}
	s.usageMu.Lock()
	s.usage = &core.ContextUsage{UsedTokens: u.Used, TotalTokens: u.Used, ContextWindow: u.Size}
	s.usageMu.Unlock()
}

// reopen continues an earlier conversation: session/resume when agy offers
// it (no history replay), else session/load with the replayed history
// dropped. It returns nil when neither works, so a new session is started.
func (s *session) reopen(init initializeResult, id string, base map[string]any) *sessionSetup {
	params := make(map[string]any, len(base)+1)
	for k, v := range base {
		params[k] = v
	}
	params["sessionId"] = id

	if len(init.AgentCapabilities.SessionCapabilities.Resume) > 0 {
		setup, err := s.callSetup("session/resume", params)
		if err == nil {
			return withID(setup, id)
		}
		slog.Warn("antigravity: session/resume failed", "session_id", id, "error", err)
	}
	if init.AgentCapabilities.LoadSession {
		s.replaying.Store(true)
		setup, err := s.callSetup("session/load", params)
		s.replaying.Store(false)
		if err == nil {
			return withID(setup, id)
		}
		slog.Warn("antigravity: session/load failed", "session_id", id, "error", err)
	}
	slog.Warn("antigravity: could not reopen conversation, starting a new one", "session_id", id)
	return nil
}

// withID fills in the session id, which resume/load responses may omit.
func withID(setup *sessionSetup, id string) *sessionSetup {
	if setup.SessionID == "" {
		setup.SessionID = id
	}
	return setup
}

func (s *session) callSetup(method string, params map[string]any) (*sessionSetup, error) {
	raw, err := s.tr.call(s.ctx, method, params)
	if err != nil {
		var rpcErr *rpcErrPayload
		if errors.As(err, &rpcErr) && rpcErr.Code == rpcAuthRequired {
			return nil, fmt.Errorf("antigravity: %s: agy is not signed in; run `agy` once in a terminal to sign in: %w", method, err)
		}
		return nil, s.withStderr(fmt.Errorf("antigravity: %s: %w", method, err))
	}
	var setup sessionSetup
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &setup); err != nil {
			return nil, fmt.Errorf("antigravity: parse %s: %w", method, err)
		}
	}
	if method == "session/new" && setup.SessionID == "" {
		return nil, fmt.Errorf("antigravity: session/new returned no sessionId")
	}
	return &setup, nil
}

func (s *session) setID(id string) {
	s.idMu.Lock()
	s.id = id
	s.idMu.Unlock()
}

// ID returns the ACP session id, which is agy's conversation id.
func (s *session) ID() string {
	s.idMu.RLock()
	defer s.idMu.RUnlock()
	return s.id
}

func (s *session) absorbModes(m *acpModes) {
	if m == nil {
		return
	}
	s.modeMu.Lock()
	if len(m.AvailableModes) > 0 {
		s.modes = append([]acpMode(nil), m.AvailableModes...)
	}
	if m.CurrentModeID != "" {
		s.mode = m.CurrentModeID
	}
	modes, current := append([]acpMode(nil), s.modes...), s.mode
	s.modeMu.Unlock()
	if s.reporter != nil {
		s.reporter.reportModes(modes, current)
	}
}

func (s *session) absorbModels(m *acpModels) {
	if m == nil || s.reporter == nil {
		return
	}
	s.reporter.reportModels(m.AvailableModels, m.CurrentModelID)
}

// SetLiveMode implements core.LiveModeSwitcher. Yolo is a process flag, so
// switching into or out of it needs a new session and returns false.
func (s *session) SetLiveMode(mode string) bool {
	key := normalizeMode(mode)
	if !s.alive.Load() || key == modeYolo || s.yolo || key == "" {
		return false
	}
	s.modeMu.RLock()
	modeID := matchMode(key, s.modes)
	s.modeMu.RUnlock()
	if modeID == "" {
		slog.Warn("antigravity: agy offers no matching mode", "mode", mode)
		return false
	}
	if _, err := s.tr.call(s.ctx, "session/set_mode", map[string]any{"sessionId": s.ID(), "modeId": modeID}); err != nil {
		slog.Warn("antigravity: session/set_mode failed", "mode", modeID, "error", err)
		return false
	}
	s.absorbModes(&acpModes{CurrentModeID: modeID})
	return true
}

func (s *session) onNotification(method string, params json.RawMessage) {
	if method != "session/update" {
		slog.Debug("antigravity: notification", "method", method)
		return
	}
	if s.replaying.Load() {
		return
	}
	slog.Debug("antigravity: session/update", "params", truncate(string(params), 2000))
	var wrap struct {
		Update json.RawMessage `json:"update"`
	}
	var head struct {
		Kind          string            `json:"sessionUpdate"`
		CurrentModeID string            `json:"currentModeId"`
		ConfigOptions []acpConfigOption `json:"configOptions"`
	}
	if json.Unmarshal(params, &wrap) != nil || json.Unmarshal(wrap.Update, &head) != nil {
		return
	}
	switch head.Kind {
	case "current_mode_update":
		s.absorbModes(&acpModes{CurrentModeID: head.CurrentModeID})
	case "config_option_update":
		s.absorbConfigOptions(head.ConfigOptions)
	case "usage_update":
		s.absorbUsage(wrap.Update)
	}

	events := s.mapper.mapUpdate(params)
	var tc acpToolCall
	if head.Kind == "tool_call" || head.Kind == "tool_call_update" {
		_ = json.Unmarshal(wrap.Update, &tc)
		info, _ := s.mapper.lookup(tc.ToolCallID)
		s.tools.observe(head.Kind, tc, info)
	}
	if !s.turnActive.Load() {
		s.agentTurn.observe(head.Kind, tc.ToolCallID, tc.Status, len(events) > 0, time.Now())
	}
	for _, ev := range events {
		s.emit(ev)
	}
}

// watchAgentTurns ends turns agy started on its own (see agentTurnTracker)
// by emitting the EventResult the engine waits for.
func (s *session) watchAgentTurns() {
	defer s.wg.Done()
	ticker := time.NewTicker(agentTurnTick)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case now := <-ticker.C:
			if s.turnActive.Load() {
				continue
			}
			s.permMu.Lock()
			pending := len(s.perms)
			s.permMu.Unlock()
			if s.agentTurn.finish(now, pending) {
				slog.Debug("antigravity: turn agy started on its own has ended", "session_id", s.ID())
				s.emit(core.Event{Type: core.EventResult, Done: true})
			}
		}
	}
}

// BackgroundTasks implements core.BackgroundTaskReporter: the tool calls
// still running while no prompted turn is.
func (s *session) BackgroundTasks() []core.BackgroundTask {
	if s.turnActive.Load() {
		return nil
	}
	return s.tools.tasks()
}

func (s *session) onServerRequest(method string, id json.RawMessage, params json.RawMessage) {
	if method != "session/request_permission" {
		// The bot advertises no fs or terminal capability, so agy should
		// not ask for anything else.
		slog.Debug("antigravity: unsupported request from agy", "method", method)
		_ = s.tr.respondError(id, -32601, "method not supported by lark-agent-bot")
		return
	}
	var p struct {
		ToolCall acpToolCall        `json:"toolCall"`
		Options  []permissionOption `json:"options"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		_ = s.tr.respondError(id, -32602, "invalid params")
		return
	}
	slog.Debug("antigravity: session/request_permission", "params", truncate(string(params), 2000))
	requestID := "agy-perm-" + jsonIDKey(id)
	s.permMu.Lock()
	s.perms[requestID] = pendingPermission{rpcID: id, options: p.Options}
	s.permMu.Unlock()

	info, ok := s.mapper.lookup(p.ToolCall.ToolCallID)
	if !ok || info.name == "" {
		info.name = toolName(p.ToolCall)
	}
	if input := summarizeToolInput(p.ToolCall.Kind, p.ToolCall.RawInput); input != "" {
		info.input = input
	}
	if info.input == "" {
		info.input = p.ToolCall.Title
	}
	var rawInput map[string]any
	_ = json.Unmarshal(p.ToolCall.RawInput, &rawInput)
	if !s.turnActive.Load() {
		s.agentTurn.observe("permission", "", "", true, time.Now())
	}
	s.emit(core.Event{
		Type:         core.EventPermissionRequest,
		RequestID:    requestID,
		ToolName:     info.name,
		ToolInput:    info.input,
		ToolInputRaw: rawInput,
	})
}

// RespondPermission answers a pending session/request_permission.
func (s *session) RespondPermission(requestID string, result core.PermissionResult) error {
	s.permMu.Lock()
	pending, ok := s.perms[requestID]
	delete(s.perms, requestID)
	s.permMu.Unlock()
	if !ok {
		return fmt.Errorf("antigravity: unknown permission request %q", requestID)
	}
	allow := strings.EqualFold(strings.TrimSpace(result.Behavior), "allow")
	optionID := pickPermissionOption(allow, pending.options)
	if allow && optionID == "" {
		_ = s.tr.respondSuccess(pending.rpcID, permissionResponse(""))
		return fmt.Errorf("antigravity: agy offered no allow option for %q; the tool call was cancelled", requestID)
	}
	if err := s.tr.respondSuccess(pending.rpcID, permissionResponse(optionID)); err != nil {
		return fmt.Errorf("antigravity: answer permission request: %w", err)
	}
	return nil
}

// Send starts a turn and returns once agy has the prompt; the turn's output
// arrives on Events, ending with EventResult (or EventError on failure).
func (s *session) Send(prompt string, messageID string, images []core.ImageAttachment, files []core.FileAttachment) error {
	if !s.alive.Load() {
		return fmt.Errorf("antigravity: session is closed")
	}
	if !s.turnActive.CompareAndSwap(false, true) {
		return fmt.Errorf("antigravity: a turn is already running")
	}
	s.agentTurn.reset()
	paths := core.SaveFilesToDisk(s.workDir, messageID, files)
	blocks := promptBlocks(core.AppendFileRefs(prompt, paths), images)
	params := map[string]any{"sessionId": s.ID(), "prompt": blocks}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		raw, err := s.tr.call(s.ctx, "session/prompt", params)
		s.tools.turnEnded()
		s.turnActive.Store(false)
		if err != nil {
			if s.ctx.Err() != nil {
				return // closed by us
			}
			// If agy died, report that once (the exit watcher may too).
			if s.tr.closed() && !s.exitReported.CompareAndSwap(false, true) {
				return
			}
			s.emit(core.Event{Type: core.EventError, Error: s.withStderr(fmt.Errorf("antigravity: session/prompt: %w", err))})
			return
		}
		var res struct {
			StopReason string `json:"stopReason"`
			Usage      struct {
				InputTokens      int `json:"inputTokens"`
				OutputTokens     int `json:"outputTokens"`
				CachedReadTokens int `json:"cachedReadTokens"`
			} `json:"usage"`
		}
		_ = json.Unmarshal(raw, &res)
		if err := stopReasonError(res.StopReason); err != nil {
			s.emit(core.Event{Type: core.EventError, Error: err})
			return
		}
		s.emit(core.Event{
			Type:                 core.EventResult,
			Done:                 true,
			InputTokens:          res.Usage.InputTokens,
			OutputTokens:         res.Usage.OutputTokens,
			CacheReadInputTokens: res.Usage.CachedReadTokens,
		})
	}()
	return nil
}

// promptBlocks builds the ACP prompt: the text, then each image inline.
func promptBlocks(text string, images []core.ImageAttachment) []any {
	if strings.TrimSpace(text) == "" && len(images) > 0 {
		text = "Please look at the attached image(s)."
	}
	blocks := []any{map[string]any{"type": "text", "text": text}}
	for _, img := range images {
		mime := img.MimeType
		if mime == "" {
			mime = "image/png"
		}
		blocks = append(blocks, map[string]any{
			"type":     "image",
			"mimeType": mime,
			"data":     base64.StdEncoding.EncodeToString(img.Data),
		})
	}
	return blocks
}

func (s *session) emit(ev core.Event) {
	if ev.SessionID == "" {
		ev.SessionID = s.ID()
	}
	select {
	case s.events <- ev:
	case <-s.ctx.Done():
	}
}

// withStderr appends the tail of agy's stderr to err, when there is any.
func (s *session) withStderr(err error) error {
	if tail := strings.TrimSpace(s.stderr.String()); tail != "" {
		return fmt.Errorf("%w (agy stderr: %s)", err, truncate(tail, 1000))
	}
	return err
}

func (s *session) Events() <-chan core.Event { return s.events }
func (s *session) CurrentSessionID() string  { return s.ID() }
func (s *session) Alive() bool               { return s.alive.Load() }

// Close ends the session: it closes agy's stdin so agy can save the
// conversation and exit, kills it after closeGrace, and closes Events.
func (s *session) Close() error {
	s.closeOnce.Do(func() {
		s.alive.Store(false)
		s.cancel()
		_ = s.stdin.Close()
		select {
		case <-s.exited:
		case <-time.After(closeGrace):
			if s.cmd.Process != nil {
				_ = s.cmd.Process.Kill()
			}
		}
		done := make(chan struct{})
		go func() {
			s.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
			close(s.events)
		case <-time.After(closeGrace):
			slog.Warn("antigravity: close timed out waiting for the session's goroutines")
		}
	})
	return nil
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func newTailBuffer(max int) *tailBuffer { return &tailBuffer{max: max} }

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.max; over > 0 {
		b.buf = append([]byte(nil), b.buf[over:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
