package core

import (
	"context"
	"slices"
	"testing"
)

type recallReceiptPlatform struct {
	stubPlatformEngine
	contexts []any
}

func (p *recallReceiptPlatform) NotifyMessageRecall(ctx context.Context, replyCtx any, content string) error {
	p.contexts = append(p.contexts, replyCtx)
	return p.Send(ctx, replyCtx, content)
}

// recallReplyPlatform records which reply context each quoted reply targets.
type recallReplyPlatform struct {
	stubPlatformEngine
	replyCtxs []any
}

func (p *recallReplyPlatform) Reply(ctx context.Context, replyCtx any, content string) error {
	p.mu.Lock()
	p.replyCtxs = append(p.replyCtxs, replyCtx)
	p.mu.Unlock()
	return p.stubPlatformEngine.Reply(ctx, replyCtx, content)
}

type recallCancellableSession struct {
	AgentSession
	cancellations int
}

func (s *recallCancellableSession) CancelTurn() error {
	s.cancellations++
	return nil
}

func TestRecallNotification_UsesOriginalQueuedContext(t *testing.T) {
	original := &recallReceiptPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}
	incoming := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{original}, "", LangChinese)
	e.interactiveStates["test:user"] = &interactiveState{
		pendingMessages: []queuedMessage{{messageID: "queued", platform: original, replyCtx: "original-chat"}},
	}
	e.ReceiveMessage(incoming, &Message{MessageID: "queued", Recalled: true, ReplyCtx: "incomplete-event-context"})
	if len(original.contexts) != 1 || original.contexts[0] != "original-chat" {
		t.Fatalf("notification contexts = %v, want original queued context", original.contexts)
	}
	if len(incoming.getSent()) != 0 {
		t.Fatal("notification was sent using the recall event's platform")
	}
}

func TestRecallNotification_GracefulCancelNotifiesOnceAndRejectsStaleTurn(t *testing.T) {
	p := &recallReceiptPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}
	session := &recallCancellableSession{AgentSession: newControllableSession("recall")}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	state := &interactiveState{agentSession: session, platform: p, replyCtx: "original-chat", currentMessageID: "old"}
	e.interactiveStates["test:user"] = state
	for range 2 {
		e.ReceiveMessage(p, &Message{MessageID: "old", Recalled: true})
	}
	// A delayed fallback probe for the old message must not stop a new turn.
	state.currentMessageID = "new"
	if e.stopRecalledMessage("test:user", "old") {
		t.Fatal("stale recall stopped a newer turn")
	}
	if session.cancellations != 1 || len(p.getSent()) != 1 || len(p.contexts) != 1 || p.contexts[0] != "original-chat" {
		t.Fatalf("cancellations=%d, messages=%v, contexts=%v", session.cancellations, p.getSent(), p.contexts)
	}
}

func TestRecallNotification_ActiveRecallTellsEachQueuedMessageToResend(t *testing.T) {
	for _, tc := range []struct {
		name    string
		session func() AgentSession
	}{
		{"close", func() AgentSession { return newControllableSession("recall-close") }},
		{"graceful cancel", func() AgentSession {
			return &recallCancellableSession{AgentSession: newControllableSession("recall-cancel")}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &recallReplyPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}
			e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
			state := &interactiveState{
				agentSession: tc.session(), platform: p, replyCtx: "ctx-active", currentMessageID: "active",
				pendingMessages: []queuedMessage{
					{messageID: "q1", platform: p, replyCtx: "ctx-q1"},
					{messageID: "q2", platform: p, replyCtx: "ctx-q2"},
				},
			}
			e.interactiveStates["test:user"] = state

			e.ReceiveMessage(p, &Message{MessageID: "active", Recalled: true})

			want := []string{e.i18n.T(MsgRecallActiveStopping), e.i18n.T(MsgRecallQueuedDropped), e.i18n.T(MsgRecallQueuedDropped)}
			if sent := p.getSent(); !slices.Equal(sent, want) {
				t.Fatalf("sent = %v, want %v", sent, want)
			}
			// Each resend notice quotes the queued message that will not run.
			p.mu.Lock()
			replyCtxs := slices.Clone(p.replyCtxs)
			p.mu.Unlock()
			if !slices.Equal(replyCtxs, []any{"ctx-q1", "ctx-q2"}) {
				t.Fatalf("resend notices replied to %v, want ctx-q1 then ctx-q2", replyCtxs)
			}
			state.mu.Lock()
			remaining := len(state.pendingMessages)
			state.mu.Unlock()
			if remaining != 0 {
				t.Fatalf("pending messages = %d, want the queue cleared", remaining)
			}
		})
	}
}
