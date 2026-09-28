package core

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type workspacePickerPlatform struct {
	stubPlatformEngine
	cards []*Card
}

func (p *workspacePickerPlatform) ReplyCard(_ context.Context, _ any, card *Card) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cards = append(p.cards, card)
	p.sent = append(p.sent, card.RenderText())
	return nil
}

func (p *workspacePickerPlatform) SendCard(ctx context.Context, replyCtx any, card *Card) error {
	return p.ReplyCard(ctx, replyCtx, card)
}

func (p *workspacePickerPlatform) lastCard(t *testing.T) *Card {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.cards) == 0 {
		t.Fatal("expected a project picker card")
	}
	return p.cards[len(p.cards)-1]
}

func pickerItems(card *Card) []CardListItem {
	var items []CardListItem
	for _, element := range card.Elements {
		if item, ok := element.(CardListItem); ok {
			items = append(items, item)
		}
	}
	return items
}

func TestWorkspacePicker_HelpUsesInPlaceActions(t *testing.T) {
	e := NewEngine("test", &stubAgent{}, nil, "", LangEnglish)
	e.SetMultiWorkspace(t.TempDir(), filepath.Join(t.TempDir(), "bindings.json"))
	card := e.renderHelpGroupCard("system")
	for _, item := range pickerItems(card) {
		if strings.HasPrefix(item.Text, "**/bind**") || strings.HasPrefix(item.Text, "**/workspace**") {
			if item.BtnValue != "nav:/workspace bind" {
				t.Fatalf("project menu must update the same message, got %q", item.BtnValue)
			}
		}
	}
}

func TestWorkspacePicker_NavigationAndSelectionDoNotSendNewMessages(t *testing.T) {
	p := &workspacePickerPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	root := t.TempDir()
	e.SetMultiWorkspace(root, filepath.Join(t.TempDir(), "bindings.json"))
	for i := 0; i < 10; i++ {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("project-%02d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	msg := &Message{SessionKey: "test:group:root:thread-a", Platform: "test", ChannelKey: "group:topic:thread-a", UserID: "clicker"}
	first := e.handleCardNavWithContext("nav:/workspace bind", msg)
	if len(pickerItems(first)) != 8 {
		t.Fatal("expected the first page in the callback response")
	}
	next := e.handleCardNavWithContext("nav:/workspace available 2", msg)
	items := pickerItems(next)
	if len(items) != 2 {
		t.Fatal("expected the second page in the callback response")
	}
	selected := e.handleCardNavWithContext(items[1].BtnValue, msg)
	if !strings.Contains(selected.RenderText(), e.i18n.Tf(MsgWsBindSuccess, "project-09")) {
		t.Fatal("binding success must be displayed inside the updated card")
	}
	if got := pickerItems(selected); len(got) != 8 || got[0].Text != "project-09" || got[1].Text != "project-00" || got[0].BtnText != e.i18n.T(MsgWsPickerSelected) {
		t.Fatalf("selection must return to page one with current project first: %+v", got)
	}
	selected = e.handleCardNavWithContext(items[0].BtnValue, msg)
	if got := pickerItems(selected); got[0].Text != "project-08" || got[1].Text != "project-00" || got[0].BtnText != e.i18n.T(MsgWsPickerSelected) || got[1].BtnText != e.i18n.T(MsgWsPickerSelect) {
		t.Fatalf("current-project button must move to the newly bound project: %+v", got)
	}
	other := e.handleCardNavWithContext("nav:/workspace bind", &Message{SessionKey: "test:group:root:thread-b", Platform: "test", ChannelKey: "group:topic:thread-b", UserID: "clicker"})
	if !strings.Contains(other.RenderText(), e.i18n.T(MsgWsNoBinding)) {
		t.Fatal("selection leaked into another topic")
	}
	e.SetDisabledCommands([]string{"workspace"})
	blocked := e.handleCardNavWithContext(items[0].BtnValue, msg)
	if !strings.Contains(blocked.RenderText(), e.i18n.Tf(MsgCommandDisabled, "/workspace")) {
		t.Fatal("card callback bypassed disabled command")
	}
	if len(p.getSent()) != 0 || len(p.cards) != 0 {
		t.Fatal("card navigation must never send a new message")
	}
}

func TestWorkspacePicker_PaginationFiltersAndCurrentBinding(t *testing.T) {
	e := NewEngine("test", &stubAgent{}, nil, "", LangEnglish)
	root := t.TempDir()
	e.SetMultiWorkspace(root, filepath.Join(t.TempDir(), "bindings.json"))
	for i := 0; i < 10; i++ {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("project-%02d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	e.workspaceBindings.Bind("project:test", "test:group", "", normalizeWorkspacePath(filepath.Join(root, "project-09")))
	first, err := e.workspacePickerCard("test:group", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := pickerItems(first); len(got) != 8 || got[0].Text != "project-09" || got[0].BtnText != e.i18n.T(MsgWsPickerSelected) || got[1].Text != "project-00" {
		t.Fatalf("first page = %+v", got)
	}
	if _, ok := findCardAction(first, "nav:/workspace available 2"); !ok {
		t.Fatal("missing next-page action")
	}
	last, err := e.workspacePickerCard("test:group", 999)
	if err != nil {
		t.Fatal(err)
	}
	items := pickerItems(last)
	if len(items) != 2 || items[0].Text != "project-07" || items[1].Text != "project-08" {
		t.Fatalf("last page = %+v", items)
	}
	seen := make(map[string]bool)
	for _, item := range append(pickerItems(first), items...) {
		if seen[item.Text] {
			t.Fatalf("duplicate project across pages: %q", item.Text)
		}
		seen[item.Text] = true
	}
	if len(seen) != 10 {
		t.Fatalf("projects lost after pinning current: %v", seen)
	}
	if _, ok := findCardAction(last, "nav:/workspace available 1"); !ok {
		t.Fatal("missing previous-page action")
	}
}

func TestWorkspacePicker_RejectsStaleAndForgedSelections(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	root := t.TempDir()
	e.SetMultiWorkspace(root, filepath.Join(t.TempDir(), "bindings.json"))
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"removed", "../outside", "file"} {
		p.clearSent()
		msg := &Message{SessionKey: "test:group:user", Platform: "test", ReplyCtx: "ctx"}
		e.selectWorkspaceFromPicker(p, msg, []string{base64.RawURLEncoding.EncodeToString([]byte(name))})
		if got := strings.Join(p.getSent(), "\n"); !strings.Contains(got, e.i18n.T(MsgWsPickerStale)) {
			t.Fatalf("selection %q: %s", name, got)
		}
		if binding := e.workspaceBindings.Lookup("project:test", "test:group"); binding != nil {
			t.Fatalf("invalid selection bound workspace: %+v", binding)
		}
	}
}

func TestWorkspacePicker_EmptyAndUnreadableRoot(t *testing.T) {
	e := NewEngine("test", &stubAgent{}, nil, "", LangEnglish)
	e.SetMultiWorkspace(t.TempDir(), filepath.Join(t.TempDir(), "bindings.json"))
	card, err := e.workspacePickerCard("test:group", 1)
	if err != nil || !strings.Contains(card.RenderText(), e.i18n.T(MsgWsPickerEmpty)) {
		t.Fatalf("empty picker = %#v, %v", card, err)
	}
	e.baseDir = filepath.Join(e.baseDir, "missing")
	if _, err := e.workspacePickerCard("test:group", 1); err == nil {
		t.Fatal("missing root must report an error")
	}
}

func TestWorkspacePicker_HelpAndRelayCompatibility(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "test:group:user", ReplyCtx: "ctx"}
	e.cmdBind(p, msg, nil)
	if got := strings.Join(p.getSent(), "\n"); !strings.Contains(got, e.i18n.T(MsgRelayNotAvailable)) {
		t.Fatalf("single-workspace relay behavior changed: %q", got)
	}
	e.SetMultiWorkspace(t.TempDir(), filepath.Join(t.TempDir(), "bindings.json"))
	help := e.renderHelpGroupCard("system")
	if _, ok := findCardAction(help, "nav:/workspace bind"); !ok {
		t.Fatal("workspace help entry must open picker")
	}
	if !strings.Contains(help.RenderText(), e.i18n.T(MsgWsPickerDescription)) {
		t.Fatal("bind help must describe project selection")
	}
	p.clearSent()
	e.cmdBind(p, msg, []string{"other-bot"})
	if got := strings.Join(p.getSent(), "\n"); !strings.Contains(got, e.i18n.T(MsgRelayNotAvailable)) {
		t.Fatalf("explicit relay binding changed: %q", got)
	}
}
