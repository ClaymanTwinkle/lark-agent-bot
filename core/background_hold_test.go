package core

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// bgTaskSession is an agent session that reports background tasks, like a
// Claude Code process whose backgrounded shells and subagents outlive the
// turn that started them.
type bgTaskSession struct {
	events    chan Event
	closed    chan struct{}
	alive     atomic.Bool
	closeOnce sync.Once

	mu     sync.Mutex
	tasks  []BackgroundTask
	sends  int
	onSend func(n int) // called with the 1-based Send count
}

func newBgTaskSession() *bgTaskSession {
	s := &bgTaskSession{events: make(chan Event, 16), closed: make(chan struct{})}
	s.alive.Store(true)
	return s
}

func (s *bgTaskSession) setTasks(ids ...string) {
	tasks := make([]BackgroundTask, 0, len(ids))
	for _, id := range ids {
		tasks = append(tasks, BackgroundTask{ID: id, Type: "local_agent"})
	}
	s.mu.Lock()
	s.tasks = tasks
	s.mu.Unlock()
}

func (s *bgTaskSession) BackgroundTasks() []BackgroundTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.tasks)
}

func (s *bgTaskSession) Send(string, string, []ImageAttachment, []FileAttachment) error {
	s.mu.Lock()
	s.sends++
	n, f := s.sends, s.onSend
	s.mu.Unlock()
	if f != nil {
		f(n)
	}
	return nil
}

func (s *bgTaskSession) RespondPermission(string, PermissionResult) error { return nil }
func (s *bgTaskSession) Events() <-chan Event                             { return s.events }
func (s *bgTaskSession) CurrentSessionID() string                         { return "bg-session" }
func (s *bgTaskSession) Alive() bool                                      { return s.alive.Load() }
func (s *bgTaskSession) Close() error {
	s.closeOnce.Do(func() {
		s.alive.Store(false)
		close(s.events)
		close(s.closed)
	})
	return nil
}

// reply emits a finished turn whose answer is text.
func (s *bgTaskSession) reply(text string) {
	s.events <- Event{Type: EventText, Content: text}
	s.events <- Event{Type: EventResult, Content: text, Done: true}
}

// typingPlatform records the processing indicator and done reactions.
type typingPlatform struct {
	stubPlatformEngine
	tmu     sync.Mutex
	started []any
	stopped []any
	done    []any
}

func (p *typingPlatform) StartTyping(_ context.Context, replyCtx any) func() {
	p.tmu.Lock()
	p.started = append(p.started, replyCtx)
	p.tmu.Unlock()
	return func() {
		p.tmu.Lock()
		p.stopped = append(p.stopped, replyCtx)
		p.tmu.Unlock()
	}
}

func (p *typingPlatform) AddDoneReaction(replyCtx any) {
	p.tmu.Lock()
	p.done = append(p.done, replyCtx)
	p.tmu.Unlock()
}

func (p *typingPlatform) reactions() (started, stopped, done []any) {
	p.tmu.Lock()
	defer p.tmu.Unlock()
	return slices.Clone(p.started), slices.Clone(p.stopped), slices.Clone(p.done)
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func sentContaining(p *stubPlatformEngine, text string) bool {
	for _, s := range p.getSent() {
		if strings.Contains(s, text) {
			return true
		}
	}
	return false
}

func heldMessages(state *interactiveState) int {
	state.mu.Lock()
	defer state.mu.Unlock()
	return len(state.backgroundHolds)
}

type bgTurnHarness struct {
	t       *testing.T
	e       *Engine
	p       *typingPlatform
	sess    *bgTaskSession
	agent   *controllableAgent
	session *Session
	key     string
}

func newBgTurnHarness(t *testing.T) *bgTurnHarness {
	t.Helper()
	p := &typingPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}
	sess := newBgTaskSession()
	agent := &controllableAgent{nextSession: sess}
	e := NewEngine("test", agent, []Platform{p}, filepath.Join(t.TempDir(), "sessions.json"), LangEnglish)
	t.Cleanup(func() { _ = e.Stop() })
	key := "test:chat:user1"
	return &bgTurnHarness{t: t, e: e, p: p, sess: sess, agent: agent, session: e.sessions.GetOrCreateActive(key), key: key}
}

// turn runs one user turn to completion.
func (h *bgTurnHarness) turn(content, replyCtx string) {
	h.e.processInteractiveMessageWith(h.p, &Message{
		Platform: "test", SessionKey: h.key, UserID: "user1", UserName: "User One",
		Content: content, MessageID: content, ReplyCtx: replyCtx,
	}, h.session, h.agent, h.e.sessions, h.key, "", "", 0)
}

// settle waits until the event reader has handled every event sent so far:
// it reads events in order, so once it has taken a trailing content-less
// event, everything before it is done.
func (h *bgTurnHarness) settle() {
	h.t.Helper()
	h.sess.events <- Event{Type: EventText}
	waitUntil(h.t, "the reader catches up", func() bool { return len(h.sess.events) == 0 })
}

func (h *bgTurnHarness) state() *interactiveState {
	h.e.interactiveMu.Lock()
	defer h.e.interactiveMu.Unlock()
	return h.e.interactiveStates[h.key]
}

// The bug this guards: the agent reports a result as soon as the model
// stops, while the subagents it launched still run. The message must stay in
// progress until they report back and the agent's follow-up turn ends.
func TestBackgroundHold_MessageStaysInProgressUntilBackgroundTasksFinish(t *testing.T) {
	h := newBgTurnHarness(t)
	h.sess.onSend = func(int) {
		h.sess.setTasks("survey-1", "survey-2")
		h.sess.reply("Two surveys are running; I will continue when they report back.")
	}

	h.turn("audit the issues", "ctx-1")

	started, stopped, done := h.p.reactions()
	if !slices.Equal(started, []any{"ctx-1"}) || len(stopped) != 0 || len(done) != 0 {
		t.Fatalf("after the turn: started=%v stopped=%v done=%v, want the indicator still on ctx-1 and no done reaction", started, stopped, done)
	}
	state := h.state()
	if state == nil || heldMessages(state) != 1 {
		t.Fatal("expected the message to be held for its background tasks")
	}

	// One survey reports back: the agent's follow-up turn ends while the
	// other one still runs.
	h.sess.setTasks("survey-2")
	h.sess.events <- Event{Type: EventText} // content-less wake-up
	h.sess.reply("Survey 1 is in.")
	h.settle()
	if !sentContaining(&h.p.stubPlatformEngine, "Survey 1 is in.") {
		t.Fatal("expected the first follow-up reply to be delivered")
	}
	if _, stopped, done := h.p.reactions(); len(stopped) != 0 || len(done) != 0 {
		t.Fatalf("with a survey still running: stopped=%v done=%v, want the message still in progress", stopped, done)
	}

	h.sess.setTasks()
	h.sess.reply("Both surveys are in; here is the plan.")
	waitUntil(t, "the message is marked done", func() bool {
		_, stopped, done := h.p.reactions()
		return len(stopped) == 1 && len(done) == 1
	})
	if _, stopped, done := h.p.reactions(); stopped[0] != "ctx-1" || done[0] != "ctx-1" {
		t.Fatalf("stopped=%v done=%v, want both on ctx-1", stopped, done)
	}
	if !sentContaining(&h.p.stubPlatformEngine, "here is the plan") {
		t.Fatal("expected the final follow-up reply to be delivered")
	}
	if heldMessages(state) != 0 {
		t.Fatal("expected no message held once the background work is done")
	}
}

// A message queued behind a turn that launched background work runs in the
// same event loop; the earlier message stays held while the queued one gets
// its own indicator and finishes normally.
func TestBackgroundHold_QueuedMessageRunsWhileEarlierOneIsHeld(t *testing.T) {
	h := newBgTurnHarness(t)
	h.sess.onSend = func(n int) {
		switch n {
		case 1:
			// The second message arrives while the first turn runs.
			state := h.state()
			state.mu.Lock()
			state.pendingMessages = append(state.pendingMessages, queuedMessage{
				messageID: "m2", platform: h.p, replyCtx: "ctx-2", content: "and the other thing?",
				userID: "user1", msgPlatform: "test", msgSessionKey: h.key,
			})
			state.mu.Unlock()
			h.sess.setTasks("survey")
			h.sess.reply("Survey launched.")
		case 2:
			h.sess.reply("The other thing is fine.")
		}
	}

	h.turn("start the survey", "ctx-1")

	started, stopped, done := h.p.reactions()
	if !slices.Equal(started, []any{"ctx-1", "ctx-2"}) || !slices.Equal(stopped, []any{"ctx-2"}) || !slices.Equal(done, []any{"ctx-2"}) {
		t.Fatalf("started=%v stopped=%v done=%v, want ctx-1 still in progress and ctx-2 finished", started, stopped, done)
	}
	if heldMessages(h.state()) != 1 {
		t.Fatal("expected the first message to be held for its survey")
	}

	h.sess.setTasks()
	h.sess.reply("The survey is in.")
	waitUntil(t, "the first message is marked done", func() bool {
		_, stopped, done := h.p.reactions()
		return slices.Contains(stopped, any("ctx-1")) && slices.Contains(done, any("ctx-1"))
	})
}

// A background shell that never ends (a dev server) must not keep every
// later message in progress: a message waits only for its own tasks.
func TestBackgroundHold_EarlierBackgroundTasksDoNotHoldNewMessage(t *testing.T) {
	h := newBgTurnHarness(t)
	h.sess.setTasks("dev-server")
	h.sess.onSend = func(int) { h.sess.reply("The server is still up.") }

	h.turn("is the server up?", "ctx-1")

	started, stopped, done := h.p.reactions()
	if !slices.Equal(started, []any{"ctx-1"}) || !slices.Equal(stopped, []any{"ctx-1"}) || !slices.Equal(done, []any{"ctx-1"}) {
		t.Fatalf("started=%v stopped=%v done=%v, want a normal finished turn on ctx-1", started, stopped, done)
	}
	if state := h.state(); state != nil && heldMessages(state) != 0 {
		t.Fatal("expected no hold for a task the turn did not launch")
	}
}

// A task's completion is folded into whatever turn is running, so a later
// user turn can be the one that finishes an earlier message's work.
func TestBackgroundHold_LaterTurnFinishesEarlierMessage(t *testing.T) {
	h := newBgTurnHarness(t)
	h.sess.onSend = func(n int) {
		switch n {
		case 1:
			h.sess.setTasks("survey")
			h.sess.reply("Survey launched.")
		case 2:
			h.sess.setTasks() // the survey reported back during this turn
			h.sess.reply("The survey is in, and it says yes.")
		}
	}

	h.turn("start the survey", "ctx-1")
	h.turn("any news?", "ctx-2")

	_, stopped, done := h.p.reactions()
	if !slices.Contains(stopped, any("ctx-1")) || !slices.Contains(done, any("ctx-1")) {
		t.Fatalf("stopped=%v done=%v, want the first message finished by the second turn", stopped, done)
	}
	if !slices.Contains(done, any("ctx-2")) {
		t.Fatalf("done=%v, want the second message done as usual", done)
	}
	if state := h.state(); state != nil && heldMessages(state) != 0 {
		t.Fatal("expected no message held once the survey reported back")
	}
}

// A follow-up turn that resumes a subagent (SendMessage) or launches another
// one continues the held message's work.
func TestBackgroundHold_FollowUpTurnTasksExtendTheHold(t *testing.T) {
	h := newBgTurnHarness(t)
	h.sess.onSend = func(int) {
		h.sess.setTasks("survey")
		h.sess.reply("Survey launched.")
	}
	h.turn("start the survey", "ctx-1")

	// The survey finished; the follow-up turn launches another one. Without
	// adopting it the hold would end here, its only task being gone.
	h.sess.setTasks()
	h.sess.events <- Event{Type: EventToolUse, ToolName: "Agent"}
	h.settle()
	h.sess.setTasks("deeper-survey")
	h.sess.reply("Launched a deeper survey.")
	h.settle()
	if _, stopped, done := h.p.reactions(); len(stopped) != 0 || len(done) != 0 {
		t.Fatalf("stopped=%v done=%v, want the message still in progress", stopped, done)
	}

	h.sess.setTasks()
	h.sess.reply("Deeper survey is in.")
	waitUntil(t, "the message is marked done", func() bool {
		_, stopped, done := h.p.reactions()
		return slices.Equal(stopped, []any{"ctx-1"}) && slices.Equal(done, []any{"ctx-1"})
	})
}

// Race seen with Claude Code: a task finishes just before the turn's result,
// so nothing is held, and the agent starts its follow-up turn right after.
// That turn still shows the latest message in progress.
func TestUnsolicitedReader_FollowUpTurnMarksLatestMessageInProgress(t *testing.T) {
	p := &typingPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}
	sess := newBgTaskSession()
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	defer func() { _ = e.Stop() }()
	session := e.sessions.GetOrCreateActive("test:ch:u1")
	state := &interactiveState{agentSession: sess, platform: p, replyCtx: "ctx-latest"}
	e.interactiveStates["k"] = state

	e.startUnsolicitedReader(state, session, e.sessions, "k", "")
	defer e.stopUnsolicitedReader(state)

	sess.events <- Event{Type: EventText} // content-less: not a turn
	sess.reply("The build you started has finished: all green.")

	waitUntil(t, "the follow-up turn is marked done", func() bool {
		_, stopped, done := p.reactions()
		return len(stopped) == 1 && len(done) == 1
	})
	started, stopped, done := p.reactions()
	if !slices.Equal(started, []any{"ctx-latest"}) || stopped[0] != "ctx-latest" || done[0] != "ctx-latest" {
		t.Fatalf("started=%v stopped=%v done=%v, want the indicator on ctx-latest", started, stopped, done)
	}
}

func TestUnsolicitedReader_ContentlessEventsDoNotCancelIdleClose(t *testing.T) {
	sess := newBgTaskSession()
	e := NewEngine("test", &stubAgent{}, []Platform{&stubPlatformEngine{n: "test"}}, "", LangEnglish)
	defer func() { _ = e.Stop() }()
	e.SetAgentSessionIdleTimeout(time.Hour)
	session := e.sessions.GetOrCreateActive("test:ch:u1")
	state := &interactiveState{agentSession: sess, platform: &stubPlatformEngine{n: "test"}, replyCtx: "ctx"}
	e.interactiveStates["k"] = state
	e.scheduleAgentSessionIdleClose("k", state)

	idleToken := func() uint64 {
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.agentSessionIdleToken
	}
	if idleToken() == 0 {
		t.Fatal("expected an idle close to be scheduled")
	}

	e.startUnsolicitedReader(state, session, e.sessions, "k", "")
	defer e.stopUnsolicitedReader(state)

	// Claude Code sends task progress and similar system messages between
	// turns; they do not mean the agent started working.
	sess.events <- Event{Type: EventText, SessionID: "bg-session"}
	waitUntil(t, "the content-less event is read", func() bool { return len(sess.events) == 0 })
	time.Sleep(20 * time.Millisecond)
	if idleToken() == 0 {
		t.Fatal("a content-less event cancelled the idle close")
	}

	sess.events <- Event{Type: EventThinking, Content: "a task finished, reading its output"}
	waitUntil(t, "the follow-up turn cancels the idle close", func() bool { return idleToken() == 0 })
}

func TestUnsolicitedReader_SilentReplyIsNotSent(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	sess := newBgTaskSession()
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	defer func() { _ = e.Stop() }()
	session := e.sessions.GetOrCreateActive("test:ch:u1")
	state := &interactiveState{agentSession: sess, platform: p, replyCtx: "ctx"}
	e.interactiveStates["k"] = state

	e.startUnsolicitedReader(state, session, e.sessions, "k", "")
	defer e.stopUnsolicitedReader(state)

	sess.events <- Event{Type: EventResult, Content: "NO_REPLY", Done: true}
	sess.events <- Event{Type: EventResult, Content: "Checked the log.\nNO_REPLY", Done: true}
	sess.events <- Event{Type: EventResult, Content: "Tests passed.", Done: true}

	waitUntil(t, "the last reply is delivered", func() bool { return sentContaining(p, "Tests passed.") })
	for _, s := range p.getSent() {
		if strings.Contains(s, "NO_REPLY") {
			t.Fatalf("sent %q, want the NO_REPLY marker suppressed", s)
		}
	}
	if !sentContaining(p, "Checked the log.") {
		t.Fatalf("sent %v, want the text before a trailing marker delivered", p.getSent())
	}
}

func TestUnsolicitedReader_AgentExitWhileHeldTellsUser(t *testing.T) {
	p := &typingPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}
	sess := newBgTaskSession()
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	defer func() { _ = e.Stop() }()
	session := e.sessions.GetOrCreateActive("test:ch:u1")
	state := &interactiveState{agentSession: sess, platform: p, replyCtx: "ctx"}
	e.interactiveStates["k"] = state
	stop := p.StartTyping(context.Background(), "ctx-held")
	e.holdForBackground(state, "k", &backgroundHold{messageID: "m1", platform: p, replyCtx: "ctx-held", stopTyping: stop, waitFor: map[string]struct{}{"survey": {}}})

	e.startUnsolicitedReader(state, session, e.sessions, "k", "")
	defer e.stopUnsolicitedReader(state)
	_ = sess.Close() // the process died

	waitUntil(t, "the user is told", func() bool { return sentContaining(&p.stubPlatformEngine, e.i18n.T(MsgAgentExitedMidTurn)) })
	_, stopped, done := p.reactions()
	if !slices.Equal(stopped, []any{"ctx-held"}) || len(done) != 0 {
		t.Fatalf("stopped=%v done=%v, want the indicator removed without a done reaction", stopped, done)
	}
}

func TestCleanupInteractiveState_ReleasesHeldMessages(t *testing.T) {
	p := &typingPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}
	sess := newBgTaskSession()
	sess.setTasks("survey")
	e := newTestEngine()
	state := &interactiveState{agentSession: sess, platform: p, replyCtx: "ctx"}
	e.interactiveStates["k"] = state
	e.holdForBackground(state, "k", &backgroundHold{platform: p, replyCtx: "ctx-held", stopTyping: p.StartTyping(context.Background(), "ctx-held"), waitFor: map[string]struct{}{"survey": {}}})

	e.cleanupInteractiveState("k")

	_, stopped, done := p.reactions()
	if !slices.Equal(stopped, []any{"ctx-held"}) || len(done) != 0 {
		t.Fatalf("stopped=%v done=%v, want the indicator removed without a done reaction", stopped, done)
	}
}

func TestAgentSessionIdleTimeout_KeepsSessionWhileBackgroundTasksRun(t *testing.T) {
	e := newTestEngine()
	e.SetAgentSessionIdleTimeout(20 * time.Millisecond)
	sess := newBgTaskSession()
	sess.setTasks("survey")
	state := &interactiveState{agentSession: sess}
	e.interactiveStates["k"] = state

	e.scheduleAgentSessionIdleClose("k", state)

	select {
	case <-sess.closed:
		t.Fatal("idle close killed the agent while a background task was running")
	case <-time.After(100 * time.Millisecond):
	}

	// Once the work is done the session is idle like any other.
	sess.setTasks()
	select {
	case <-sess.closed:
	case <-time.After(time.Second):
		t.Fatal("agent session was not closed after its background work finished")
	}
}

func TestReapIdleWorkspaces_KeepsWorkspaceWithBackgroundWork(t *testing.T) {
	e := newTestEngine()
	e.workspacePool = newWorkspacePool(time.Hour)
	ws := e.workspacePool.GetOrCreate(t.TempDir())
	sess := newBgTaskSession()
	sess.setTasks("survey")
	e.interactiveStates["k"] = &interactiveState{agentSession: sess, workspaceDir: ws.workspace}
	makeIdle := func() {
		ws.mu.Lock()
		ws.lastActivity = time.Now().Add(-2 * time.Hour)
		ws.mu.Unlock()
	}

	makeIdle()
	e.reapIdleWorkspaces()
	if e.workspacePool.Get(ws.workspace) == nil {
		t.Fatal("workspace reaped while its agent had a background task running")
	}

	sess.setTasks()
	makeIdle()
	e.reapIdleWorkspaces()
	if e.workspacePool.Get(ws.workspace) != nil {
		t.Fatal("expected the idle workspace to be reaped once the background work finished")
	}
}

func TestMaybeAutoResetSessionOnIdle_KeepsSessionWithBackgroundWork(t *testing.T) {
	e := newTestEngine()
	e.SetResetOnIdle(30 * time.Minute)
	sm := NewSessionManager(t.TempDir())
	session := sm.GetOrCreateActive("user:sk")
	session.AddHistory("user", "hello")
	session.SetAgentSessionID("agent-id-1", "claudecode")
	session.TryLock()
	session.mu.Lock()
	session.LastUserActivity = time.Now().Add(-35 * time.Minute)
	session.mu.Unlock()

	sess := newBgTaskSession()
	sess.setTasks("long-build")
	e.interactiveStates["ws:sk"] = &interactiveState{agentSession: sess}

	p := &stubPlatformEngine{n: "test"}
	if rotated, _ := e.maybeAutoResetSessionOnIdle(p, &Message{SessionKey: "sk", ReplyCtx: "ctx"}, sm, "ws:sk", session, 0); rotated != nil {
		t.Fatal("idle reset rotated the session while its agent had background work")
	}
	select {
	case <-sess.closed:
		t.Fatal("idle reset closed the agent while it had background work")
	default:
	}
}

func TestNewBackgroundTasks(t *testing.T) {
	set := func(ids ...string) map[string]struct{} {
		m := make(map[string]struct{})
		for _, id := range ids {
			m[id] = struct{}{}
		}
		return m
	}
	if got := newBackgroundTasks(set("server", "survey"), set("server")); len(got) != 1 {
		t.Fatalf("newBackgroundTasks = %v, want only survey", got)
	} else if _, ok := got["survey"]; !ok {
		t.Fatalf("newBackgroundTasks = %v, want only survey", got)
	}
	if got := newBackgroundTasks(set("server"), set("server")); got != nil {
		t.Fatalf("newBackgroundTasks = %v, want none", got)
	}
	if got := newBackgroundTasks(nil, nil); got != nil {
		t.Fatalf("newBackgroundTasks = %v, want none", got)
	}
}
