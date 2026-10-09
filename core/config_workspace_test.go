package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type workspaceModeSwitch struct {
	multi bool
	dir   string
}

// newWorkspaceModeEngine is a single-workspace engine working in root/app,
// with admin "boss" and a recording workspace mode saver.
func newWorkspaceModeEngine(t *testing.T) (e *Engine, root string, saved *[]workspaceModeSwitch) {
	t.Helper()
	root = t.TempDir()
	workDir := filepath.Join(root, "app")
	if err := os.Mkdir(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	e = NewEngine("test", &stubWorkDirAgent{workDir: workDir}, nil, "", LangEnglish)
	e.SetAdminFrom("boss")
	var switches []workspaceModeSwitch
	e.SetWorkspaceModeSaver(func(multi bool, dir string) error {
		switches = append(switches, workspaceModeSwitch{multi, dir})
		return nil
	})
	drainRestartCh(t)
	return e, root, &switches
}

func drainRestartCh(t *testing.T) {
	t.Helper()
	drain := func() {
		select {
		case <-RestartCh:
		default:
		}
	}
	drain()
	t.Cleanup(drain)
}

func takeRestart() (RestartRequest, bool) {
	select {
	case req := <-RestartCh:
		return req, true
	default:
		return RestartRequest{}, false
	}
}

func TestWorkspaceMode_SingleToMultiFromCard(t *testing.T) {
	e, root, saved := newWorkspaceModeEngine(t)
	store := NewProjectStateStore(filepath.Join(t.TempDir(), "state.json"))
	store.SetWorkDirOverride(filepath.Join(root, "old"))
	e.SetProjectStateStore(store)
	boss := configClicker("boss")

	project := e.handleCardNavWithContext("nav:/config project", boss)
	if !cardHasAction(project, "nav:/config workspace multi") {
		t.Fatalf("project page lacks the switch button: %q", project.RenderText())
	}

	confirm := e.handleCardNavWithContext("nav:/config workspace multi", boss)
	if !strings.Contains(confirm.RenderText(), e.i18n.Tf(MsgConfigWorkspaceConfirmMulti, root)) {
		t.Fatalf("the confirmation must explain the switch with the parent directory: %q", confirm.RenderText())
	}
	action := "act:/config workspace confirm multi " + encodeConfigDir(root)
	if !cardHasAction(confirm, action) || !cardHasAction(confirm, "nav:/config project") {
		t.Fatalf("confirmation lacks confirm/cancel buttons: %q", confirm.RenderText())
	}
	if len(*saved) != 0 {
		t.Fatal("opening the confirmation must not switch")
	}

	done := e.handleCardNavWithContext(action, boss)
	if len(*saved) != 1 || (*saved)[0] != (workspaceModeSwitch{true, root}) {
		t.Fatalf("saved = %+v, want multi-workspace with base_dir %q", *saved, root)
	}
	if req, ok := takeRestart(); !ok || req.SessionKey != boss.SessionKey {
		t.Fatalf("restart = %+v, %t; want one for the admin's chat", req, ok)
	}
	if !strings.Contains(done.RenderText(), e.i18n.T(MsgConfigWorkspaceSaved)) {
		t.Fatalf("the card must confirm the saved switch: %q", done.RenderText())
	}
	if store.WorkDirOverride() != "" {
		t.Fatal("a /dir working directory must be cleared, or it overrides the new mode")
	}
}

func TestWorkspaceMode_MultiToSingleOffersProjects(t *testing.T) {
	e, _, saved := newWorkspaceModeEngine(t)
	base := t.TempDir()
	project := filepath.Join(base, "project A")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	e.SetMultiWorkspace(base, filepath.Join(t.TempDir(), "bindings.json"))
	boss := configClicker("boss")

	pick := e.handleCardNavWithContext("nav:/config workspace single", boss)
	choose := "nav:/config workspace single " + encodeConfigDir(project)
	var found bool
	for _, el := range pick.Elements {
		if sel, ok := el.(CardSelect); ok {
			for _, opt := range sel.Options {
				found = found || opt.Value == choose
			}
		}
	}
	if !found {
		t.Fatalf("an unbound chat must be offered the projects under base_dir: %q", pick.RenderText())
	}

	// A bound chat defaults to its project.
	e.workspaceBindings.Bind("project:test", effectiveWorkspaceChannelKey(boss), "", normalizeWorkspacePath(project))
	confirm := e.handleCardNavWithContext("nav:/config workspace single", boss)
	action := "act:/config workspace confirm single " + encodeConfigDir(normalizeWorkspacePath(project))
	if !cardHasAction(confirm, action) {
		t.Fatalf("a bound chat must default to its project: %q", confirm.RenderText())
	}
	e.handleCardNavWithContext(action, boss)
	if len(*saved) != 1 || (*saved)[0].multi || (*saved)[0].dir != filepath.Clean(normalizeWorkspacePath(project)) {
		t.Fatalf("saved = %+v, want single workspace in the bound project", *saved)
	}
}

func TestWorkspaceMode_RefusedForNonAdmins(t *testing.T) {
	e, root, saved := newWorkspaceModeEngine(t)
	action := "act:/config workspace confirm multi " + encodeConfigDir(root)
	refused := e.i18n.Tf(MsgAdminRequired, "/config")

	for name, card := range map[string]*Card{
		"non-admin confirm":     e.handleCardNavWithContext(action, configClicker("member")),
		"non-admin card":        e.handleCardNavWithContext("nav:/config workspace multi", configClicker("member")),
		"unknown clicker":       e.handleCardNav(action, "test:chat1:member"),
		"unknown clicker (nav)": e.handleCardNav("nav:/config workspace multi", "test:chat1:member"),
	} {
		if !strings.Contains(card.RenderText(), refused) {
			t.Fatalf("%s was not refused: %q", name, card.RenderText())
		}
	}
	if len(*saved) != 0 {
		t.Fatal("a refused switch was saved")
	}
	if _, ok := takeRestart(); ok {
		t.Fatal("a refused switch restarted the bot")
	}
}

func TestWorkspaceMode_FailuresDoNotRestart(t *testing.T) {
	e, root, saved := newWorkspaceModeEngine(t)
	boss := configClicker("boss")

	missing := filepath.Join(root, "missing")
	card := e.handleCardNavWithContext("act:/config workspace confirm multi "+encodeConfigDir(missing), boss)
	if len(*saved) != 0 || !strings.Contains(card.RenderText(), "is not a directory") {
		t.Fatalf("a missing directory must be refused before saving: %q", card.RenderText())
	}

	e.SetWorkspaceModeSaver(func(bool, string) error { return errors.New("config has unknown keys") })
	card = e.handleCardNavWithContext("act:/config workspace confirm multi "+encodeConfigDir(root), boss)
	if !strings.Contains(card.RenderText(), "config has unknown keys") {
		t.Fatalf("a save error must be shown: %q", card.RenderText())
	}
	if _, ok := takeRestart(); ok {
		t.Fatal("a failed switch restarted the bot")
	}

	// A restart already queued is not a failure: the switch rides along.
	e.SetWorkspaceModeSaver(func(bool, string) error { return nil })
	RestartCh <- RestartRequest{}
	card = e.handleCardNavWithContext("act:/config workspace confirm multi "+encodeConfigDir(root), boss)
	if !strings.Contains(card.RenderText(), e.i18n.T(MsgConfigRestartPending)) {
		t.Fatalf("a queued restart must be reported as such: %q", card.RenderText())
	}
}

func TestWorkspaceMode_TextCommand(t *testing.T) {
	e, root, saved := newWorkspaceModeEngine(t)
	p := &stubPlatformEngine{n: "plain"}
	send := func(userID, raw string) string {
		p.clearSent()
		e.handleCommand(p, &Message{SessionKey: "plain:chat1:" + userID, Platform: "plain", UserID: userID, ReplyCtx: "ctx"}, raw)
		return strings.Join(p.getSent(), "\n")
	}
	target := filepath.Join(root, "with space")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := send("member", "/config workspace multi "+target+" confirm"); got != e.i18n.Tf(MsgAdminRequired, "/config") {
		t.Fatalf("non-admin: %q", got)
	}
	got := send("boss", "/config workspace multi "+target)
	if !strings.Contains(got, e.i18n.Tf(MsgConfigWorkspaceConfirmMulti, target)) || len(*saved) != 0 {
		t.Fatalf("without confirm the command must only explain: %q", got)
	}
	send("boss", "/config workspace multi "+target+" confirm")
	if len(*saved) != 1 || (*saved)[0] != (workspaceModeSwitch{true, target}) {
		t.Fatalf("saved = %+v, want the path with its space", *saved)
	}
	if _, ok := takeRestart(); !ok {
		t.Fatal("confirming must request a restart")
	}
}

func TestWorkspaceCommand_SingleModeExplainsSwitch(t *testing.T) {
	e, _, _ := newWorkspaceModeEngine(t)
	plain := &stubPlatformEngine{n: "plain"}
	e.handleCommand(plain, &Message{SessionKey: "plain:chat1:member", Platform: "plain", UserID: "member", ReplyCtx: "ctx"}, "/workspace")
	if got := strings.Join(plain.getSent(), "\n"); got != e.i18n.T(MsgWsSingleModeHint) {
		t.Fatalf("/workspace in single-workspace mode: %q", got)
	}

	cards := &workspacePickerPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}
	e.handleCommand(cards, &Message{SessionKey: "test:chat1:boss", Platform: "test", UserID: "boss", ReplyCtx: "ctx"}, "/workspace")
	if card := cards.lastCard(t); !cardHasAction(card, "nav:/config project") {
		t.Fatalf("admins must get a button to the project page: %q", card.RenderText())
	}
}
