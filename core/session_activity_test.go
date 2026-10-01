package core

import (
	"testing"
	"time"
)

func TestInteractiveStateWork(t *testing.T) {
	live := func() AgentSession { return newBgTaskSession() }
	withTasks := func() AgentSession {
		s := newBgTaskSession()
		s.setTasks("survey")
		return s
	}
	dead := func() AgentSession {
		s := newBgTaskSession()
		_ = s.Close()
		return s
	}
	for _, tc := range []struct {
		name  string
		state func() *interactiveState
		want  sessionWork
	}{
		{"idle", func() *interactiveState { return &interactiveState{agentSession: live()} }, workIdle},
		{"no agent", func() *interactiveState { return &interactiveState{} }, workIdle},
		{"turn", func() *interactiveState {
			s := &interactiveState{agentSession: live()}
			s.beginTurn()
			return s
		}, workTurn},
		{"permission prompt inside a turn reads as the turn", func() *interactiveState {
			s := &interactiveState{agentSession: live(), pending: &pendingPermission{}}
			s.beginTurn()
			return s
		}, workTurn},
		{"agent turn", func() *interactiveState {
			s := &interactiveState{agentSession: live()}
			s.setAgentTurn(true)
			return s
		}, workAgentTurn},
		{"awaiting user", func() *interactiveState {
			return &interactiveState{agentSession: live(), pending: &pendingPermission{}}
		}, workAwaitingUser},
		{"queued messages", func() *interactiveState {
			return &interactiveState{agentSession: live(), pendingMessages: []queuedMessage{{content: "next"}}}
		}, workQueued},
		{"background tasks", func() *interactiveState { return &interactiveState{agentSession: withTasks()} }, workBackground},
		{"held message", func() *interactiveState {
			return &interactiveState{agentSession: live(), backgroundHolds: []*backgroundHold{{}}}
		}, workBackground},
		{"dead agent with a stale hold", func() *interactiveState {
			return &interactiveState{agentSession: dead(), backgroundHolds: []*backgroundHold{{}}}
		}, workIdle},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.state().work(); got != tc.want {
				t.Fatalf("work() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInteractiveStateTurnsNest(t *testing.T) {
	s := &interactiveState{agentSession: newBgTaskSession()}
	endOuter := s.beginTurn()
	endInner := s.beginTurn() // an automatic compress inside the turn
	endInner()
	endInner() // ending twice is harmless
	if got := s.work(); got != workTurn {
		t.Fatalf("work() with the outer turn running = %q, want %q", got, workTurn)
	}
	endOuter()
	if got := s.work(); got != workIdle {
		t.Fatalf("work() after both turns ended = %q, want idle", got)
	}
	if s.workEndedAt.IsZero() {
		t.Fatal("expected the end of the work to be recorded")
	}
}

// Every reason the session is busy keeps the agent alive past the idle
// timeout; the close happens once the session is idle.
func TestAgentSessionIdleTimeout_KeepsBusySession(t *testing.T) {
	for _, tc := range []struct {
		name string
		busy func(s *interactiveState, sess *bgTaskSession) (release func())
	}{
		{"turn", func(s *interactiveState, _ *bgTaskSession) func() { return s.beginTurn() }},
		{"agent turn", func(s *interactiveState, _ *bgTaskSession) func() {
			s.setAgentTurn(true)
			return func() { s.setAgentTurn(false) }
		}},
		{"awaiting user", func(s *interactiveState, _ *bgTaskSession) func() {
			s.mu.Lock()
			s.pending = &pendingPermission{}
			s.mu.Unlock()
			return func() {
				s.mu.Lock()
				s.pending = nil
				s.mu.Unlock()
			}
		}},
		{"queued messages", func(s *interactiveState, _ *bgTaskSession) func() {
			s.mu.Lock()
			s.pendingMessages = []queuedMessage{{content: "next"}}
			s.mu.Unlock()
			return func() {
				s.mu.Lock()
				s.pendingMessages = nil
				s.mu.Unlock()
			}
		}},
		{"background tasks", func(_ *interactiveState, sess *bgTaskSession) func() {
			sess.setTasks("survey")
			return func() { sess.setTasks() }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEngine()
			e.SetAgentSessionIdleTimeout(20 * time.Millisecond)
			sess := newBgTaskSession()
			state := &interactiveState{agentSession: sess}
			e.interactiveStates["k"] = state
			release := tc.busy(state, sess)

			e.scheduleAgentSessionIdleClose("k", state)
			select {
			case <-sess.closed:
				t.Fatal("idle close killed a busy session")
			case <-time.After(100 * time.Millisecond):
			}

			release()
			select {
			case <-sess.closed:
			case <-time.After(time.Second):
				t.Fatal("agent session was not closed once it was idle")
			}
		})
	}
}

// A manual /compress is a turn: the idle close firing while it runs leaves
// the agent alone.
func TestRunCompress_CountsAsTurn(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	sess := newBgTaskSession()
	e := NewEngine("test", &stubCompressorAgent{cmd: "/compact"}, []Platform{p}, "", LangEnglish)
	defer func() { _ = e.Stop() }()
	state := &interactiveState{agentSession: sess, platform: p, replyCtx: "ctx"}
	e.interactiveStates["k"] = state
	session := e.sessions.GetOrCreateActive("k")
	lockGen, _ := session.TryLock()

	done := make(chan struct{})
	go func() {
		e.runCompress(state, session, e.sessions, "k", p, "ctx", false, lockGen)
		close(done)
	}()
	waitUntil(t, "the compress command is sent", func() bool {
		sess.mu.Lock()
		defer sess.mu.Unlock()
		return sess.sends == 1
	})
	if got := state.work(); got != workTurn {
		t.Fatalf("work() during /compress = %q, want %q", got, workTurn)
	}

	sess.events <- Event{Type: EventResult, Content: "Compacted.", Done: true}
	<-done
	if got := state.work(); got != workIdle {
		t.Fatalf("work() after /compress = %q, want idle", got)
	}
}

// A workspace stays while a session in it worked recently, even though no
// message arrived for longer than the idle timeout.
func TestReapIdleWorkspaces_UsesSessionWorkTime(t *testing.T) {
	e := newTestEngine()
	e.workspacePool = newWorkspacePool(time.Hour)
	ws := e.workspacePool.GetOrCreate(t.TempDir())
	ws.mu.Lock()
	ws.lastActivity = time.Now().Add(-2 * time.Hour)
	ws.mu.Unlock()
	sess := newBgTaskSession()
	state := &interactiveState{agentSession: sess, workspaceDir: ws.workspace}
	state.beginTurn()() // a turn just ended
	e.interactiveStates["k"] = state

	e.reapIdleWorkspaces()
	if e.workspacePool.Get(ws.workspace) == nil {
		t.Fatal("workspace reaped right after its session finished a turn")
	}

	state.mu.Lock()
	state.workEndedAt = time.Now().Add(-2 * time.Hour)
	state.mu.Unlock()
	e.reapIdleWorkspaces()
	if e.workspacePool.Get(ws.workspace) != nil {
		t.Fatal("expected the workspace to be reaped once its session had been idle for the timeout")
	}
	select {
	case <-sess.closed:
	case <-time.After(time.Second):
		t.Fatal("expected the reaped workspace's agent to be closed")
	}
}

func TestMaybeAutoResetSessionOnIdle_KeepsSessionInAgentTurn(t *testing.T) {
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

	state := &interactiveState{agentSession: newBgTaskSession()}
	state.setAgentTurn(true)
	e.interactiveStates["ws:sk"] = state

	p := &stubPlatformEngine{n: "test"}
	if rotated, _ := e.maybeAutoResetSessionOnIdle(p, &Message{SessionKey: "sk", ReplyCtx: "ctx"}, sm, "ws:sk", session, 0); rotated != nil {
		t.Fatal("idle reset rotated the session while the agent ran a turn of its own")
	}
	if sent := p.getSent(); len(sent) != 0 {
		t.Fatalf("sent %q, want no reset notices", sent)
	}
}
