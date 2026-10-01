package core

import (
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

// startedBuild runs a first user turn whose reply leaves the agent working
// in the background, so the reader runs between turns as after any reply.
func (h *bgTurnHarness) startedBuild(onLaterSend func(n int)) {
	h.t.Helper()
	h.sess.onSend = func(n int) {
		if n == 1 {
			h.sess.reply("The build runs in the background.")
			return
		}
		if onLaterSend != nil {
			onLaterSend(n)
		}
	}
	h.turn("build it", "ctx-1")
}

func (h *bgTurnHarness) sent(text string) bool {
	return sentContaining(&h.p.stubPlatformEngine, text)
}

// The bug this guards: a permission request in a turn the agent started on
// its own was denied at once, leaving the user no way to approve it.
func TestAgentTurn_AsksUserForPermission(t *testing.T) {
	h := newBgTurnHarness(t)
	h.startedBuild(nil)
	state := h.state()

	// The build finished; the agent's own turn wants to deploy it.
	h.sess.events <- Event{Type: EventPermissionRequest, RequestID: "req-1", ToolName: "Bash", ToolInput: "make deploy"}
	waitUntil(t, "the user is asked", func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.pending != nil
	})
	if got := h.sess.permissions(); len(got) != 0 {
		t.Fatalf("answered %v before the user did", got)
	}
	if state.work() == workIdle || h.e.WorkInProgress() == 0 {
		t.Fatal("the session looks idle while the agent waits for the user")
	}

	if !h.e.handlePendingPermission(h.p, &Message{SessionKey: h.key, UserID: "user1", Content: "allow", ReplyCtx: "ctx-1"}, "allow", h.key) {
		t.Fatal("the user's answer did not reach the request")
	}
	waitUntil(t, "the agent gets the answer", func() bool { return len(h.sess.permissions()) == 1 })
	if got := h.sess.permissions()[0].Behavior; got != "allow" {
		t.Fatalf("behavior = %q, want allow", got)
	}

	h.sess.reply("Deployed.")
	waitUntil(t, "the reply is delivered", func() bool { return h.sent("Deployed.") })
}

func TestAgentTurn_YoloApprovesPermission(t *testing.T) {
	h := newBgTurnHarness(t)
	h.startedBuild(nil)
	state := h.state()
	state.mu.Lock()
	state.approveAll = true
	state.mu.Unlock()

	h.sess.events <- Event{Type: EventPermissionRequest, RequestID: "req-1", ToolName: "Bash"}
	waitUntil(t, "the request is answered", func() bool { return len(h.sess.permissions()) == 1 })
	if got := h.sess.permissions()[0].Behavior; got != "allow" {
		t.Fatalf("behavior = %q, want allow", got)
	}
	h.sess.reply("Deployed.")
	waitUntil(t, "the reply is delivered", func() bool { return h.sent("Deployed.") })
}

// A message sent while the agent's own turn ran used to take the agent's
// events over: the turn's text was dropped and its result became the
// message's reply. The message now waits for the turn and gets its own.
func TestAgentTurn_UserMessageWaitsForIt(t *testing.T) {
	h := newBgTurnHarness(t)
	var agentRepliedFirst atomic.Bool
	h.startedBuild(func(int) {
		agentRepliedFirst.Store(h.sent("The build is green."))
		h.sess.reply("Lunch is at noon.")
	})
	state := h.state()

	h.sess.events <- Event{Type: EventText, Content: "The build is green."}
	waitUntil(t, "the agent's own turn holds the session", h.session.Busy)

	h.e.handleMessage(h.p, &Message{
		Platform: "test", SessionKey: h.key, UserID: "user1", UserName: "User One",
		Content: "when is lunch?", MessageID: "m2", ReplyCtx: "ctx-2",
	})
	waitUntil(t, "the message is queued", func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		return len(state.pendingMessages) == 1
	})
	if n := h.sess.sendCount(); n != 1 {
		t.Fatalf("sends = %d, want the message kept from the agent's running turn", n)
	}

	h.sess.events <- Event{Type: EventResult, Done: true}
	waitUntil(t, "the message is answered", func() bool { return h.sent("Lunch is at noon.") })
	if !agentRepliedFirst.Load() {
		t.Fatal("the agent's own turn was not answered before the message was sent")
	}
	waitUntil(t, "both messages are done", func() bool {
		_, _, done := h.p.reactions()
		return slices.Contains(done, any("ctx-1")) && slices.Contains(done, any("ctx-2"))
	})
}

func TestAgentTurn_KeepsTheTextThatStartedIt(t *testing.T) {
	h := newBgTurnHarness(t)
	h.startedBuild(nil)

	h.sess.events <- Event{Type: EventText, Content: "The build passed. "}
	h.sess.events <- Event{Type: EventText, Content: "All 312 tests are green."}
	h.sess.events <- Event{Type: EventResult, Done: true}
	waitUntil(t, "the reply is delivered", func() bool { return h.sent("The build passed. All 312 tests are green.") })
}

// The agent can start its own turn just as a user message takes the
// session. The turn runs first, so the message is not folded into it.
func TestAgentTurn_StartedAsUserMessageArrivesRunsFirst(t *testing.T) {
	h := newBgTurnHarness(t)
	var agentRepliedFirst atomic.Bool
	h.startedBuild(func(int) {
		agentRepliedFirst.Store(h.sent("The build is green."))
		h.sess.reply("Lunch is at noon.")
	})
	state := h.state()

	// The message takes the session...
	lockGen, ok := h.session.TryLock()
	if !ok {
		t.Fatal("session busy")
	}
	// ...as the agent starts its own turn.
	h.sess.events <- Event{Type: EventText, Content: "The build is green."}
	waitUntil(t, "the reader sees the agent's turn", func() bool { return state.work() == workAgentTurn })

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.e.processInteractiveMessageWith(h.p, &Message{
			Platform: "test", SessionKey: h.key, UserID: "user1", UserName: "User One",
			Content: "when is lunch?", MessageID: "m2", ReplyCtx: "ctx-2",
		}, h.session, h.agent, h.e.sessions, h.key, "", "", lockGen)
	}()
	waitUntil(t, "the message takes the events from the reader", func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.unsolicitedDone == nil || h.sess.sendCount() > 1
	})
	// The agent's turn ends only now, after the reader has given it up.
	h.sess.events <- Event{Type: EventResult, Done: true}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the message's turn did not finish")
	}

	if !h.sent("The build is green.") {
		t.Fatalf("sent %v, want the agent's own turn answered", h.p.getSent())
	}
	if !agentRepliedFirst.Load() {
		t.Fatal("the message was sent before the agent's own turn ended")
	}
	if !h.sent("Lunch is at noon.") {
		t.Fatalf("sent %v, want the message answered", h.p.getSent())
	}
	if h.session.Busy() {
		t.Fatal("session still locked after both turns")
	}
}

func journaledMessage(e *Engine, key string) string {
	e.turnJournal.mu.Lock()
	defer e.turnJournal.mu.Unlock()
	return e.turnJournal.turns[key].MessageID
}

// The agent's own turn must not end the journal entry of the message whose
// work it continues: a restart while that work runs still reports it.
func TestAgentTurn_KeepsHeldMessageJournaled(t *testing.T) {
	h := newBgTurnHarness(t)
	h.e.SetDataDir(t.TempDir())
	h.sess.onSend = func(int) {
		h.sess.setTasks("survey-1", "survey-2")
		h.sess.reply("Two surveys are running.")
	}
	h.turn("audit the issues", "ctx-1")
	if got := journaledMessage(h.e, h.key); got != "audit the issues" {
		t.Fatalf("journaled %q, want the held message", got)
	}

	h.sess.setTasks("survey-2")
	h.sess.reply("Survey 1 is in.")
	h.settle()
	if got := journaledMessage(h.e, h.key); got != "audit the issues" {
		t.Fatalf("journaled %q after the agent's own turn, want the held message", got)
	}

	h.sess.setTasks()
	h.sess.reply("Both surveys are in.")
	waitUntil(t, "the held message's entry ends", func() bool { return journaledMessage(h.e, h.key) == "" })
}

func TestAgentTurn_TellsUserWhenItGoesQuiet(t *testing.T) {
	h := newBgTurnHarness(t)
	h.e.SetStallNotice(100*time.Millisecond, time.Hour)
	h.startedBuild(nil)

	h.sess.events <- Event{Type: EventThinking, Content: "reading the build log"}
	waitUntil(t, "the user is told", func() bool { return h.sent(stallModelMarker) })
	h.sess.reply("Done.")
	waitUntil(t, "the reply is delivered", func() bool { return h.sent("Done.") })
}
