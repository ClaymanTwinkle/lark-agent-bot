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

// observe records an agent event and restarts the silence clock.
func (w *turnStallWatch) observe(ev Event) {
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
