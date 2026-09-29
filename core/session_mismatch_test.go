package core

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// newReasoningWorkspaceEngine returns a multi-workspace engine whose chat
// "feishu:<channel>:u1" is bound to a workspace with its own agent and
// sessions, next to a global agent the chat does not use.
func newReasoningWorkspaceEngine(t *testing.T) (e *Engine, p *stubPlatformEngine, globalAgent, wsAgent *stubModelModeAgent, ws *workspaceState, sessionKey string) {
	t.Helper()
	p = &stubPlatformEngine{n: "feishu"}
	globalAgent = &stubModelModeAgent{}
	e = NewEngine("test", globalAgent, []Platform{p}, "", LangEnglish)
	e.SetMultiWorkspace(t.TempDir(), filepath.Join(t.TempDir(), "bindings.json"))

	wsDir := normalizeWorkspacePath(t.TempDir())
	channelID := "C-card-reasoning"
	e.workspaceBindings.Bind("project:test", "feishu:"+channelID, "chan", wsDir)
	ws = e.workspacePool.GetOrCreate(wsDir)
	wsAgent = &stubModelModeAgent{}
	ws.agent = wsAgent
	ws.sessions = NewSessionManager("")
	return e, p, globalAgent, wsAgent, ws, "feishu:" + channelID + ":u1"
}

// Regression: the card's reasoning buttons changed the engine's global agent
// and reset a session in the global store, while stopping the running turn of
// the chat's workspace. The chat kept its old effort and lost its turn.
func TestCardReasoning_MultiWorkspaceChangesTheChatsWorkspaceAgent(t *testing.T) {
	e, _, globalAgent, wsAgent, ws, key := newReasoningWorkspaceEngine(t)
	wsSession := ws.sessions.GetOrCreateActive(key)
	wsSession.SetAgentSessionID("ws-thread", "test")
	globalSession := e.sessions.GetOrCreateActive(key)
	globalSession.SetAgentSessionID("global-thread", "test")

	e.executeCardAction("/reasoning", "3", key) // "high"

	if wsAgent.reasoningEffort != "high" {
		t.Fatalf("workspace agent effort = %q, want high", wsAgent.reasoningEffort)
	}
	if globalAgent.reasoningEffort != "" {
		t.Fatalf("global agent effort = %q, want untouched", globalAgent.reasoningEffort)
	}
	if got := wsSession.GetAgentSessionID(); got != "" {
		t.Fatalf("workspace session = %q, want reset like /reasoning", got)
	}
	if got := globalSession.GetAgentSessionID(); got != "global-thread" {
		t.Fatalf("global session = %q, want untouched", got)
	}
}

func TestRenderReasoningCard_ShowsTheChatsWorkspaceAgent(t *testing.T) {
	e, _, _, wsAgent, _, key := newReasoningWorkspaceEngine(t)
	wsAgent.reasoningEffort = "high"

	text := e.renderReasoningCard(key).RenderText()
	if !strings.Contains(text, e.i18n.Tf(MsgReasoningCurrent, "high")) {
		t.Fatalf("card = %q, want the workspace agent's effort", text)
	}
}

func TestSetReasoningEffort_TellsTheChatWhenItStopsARunningTurn(t *testing.T) {
	for _, running := range []bool{true, false} {
		p := &stubPlatformEngine{n: "test"}
		e := NewEngine("test", &stubModelModeAgent{}, []Platform{p}, "", LangEnglish)
		key := "test:user1"
		session := e.sessions.GetOrCreateActive(key)
		if running {
			if _, ok := session.TryLock(); !ok {
				t.Fatal("could not mark the session busy")
			}
		}
		e.interactiveMu.Lock()
		e.interactiveStates[key] = &interactiveState{platform: p, replyCtx: "ctx"}
		e.interactiveMu.Unlock()

		e.executeCardAction("/reasoning", "3", key)

		notice := e.i18n.T(MsgTurnStoppedBySettingChange)
		told := strings.Contains(strings.Join(p.getSent(), "\n"), notice)
		if told != running {
			t.Errorf("running=%v: sent %q, want notice=%v", running, p.getSent(), running)
		}
	}
}

// Regression: when a saved conversation could not be resumed, the engine
// silently started a new one and pinned the old conversation's title onto
// it, so the session card showed a title that belonged to another thread.
func TestResumeFailure_TellsTheUserAndDropsTheOldTitle(t *testing.T) {
	fresh := newControllableSession("fresh-thread")
	agent := &controllableAgent{
		startSessionFn: func(_ context.Context, sessionID string) (AgentSession, error) {
			if sessionID != "" {
				return nil, errors.New("bufio.Scanner: token too long")
			}
			return fresh, nil
		},
	}
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	key := "test:user1"
	session := e.sessions.GetOrCreateActive(key)
	session.SetName("Tripo models to split")
	session.SetAgentSessionID("old-thread-id", "controllable")

	state := e.getOrCreateInteractiveStateWith(key, p, "ctx", session, e.sessions, nil, "")
	if state.agentSession != fresh {
		t.Fatal("expected the fresh session after the resume failure")
	}

	want := e.i18n.Tf(MsgResumeFailedNewSession, "Tripo models to split")
	if sent := strings.Join(p.getSent(), "\n"); !strings.Contains(sent, want) {
		t.Fatalf("sent = %q, want the resume-failure notice %q", sent, want)
	}
	if got := e.sessions.GetSessionName("fresh-thread"); got != "" {
		t.Fatalf("new thread titled %q, want no title carried over from the lost conversation", got)
	}
}

func TestResumeFailureLabel(t *testing.T) {
	for _, tc := range []struct{ name, id, want string }{
		{"Tripo models", "01a0e8cb-35a3", "Tripo models"},
		{"default", "01a0e8cb-35a3", "01a0e8cb"},
		{"", "abc", "abc"},
		{strings.Repeat("长", 40), "x", strings.Repeat("长", 30) + "…"},
	} {
		if got := resumeFailureLabel(tc.name, tc.id); got != tc.want {
			t.Errorf("resumeFailureLabel(%q, %q) = %q, want %q", tc.name, tc.id, got, tc.want)
		}
	}
}
