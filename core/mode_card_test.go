package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

type modeJourneyAgent struct {
	stubModelModeAgent
}

func (a *modeJourneyAgent) StartSession(_ context.Context, id string) (AgentSession, error) {
	s := newCUJAgentSession()
	s.reply = "mode=" + a.GetMode() + "; resumed=" + id
	return s, nil
}

func newModeCardWorkspaceEngine(t *testing.T) (*Engine, *stubPlatformEngine, *stubModelModeAgent, *modeJourneyAgent, *SessionManager, string) {
	t.Helper()
	p := &stubPlatformEngine{n: "test"}
	global := &stubModelModeAgent{mode: "default"}
	e := NewEngine("test", global, []Platform{p}, filepath.Join(t.TempDir(), "sessions.json"), LangEnglish)
	root := t.TempDir()
	e.SetMultiWorkspace(root, filepath.Join(t.TempDir(), "bindings.json"))
	key := "test:group:user"
	workspace := normalizeWorkspacePath(root)
	e.workspaceBindings.Bind("project:test", "test:group", "", workspace)
	ws := e.workspacePool.GetOrCreate(workspace)
	a := &modeJourneyAgent{}
	sm := NewSessionManager(filepath.Join(t.TempDir(), "workspace-sessions.json"))
	ws.agent, ws.sessions = a, sm
	t.Cleanup(func() { e.cleanupInteractiveState(e.interactiveKeyForSessionKey(key)) })
	return e, p, global, a, sm, key
}

func TestModeCard_ChangesBoundWorkspaceAndPreservesHistory(t *testing.T) {
	e, _, global, agent, sessions, key := newModeCardWorkspaceEngine(t)
	s := sessions.GetOrCreateActive(key)
	s.SetAgentSessionID("existing-thread", "stub")
	s.AddHistory("user", "keep this context")
	card := e.handleCardNav("act:/mode yolo", key)
	if agent.GetMode() != "yolo" || global.GetMode() != "default" {
		t.Fatalf("wrong agent changed: workspace=%s global=%s", agent.GetMode(), global.GetMode())
	}
	if !strings.Contains(card.RenderText(), "▶ **YOLO**") {
		t.Fatalf("card does not show workspace mode: %s", card.RenderText())
	}
	if s.GetAgentSessionID() != "existing-thread" || len(s.GetHistory(0)) != 1 {
		t.Fatal("mode selection discarded the existing conversation")
	}
}

func TestModeCard_LiveChangeKeepsSession(t *testing.T) {
	e, p, _, _, _, key := newModeCardWorkspaceEngine(t)
	live := &stubLiveModeSession{}
	state := &interactiveState{agentSession: live, platform: p}
	iKey := e.interactiveKeyForSessionKey(key)
	e.interactiveStates[iKey] = state
	e.handleCardNav("act:/mode yolo", key)
	if len(live.modes) != 1 || live.modes[0] != "yolo" || state.agentSession != live {
		t.Fatal("successful live mode update must keep the running session")
	}
}

func TestModeCard_SingleWorkspacePreservesHistory(t *testing.T) {
	e := NewEngine("test", &stubModelModeAgent{}, nil, filepath.Join(t.TempDir(), "sessions.json"), LangEnglish)
	s := e.sessions.GetOrCreateActive("test:group:user")
	s.SetAgentSessionID("existing-thread", "stub")
	s.AddHistory("user", "keep this context")
	e.handleCardNav("act:/mode yolo", "test:group:user")
	if s.GetAgentSessionID() != "existing-thread" || len(s.GetHistory(0)) != 1 {
		t.Fatal("mode selection discarded the existing conversation")
	}
}
