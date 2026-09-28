package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

type strictModeJourneyAgent struct{ modeJourneyAgent }

func (a *strictModeJourneyAgent) ValidateMode(mode string) error {
	for _, item := range a.PermissionModes() {
		if item.Key == mode {
			return nil
		}
	}
	return fmt.Errorf("unsupported permission mode %q", mode)
}

func (a *strictModeJourneyAgent) PermissionModes() []PermissionModeInfo {
	return []PermissionModeInfo{
		{Key: "default", NameKey: MsgPermissionDefaultName, DescKey: MsgPermissionDefaultDesc},
		{Key: "auto-review", NameKey: MsgPermissionAutoReviewName, DescKey: MsgPermissionAutoReviewDesc},
		{Key: "read-only", NameKey: MsgPermissionReadOnlyName, DescKey: MsgPermissionReadOnlyDesc},
		{Key: "full-access", NameKey: MsgPermissionFullAccessName, DescKey: MsgPermissionFullAccessDesc},
	}
}

func TestModeCard_RejectsObsoleteActionWithoutClosingSession(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	agent := &strictModeJourneyAgent{}
	e := NewEngine("test", agent, []Platform{p}, filepath.Join(t.TempDir(), "sessions.json"), LangEnglish)
	key := "test:group:user"
	live := &stubLiveModeSession{}
	state := &interactiveState{agentSession: live, platform: p}
	e.interactiveStates[key] = state
	s := e.sessions.GetOrCreateActive(key)
	s.SetAgentSessionID("kept-thread", "stub")
	s.AddHistory("user", "keep history")
	for _, action := range []string{"act:/mode full-auto", "act:/mode auto-edit"} {
		card := e.handleCardNav(action, key)
		if !strings.Contains(card.RenderText(), "Unsupported permission mode") {
			t.Fatalf("missing visible rejection: %s", card.RenderText())
		}
		if state.agentSession != live || len(live.modes) != 0 || agent.GetMode() != "default" || s.GetAgentSessionID() != "kept-thread" || len(s.GetHistory(0)) != 1 {
			t.Fatal("invalid action changed or closed the conversation")
		}
	}
	e.cleanupInteractiveState(key)
}

func TestPermissionModes_LocalizedCardAndText(t *testing.T) {
	for _, lang := range []Language{LangEnglish, LangChinese, LangTraditionalChinese, LangJapanese, LangSpanish} {
		for _, cards := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cards=%t", lang, cards), func(t *testing.T) {
				base := &stubPlatformEngine{n: "test"}
				var p Platform = base
				if cards {
					p = &workspacePickerPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}
				}
				e := NewEngine("test", &strictModeJourneyAgent{}, []Platform{p}, filepath.Join(t.TempDir(), "sessions.json"), lang)
				e.ReceiveMessage(p, &Message{SessionKey: "test:group:user", Platform: "test", UserID: "user", Content: "/mode", ReplyCtx: "ctx"})
				output := strings.Join(base.getSent(), "\n")
				if cards {
					output = strings.Join(p.(*workspacePickerPlatform).getSent(), "\n")
				}
				for _, mode := range (&strictModeJourneyAgent{}).PermissionModes() {
					if !strings.Contains(output, e.i18n.T(mode.NameKey)) || !strings.Contains(output, e.i18n.T(mode.DescKey)) {
						t.Fatalf("missing localized mode %s: %s", mode.Key, output)
					}
				}
			})
		}
	}
}

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
