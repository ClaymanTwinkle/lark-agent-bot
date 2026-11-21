package core

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// cancellableSession is an agent session that supports TurnCanceller. By
// default CancelTurn ends the running turn the way agents do: it sends the
// turn's terminal EventResult. Set cancelErr to refuse, or silent to accept
// without ever ending the turn.
type cancellableSession struct {
	*controllableAgentSession
	cancels   atomic.Int32
	cancelErr error
	silent    bool
	prompts   chan string
	live      atomic.Bool // Alive and Close run on different goroutines
	closeOnce sync.Once
}

func newCancellableSession(id string) *cancellableSession {
	s := &cancellableSession{controllableAgentSession: newControllableSession(id), prompts: make(chan string, 8)}
	s.live.Store(true)
	return s
}

func (s *cancellableSession) Alive() bool { return s.live.Load() }

func (s *cancellableSession) Close() error {
	s.closeOnce.Do(func() {
		s.live.Store(false)
		close(s.closed)
	})
	return nil
}

func (s *cancellableSession) Send(prompt string, _ string, _ []ImageAttachment, _ []FileAttachment) error {
	s.prompts <- prompt
	return nil
}

func (s *cancellableSession) CancelTurn() error {
	s.cancels.Add(1)
	if s.cancelErr != nil {
		return s.cancelErr
	}
	if !s.silent {
		s.events <- Event{Type: EventResult, Done: true}
	}
	return nil
}

func (s *cancellableSession) isClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

type cancelEnv struct {
	t      *testing.T
	engine *Engine
	plat   *stubPlatformEngine
	sess   *cancellableSession
	starts atomic.Int32
	key    string
}

func newCancelEnv(t *testing.T, sess *cancellableSession) *cancelEnv {
	t.Helper()
	env := &cancelEnv{t: t, sess: sess, plat: &stubPlatformEngine{n: "test"}, key: "test:cancel:user"}
	agent := &controllableAgent{startSessionFn: func(context.Context, string) (AgentSession, error) {
		env.starts.Add(1)
		return sess, nil
	}}
	env.engine = NewEngine("test", agent, []Platform{env.plat}, "", LangEnglish)
	t.Cleanup(func() { env.engine.cancel() })
	return env
}

func (env *cancelEnv) userSends(content string) {
	env.userSendsID(content, "")
}

func (env *cancelEnv) userSendsID(content, messageID string) {
	env.engine.ReceiveMessage(env.plat, &Message{SessionKey: env.key, Platform: "test", UserID: "user", Content: content, ReplyCtx: "ctx", MessageID: messageID})
}

func (env *cancelEnv) waitFor(what string, cond func() bool) {
	env.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			env.t.Fatalf("timed out waiting for %s; sent=%q", what, env.plat.getSent())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// startTurn sends a prompt and waits until the agent has it and the turn runs.
func (env *cancelEnv) startTurn(content string) {
	env.t.Helper()
	env.userSends(content)
	select {
	case <-env.sess.prompts:
	case <-time.After(3 * time.Second):
		env.t.Fatal("agent never got the prompt")
	}
	env.waitFor("turn running", func() bool {
		env.engine.interactiveMu.Lock()
		state := env.engine.interactiveStates[env.key]
		env.engine.interactiveMu.Unlock()
		return state != nil && state.work() == workTurn
	})
}

func (env *cancelEnv) turnRunning() bool {
	env.engine.interactiveMu.Lock()
	state := env.engine.interactiveStates[env.key]
	env.engine.interactiveMu.Unlock()
	return state != nil && state.work() == workTurn
}

func (env *cancelEnv) sentContains(s string) bool {
	return strings.Contains(strings.Join(env.plat.getSent(), "\n"), s)
}

func TestStop_CancelsTurnKeepsSessionAndPartialReply(t *testing.T) {
	sess := newCancellableSession("agent-1")
	env := newCancelEnv(t, sess)

	env.startTurn("write an essay")
	sess.events <- Event{Type: EventText, Content: "partial essay"}
	env.userSends("/stop")

	env.waitFor("turn ended", func() bool { return !env.turnRunning() })
	env.waitFor("partial reply", func() bool { return env.sentContains("partial essay") })
	if sess.cancels.Load() != 1 {
		t.Errorf("CancelTurn called %d times", sess.cancels.Load())
	}
	if sess.isClosed() {
		t.Fatal("/stop closed the agent session; it should only end the turn")
	}
	if !env.sentContains(env.engine.i18n.T(MsgExecutionStopped)) {
		t.Errorf("no stop confirmation; sent=%q", env.plat.getSent())
	}

	// The next message runs in the same agent process.
	env.plat.clearSent()
	env.startTurn("next question")
	sess.events <- Event{Type: EventResult, Content: "next answer", Done: true}
	env.waitFor("next answer", func() bool { return env.sentContains("next answer") })
	if n := env.starts.Load(); n != 1 {
		t.Errorf("agent sessions started = %d, want 1", n)
	}
}

func TestStop_CancelledTurnWithoutOutputStaysQuiet(t *testing.T) {
	sess := newCancellableSession("agent-1")
	env := newCancelEnv(t, sess)

	env.startTurn("write an essay")
	env.userSends("/stop")
	env.waitFor("turn ended", func() bool { return !env.turnRunning() })
	time.Sleep(100 * time.Millisecond)
	if env.sentContains(env.engine.i18n.T(MsgEmptyResponse)) {
		t.Errorf("cancelled turn sent the empty-response text; sent=%q", env.plat.getSent())
	}
	if sess.isClosed() {
		t.Error("session closed")
	}
}

func TestStop_FallsBackToCloseWhenCancelFails(t *testing.T) {
	sess := newCancellableSession("agent-1")
	sess.cancelErr = errors.New("cannot interrupt")
	env := newCancelEnv(t, sess)

	env.startTurn("write an essay")
	env.userSends("/stop")
	env.waitFor("session closed", sess.isClosed)
}

func TestStop_ClosesWhenCancelledTurnNeverEnds(t *testing.T) {
	sess := newCancellableSession("agent-1")
	sess.silent = true
	env := newCancelEnv(t, sess)
	env.engine.cancelledTurnTimeout = 200 * time.Millisecond

	env.startTurn("write an essay")
	env.userSends("/stop")
	time.Sleep(50 * time.Millisecond)
	if sess.isClosed() {
		t.Fatal("closed before the cancel timeout")
	}
	env.waitFor("session closed after timeout", sess.isClosed)
}

func TestStop_WithoutRunningTurnClosesSession(t *testing.T) {
	sess := newCancellableSession("agent-1")
	env := newCancelEnv(t, sess)

	env.startTurn("hi")
	sess.events <- Event{Type: EventResult, Content: "hello", Done: true}
	env.waitFor("answer", func() bool { return env.sentContains("hello") })
	env.waitFor("turn ended", func() bool { return !env.turnRunning() })

	env.userSends("/stop")
	env.waitFor("session closed", sess.isClosed)
	if sess.cancels.Load() != 0 {
		t.Errorf("CancelTurn called with no turn running")
	}
}

func TestCancelCommand_StillClosesSession(t *testing.T) {
	sess := newCancellableSession("agent-1")
	env := newCancelEnv(t, sess)

	env.startTurn("write an essay")
	env.userSends("/cancel")
	env.waitFor("session closed", sess.isClosed)
	if sess.cancels.Load() != 0 {
		t.Errorf("/cancel used CancelTurn; it must reset the session")
	}
}

// awaitCancelledTurn must not close a session whose cancelled turn ended
// and whose next turn is already running.
func TestAwaitCancelledTurn_IgnoresLaterTurn(t *testing.T) {
	e := newTestEngine()
	e.cancelledTurnTimeout, e.cancelledTurnPoll = 150*time.Millisecond, 10*time.Millisecond
	sess := newCancellableSession("s")
	state := &interactiveState{agentSession: sess}
	key := "test:later"
	e.interactiveMu.Lock()
	e.interactiveStates[key] = state
	e.interactiveMu.Unlock()

	end := state.beginTurn()
	state.mu.Lock()
	seq := state.turnSeq
	state.mu.Unlock()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		e.awaitCancelledTurn(key, state, seq)
	}()
	// The next turn starts before the cancelled one is seen to end, so only
	// the changed turn sequence tells them apart.
	next := state.beginTurn()
	defer next()
	end()
	wg.Wait()
	if sess.isClosed() {
		t.Error("closed the session running the next turn")
	}
}

// Recalling the message a turn answers cancels the turn like /stop, keeps
// the agent process, and sends no answer to the withdrawn message.
func TestRecall_CancelsTurnWithoutReplying(t *testing.T) {
	sess := newCancellableSession("agent-1")
	env := newCancelEnv(t, sess)

	env.userSendsID("write an essay", "m1")
	select {
	case <-sess.prompts:
	case <-time.After(3 * time.Second):
		t.Fatal("agent never got the prompt")
	}
	env.waitFor("turn running", env.turnRunning)
	sess.events <- Event{Type: EventText, Content: "partial essay"}

	if !env.engine.stopRecalledMessage(env.key, "m1") {
		t.Fatal("recall did not stop the turn")
	}
	env.waitFor("turn ended", func() bool { return !env.turnRunning() })
	time.Sleep(100 * time.Millisecond)
	if sess.cancels.Load() != 1 || sess.isClosed() {
		t.Fatalf("cancels=%d closed=%v; want the turn cancelled and the session kept", sess.cancels.Load(), sess.isClosed())
	}
	if !env.sentContains(env.engine.i18n.T(MsgRecallActiveStopping)) {
		t.Errorf("no recall notice; sent=%q", env.plat.getSent())
	}
	if env.sentContains("partial essay") || env.sentContains(env.engine.i18n.T(MsgEmptyResponse)) {
		t.Errorf("the recalled message got an answer; sent=%q", env.plat.getSent())
	}

	// The next message is answered normally, in the same process.
	env.plat.clearSent()
	env.startTurn("next question")
	sess.events <- Event{Type: EventResult, Content: "next answer", Done: true}
	env.waitFor("next answer", func() bool { return env.sentContains("next answer") })
	if n := env.starts.Load(); n != 1 {
		t.Errorf("agent sessions started = %d, want 1", n)
	}
}
