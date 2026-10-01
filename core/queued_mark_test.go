package core

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// markPlatform marks queued messages and logs every reaction event in order.
type markPlatform struct {
	typingPlatform
	markOK bool
	lmu    sync.Mutex
	log    []string
}

func newMarkPlatform(markOK bool) *markPlatform {
	return &markPlatform{typingPlatform: typingPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}, markOK: markOK}
}

func (p *markPlatform) record(event string, replyCtx any) {
	p.lmu.Lock()
	p.log = append(p.log, fmt.Sprintf("%s:%v", event, replyCtx))
	p.lmu.Unlock()
}

func (p *markPlatform) events() []string {
	p.lmu.Lock()
	defer p.lmu.Unlock()
	return slices.Clone(p.log)
}

func (p *markPlatform) MarkQueued(_ context.Context, replyCtx any) (func(), bool) {
	if !p.markOK {
		return nil, false
	}
	p.record("mark", replyCtx)
	return func() { p.record("clear", replyCtx) }, true
}

func (p *markPlatform) StartTyping(ctx context.Context, replyCtx any) func() {
	p.record("start", replyCtx)
	return p.typingPlatform.StartTyping(ctx, replyCtx)
}

func (p *markPlatform) AddDoneReaction(replyCtx any) {
	p.record("done", replyCtx)
	p.typingPlatform.AddDoneReaction(replyCtx)
}

// queueTestState registers a busy session state that accepts queued messages.
func queueTestState(e *Engine, p Platform, key string) *interactiveState {
	state := &interactiveState{platform: p, replyCtx: "ctx"}
	e.interactiveMu.Lock()
	e.interactiveStates[key] = state
	e.interactiveMu.Unlock()
	return state
}

func countSent(p *stubPlatformEngine, text string) int {
	n := 0
	for _, s := range p.getSent() {
		if strings.Contains(s, text) {
			n++
		}
	}
	return n
}

func TestQueueMessage_MarkReplacesTextNotice(t *testing.T) {
	p := newMarkPlatform(true)
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	key := "test:mark"
	queueTestState(e, p, key)

	if !e.queueMessageForBusySession(p, &Message{SessionKey: key, Content: "next", ReplyCtx: "ctx-2"}, key) {
		t.Fatal("expected the message to be queued")
	}

	if got := p.events(); !slices.Equal(got, []string{"mark:ctx-2"}) {
		t.Fatalf("events = %v, want the queued message marked", got)
	}
	if n := countSent(&p.stubPlatformEngine, e.i18n.T(MsgMessageQueued)); n != 0 {
		t.Fatalf("queued text notices = %d, want none once the message is marked", n)
	}
}

func TestQueueMessage_UnmarkedFallsBackToTextNotice(t *testing.T) {
	p := newMarkPlatform(false)
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	key := "test:no-mark"
	queueTestState(e, p, key)

	if !e.queueMessageForBusySession(p, &Message{SessionKey: key, Content: "next", ReplyCtx: "ctx-2"}, key) {
		t.Fatal("expected the message to be queued")
	}

	if n := countSent(&p.stubPlatformEngine, e.i18n.T(MsgMessageQueued)); n != 1 {
		t.Fatalf("queued text notices = %d, want 1 when no mark was shown", n)
	}
}

func TestQueueMessage_QueueFullKeepsTextNotice(t *testing.T) {
	p := newMarkPlatform(true)
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	key := "test:full"
	queueTestState(e, p, key)

	for i := 0; i <= defaultMaxQueuedMessages; i++ {
		e.queueMessageForBusySession(p, &Message{SessionKey: key, Content: "m", ReplyCtx: fmt.Sprintf("ctx-%d", i)}, key)
	}

	if got := len(p.events()); got != defaultMaxQueuedMessages {
		t.Fatalf("marks = %d, want %d (the rejected message is not marked)", got, defaultMaxQueuedMessages)
	}
	if n := len(p.getSent()); n != 1 {
		t.Fatalf("text replies = %d, want only the queue-full notice", n)
	}
}

func TestQueueMessage_DroppedMessagesAreUnmarked(t *testing.T) {
	p := newMarkPlatform(true)
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	key := "test:drop"
	state := queueTestState(e, p, key)
	for _, rc := range []string{"ctx-2", "ctx-3"} {
		e.queueMessageForBusySession(p, &Message{SessionKey: key, Content: "m", ReplyCtx: rc}, key)
	}

	e.notifyDroppedQueuedMessages(state, fmt.Errorf("session reset"))

	want := []string{"mark:ctx-2", "mark:ctx-3", "clear:ctx-2", "clear:ctx-3"}
	if got := p.events(); !slices.Equal(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}

	e.queueMessageForBusySession(p, &Message{SessionKey: key, Content: "m", ReplyCtx: "ctx-4"}, key)
	if dropped := takePendingMessages(state); len(dropped) != 1 {
		t.Fatalf("taken = %d, want 1", len(dropped))
	}
	if got := p.events(); got[len(got)-1] != "clear:ctx-4" {
		t.Fatalf("events = %v, want the taken message unmarked", got)
	}
}

func TestQueueMessage_RecalledQueuedMessageIsUnmarked(t *testing.T) {
	p := newMarkPlatform(true)
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	key := "test:recall"
	queueTestState(e, p, key)
	e.queueMessageForBusySession(p, &Message{SessionKey: key, MessageID: "m2", Content: "m", ReplyCtx: "ctx-2"}, key)

	if _, _, ok := e.removeQueuedMessageByID("m2"); !ok {
		t.Fatal("expected the queued message to be removed")
	}

	if got := p.events(); !slices.Equal(got, []string{"mark:ctx-2", "clear:ctx-2"}) {
		t.Fatalf("events = %v, want the recalled message unmarked", got)
	}
}

// A message queued behind a running turn is unmarked as it starts, and the
// message whose turn ended before it gets its done reaction then, not never.
func TestQueuedMessage_UnmarkedWhenItStartsAndPreviousMarkedDone(t *testing.T) {
	p := newMarkPlatform(true)
	sess := newBgTaskSession()
	agent := &controllableAgent{nextSession: sess}
	e := NewEngine("test", agent, []Platform{p}, filepath.Join(t.TempDir(), "sessions.json"), LangEnglish)
	t.Cleanup(func() { _ = e.Stop() })
	key := "test:chat:user1"
	sess.onSend = func(n int) {
		switch n {
		case 1:
			// The second message arrives while the first turn runs.
			if !e.queueMessageForBusySession(p, &Message{
				Platform: "test", SessionKey: key, UserID: "user1", UserName: "User One",
				Content: "and the other thing?", MessageID: "m2", ReplyCtx: "ctx-2",
			}, key) {
				t.Error("expected the second message to be queued")
			}
			sess.reply("First answer.")
		case 2:
			sess.reply("The other thing is fine.")
		}
	}

	e.processInteractiveMessageWith(p, &Message{
		Platform: "test", SessionKey: key, UserID: "user1", UserName: "User One",
		Content: "first", MessageID: "m1", ReplyCtx: "ctx-1",
	}, e.sessions.GetOrCreateActive(key), agent, e.sessions, key, "", "", 0)

	want := []string{"start:ctx-1", "mark:ctx-2", "done:ctx-1", "clear:ctx-2", "start:ctx-2", "done:ctx-2"}
	if got := p.events(); !slices.Equal(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if n := countSent(&p.stubPlatformEngine, e.i18n.T(MsgMessageQueued)); n != 0 {
		t.Fatalf("queued text notices = %d, want none", n)
	}
}
