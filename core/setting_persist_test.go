package core

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// /mode, /reasoning, /lang and /allow used to change only the running bot,
// so a restart (and, for a workspace agent, the idle reap) put them back to
// the config file's values. These tests pin that each one is saved where
// the rebuilt agent or the next start reads it.

// stubToolAuthAgent is a mode/model stub that also takes /allow.
type stubToolAuthAgent struct {
	namedStubModelModeAgent
	allowed []string
}

func (a *stubToolAuthAgent) AddAllowedTools(tools ...string) error {
	for _, tool := range tools {
		if !slices.Contains(a.allowed, tool) {
			a.allowed = append(a.allowed, tool)
		}
	}
	return nil
}

func (a *stubToolAuthAgent) GetAllowedTools() []string { return slices.Clone(a.allowed) }

func (a *stubToolAuthAgent) WorkspaceAgentOptions() map[string]any {
	opts := map[string]any{}
	if len(a.allowed) > 0 {
		opts["allowed_tools"] = mergeAllowedToolsOption(nil, a.allowed)
	}
	return opts
}

func lastSent(p *stubPlatformEngine) string {
	sent := p.getSent()
	if len(sent) == 0 {
		return ""
	}
	return sent[len(sent)-1]
}

// newWorkspaceSettingEngine returns a multi-workspace engine whose project
// agent is global, with one workspace bound to a channel and its agent
// already created. The project settings saver must not be reached from a
// workspace, so it fails the test.
func newWorkspaceSettingEngine(t *testing.T, global Agent) (e *Engine, statePath, wsDir, sessionKey string) {
	t.Helper()
	p := &stubPlatformEngine{n: "plain"}
	e = NewEngine("test", global, []Platform{p}, filepath.Join(t.TempDir(), "sessions.json"), LangEnglish)
	statePath = filepath.Join(t.TempDir(), "projects", "test.state.json")
	e.SetProjectStateStore(NewProjectStateStore(statePath))
	e.SetMultiWorkspace(t.TempDir(), filepath.Join(t.TempDir(), "bindings.json"))
	e.SetProjectSettingsSaver(func(u ProjectSettingsUpdate) error {
		t.Errorf("a workspace setting reached the project config: %+v", u)
		return nil
	})
	e.SetAllowedToolsSaveFunc(func(tools []string) error {
		t.Errorf("a workspace /allow reached the project config: %v", tools)
		return nil
	})

	wsDir = normalizeWorkspacePath(t.TempDir())
	channelID := "C-setting-persist"
	e.workspaceBindings.Bind("project:test", channelID, "chan", wsDir)
	sessionKey = "feishu:" + channelID + ":u1"
	if _, _, err := e.getOrCreateWorkspaceAgent(wsDir); err != nil {
		t.Fatalf("getOrCreateWorkspaceAgent: %v", err)
	}
	return e, statePath, wsDir, sessionKey
}

// reapWorkspaceAgent drops the workspace's agent the way the idle reap does,
// then rebuilds it.
func reapWorkspaceAgent(t *testing.T, e *Engine, wsDir string) Agent {
	t.Helper()
	ws := e.workspacePool.GetOrCreate(wsDir)
	ws.mu.Lock()
	ws.agent = nil
	ws.sessions = nil
	ws.mu.Unlock()
	agent, _, err := e.getOrCreateWorkspaceAgent(wsDir)
	if err != nil {
		t.Fatalf("getOrCreateWorkspaceAgent after reap: %v", err)
	}
	return agent
}

func TestSetMode_ProjectAgentSavesToConfig(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(e *Engine, p Platform, sessionKey string)
	}{
		{"command", func(e *Engine, p Platform, sessionKey string) {
			e.cmdMode(p, &Message{SessionKey: sessionKey, ReplyCtx: "ctx"}, []string{"yolo"})
		}},
		{"card", func(e *Engine, _ Platform, sessionKey string) {
			e.handleCardNav("act:/mode yolo", sessionKey)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &stubPlatformEngine{n: "plain"}
			agent := &stubModelModeAgent{}
			e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
			var saved []ProjectSettingsUpdate
			e.SetProjectSettingsSaver(func(u ProjectSettingsUpdate) error {
				saved = append(saved, u)
				return nil
			})

			tc.apply(e, p, "feishu:chat1:u1")

			if len(saved) != 1 || saved[0].Mode == nil || *saved[0].Mode != "yolo" {
				t.Fatalf("saved = %+v, want one update with mode yolo", saved)
			}
			if got := agent.GetMode(); got != "yolo" {
				t.Fatalf("agent mode = %q, want yolo", got)
			}
		})
	}
}

func TestSetMode_SaveFailureKeepsOldMode(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	agent := &stubModelModeAgent{}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	e.SetProjectSettingsSaver(func(ProjectSettingsUpdate) error { return errors.New("disk full") })

	e.cmdMode(p, &Message{SessionKey: "feishu:chat1:u1", ReplyCtx: "ctx"}, []string{"yolo"})
	if got := agent.GetMode(); got != "default" {
		t.Fatalf("agent mode after a failed save = %q, want default", got)
	}
	if reply := lastSent(p); !strings.Contains(reply, "disk full") {
		t.Fatalf("reply = %q, want the save error", reply)
	}

	card := e.handleCardNav("act:/mode yolo", "feishu:chat1:u1")
	if got := agent.GetMode(); got != "default" {
		t.Fatalf("agent mode after a failed card save = %q, want default", got)
	}
	if card == nil || !strings.Contains(card.RenderText(), "disk full") {
		t.Fatalf("card must show the save error, got %v", card)
	}
}

func TestSetMode_MultiWorkspacePersistsForRecreatedAgent(t *testing.T) {
	agentName := "test-workspace-mode-override"
	RegisterAgent(agentName, func(opts map[string]any) (Agent, error) {
		agent := &namedStubModelModeAgent{name: agentName}
		agent.mode, _ = opts["mode"].(string)
		return agent, nil
	})

	for _, tc := range []struct {
		name  string
		apply func(e *Engine, sessionKey string)
	}{
		{"command", func(e *Engine, sessionKey string) {
			e.cmdMode(e.platforms[0], &Message{SessionKey: sessionKey, ReplyCtx: "ctx"}, []string{"yolo"})
		}},
		{"card", func(e *Engine, sessionKey string) {
			e.handleCardNav("act:/mode yolo", sessionKey)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			global := &namedStubModelModeAgent{name: agentName}
			e, statePath, wsDir, sessionKey := newWorkspaceSettingEngine(t, global)

			tc.apply(e, sessionKey)

			if got := global.GetMode(); got != "default" {
				t.Fatalf("project agent mode = %q, want untouched", got)
			}
			if got := NewProjectStateStore(statePath).WorkspaceModeOverride(wsDir); got != "yolo" {
				t.Fatalf("saved WorkspaceModeOverride(%q) = %q, want yolo", wsDir, got)
			}
			recreated := reapWorkspaceAgent(t, e, wsDir)
			if got := recreated.(*namedStubModelModeAgent).GetMode(); got != "yolo" {
				t.Fatalf("recreated workspace agent mode = %q, want the persisted yolo", got)
			}
		})
	}
}

func TestSetReasoningEffort_ProjectAgentSavesToConfig(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(e *Engine, p Platform, sessionKey string)
	}{
		{"command", func(e *Engine, p Platform, sessionKey string) {
			e.cmdReasoning(p, &Message{SessionKey: sessionKey, ReplyCtx: "ctx"}, []string{"high"})
		}},
		{"card", func(e *Engine, _ Platform, sessionKey string) {
			e.handleCardNav("act:/reasoning high", sessionKey)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &stubPlatformEngine{n: "plain"}
			agent := &stubModelModeAgent{}
			e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
			var saved []string
			e.SetReasoningSaveFunc(func(effort string) error {
				saved = append(saved, effort)
				return nil
			})

			tc.apply(e, p, "feishu:chat1:u1")

			if !slices.Equal(saved, []string{"high"}) {
				t.Fatalf("saved efforts = %v, want [high]", saved)
			}
			if agent.reasoningEffort != "high" {
				t.Fatalf("agent effort = %q, want high", agent.reasoningEffort)
			}
		})
	}
}

func TestSetReasoningEffort_SaveFailureKeepsOldEffort(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	agent := &stubModelModeAgent{reasoningEffort: "low"}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	e.SetReasoningSaveFunc(func(string) error { return errors.New("disk full") })

	e.cmdReasoning(p, &Message{SessionKey: "feishu:chat1:u1", ReplyCtx: "ctx"}, []string{"high"})
	if agent.reasoningEffort != "low" {
		t.Fatalf("agent effort after a failed save = %q, want low", agent.reasoningEffort)
	}
	if reply := lastSent(p); !strings.Contains(reply, "disk full") {
		t.Fatalf("reply = %q, want the save error", reply)
	}

	card := e.handleCardNav("act:/reasoning high", "feishu:chat1:u1")
	if agent.reasoningEffort != "low" {
		t.Fatalf("agent effort after a failed card save = %q, want low", agent.reasoningEffort)
	}
	if card == nil || !strings.Contains(card.RenderText(), "disk full") {
		t.Fatalf("card must show the save error, got %v", card)
	}
}

// A workspace's /reasoning stays in the project state file (#64) and must
// not overwrite the project's config default.
func TestSetReasoningEffort_WorkspaceSkipsConfigSave(t *testing.T) {
	agentName := "test-workspace-reasoning-no-config"
	RegisterAgent(agentName, func(map[string]any) (Agent, error) {
		return &namedStubModelModeAgent{name: agentName}, nil
	})
	e, _, _, sessionKey := newWorkspaceSettingEngine(t, &namedStubModelModeAgent{name: agentName})
	e.SetReasoningSaveFunc(func(effort string) error {
		t.Errorf("a workspace /reasoning reached the project config: %q", effort)
		return nil
	})

	e.cmdReasoning(e.platforms[0], &Message{SessionKey: sessionKey, ReplyCtx: "ctx"}, []string{"high"})
}

func TestSetLanguage_SavesToConfig(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(e *Engine, p Platform, lang string)
	}{
		{"command", func(e *Engine, p Platform, lang string) {
			e.cmdLang(p, &Message{SessionKey: "feishu:chat1:u1", ReplyCtx: "ctx"}, []string{lang})
		}},
		{"card", func(e *Engine, _ Platform, lang string) {
			e.handleCardNav("act:/lang "+lang, "feishu:chat1:u1")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &stubPlatformEngine{n: "plain"}
			e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
			var saved []string
			e.SetProjectSettingsSaver(func(u ProjectSettingsUpdate) error {
				if u.Language == nil {
					t.Fatalf("update without language: %+v", u)
				}
				saved = append(saved, *u.Language)
				return nil
			})

			tc.apply(e, p, "ja")
			if got := e.i18n.CurrentLang(); got != LangJapanese {
				t.Fatalf("language = %q, want ja", got)
			}
			tc.apply(e, p, "auto")

			if !slices.Equal(saved, []string{"ja", ""}) {
				t.Fatalf("saved languages = %q, want [ja, \"\"] (auto is saved as empty)", saved)
			}
		})
	}
}

func TestSetLanguage_SaveFailureKeepsOldLanguage(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.SetProjectSettingsSaver(func(ProjectSettingsUpdate) error { return errors.New("disk full") })

	e.cmdLang(p, &Message{SessionKey: "feishu:chat1:u1", ReplyCtx: "ctx"}, []string{"ja"})
	if got := e.i18n.CurrentLang(); got != LangEnglish {
		t.Fatalf("language after a failed save = %q, want en", got)
	}
	if reply := lastSent(p); !strings.Contains(reply, "disk full") {
		t.Fatalf("reply = %q, want the save error", reply)
	}

	card := e.handleCardNav("act:/lang ja", "feishu:chat1:u1")
	if got := e.i18n.CurrentLang(); got != LangEnglish {
		t.Fatalf("language after a failed card save = %q, want en", got)
	}
	if card == nil || !strings.Contains(card.RenderText(), "disk full") {
		t.Fatalf("card must show the save error, got %v", card)
	}
}

func TestCmdAllow_ProjectAgentSavesWholeList(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	agent := &stubToolAuthAgent{allowed: []string{"Read"}}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	var saved [][]string
	e.SetAllowedToolsSaveFunc(func(tools []string) error {
		saved = append(saved, tools)
		return nil
	})
	msg := &Message{SessionKey: "feishu:chat1:u1", ReplyCtx: "ctx"}

	e.cmdAllow(p, msg, []string{"Bash"})
	e.cmdAllow(p, msg, []string{"Bash"}) // already allowed: nothing to save

	if len(saved) != 1 || !slices.Equal(saved[0], []string{"Read", "Bash"}) {
		t.Fatalf("saved = %v, want one save of [Read Bash]", saved)
	}
	if !slices.Equal(agent.allowed, []string{"Read", "Bash"}) {
		t.Fatalf("agent allowed tools = %v, want [Read Bash]", agent.allowed)
	}
}

func TestCmdAllow_SaveFailureKeepsOldList(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	agent := &stubToolAuthAgent{allowed: []string{"Read"}}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	e.SetAllowedToolsSaveFunc(func([]string) error { return errors.New("disk full") })

	e.cmdAllow(p, &Message{SessionKey: "feishu:chat1:u1", ReplyCtx: "ctx"}, []string{"Bash"})

	if !slices.Equal(agent.allowed, []string{"Read"}) {
		t.Fatalf("agent allowed tools after a failed save = %v, want [Read]", agent.allowed)
	}
	if reply := lastSent(p); !strings.Contains(reply, "disk full") {
		t.Fatalf("reply = %q, want the save error", reply)
	}
}

func TestCmdAllow_MultiWorkspacePersistsForRecreatedAgent(t *testing.T) {
	agentName := "test-workspace-allowed-tools"
	RegisterAgent(agentName, func(opts map[string]any) (Agent, error) {
		agent := &stubToolAuthAgent{namedStubModelModeAgent: namedStubModelModeAgent{name: agentName}}
		tools, _ := opts["allowed_tools"].([]any)
		for _, tool := range tools {
			agent.allowed = append(agent.allowed, tool.(string))
		}
		return agent, nil
	})
	global := &stubToolAuthAgent{namedStubModelModeAgent: namedStubModelModeAgent{name: agentName}, allowed: []string{"Read"}}
	e, statePath, wsDir, sessionKey := newWorkspaceSettingEngine(t, global)

	e.cmdAllow(e.platforms[0], &Message{SessionKey: sessionKey, ReplyCtx: "ctx"}, []string{"Bash"})

	if !slices.Equal(global.allowed, []string{"Read"}) {
		t.Fatalf("project agent allowed tools = %v, want untouched [Read]", global.allowed)
	}
	if got := NewProjectStateStore(statePath).WorkspaceAllowedTools(wsDir); !slices.Equal(got, []string{"Bash"}) {
		t.Fatalf("saved WorkspaceAllowedTools(%q) = %v, want [Bash]", wsDir, got)
	}
	recreated := reapWorkspaceAgent(t, e, wsDir)
	if got := recreated.(*stubToolAuthAgent).allowed; !slices.Equal(got, []string{"Read", "Bash"}) {
		t.Fatalf("recreated workspace agent allowed tools = %v, want the project's plus the persisted [Read Bash]", got)
	}
}

func TestMergeAllowedToolsOption(t *testing.T) {
	for _, tc := range []struct {
		name  string
		base  any
		extra []string
		want  []any
	}{
		{"nil base", nil, []string{"Bash"}, []any{"Bash"}},
		{"any base", []any{"Read", "Bash"}, []string{"Bash", "Grep"}, []any{"Read", "Bash", "Grep"}},
		{"string base", []string{"Read"}, []string{"Grep", "Grep"}, []any{"Read", "Grep"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mergeAllowedToolsOption(tc.base, tc.extra); !slices.Equal(got, tc.want) {
				t.Fatalf("mergeAllowedToolsOption(%v, %v) = %v, want %v", tc.base, tc.extra, got, tc.want)
			}
		})
	}
}

func TestWorkspaceModeOverrideAndAllowedTools(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "projects", "demo.state.json")
	store := NewProjectStateStore(statePath)
	store.SetWorkspaceModeOverride("/tmp/a", "yolo")
	store.SetWorkspaceModeOverride("/tmp/b", "plan")
	store.AddWorkspaceAllowedTool("/tmp/a", "Bash")
	store.AddWorkspaceAllowedTool("/tmp/a", "Bash")
	store.AddWorkspaceAllowedTool("/tmp/a", "Grep")
	store.Save()

	reloaded := NewProjectStateStore(statePath)
	if got := reloaded.WorkspaceModeOverride("/tmp/a"); got != "yolo" {
		t.Fatalf("WorkspaceModeOverride(a) = %q, want yolo", got)
	}
	if got := reloaded.WorkspaceAllowedTools("/tmp/a"); !slices.Equal(got, []string{"Bash", "Grep"}) {
		t.Fatalf("WorkspaceAllowedTools(a) = %v, want [Bash Grep]", got)
	}
	if got := reloaded.WorkspaceAllowedTools("/tmp/b"); len(got) != 0 {
		t.Fatalf("WorkspaceAllowedTools(b) = %v, want none", got)
	}

	reloaded.SetWorkspaceModeOverride("/tmp/a", "")
	reloaded.Save()
	cleared := NewProjectStateStore(statePath)
	if got := cleared.WorkspaceModeOverride("/tmp/a"); got != "" {
		t.Fatalf("WorkspaceModeOverride(a) after clear = %q, want empty", got)
	}
	if got := cleared.WorkspaceModeOverride("/tmp/b"); got != "plan" {
		t.Fatalf("WorkspaceModeOverride(b) = %q, want plan", got)
	}
}
