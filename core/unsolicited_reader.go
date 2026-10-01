package core

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// unsolicitedReaderStopTimeout bounds how long stopUnsolicitedReader waits
// for the reader goroutine to exit. The reader is structured so its iterations
// are short (blocking adapter calls like RespondPermission are offloaded), so
// this timeout should almost always be non-binding. If it does fire, callers
// force a resync of the Events channel to preserve single-reader correctness.
const unsolicitedReaderStopTimeout = 5 * time.Second

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
// completions in Claude Code). Events are relayed to the platform immediately.
// The goroutine exits when its context is cancelled (by a new foreground turn
// or session cleanup) or when the Events channel is closed.
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

// runUnsolicitedReader is the goroutine body for the unsolicited event reader.
// agentSession is captured by the caller so we don't race with
// cleanupInteractiveState nilling state.agentSession.
func (e *Engine) runUnsolicitedReader(ctx context.Context, cancel context.CancelFunc, done chan struct{}, state *interactiveState, agentSession AgentSession, session *Session, sessions *SessionManager, sessionKey string) {
	defer close(done)
	defer cancel()

	events := agentSession.Events()

	// turnActive is true from the first sign of the agent working on a turn
	// of its own until the turn's EventResult.
	var turnActive bool
	// turnBaseline is the background tasks running when that turn started;
	// the ones it launches continue the held message's work.
	var turnBaseline map[string]struct{}
	defer func() {
		if turnActive {
			state.setAgentTurn(false)
		}
	}()

	var textParts []string
	var toolsUsed []string

	for {
		select {
		case <-ctx.Done():
			// Context cancelled (new foreground turn or cleanup). Don't set
			// eventsNeedResync — the caller (stopUnsolicitedReader) knows the
			// channel state is clean because it just took ownership.
			return

		case event, ok := <-events:
			if !ok {
				// Channel closed — agent process exited. Log any buffered
				// tool/text context so it isn't lost silently.
				if len(toolsUsed) > 0 || len(textParts) > 0 {
					slog.Warn("unsolicited reader: agent channel closed mid-turn",
						"session", sessionKey,
						"tools_used", toolsUsed,
						"text_fragments", len(textParts))
				}
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

			// Go's select is non-deterministic when multiple cases are
			// ready, so even after ctx is cancelled we may still read one
			// last event from the channel. If ownership has been handed
			// off, drop the event rather than processing it — otherwise we
			// could relay (or worse, respond to) an event that belongs to
			// the incoming foreground turn. The caller has already set
			// eventsNeedResync on timeout, so any buffered events will be
			// drained before the foreground turn reads them.
			select {
			case <-ctx.Done():
				slog.Warn("unsolicited reader: event received after cancellation, dropping",
					"session", sessionKey, "event_type", event.Type)
				state.mu.Lock()
				state.eventsNeedResync = true
				state.mu.Unlock()
				return
			default:
			}

			// The agent started a turn on its own, typically because a
			// background task finished. It is not idle until the turn ends,
			// and the user's message shows it is being worked on.
			if !turnActive && isAgentActivity(event) {
				turnActive = true
				state.setAgentTurn(true)
				e.cancelAgentSessionIdleClose(state)
				turnBaseline = state.pendingBackgroundTasks()
				e.holdForFollowUpTurn(state)
				slog.Info("unsolicited events detected, relaying to platform",
					"session", sessionKey)
			}

			state.mu.Lock()
			p := state.platform
			replyCtx := state.replyCtx
			state.mu.Unlock()

			switch event.Type {
			case EventText:
				if event.Content != "" {
					textParts = append(textParts, event.Content)
				}

			case EventToolUse:
				// Record tool name so we can log or surface context if the
				// channel closes before a clean EventResult. Output is
				// delivered via EventResult; we intentionally do not relay
				// per-tool progress here (no active user turn to observe it).
				if event.ToolName != "" {
					toolsUsed = append(toolsUsed, event.ToolName)
				}
				slog.Debug("unsolicited tool use",
					"session", sessionKey,
					"tool", event.ToolName)

			case EventToolResult:
				slog.Debug("unsolicited tool result",
					"session", sessionKey,
					"status", event.ToolStatus)

			case EventResult:
				// A mid-turn compaction (Done=false) does not end the turn.
				if !event.Done {
					continue
				}
				fullResponse := event.Content
				if fullResponse == "" && len(textParts) > 0 {
					fullResponse = strings.Join(textParts, "")
				}

				// Respect NO_REPLY like a foreground turn: deliver nothing
				// for a bare marker, strip a trailing one.
				visible := fullResponse
				if isSilentReply(visible) {
					visible = ""
				} else if stripped, ok := stripTrailingSilent(visible); ok {
					visible = stripped
				}
				if strings.TrimSpace(visible) != "" {
					for _, chunk := range SplitMessageCodeFenceAware(visible, maxPlatformMessageLen) {
						e.send(p, replyCtx, chunk)
					}
				}

				// Safety note: concurrent writes to session.History by the
				// unsolicited reader and a foreground turn cannot overlap.
				// Session.AddHistory takes session.mu internally, and
				// stopUnsolicitedReader (called before any foreground turn
				// takes event-channel ownership) blocks until this goroutine
				// exits — so a foreground AddHistory is always ordered after
				// any unsolicited AddHistory.
				session.AddHistory("assistant", fullResponse)
				sessions.Save()

				// Tasks this turn launched continue the held message's work;
				// held messages whose tasks are all done are done now.
				pending := state.pendingBackgroundTasks()
				e.adoptBackgroundTasks(state, newBackgroundTasks(pending, turnBaseline))
				end := holdDone
				if strings.TrimSpace(visible) == "" {
					end = holdDoneSilent
				}
				e.settleBackgroundHolds(state, sessionKey, pending, end)

				// Reset for potential subsequent unsolicited turn.
				textParts = nil
				toolsUsed = nil
				turnActive = false
				turnBaseline = nil
				state.setAgentTurn(false)

				// Mark clean exit so next foreground turn preserves events.
				state.mu.Lock()
				state.eventsNeedResync = false
				state.mu.Unlock()

				// Reset the per-session idle close timer so a series of
				// background task completions does not get cut short by the
				// idle timeout armed at the end of the last foreground turn.
				// Without this, a long-running background turn (e.g. cron task
				// that reports progress every minute) can be killed mid-flight
				// when the original idle timer fires. See #1686 P1-C P1-1.
				e.scheduleAgentSessionIdleClose(sessionKey, state)

				slog.Info("unsolicited turn complete",
					"session", sessionKey,
					"response_len", len(fullResponse))

			case EventPermissionRequest:
				// If approveAll (/yolo) is set, grant the request. Otherwise
				// deny — there is no active user turn to consult — and notify
				// the user on the platform so a silently blocked background
				// task is not invisible. RespondPermission may make a slow
				// adapter call, so we run it in a detached goroutine to keep
				// reader iterations fast (stopUnsolicitedReader relies on a
				// bounded wait for the reader to exit).
				state.mu.Lock()
				autoApprove := state.approveAll
				state.mu.Unlock()

				result := PermissionResult{Behavior: "deny", Message: "denied: no active user turn"}
				if autoApprove {
					result = PermissionResult{Behavior: "allow", UpdatedInput: event.ToolInputRaw}
				}
				reqID := event.RequestID
				respondCtx := ctx // capture current unsolicited reader context
				go func() {
					// Run in a goroutine to keep reader iterations fast, but honour
					// the reader's context so we don't call into a dead session after
					// stopUnsolicitedReader cancels the context.
					select {
					case <-respondCtx.Done():
						return
					default:
					}
					if err := agentSession.RespondPermission(reqID, result); err != nil {
						if respondCtx.Err() == nil {
							slog.Error("unsolicited: failed to respond permission", "error", err)
						}
					}
				}()
				if !autoApprove {
					toolName := event.ToolName
					if toolName == "" {
						toolName = "(unknown)"
					}
					e.send(p, replyCtx, fmt.Sprintf(e.i18n.T(MsgBackgroundAutoDenied), toolName))
				}

			case EventError:
				if event.Error != nil {
					slog.Error("unsolicited agent error", "error", event.Error, "session", sessionKey)
					e.send(p, replyCtx, fmt.Sprintf(e.i18n.T(MsgError), event.Error))
				}
				state.mu.Lock()
				state.eventsNeedResync = true
				state.mu.Unlock()
				e.releaseBackgroundHolds(state, sessionKey, holdAbandoned)
				return
			}
		}
	}
}
