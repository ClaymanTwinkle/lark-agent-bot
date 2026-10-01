package core

import (
	"fmt"
	"strings"
	"time"
)

const (
	// DefaultStallNoticeModel is how long a turn may go without agent events
	// while the agent waits on the model before the user is told. Normal model
	// round trips stay well under it; network trouble or a hung agent do not.
	DefaultStallNoticeModel = 5 * time.Minute
	// DefaultStallNoticeTool is the same limit while a tool runs. Tools are
	// legitimately silent for longer: Claude Code's Bash tool alone may run
	// for up to 10 minutes.
	DefaultStallNoticeTool = 15 * time.Minute

	stallToolInputMaxRunes = 60
)

// turnStallWatch tracks what a turn is waiting on — the model or a running
// tool — and fires once the agent has been silent longer than that phase
// normally takes. It is owned by the turn's event loop goroutine.
type turnStallWatch struct {
	modelAfter time.Duration
	toolAfter  time.Duration
	timer      *time.Timer
	armed      bool

	openTools int
	toolName  string
	toolInput string
	lastEvent time.Time
	notified  bool
}

func newTurnStallWatch(modelAfter, toolAfter time.Duration) *turnStallWatch {
	w := &turnStallWatch{modelAfter: modelAfter, toolAfter: toolAfter}
	w.reset()
	return w
}

// C fires when the current phase has been silent too long. It is nil while
// the watch is disarmed, which disables the select case.
func (w *turnStallWatch) C() <-chan time.Time {
	if !w.armed {
		return nil
	}
	return w.timer.C
}

// reset starts a new turn: back to waiting on the model.
func (w *turnStallWatch) reset() {
	w.openTools = 0
	w.notified = false
	w.lastEvent = time.Now()
	w.arm()
}

// observe records an agent event and restarts the silence clock. A retry of a
// failed model request is not progress, so it leaves the clock running.
func (w *turnStallWatch) observe(ev Event) {
	if ev.Type == EventRetry {
		return
	}
	now := time.Now()
	w.lastEvent = now
	w.notified = false
	switch ev.Type {
	case EventToolUse:
		w.openTools++
		w.toolName = ev.ToolName
		w.toolInput = ev.ToolInput
	case EventToolResult:
		if w.openTools > 0 {
			w.openTools--
		}
	case EventText, EventThinking:
		// The model only speaks again once every tool result is in, so this
		// also recovers from an adapter that never reports a tool result.
		// Content-less events (Claude Code system messages carry only a
		// session ID) can arrive while a tool runs and say nothing about the
		// phase.
		if ev.Content != "" {
			w.openTools = 0
		}
	case EventResult:
		w.openTools = 0
	}
	w.arm()
}

// pause stops the clock while the turn waits on the user (permission prompt
// or question); resume restarts it once the user has answered.
func (w *turnStallWatch) pause() {
	w.disarm()
}

func (w *turnStallWatch) resume() {
	w.lastEvent = time.Now()
	w.arm()
}

// fired disarms the watch after C fired; the next event re-arms it.
func (w *turnStallWatch) fired() {
	w.armed = false
}

func (w *turnStallWatch) stop() {
	w.disarm()
}

func (w *turnStallWatch) arm() {
	w.disarm()
	d := w.modelAfter
	if w.openTools > 0 {
		d = w.toolAfter
	}
	if d <= 0 {
		return
	}
	if w.timer == nil {
		w.timer = time.NewTimer(d)
	} else {
		w.timer.Reset(d)
	}
	w.armed = true
}

func (w *turnStallWatch) disarm() {
	if w.timer != nil && !w.timer.Stop() {
		select {
		case <-w.timer.C:
		default:
		}
	}
	w.armed = false
}

// notice describes the stall for the user.
func (w *turnStallWatch) notice(i18n *I18n, now time.Time) string {
	if w.openTools > 0 {
		tool := w.toolName
		if input := truncateIf(strings.Join(strings.Fields(w.toolInput), " "), stallToolInputMaxRunes); input != "" {
			tool = fmt.Sprintf("%s: %s", tool, input)
		}
		return i18n.Tf(MsgStallTool, tool, int(now.Sub(w.lastEvent).Minutes()))
	}
	return i18n.Tf(MsgStallModel, int(now.Sub(w.lastEvent).Minutes()))
}

// DefaultRetryNoticeAttempts is the retry attempt at which the user is told
// that the agent keeps failing to reach the model.
const DefaultRetryNoticeAttempts = 3

// turnRetryWatch decides when the agent's retries of a failed model request
// are worth telling the user about: once per run of consecutive retries, when
// they reach minAttempt, when the next attempt is at least a minute away, or
// when the request got no response at all. It is owned by the turn's event
// loop goroutine.
type turnRetryWatch struct {
	minAttempt int // 0 disables the notice
	notified   bool
}

// observe records an agent event and reports whether it is a retry the user
// should now hear about. Any real output ends the run of retries.
func (w *turnRetryWatch) observe(ev Event) bool {
	switch ev.Type {
	case EventRetry:
		r := ev.Retry
		if w.minAttempt <= 0 || w.notified || r == nil {
			return false
		}
		if r.Attempt >= w.minAttempt || r.Delay >= time.Minute || r.NoResponse {
			w.notified = true
			return true
		}
	case EventText, EventThinking:
		if ev.Content != "" {
			w.notified = false
		}
	case EventToolUse, EventToolResult, EventResult:
		w.notified = false
	}
	return false
}

func (w *turnRetryWatch) reset() {
	w.notified = false
}

// retryNotice describes a retry of a failed model request for the user.
func retryNotice(i18n *I18n, r *RetryInfo) string {
	attempt := fmt.Sprintf("%d", r.Attempt)
	if r.MaxAttempts > 0 {
		attempt = fmt.Sprintf("%d/%d", r.Attempt, r.MaxAttempts)
	}
	var reason MsgKey
	switch {
	case r.NoResponse:
		reason = MsgRetryReasonNoResponse
	case r.Reason == RetryReasonRateLimit:
		reason = MsgRetryReasonRateLimit
	case r.Reason == RetryReasonOverloaded:
		reason = MsgRetryReasonOverloaded
	case r.Reason == RetryReasonAuth:
		reason = MsgRetryReasonAuth
	case r.Reason == RetryReasonServer:
		reason = MsgRetryReasonServer
	default:
		reason = MsgRetryReasonNetwork
	}
	why := i18n.T(reason)
	if r.Status > 0 {
		why += fmt.Sprintf(" (HTTP %d)", r.Status)
	}
	next := ""
	if r.Delay >= time.Minute {
		next = i18n.Tf(MsgRetryNextIn, formatDurationI18n(r.Delay, i18n.CurrentLang()))
	}
	return i18n.Tf(MsgRetryNotice, attempt, why, next)
}
