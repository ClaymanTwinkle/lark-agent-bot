package core

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newGroupWorkspaceEngine is a multi-workspace engine whose base_dir holds
// "project A" and "project B".
func newGroupWorkspaceEngine(t *testing.T, platforms ...Platform) (e *Engine, root string) {
	t.Helper()
	e = NewEngine("test", &stubAgent{}, platforms, "", LangEnglish)
	root = t.TempDir()
	for _, name := range []string{"project A", "project B"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.SetMultiWorkspace(root, filepath.Join(t.TempDir(), "bindings.json"))
	t.Cleanup(func() { _ = e.Stop() })
	return e, root
}

// topicMessage is a message in topic thread of chat "group", as a platform
// with topic sessions sends it.
func topicMessage(thread string) *Message {
	return &Message{
		SessionKey:       "test:group:root:" + thread,
		Platform:         "test",
		ChannelKey:       "group:topic:" + thread,
		LegacyChannelKey: "group",
		UserID:           "user",
		ReplyCtx:         "ctx",
	}
}

func selectProjectAction(name string) string {
	return "act:/workspace select " + base64.RawURLEncoding.EncodeToString([]byte(name))
}

func boundWorkspace(e *Engine, channelKey string) string {
	if b := e.workspaceBindings.Lookup("project:test", channelKey); b != nil {
		return b.Workspace
	}
	return ""
}

func TestWorkspaceGroup_TopicPickOffersTheGroup(t *testing.T) {
	e, root := newGroupWorkspaceEngine(t)
	projectA := normalizeWorkspacePath(filepath.Join(root, "project A"))
	projectB := normalizeWorkspacePath(filepath.Join(root, "project B"))
	topic := topicMessage("thread-a")
	e.workspaceBindings.Bind("project:test", "test:group:topic:thread-c", "", projectB)

	card := e.handleCardNavWithContext(selectProjectAction("project A"), topic)
	if got := boundWorkspace(e, "test:group:topic:thread-a"); got != projectA {
		t.Fatalf("topic binding = %q, want %q", got, projectA)
	}
	if got := boundWorkspace(e, "test:group"); got != "" {
		t.Fatalf("picking in a topic must not bind the group yet: %q", got)
	}
	if !strings.Contains(card.RenderText(), e.i18n.Tf(MsgWsGroupOffer, projectA)) ||
		!cardHasAction(card, "act:/workspace group") || !cardHasAction(card, "nav:/workspace bind") {
		t.Fatalf("the card must ask whether to apply the project to the group: %q", card.RenderText())
	}

	applied := e.handleCardNavWithContext("act:/workspace group", topic)
	if !strings.Contains(applied.RenderText(), e.i18n.Tf(MsgWsGroupApplied, projectA)) {
		t.Fatalf("applying must say so: %q", applied.RenderText())
	}
	if got := boundWorkspace(e, "test:group"); got != projectA {
		t.Fatalf("group binding = %q, want %q", got, projectA)
	}
	newTopic := topicMessage("thread-b")
	e.migrateLegacyWorkspaceBindings(newTopic)
	if got := boundWorkspace(e, effectiveWorkspaceChannelKey(newTopic)); got != projectA {
		t.Fatalf("a new topic must start with the group's project, got %q", got)
	}
	if got := boundWorkspace(e, "test:group:topic:thread-c"); got != projectB {
		t.Fatalf("a topic with its own project must keep it, got %q", got)
	}

	if card := e.handleCardNavWithContext(selectProjectAction("project A"), topic); cardHasAction(card, "act:/workspace group") {
		t.Fatalf("no offer when the group already has the project: %q", card.RenderText())
	}

	e.SetDisabledCommands([]string{"workspace"})
	e.handleCardNavWithContext(selectProjectAction("project B"), topicMessage("thread-d"))
	if card := e.handleCardNavWithContext("act:/workspace group", topicMessage("thread-d")); !strings.Contains(card.RenderText(), e.i18n.Tf(MsgCommandDisabled, "/workspace")) {
		t.Fatalf("a disabled /workspace must block applying: %q", card.RenderText())
	}
	if got := boundWorkspace(e, "test:group"); got != projectA {
		t.Fatalf("a blocked apply changed the group binding to %q", got)
	}
}

func TestWorkspaceGroup_OutsideTopicsAndUnboundTopics(t *testing.T) {
	e, root := newGroupWorkspaceEngine(t)
	projectA := normalizeWorkspacePath(filepath.Join(root, "project A"))
	chat := &Message{SessionKey: "test:group:user", Platform: "test", UserID: "user", ReplyCtx: "ctx"}

	if card := e.handleCardNavWithContext(selectProjectAction("project A"), chat); cardHasAction(card, "act:/workspace group") {
		t.Fatalf("a chat outside a topic is the group itself; nothing to offer: %q", card.RenderText())
	}
	if card := e.handleCardNavWithContext("act:/workspace group", chat); !strings.Contains(card.RenderText(), e.i18n.T(MsgWsGroupNotTopic)) {
		t.Fatalf("applying outside a topic must explain: %q", card.RenderText())
	}
	if card := e.handleCardNavWithContext("act:/workspace group", topicMessage("unbound")); !strings.Contains(card.RenderText(), e.i18n.T(MsgWsNoBinding)) {
		t.Fatalf("an unbound topic has nothing to apply: %q", card.RenderText())
	}
	if got := boundWorkspace(e, "test:group"); got != projectA {
		t.Fatalf("group binding = %q, want it unchanged at %q", got, projectA)
	}
}

func TestWorkspaceGroup_TextCommands(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e, root := newGroupWorkspaceEngine(t, p)
	projectA := normalizeWorkspacePath(filepath.Join(root, "project A"))
	topic := topicMessage("thread-a")

	e.handleCommand(p, topic, "/workspace bind project A")
	got := strings.Join(p.getSent(), "\n")
	if !strings.Contains(got, e.i18n.Tf(MsgWsBindSuccess, "project A")) ||
		!strings.Contains(got, e.i18n.Tf(MsgWsGroupOffer, projectA)) || !strings.Contains(got, e.i18n.T(MsgWsGroupTextHint)) {
		t.Fatalf("a text bind in a topic must offer /workspace group: %q", got)
	}
	if boundWorkspace(e, "test:group") != "" {
		t.Fatal("binding in a topic must not bind the group yet")
	}

	p.clearSent()
	e.handleCommand(p, topic, "/workspace group")
	if got := strings.Join(p.getSent(), "\n"); got != e.i18n.Tf(MsgWsGroupApplied, projectA) {
		t.Fatalf("/workspace group: %q", got)
	}
	if got := boundWorkspace(e, "test:group"); got != projectA {
		t.Fatalf("group binding = %q, want %q", got, projectA)
	}
}

func TestWorkspaceGroup_TypedNameInTopicOffersCard(t *testing.T) {
	p := &workspacePickerPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}
	e, _ := newGroupWorkspaceEngine(t, p)
	topic := topicMessage("thread-a")
	topic.Content = "project A"

	e.ReceiveMessage(p, topic)
	if card := p.lastCard(t); !cardHasAction(card, "act:/workspace group") {
		t.Fatalf("a typed project name in a topic must offer the group: %q", card.RenderText())
	}
}
