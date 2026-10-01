package core

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// sessionWork says what keeps an interactive session's agent busy, or
// workIdle when nothing does. It is the one answer to "is this session
// idle?" for everything that closes or reaps sessions: the per-session idle
// close, workspace reaping, the idle reset and worktree removal. Each of them
// logs the reason when it leaves a session alone.
type sessionWork string

const (
	workIdle sessionWork = ""
	// workTurn: a user, scheduled, queued or compress turn is running.
	workTurn sessionWork = "turn"
	// workAgentTurn: the agent runs a turn it started on its own, typically
	// because a background task finished.
	workAgentTurn sessionWork = "agent turn"
	// workAwaitingUser: a permission prompt or question waits for the user.
	workAwaitingUser sessionWork = "awaiting user"
	// workQueued: messages wait for the session.
	workQueued sessionWork = "queued messages"
	// workBackground: background tasks still run, or a message is still held
	// for tasks whose follow-up turn has not run yet.
	workBackground sessionWork = "background tasks"
)

// workLocked reports what the session's agent is doing. s.mu must be held.
func (s *interactiveState) workLocked() sessionWork {
	switch {
	case s.turns > 0:
		return workTurn
	case s.agentTurn:
		return workAgentTurn
	case s.pending != nil:
		return workAwaitingUser
	case len(s.pendingMessages) > 0:
		return workQueued
	}
	// Background work dies with the agent process; holds left on a dead
	// session are released by its teardown.
	if s.agentSession != nil && s.agentSession.Alive() &&
		(len(s.backgroundHolds) > 0 || len(pendingBackgroundTasks(s.agentSession)) > 0) {
		return workBackground
	}
	return workIdle
}

func (s *interactiveState) work() sessionWork {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workLocked()
}

// beginTurn records that a turn uses the session and returns the function
// that ends it. Turns nest: an automatic compress runs inside the turn that
// triggered it.
func (s *interactiveState) beginTurn() (end func()) {
	s.mu.Lock()
	s.turns++
	s.idleCloseKeptFor = workIdle
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			s.turns--
			s.workEndedAt = time.Now()
			s.mu.Unlock()
		})
	}
}

// setAgentTurn records the start or end of a turn the agent started on its
// own.
func (s *interactiveState) setAgentTurn(active bool) {
	s.mu.Lock()
	s.agentTurn = active
	if active {
		s.idleCloseKeptFor = workIdle
	} else {
		s.workEndedAt = time.Now()
	}
	s.mu.Unlock()
}

// inUseSince reports whether the session is busy or, for a non-zero cutoff,
// last worked after it, with the reason when it is busy.
func (s *interactiveState) inUseSince(cutoff time.Time) (bool, sessionWork) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w := s.workLocked(); w != workIdle {
		return true, w
	}
	return !cutoff.IsZero() && s.workEndedAt.After(cutoff), workIdle
}

// workspaceInUse reports whether a session working in dir is busy or worked
// after cutoff (zero cutoff: busy only), with the busy reason.
func (e *Engine) workspaceInUse(dir string, cutoff time.Time) (bool, sessionWork) {
	type candidate struct {
		state *interactiveState
		dir   string
	}
	e.interactiveMu.Lock()
	candidates := make([]candidate, 0, len(e.interactiveStates))
	for _, state := range e.interactiveStates {
		state.mu.Lock()
		wsDir := state.workspaceDir
		state.mu.Unlock()
		if wsDir != "" {
			candidates = append(candidates, candidate{state, wsDir})
		}
	}
	e.interactiveMu.Unlock()

	// Normalizing resolves symlinks on disk: not under the engine lock.
	dir = normalizeWorkspacePath(dir)
	for _, c := range candidates {
		if !sameWorkspacePath(normalizeWorkspacePath(c.dir), dir) {
			continue
		}
		if inUse, why := c.state.inUseSince(cutoff); inUse {
			return true, why
		}
	}
	return false, workIdle
}

// WorkInProgress counts the sessions whose agent has work in progress (see
// workLocked). A restart would cut that work off.
func (e *Engine) WorkInProgress() int {
	e.interactiveMu.Lock()
	states := make([]*interactiveState, 0, len(e.interactiveStates))
	for _, state := range e.interactiveStates {
		if state != nil {
			states = append(states, state)
		}
	}
	e.interactiveMu.Unlock()

	busy := 0
	for _, state := range states {
		if state.work() != workIdle {
			busy++
		}
	}
	return busy
}

func (e *Engine) cancelAllAgentSessionIdleCloses() {
	e.interactiveMu.Lock()
	states := make([]*interactiveState, 0, len(e.interactiveStates))
	for _, state := range e.interactiveStates {
		states = append(states, state)
	}
	e.interactiveMu.Unlock()
	for _, state := range states {
		e.cancelAgentSessionIdleClose(state)
	}
}

func (e *Engine) cancelAgentSessionIdleClose(state *interactiveState) {
	if state == nil {
		return
	}
	state.mu.Lock()
	cancel := state.agentSessionIdleCancel
	state.agentSessionIdleCancel = nil
	state.agentSessionIdleToken = 0
	state.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (e *Engine) scheduleAgentSessionIdleClose(sessionKey string, state *interactiveState) {
	if state == nil {
		return
	}
	timeout := time.Duration(e.agentSessionIdleTimeoutNanos.Load())
	if timeout <= 0 {
		return
	}

	e.cancelAgentSessionIdleClose(state)
	ctx, cancel := context.WithCancel(e.ctx)
	token := e.agentSessionIdleSeq.Add(1)
	state.mu.Lock()
	// Whether the session is busy is decided when the timer fires.
	if state.stopped ||
		state.agentSession == nil ||
		!state.agentSession.Alive() ||
		state.eventsNeedResync {
		state.mu.Unlock()
		cancel()
		return
	}
	state.agentSessionIdleCancel = cancel
	state.agentSessionIdleToken = token
	state.mu.Unlock()

	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		e.cleanupInteractiveStateForIdleToken(sessionKey, state, token, timeout)
	}()
}

func (e *Engine) cleanupInteractiveStateForIdleToken(sessionKey string, expected *interactiveState, token uint64, timeout time.Duration) {
	e.interactiveMu.Lock()
	state, ok := e.interactiveStates[sessionKey]
	if !ok || state == nil || state != expected {
		e.interactiveMu.Unlock()
		return
	}

	var agentSession AgentSession
	state.mu.Lock()
	if state.agentSessionIdleToken != token ||
		state.agentSession == nil ||
		!state.agentSession.Alive() ||
		state.stopped ||
		state.eventsNeedResync {
		state.mu.Unlock()
		e.interactiveMu.Unlock()
		return
	}
	// The turn that armed the timer ended, but the session may still be
	// busy (background tasks, a turn the agent started, queued messages);
	// closing the process would kill that work. Look again later rather than
	// relying on the work's last turn to re-arm the timer.
	if why := state.workLocked(); why != workIdle {
		repeat := state.idleCloseKeptFor == why
		state.idleCloseKeptFor = why
		state.mu.Unlock()
		e.interactiveMu.Unlock()
		logKept := slog.Info
		if repeat {
			logKept = slog.Debug
		}
		logKept("agent session idle timeout: session still busy, keeping it",
			"session_key", sessionKey, "timeout", timeout, "work", why)
		e.scheduleAgentSessionIdleClose(sessionKey, state)
		return
	}
	state.idleCloseKeptFor = workIdle
	agentSession = state.agentSession
	state.agentSession = nil
	state.agentSessionIdleCancel = nil
	state.agentSessionIdleToken = 0
	closePlatform := state.platform
	closeReplyCtx := state.replyCtx
	state.mu.Unlock()
	e.interactiveMu.Unlock()

	slog.Info("agent session idle timeout: closing live agent session",
		"session_key", sessionKey, "timeout", timeout)
	e.stopUnsolicitedReader(state)
	state.markStopped()
	e.releaseBackgroundHolds(state, sessionKey, holdAbandoned)

	state.mu.Lock()
	pending := state.pending
	state.pending = nil
	state.mu.Unlock()
	if pending != nil {
		pending.resolve()
	}
	e.notifyDroppedQueuedMessages(state, fmt.Errorf("session reset"))

	e.closeAgentSessionWithTimeout(sessionKey, agentSession, closePlatform, closeReplyCtx)

	e.interactiveMu.Lock()
	if currentState, currentOk := e.interactiveStates[sessionKey]; currentOk && currentState == expected {
		delete(e.interactiveStates, sessionKey)
	}
	e.interactiveMu.Unlock()
}

func (e *Engine) reapIdleWorkspaces() {
	if e.workspacePool == nil {
		return
	}

	reaped := e.workspacePool.ReapIdle(func(dir string, cutoff time.Time) bool {
		inUse, why := e.workspaceInUse(dir, cutoff)
		if why != workIdle {
			slog.Debug("workspace not reaped: a session is busy", "workspace", dir, "work", why)
		}
		return inUse
	})
	if len(reaped) == 0 {
		return
	}

	reapedSet := make(map[string]struct{}, len(reaped))
	for _, ws := range reaped {
		reapedSet[ws] = struct{}{}
	}

	type cleanupTarget struct {
		key   string
		state *interactiveState
	}

	var targets []cleanupTarget
	e.interactiveMu.Lock()
	for key, state := range e.interactiveStates {
		if _, ok := reapedSet[state.workspaceDir]; ok {
			targets = append(targets, cleanupTarget{key: key, state: state})
		}
	}
	e.interactiveMu.Unlock()

	for _, target := range targets {
		e.cleanupInteractiveState(target.key, target.state)
	}
	for _, ws := range reaped {
		slog.Info("workspace idle-reaped", "workspace", ws)
	}
}
