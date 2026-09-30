package core

import (
	"context"
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
