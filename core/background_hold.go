package core

import "log/slog"

// backgroundHold keeps a user message marked in progress after its turn has
// ended, while background tasks that turn launched are still running. The
// agent reports the turn's result as soon as the model stops, so without it
// the message would look done while the agent still works on it.
//
// A message waits only for the tasks launched during its own turn (and for
// tasks its follow-up turns launch), so a background shell that never ends,
// such as a dev server, keeps one message in progress instead of every later
// one.
type backgroundHold struct {
	// messageID is the journaled turn this hold extends; "" when the hold
	// covers a follow-up turn the agent started on its own.
	messageID  string
	platform   Platform
	replyCtx   any
	stopTyping func()
	waitFor    map[string]struct{} // IDs of the background tasks still awaited
}

// holdEnd says why a background hold ends.
type holdEnd int

const (
	// holdDone: the work the message waited for has finished.
	holdDone holdEnd = iota
	// holdDoneSilent: finished, but the turn that finished it had nothing
	// to say, so the message gets no done reaction.
	holdDoneSilent
	// holdAbandoned: the session was stopped, reset or lost its agent.
	holdAbandoned
)

// pendingBackgroundTasks returns the IDs of the background tasks the agent
// session reports as still running, nil when there are none or the agent
// cannot report them.
func pendingBackgroundTasks(as AgentSession) map[string]struct{} {
	r, ok := as.(BackgroundTaskReporter)
	if !ok || !as.Alive() {
		return nil
	}
	tasks := r.BackgroundTasks()
	if len(tasks) == 0 {
		return nil
	}
	ids := make(map[string]struct{}, len(tasks))
	for _, t := range tasks {
		ids[t.ID] = struct{}{}
	}
	return ids
}

// newBackgroundTasks returns the tasks in pending that are not in baseline.
func newBackgroundTasks(pending, baseline map[string]struct{}) map[string]struct{} {
	var added map[string]struct{}
	for id := range pending {
		if _, ok := baseline[id]; ok {
			continue
		}
		if added == nil {
			added = make(map[string]struct{})
		}
		added[id] = struct{}{}
	}
	return added
}

func (s *interactiveState) pendingBackgroundTasks() map[string]struct{} {
	s.mu.Lock()
	as := s.agentSession
	s.mu.Unlock()
	return pendingBackgroundTasks(as)
}

// hasBackgroundWorkLocked reports whether the agent still works outside a
// foreground turn: background tasks are running, or a message is still held
// for them, which includes a follow-up turn the agent started on its own.
// Such a session is not idle. s.mu must be held.
func (s *interactiveState) hasBackgroundWorkLocked() bool {
	return len(s.backgroundHolds) > 0 || len(pendingBackgroundTasks(s.agentSession)) > 0
}

func (s *interactiveState) hasBackgroundWork() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hasBackgroundWorkLocked()
}

// holdForBackground keeps the message of a turn that just ended marked in
// progress while the background tasks it launched (waitFor) run. The hold
// takes over the turn's typing indicator.
func (e *Engine) holdForBackground(state *interactiveState, sessionKey string, h *backgroundHold) {
	state.mu.Lock()
	state.backgroundHolds = append(state.backgroundHolds, h)
	state.mu.Unlock()
	slog.Info("turn ended with background tasks running; message stays in progress",
		"session_key", sessionKey, "msg_id", h.messageID, "tasks", len(h.waitFor))
}

// holdForFollowUpTurn marks the latest message in progress while the agent
// runs a turn it started on its own (a background task finished), unless a
// message is already held for that work.
func (e *Engine) holdForFollowUpTurn(state *interactiveState) {
	state.mu.Lock()
	if len(state.backgroundHolds) > 0 {
		state.mu.Unlock()
		return
	}
	h := &backgroundHold{platform: state.platform, replyCtx: state.replyCtx}
	state.backgroundHolds = append(state.backgroundHolds, h)
	state.mu.Unlock()
	ti, ok := h.platform.(TypingIndicator)
	if !ok {
		return
	}
	// Starting the indicator is a platform API call; the caller is the
	// event reader, whose iterations must stay short.
	go func() {
		stop := ti.StartTyping(e.ctx, h.replyCtx)
		state.mu.Lock()
		if containsHold(state.backgroundHolds, h) {
			h.stopTyping = stop
			stop = nil
		}
		state.mu.Unlock()
		if stop != nil { // released while the indicator was starting
			stop()
		}
	}()
}

// adoptBackgroundTasks adds tasks launched during a follow-up turn to the
// newest held message: the follow-up continues that message's work.
func (e *Engine) adoptBackgroundTasks(state *interactiveState, tasks map[string]struct{}) {
	if len(tasks) == 0 {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.backgroundHolds) == 0 {
		return
	}
	h := state.backgroundHolds[len(state.backgroundHolds)-1]
	if h.waitFor == nil {
		h.waitFor = make(map[string]struct{}, len(tasks))
	}
	for id := range tasks {
		h.waitFor[id] = struct{}{}
	}
}

// settleBackgroundHolds runs at the end of every turn, foreground or not:
// the agent folds a background task's completion into whatever turn is
// running, so any turn can be the one that finishes a held message's work.
// Holds whose tasks are all gone from pending end as how says.
func (e *Engine) settleBackgroundHolds(state *interactiveState, sessionKey string, pending map[string]struct{}, how holdEnd) {
	state.mu.Lock()
	var finished []*backgroundHold
	kept := state.backgroundHolds[:0]
	for _, h := range state.backgroundHolds {
		for id := range h.waitFor {
			if _, ok := pending[id]; !ok {
				delete(h.waitFor, id)
			}
		}
		if len(h.waitFor) == 0 {
			finished = append(finished, h)
			continue
		}
		kept = append(kept, h)
	}
	clear(state.backgroundHolds[len(kept):])
	state.backgroundHolds = kept
	state.mu.Unlock()
	for _, h := range finished {
		e.endBackgroundHold(sessionKey, h, how)
	}
}

// releaseBackgroundHolds ends every hold of the session, as how says.
func (e *Engine) releaseBackgroundHolds(state *interactiveState, sessionKey string, how holdEnd) {
	state.mu.Lock()
	holds := state.backgroundHolds
	state.backgroundHolds = nil
	state.mu.Unlock()
	for _, h := range holds {
		e.endBackgroundHold(sessionKey, h, how)
	}
}

// abandonBackgroundWork handles an agent process that exited while a message
// was still held for its background work: the user is told the work was cut
// off, and the holds end.
func (e *Engine) abandonBackgroundWork(state *interactiveState, sessionKey string) {
	state.mu.Lock()
	holds := state.backgroundHolds
	state.backgroundHolds = nil
	state.mu.Unlock()
	if len(holds) == 0 {
		return
	}
	slog.Warn("agent process exited with background work outstanding", "session_key", sessionKey, "held_messages", len(holds))
	if last := holds[len(holds)-1]; last.platform != nil {
		e.send(last.platform, last.replyCtx, e.i18n.T(MsgAgentExitedMidTurn))
	}
	for _, h := range holds {
		e.endBackgroundHold(sessionKey, h, holdAbandoned)
	}
}

func (e *Engine) endBackgroundHold(sessionKey string, h *backgroundHold, how holdEnd) {
	if h.stopTyping != nil {
		h.stopTyping()
	}
	if how == holdDone {
		if done, ok := h.platform.(TypingIndicatorDone); ok {
			done.AddDoneReaction(h.replyCtx)
		}
	}
	// On shutdown the turn stays journaled so the next start reports it.
	if e.ctx.Err() == nil {
		e.turnJournal.endMessage(sessionKey, h.messageID)
	}
	slog.Info("background work over; message no longer in progress",
		"session_key", sessionKey, "msg_id", h.messageID, "abandoned", how == holdAbandoned)
}

func containsHold(holds []*backgroundHold, h *backgroundHold) bool {
	for _, x := range holds {
		if x == h {
			return true
		}
	}
	return false
}

// isAgentActivity reports whether an event shows the agent working on a turn.
// Agents also emit content-less events between turns — Claude Code turns each
// system message (background task progress, hook runs) into one — that say
// nothing about a turn.
func isAgentActivity(ev Event) bool {
	switch ev.Type {
	case EventText, EventThinking:
		return ev.Content != ""
	case EventToolUse, EventToolResult, EventPermissionRequest, EventRetry, EventResult:
		return true
	}
	return false
}
