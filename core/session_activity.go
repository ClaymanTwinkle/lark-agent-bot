package core

import (
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
	if !active {
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
	dir = normalizeWorkspacePath(dir)
	e.interactiveMu.Lock()
	var states []*interactiveState
	for _, state := range e.interactiveStates {
		state.mu.Lock()
		wsDir := state.workspaceDir
		state.mu.Unlock()
		if wsDir != "" && sameWorkspacePath(normalizeWorkspacePath(wsDir), dir) {
			states = append(states, state)
		}
	}
	e.interactiveMu.Unlock()
	for _, state := range states {
		if inUse, why := state.inUseSince(cutoff); inUse {
			return true, why
		}
	}
	return false, workIdle
}
