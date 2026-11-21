package antigravity

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

type logEntry struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// newFakeAgent returns an Agent that runs the fake agy, plus its log path.
func newFakeAgent(t *testing.T, mode, model string, env ...string) (*Agent, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "agy.log")
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{
		workDir:      t.TempDir(),
		cmd:          bin,
		mode:         normalizeMode(mode),
		model:        model,
		configEnv:    append([]string{fakeAgyEnv + "=1", fakeLogEnv + "=" + logPath}, env...),
		instructions: "BOT INSTRUCTIONS",
	}
	return a, logPath
}

func startFake(t *testing.T, a *Agent, resumeID string) *session {
	t.Helper()
	as, err := a.StartSession(context.Background(), resumeID)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	s := as.(*session)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func readLog(t *testing.T, path string) []logEntry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fake log: %v", err)
	}
	defer f.Close()
	var out []logEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		var e logEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("bad log line %q: %v", sc.Text(), err)
		}
		out = append(out, e)
	}
	return out
}

func methods(entries []logEntry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Method)
	}
	return out
}

func find(entries []logEntry, method string) (logEntry, bool) {
	for _, e := range entries {
		if e.Method == method {
			return e, true
		}
	}
	return logEntry{}, false
}

// runTurn sends a prompt and collects events until the turn ends,
// answering any permission request with behavior.
func runTurn(t *testing.T, s *session, prompt string, images []core.ImageAttachment, behavior string) []core.Event {
	t.Helper()
	if err := s.Send(prompt, "msg-1", images, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var got []core.Event
	timeout := time.After(20 * time.Second)
	for {
		select {
		case ev, ok := <-s.Events():
			if !ok {
				t.Fatalf("events closed mid-turn; got %+v", got)
			}
			got = append(got, ev)
			switch ev.Type {
			case core.EventPermissionRequest:
				if err := s.RespondPermission(ev.RequestID, core.PermissionResult{Behavior: behavior}); err != nil {
					t.Fatalf("RespondPermission: %v", err)
				}
			case core.EventResult, core.EventError:
				return got
			}
		case <-timeout:
			t.Fatalf("turn did not finish; got %+v", got)
		}
	}
}

func textOf(events []core.Event, typ core.EventType) string {
	var b strings.Builder
	for _, ev := range events {
		if ev.Type == typ {
			b.WriteString(ev.Content)
		}
	}
	return b.String()
}

func eventOf(t *testing.T, events []core.Event, typ core.EventType) core.Event {
	t.Helper()
	for _, ev := range events {
		if ev.Type == typ {
			return ev
		}
	}
	t.Fatalf("no %s event in %+v", typ, events)
	return core.Event{}
}

func TestSession_NewSessionSetupAndFullTurn(t *testing.T) {
	a, logPath := newFakeAgent(t, "accept-edits", "gemini-b")
	a.SetPlatformPrompt("PLATFORM PROMPT")
	s := startFake(t, a, "")

	if got := s.CurrentSessionID(); got != "conv-1" {
		t.Fatalf("session id = %q, want conv-1", got)
	}
	img := core.ImageAttachment{MimeType: "image/jpeg", Data: []byte("JPEGDATA")}
	events := runTurn(t, s, "please run echo", []core.ImageAttachment{img}, "allow")

	if got := textOf(events, core.EventThinking); got != "thinking about it" {
		t.Errorf("thinking = %q", got)
	}
	if got := textOf(events, core.EventText); got != "Let me done." {
		t.Errorf("text = %q, want %q", got, "Let me done.")
	}
	use := eventOf(t, events, core.EventToolUse)
	if use.ToolName != "Run echo" || use.ToolInput != "echo hi" {
		t.Errorf("tool use = %q / %q", use.ToolName, use.ToolInput)
	}
	perm := eventOf(t, events, core.EventPermissionRequest)
	if perm.ToolName != "Run echo" || perm.ToolInput != "echo hi" {
		t.Errorf("permission request = %q / %q, want the cached tool name and command", perm.ToolName, perm.ToolInput)
	}
	res := eventOf(t, events, core.EventToolResult)
	if res.ToolName != "Run echo" || res.ToolResult != "hi" || res.ToolSuccess == nil || !*res.ToolSuccess {
		t.Errorf("tool result = %+v", res)
	}
	last := events[len(events)-1]
	if last.Type != core.EventResult || !last.Done || last.SessionID != "conv-1" {
		t.Errorf("last event = %+v, want a done EventResult for conv-1", last)
	}

	log := readLog(t, logPath)
	argv, _ := find(log, "argv")
	if string(argv.Params) != `["acp"]` {
		t.Errorf("agy argv = %s, want [\"acp\"]", argv.Params)
	}
	newCall, _ := find(log, "session/new")
	var newParams struct {
		Cwd  string `json:"cwd"`
		Meta struct {
			Antigravity struct {
				SystemPrompt struct {
					Append string `json:"append"`
				} `json:"systemPrompt"`
			} `json:"antigravity"`
		} `json:"_meta"`
	}
	_ = json.Unmarshal(newCall.Params, &newParams)
	if want := "BOT INSTRUCTIONS\n\nPLATFORM PROMPT"; newParams.Meta.Antigravity.SystemPrompt.Append != want {
		t.Errorf("systemPrompt.append = %q, want %q", newParams.Meta.Antigravity.SystemPrompt.Append, want)
	}
	if newParams.Cwd != s.workDir {
		t.Errorf("cwd = %q, want %q", newParams.Cwd, s.workDir)
	}
	if e, ok := find(log, "session/set_config_option"); !ok || !strings.Contains(string(e.Params), `"configId":"model"`) || !strings.Contains(string(e.Params), `"value":"gemini-b"`) {
		t.Errorf("model not set through the model config option: %s", e.Params)
	}
	if _, ok := find(log, "session/set_model"); ok {
		t.Errorf("legacy session/set_model used for a base model")
	}
	if e, ok := find(log, "session/set_mode"); !ok || !strings.Contains(string(e.Params), `"modeId":"accept-edits"`) {
		t.Errorf("session/set_mode not called with accept-edits: %s", e.Params)
	}
	prompt, _ := find(log, "session/prompt")
	if !strings.Contains(string(prompt.Params), `"mimeType":"image/jpeg"`) || !strings.Contains(string(prompt.Params), `"data":"SlBFR0RBVEE="`) {
		t.Errorf("image block missing from prompt: %s", prompt.Params)
	}
	reply, _ := find(log, "permission_reply")
	if !strings.Contains(string(reply.Params), `"optionId":"once"`) {
		t.Errorf("permission reply = %s, want the allow_once option", reply.Params)
	}

	if got := a.GetModel(); got != "gemini-b" {
		t.Errorf("GetModel = %q", got)
	}
	var names []string
	for _, m := range a.AvailableModels(context.Background()) {
		names = append(names, m.Name)
	}
	if !slices.Equal(names, []string{"gemini-a", "gemini-b"}) {
		t.Errorf("AvailableModels = %v", names)
	}
}

func TestSession_DenyPicksRejectOption(t *testing.T) {
	a, logPath := newFakeAgent(t, "", "")
	s := startFake(t, a, "")
	events := runTurn(t, s, "run it", nil, "deny")

	res := eventOf(t, events, core.EventToolResult)
	if res.ToolSuccess == nil || *res.ToolSuccess || res.ToolStatus != "failed" {
		t.Errorf("tool result = %+v, want a failed result", res)
	}
	log := readLog(t, logPath)
	reply, _ := find(log, "permission_reply")
	if !strings.Contains(string(reply.Params), `"optionId":"no"`) {
		t.Errorf("permission reply = %s, want the reject_once option", reply.Params)
	}
	// No mode or model configured: agy's defaults are kept.
	for _, m := range []string{"session/set_mode", "session/set_model"} {
		if _, ok := find(log, m); ok {
			t.Errorf("%s called without a configured value", m)
		}
	}
	if got := a.GetMode(); got != modeDefault {
		t.Errorf("GetMode = %q, want agy's current mode %q", got, modeDefault)
	}
}

func TestSession_ResumeUsesSessionResume(t *testing.T) {
	a, logPath := newFakeAgent(t, "", "")
	s := startFake(t, a, "conv-9")

	if got := s.CurrentSessionID(); got != "conv-9" {
		t.Fatalf("session id = %q, want conv-9", got)
	}
	log := readLog(t, logPath)
	if _, ok := find(log, "session/new"); ok {
		t.Errorf("session/new called when resuming: %v", methods(log))
	}
	e, ok := find(log, "session/resume")
	if !ok || !strings.Contains(string(e.Params), `"sessionId":"conv-9"`) || !strings.Contains(string(e.Params), "BOT INSTRUCTIONS") {
		t.Errorf("session/resume params = %s, want conv-9 and the instructions", e.Params)
	}
}

func TestSession_LoadFallbackDropsReplayedHistory(t *testing.T) {
	a, logPath := newFakeAgent(t, "", "", fakeNoResumeEnv+"=1")
	s := startFake(t, a, "conv-9")

	events := runTurn(t, s, "plain please", nil, "allow")
	if got := textOf(events, core.EventText); got != "plain reply" {
		t.Errorf("text = %q; replayed history must not reach the reply", got)
	}
	if _, ok := find(readLog(t, logPath), "session/load"); !ok {
		t.Errorf("session/load not used")
	}
}

func TestSession_NotSignedIn(t *testing.T) {
	a, _ := newFakeAgent(t, "", "", fakeNoAuthEnv+"=1")
	_, err := a.StartSession(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("err = %v, want a sign-in hint", err)
	}
}

func TestSession_FailedTurns(t *testing.T) {
	a, _ := newFakeAgent(t, "", "")
	s := startFake(t, a, "")

	events := runTurn(t, s, "please fail", nil, "allow")
	ev := events[len(events)-1]
	if ev.Type != core.EventError || !strings.Contains(ev.Error.Error(), "model backend exploded") {
		t.Errorf("last event = %+v, want an EventError carrying agy's stderr", ev)
	}

	events = runTurn(t, s, "refuse this", nil, "allow")
	ev = events[len(events)-1]
	if ev.Type != core.EventError || !strings.Contains(ev.Error.Error(), "refused") {
		t.Errorf("last event = %+v, want a refusal error", ev)
	}

	// The session stays usable after a failed turn.
	events = runTurn(t, s, "plain", nil, "allow")
	if events[len(events)-1].Type != core.EventResult {
		t.Errorf("turn after failures = %+v", events)
	}
}

func TestSession_YoloStartsWithSkipPermissions(t *testing.T) {
	a, logPath := newFakeAgent(t, "yolo", "")
	s := startFake(t, a, "")

	log := readLog(t, logPath)
	argv, _ := find(log, "argv")
	if string(argv.Params) != `["acp","--dangerously-skip-permissions"]` {
		t.Errorf("agy argv = %s", argv.Params)
	}
	if _, ok := find(log, "session/set_mode"); ok {
		t.Errorf("session/set_mode called in yolo mode")
	}
	if s.SetLiveMode("plan") {
		t.Errorf("leaving yolo must restart the session, not switch live")
	}
}

func TestSession_LiveModeSwitch(t *testing.T) {
	a, logPath := newFakeAgent(t, "", "")
	s := startFake(t, a, "")

	if !s.SetLiveMode("Plan") {
		t.Fatalf("SetLiveMode(Plan) = false")
	}
	if s.SetLiveMode("yolo") {
		t.Errorf("switching into yolo must restart the session")
	}
	if s.SetLiveMode("no-such-mode") {
		t.Errorf("unknown mode accepted")
	}
	if e, ok := find(readLog(t, logPath), "session/set_mode"); !ok || !strings.Contains(string(e.Params), `"modeId":"plan"`) {
		t.Errorf("session/set_mode params = %s", e.Params)
	}
}

func TestSession_CloseStopsAgyAndClosesEvents(t *testing.T) {
	a, _ := newFakeAgent(t, "", "")
	s := startFake(t, a, "")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.exited:
	default:
		t.Errorf("agy still running after Close")
	}
	if _, ok := <-s.Events(); ok {
		t.Errorf("events channel still open after Close")
	}
	if s.Alive() {
		t.Errorf("Alive after Close")
	}
	if err := s.Send("hi", "", nil, nil); err == nil {
		t.Errorf("Send after Close succeeded")
	}
}

func TestListSessions_FiltersToWorkDir(t *testing.T) {
	a, logPath := newFakeAgent(t, "", "")
	got, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "conv-1" || got[0].Summary != "First chat" || got[0].ModifiedAt.IsZero() {
		t.Fatalf("ListSessions = %+v", got)
	}
	if _, ok := find(readLog(t, logPath), "session/new"); ok {
		t.Errorf("listing created a conversation")
	}
}
