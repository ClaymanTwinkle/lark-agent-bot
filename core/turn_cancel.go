package core

import (
	"fmt"
	"log/slog"
	"time"
)

// defaultCancelledTurnTimeout bounds how long /stop waits for a cancelled
// turn to end before it stops the session by closing it.
const defaultCancelledTurnTimeout = 20 * time.Second

// defaultCancelledTurnPoll is how often that wait checks the turn.
const defaultCancelledTurnPoll = 100 * time.Millisecond

// cancelTimings returns the wait for a cancelled turn and its poll interval;
// tests shorten them per engine.
func (e *Engine) cancelTimings() (timeout, poll time.Duration) {
	timeout, poll = defaultCancelledTurnTimeout, defaultCancelledTurnPoll
	if e.cancelledTurnTimeout > 0 {
		timeout = e.cancelledTurnTimeout
	}
	if e.cancelledTurnPoll > 0 {
		poll = e.cancelledTurnPoll
	}
	return timeout, poll
}

// turnCancellerFor returns the session's TurnCanceller when a turn is running
// that it can cancel, else nil (nothing to cancel: /stop closes the session,
// which is also how it stops background work between turns).
func turnCancellerFor(state *interactiveState) TurnCanceller {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.stopped || (state.turns == 0 && !state.agentTurn) || state.agentSession == nil || !state.agentSession.Alive() {
		return nil
	}
	canceller, _ := state.agentSession.(TurnCanceller)
	return canceller
}

// interruptTurn ends the running turn and keeps the session: queued messages
// are dropped (as with any /stop), a pending permission prompt is released,
// and the agent is asked to cancel. The turn then ends through its normal
// path, keeping what it already said, unless silent (the message was
// recalled): then it sends no reply. If the agent refuses, or the turn has
// not ended within cancelledTurnTimeout, the session is closed instead.
// interactiveMu must not be held.
func (e *Engine) interruptTurn(sessionKey string, state *interactiveState, canceller TurnCanceller, notifyQueued, silent bool) (bool, []queuedMessage) {
	var dropped []queuedMessage
	if notifyQueued {
		e.notifyDroppedQueuedMessages(state, fmt.Errorf("%s", e.i18n.T(MsgExecutionStopped)))
	} else {
		dropped = takePendingMessages(state)
	}

	state.mu.Lock()
	pending := state.pending
	state.pending = nil
	state.turnCancelled = true
	state.turnCancelSilent = silent
	seq := state.turnSeq
	state.mu.Unlock()
	if pending != nil {
		pending.resolve()
	}

	if err := canceller.CancelTurn(); err != nil {
		slog.Warn("agent could not cancel the turn, stopping the session instead", "session_key", sessionKey, "error", err)
		state.mu.Lock()
		state.turnCancelled, state.turnCancelSilent = false, false
		state.mu.Unlock()
		stopped, more := e.stopInteractiveSessionIfCurrent(sessionKey, notifyQueued, "", false)
		return stopped, append(dropped, more...)
	}
	slog.Info("asked the agent to cancel the running turn", "session_key", sessionKey)
	go e.awaitCancelledTurn(sessionKey, state, seq)
	return true, dropped
}

// awaitCancelledTurn closes the session if the turn cancelled at seq is still
// running after cancelledTurnTimeout. A later turn (seq changed) means the
// cancelled one ended.
func (e *Engine) awaitCancelledTurn(sessionKey string, state *interactiveState, seq uint64) {
	timeout, poll := e.cancelTimings()
	deadline := time.Now().Add(timeout)
	for {
		state.mu.Lock()
		ended := state.stopped || state.turnSeq != seq || (state.turns == 0 && !state.agentTurn)
		state.mu.Unlock()
		if ended {
			return
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-e.ctx.Done():
			return
		case <-time.After(poll):
		}
	}

	e.interactiveMu.Lock()
	current := e.interactiveStates[sessionKey] == state
	e.interactiveMu.Unlock()
	if !current {
		return
	}
	slog.Warn("cancelled turn did not end in time, stopping the session", "session_key", sessionKey, "timeout", timeout)
	e.stopInteractiveSessionIfCurrent(sessionKey, true, "", false)
}
