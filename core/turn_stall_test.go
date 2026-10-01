package core

import (
	"strings"
	"testing"
	"time"
)

type stallTestTurn struct {
	e       *Engine
	p       *stubPlatformEngine
	session *controllableAgentSession
	state   *interactiveState
	key     string
	done    chan struct{}
}

func startStallTestTurn(t *testing.T, model, tool time.Duration, alive bool) *stallTestTurn {
	t.Helper()
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.SetDataDir(t.TempDir())
	e.SetStallNotice(model, tool)
	as := newControllableSession("s1")
	as.alive = alive
	tt := &stallTestTurn{
		e:       e,
		p:       p,
		session: as,
		state:   &interactiveState{agentSession: as, platform: p, replyCtx: "ctx-1"},
		key:     "test:user1",
		done:    make(chan struct{}),
	}
	e.interactiveStates[tt.key] = tt.state
	e.beginTurnJournal(tt.key, "test", tt.key, "m1", "do the thing")
	session := e.sessions.GetOrCreateActive(tt.key)
	go func() {
		defer close(tt.done)
		e.processInteractiveEvents(tt.state, session, e.sessions, tt.key, "m1", time.Now(), nil, nil, "ctx-1", 0)
	}()
	t.Cleanup(func() {
		select {
		case <-tt.done:
		default:
			tt.session.events <- Event{Type: EventResult, Content: "cleanup", Done: true}
			<-tt.done
		}
	})
	return tt
}

func (tt *stallTestTurn) finish(t *testing.T) {
	t.Helper()
	tt.session.events <- Event{Type: EventResult, Content: "done", Done: true}
	select {
	case <-tt.done:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish")
	}
}

// sentMatching returns the sent messages that contain substr.
func (tt *stallTestTurn) sentMatching(substr string) []string {
	var out []string
	for _, s := range tt.p.getSent() {
		if strings.Contains(s, substr) {
			out = append(out, s)
		}
	}
	return out
}

func (tt *stallTestTurn) waitFor(t *testing.T, substr string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := tt.sentMatching(substr); len(got) > 0 {
			return got[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no message containing %q; sent = %#v", substr, tt.p.getSent())
	return ""
}

const (
	stallModelMarker = "waiting on the model"
	stallToolMarker  = "has been running for"
)

func TestStallNotice_ModelPhaseNotifiesOncePerSilence(t *testing.T) {
	tt := startStallTestTurn(t, 100*time.Millisecond, time.Hour, true)
	tt.waitFor(t, stallModelMarker)
	time.Sleep(400 * time.Millisecond)
	if got := tt.sentMatching(stallModelMarker); len(got) != 1 {
		t.Fatalf("model notices = %d, want 1 per silence: %#v", len(got), got)
	}

	// New output starts a new silence, which may be reported again.
	tt.session.events <- Event{Type: EventText, Content: "partial"}
	deadline := time.Now().Add(5 * time.Second)
	for len(tt.sentMatching(stallModelMarker)) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := tt.sentMatching(stallModelMarker); len(got) != 2 {
		t.Fatalf("model notices after new output = %d, want 2", len(got))
	}
	tt.finish(t)
}

func TestStallNotice_ToolPhaseNamesTheTool(t *testing.T) {
	tt := startStallTestTurn(t, time.Hour, 100*time.Millisecond, true)
	tt.session.events <- Event{Type: EventToolUse, ToolName: "Bash", ToolInput: "gh pr checks 98\n  --watch"}
	got := tt.waitFor(t, stallToolMarker)
	if !strings.Contains(got, "Bash: gh pr checks 98 --watch") {
		t.Fatalf("tool notice = %q, want the tool name and flattened input", got)
	}
	tt.finish(t)
}

func TestStallNotice_RunningToolUsesToolThreshold(t *testing.T) {
	tt := startStallTestTurn(t, 100*time.Millisecond, time.Hour, true)
	tt.session.events <- Event{Type: EventToolUse, ToolName: "Bash", ToolInput: "make test"}
	time.Sleep(500 * time.Millisecond)
	if got := append(tt.sentMatching(stallModelMarker), tt.sentMatching(stallToolMarker)...); len(got) != 0 {
		t.Fatalf("notice sent while a tool runs within its threshold: %#v", got)
	}

	// Once the tool result is in, the agent waits on the model again.
	tt.session.events <- Event{Type: EventToolResult, ToolName: "Bash", ToolResult: "ok"}
	tt.waitFor(t, stallModelMarker)
	tt.finish(t)
}

func TestStallNotice_DeadAgentEndsTurn(t *testing.T) {
	tt := startStallTestTurn(t, 100*time.Millisecond, time.Hour, false)
	select {
	case <-tt.done:
	case <-time.After(5 * time.Second):
		t.Fatal("turn with a dead agent did not end")
	}
	got := tt.p.getSent()
	if len(got) != 1 || got[0] != tt.e.i18n.T(MsgAgentExitedMidTurn) {
		t.Fatalf("sent = %#v, want only the agent-exited notice", got)
	}
	if leftover := journalLeftover(t, tt.e); len(leftover) != 0 {
		t.Fatalf("ended turn left a journal entry: %+v", leftover)
	}
}

func TestStallNotice_PermissionWaitDoesNotCount(t *testing.T) {
	tt := startStallTestTurn(t, 500*time.Millisecond, time.Hour, true)
	tt.session.events <- Event{Type: EventPermissionRequest, RequestID: "r1", ToolName: "Bash", ToolInput: "rm -rf build"}

	var pending *pendingPermission
	deadline := time.Now().Add(5 * time.Second)
	for pending == nil && time.Now().Before(deadline) {
		tt.state.mu.Lock()
		pending = tt.state.pending
		tt.state.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	if pending == nil {
		t.Fatal("permission request never became pending")
	}

	// The user takes longer than the threshold to answer.
	time.Sleep(1200 * time.Millisecond)
	pending.resolve()
	time.Sleep(150 * time.Millisecond)
	if got := tt.sentMatching(stallModelMarker); len(got) != 0 {
		t.Fatalf("stall notice for time spent waiting on the user: %#v", got)
	}
	tt.finish(t)
}

func TestTurnStallWatch_ContentlessEventKeepsToolPhase(t *testing.T) {
	w := newTurnStallWatch(time.Hour, time.Hour)
	defer w.stop()
	w.observe(Event{Type: EventToolUse, ToolName: "Bash"})
	// Claude Code system messages reach the engine as a text event that only
	// carries the session ID.
	w.observe(Event{Type: EventText, SessionID: "sid"})
	if w.openTools != 1 {
		t.Fatalf("openTools = %d after a content-less event, want 1", w.openTools)
	}
	w.observe(Event{Type: EventText, Content: "done"})
	if w.openTools != 0 {
		t.Fatalf("openTools = %d after model text, want 0", w.openTools)
	}
}

func TestTurnStallWatch_DisabledPhaseNeverFires(t *testing.T) {
	w := newTurnStallWatch(0, 0)
	defer w.stop()
	if w.C() != nil {
		t.Fatal("disabled watch returned a channel")
	}
	w.observe(Event{Type: EventToolUse, ToolName: "Bash"})
	if w.C() != nil {
		t.Fatal("disabled tool phase returned a channel")
	}
}
