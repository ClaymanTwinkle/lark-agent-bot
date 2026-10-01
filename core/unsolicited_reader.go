package core

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// unsolicitedReaderStopTimeout bounds how long stopUnsolicitedReader waits
// for the reader goroutine to exit. The reader is structured so its iterations
// are short (a turn the agent starts on its own is handed off, not run by the
// reader), so this timeout should almost always be non-binding. If it does
// fire, callers force a resync of the Events channel to preserve
// single-reader correctness.
const unsolicitedReaderStopTimeout = 5 * time.Second

// agentTurnLockRetry is how often the reader retries taking the session for
// a turn the agent started on its own while another holder has it.
const agentTurnLockRetry = 50 * time.Millisecond

// agentTurn is a turn the agent started on its own, typically because a
// background task finished.
type agentTurn struct {
	events   []Event             // the turn's events already read from the agent
	baseline map[string]struct{} // the background tasks running when it started
}

// stopUnsolicitedReader cancels any running unsolicited reader goroutine and
// waits (bounded) for it to exit. If the reader does not exit in time, the
// caller is responsible for draining/resyncing the Events channel before a
// new foreground turn reads from it — we set eventsNeedResync here so that
// any downstream consumer drains before resuming. We do NOT wait unbounded:
// some callers hold interactiveMu, and a reader stuck in a blocking adapter
// call would stall unrelated sessions.
func (e *Engine) stopUnsolicitedReader(state *interactiveState) {
	state.mu.Lock()
	cancel := state.unsolicitedCancel
	done := state.unsolicitedDone
	state.unsolicitedCancel = nil
	state.unsolicitedDone = nil
	state.mu.Unlock()

	if cancel == nil {
		return
	}
	cancel()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(unsolicitedReaderStopTimeout):
		slog.Warn("unsolicited reader stop timed out; forcing resync",
			"timeout", unsolicitedReaderStopTimeout)
		// Force the next foreground turn to drain Events() defensively.
		// The old reader may still be alive; its ctx-double-check will drop
		// any event read after cancellation, so concurrent consumers cannot
		// silently steal foreground events.
		state.mu.Lock()
		state.eventsNeedResync = true
		state.mu.Unlock()
	}
}

// startUnsolicitedReader launches a background goroutine that consumes agent
// events produced between user-initiated turns (e.g. background task
// completions in Claude Code). The goroutine exits when its context is
// cancelled (by a new foreground turn or session cleanup), when the Events
// channel is closed, or when the agent starts a turn, which it hands off.
func (e *Engine) startUnsolicitedReader(state *interactiveState, session *Session, sessions *SessionManager, sessionKey string) {
	// Ensure no previous reader is still running.
	e.stopUnsolicitedReader(state)

	// Capture the agent session under lock. cleanupInteractiveState may nil
	// state.agentSession concurrently, so reading it inside the goroutine
	// without synchronisation is a data race.
	state.mu.Lock()
	agentSession := state.agentSession
	state.mu.Unlock()
	if agentSession == nil {
		return
	}

	ctx, cancel := context.WithCancel(e.ctx)
	done := make(chan struct{})

	state.mu.Lock()
	state.unsolicitedCancel = cancel
	state.unsolicitedDone = done
	state.mu.Unlock()

	go e.runUnsolicitedReader(ctx, cancel, done, state, agentSession, session, sessions, sessionKey)
}

// resumeBetweenTurns hands the agent's events to the reader once the turns
// holding the session are over, and arms the idle close. A session whose
// agent is gone, stopped or out of sync gets neither.
func (e *Engine) resumeBetweenTurns(state *interactiveState, session *Session, sessions *SessionManager, sessionKey string) {
	state.mu.Lock()
	alive := state.agentSession != nil && state.agentSession.Alive() && !state.stopped && !state.eventsNeedResync
	state.mu.Unlock()
	if alive {
		e.startUnsolicitedReader(state, session, sessions, sessionKey)
		e.scheduleAgentSessionIdleClose(sessionKey, state)
	}
}

// runUnsolicitedReader is the goroutine body for the unsolicited event reader.
// agentSession is captured by the caller so we don't race with
// cleanupInteractiveState nilling state.agentSession.
func (e *Engine) runUnsolicitedReader(ctx context.Context, cancel context.CancelFunc, done chan struct{}, state *interactiveState, agentSession AgentSession, session *Session, sessions *SessionManager, sessionKey string) {
	defer close(done)
	defer cancel()

	events := agentSession.Events()
	for {
		select {
		case <-ctx.Done():
			// Context cancelled (new foreground turn or cleanup). Don't set
			// eventsNeedResync — the caller (stopUnsolicitedReader) knows the
			// channel state is clean because it just took ownership.
			return

		case event, ok := <-events:
			if !ok {
				// Channel closed — agent process exited.
				state.mu.Lock()
				state.eventsNeedResync = true
				state.mu.Unlock()
				// Shutdown and intentional closes stop this reader before the
				// channel closes; here the agent process died on its own,
				// taking the work a held message waits for with it.
				if e.ctx.Err() == nil && !state.isStopped() {
					e.abandonBackgroundWork(state, sessionKey)
				}
				return
			}

			// The agent started a turn on its own, typically because a
			// background task finished. Even if ownership has just been
			// handed off, the event must not be lost: takeAgentTurn leaves
			// it to the new owner.
			if isAgentActivity(event) {
				e.takeAgentTurn(ctx, done, state, session, sessions, sessionKey, event)
				return
			}

			if event.Type == EventError {
				state.mu.Lock()
				state.eventsNeedResync = true
				p := state.platform
				replyCtx := state.replyCtx
				state.mu.Unlock()
				// Go's select is non-deterministic when multiple cases are
				// ready, so even after ctx is cancelled we may still read one
				// last event; the resync tells the new owner.
				if ctx.Err() != nil {
					return
				}
				if event.Error != nil {
					slog.Error("unsolicited agent error", "error", event.Error, "session", sessionKey)
					e.send(p, replyCtx, fmt.Sprintf(e.i18n.T(MsgError), event.Error))
				}
				e.releaseBackgroundHolds(state, sessionKey, holdAbandoned)
				return
			}
			// Agents also emit content-less events between turns — Claude
			// Code turns each system message (background task progress, hook
			// runs) into one — that say nothing about a turn.
		}
	}
}

// takeAgentTurn makes the turn the agent just started on its own (first is
// its first event) a regular turn: it takes the session lock, so user
// messages queue behind the turn instead of being folded into it, and the
// turn shows progress, asks the user for permissions and is watched for
// stalls like any other. While another holder has the lock, it waits; if the
// holder takes the events from the reader meanwhile, the turn is left to it
// (runAgentTurnLeftByReader).
func (e *Engine) takeAgentTurn(ctx context.Context, done chan struct{}, state *interactiveState, session *Session, sessions *SessionManager, sessionKey string, first Event) {
	// The agent is not idle until the turn ends, and the message whose work
	// it continues shows it is being worked on.
	state.setAgentTurn(true)
	e.cancelAgentSessionIdleClose(state)
	at := &agentTurn{events: []Event{first}, baseline: state.pendingBackgroundTasks()}
	e.holdForFollowUpTurn(state)
	slog.Info("agent started a turn on its own", "session", sessionKey)

	for {
		if ctx.Err() != nil {
			state.leaveAgentTurn(at)
			return
		}
		if lockGen, ok := session.TryLock(); ok {
			// From here on the turn is not the reader's: stopping the reader
			// does not wait for it, and a stop reaches it through the
			// session's stop signal like any turn.
			state.mu.Lock()
			owned := state.unsolicitedDone == done
			if owned {
				state.unsolicitedCancel, state.unsolicitedDone = nil, nil
			}
			state.mu.Unlock()
			if !owned { // being stopped: whoever stops the reader takes the turn
				session.UnlockWithoutUpdate(lockGen)
				state.leaveAgentTurn(at)
				return
			}
			e.runAgentTurnLocked(state, session, sessions, sessionKey, at, lockGen)
			return
		}
		select {
		case <-ctx.Done():
		case <-time.After(agentTurnLockRetry):
		}
	}
}

// runAgentTurnLocked runs the agent's own turn under the session lock the
// reader took for it, then the messages queued meanwhile, and unlocks.
func (e *Engine) runAgentTurnLocked(state *interactiveState, session *Session, sessions *SessionManager, sessionKey string, at *agentTurn, lockGen uint64) {
	unlocked := false
	defer func() {
		if !unlocked {
			session.Unlock(lockGen)
		}
	}()
	defer state.beginTurn()()

	e.runAgentTurn(state, session, sessions, sessionKey, at, lockGen)
	unlocked = e.drainPendingMessages(state, session, sessions, sessionKey, lockGen)
}

// runAgentTurn runs a turn the agent started on its own as a regular turn,
// up to its result. The caller holds the session lock and handles the
// queued messages afterwards. The turn replies to the message whose work it
// continues: the newest held message.
func (e *Engine) runAgentTurn(state *interactiveState, session *Session, sessions *SessionManager, sessionKey string, at *agentTurn, lockGen uint64) {
	defer state.setAgentTurn(false)
	if !state.agentAlive() {
		return
	}
	state.setAgentTurn(true)
	defer state.beginTurn()()
	e.cancelAgentSessionIdleClose(state)
	e.holdForFollowUpTurn(state)
	replyCtx := state.anchorToNewestHold()
	e.processTurnEvents(state, session, sessions, sessionKey, "", time.Now(), nil, nil, replyCtx, lockGen, at)
}

// runAgentTurnLeftByReader runs the turn the agent started on its own just
// before the caller took the events from the reader (see takeAgentTurn), so
// the caller's prompt is not folded into it. The caller holds the session
// lock and has stopped the reader. It reports whether a turn ran.
func (e *Engine) runAgentTurnLeftByReader(state *interactiveState, session *Session, sessions *SessionManager, sessionKey string, lockGen uint64) bool {
	state.mu.Lock()
	at := state.agentTurnLeft
	state.agentTurnLeft = nil
	// Out of sync, the turn's events are stale: the caller drains them.
	stale := state.eventsNeedResync
	state.mu.Unlock()
	if at == nil || stale {
		return false
	}
	slog.Info("running the agent's own turn before the next prompt", "session", sessionKey)
	e.runAgentTurn(state, session, sessions, sessionKey, at, lockGen)
	return true
}

// leaveAgentTurn hands the agent's own turn the reader could not take to
// whoever takes the events from it (runAgentTurnLeftByReader).
func (s *interactiveState) leaveAgentTurn(at *agentTurn) {
	s.mu.Lock()
	s.agentTurnLeft = at
	s.mu.Unlock()
	s.setAgentTurn(false)
}

// anchorToNewestHold points the session's replies at the newest held
// message, whose work the agent's own turn continues, and returns its reply
// context.
func (s *interactiveState) anchorToNewestHold() any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := len(s.backgroundHolds); n > 0 {
		if h := s.backgroundHolds[n-1]; h.platform != nil {
			s.platform, s.replyCtx = h.platform, h.replyCtx
			if h.messageID != "" {
				s.setCurrentMessageLocked(h.messageID)
			}
		}
	}
	// Leftovers of the last user turn do not apply to this one.
	s.fromVoice = false
	s.sideText = ""
	return s.replyCtx
}
