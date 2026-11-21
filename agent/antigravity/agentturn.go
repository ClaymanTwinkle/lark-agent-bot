package antigravity

import (
	"strings"
	"sync"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// agy can start a turn on its own: when a background command or subagent it
// launched finishes, it wakes up and keeps working without a session/prompt
// from the bot. Such a turn has no session/prompt response, so ACP gives no
// signal that it ended. These timings are how the bot decides it has:
//
//   - agy sends usage_update after every model call. When the turn's last
//     call has produced its answer, nothing follows it; when the call asked
//     for a tool, the tool_call arrives at once. So a quiet agentTurnSettle
//     after a usage_update ends the turn.
//   - Otherwise (say, the model is still thinking after a tool finished) the
//     turn ends after agentTurnFallback without any update.
//
// Neither applies while a tool started in the turn is still running or a
// permission request is unanswered.
var (
	agentTurnSettle   = 1500 * time.Millisecond
	agentTurnFallback = 30 * time.Second
	agentTurnTick     = 250 * time.Millisecond
)

// agentTurnTracker follows a turn agy started on its own.
type agentTurnTracker struct {
	mu       sync.Mutex
	open     bool
	deadline time.Time
	tools    map[string]bool // tool calls started in this turn and still running
}

// observe records one session/update received while no prompted turn runs.
// produced says whether it became a user-visible event, which is what opens
// a turn; bookkeeping updates such as usage_update never do.
func (t *agentTurnTracker) observe(kind, toolCallID, status string, produced bool, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.open {
		if !produced {
			return
		}
		t.open = true
		t.tools = make(map[string]bool)
	}
	switch kind {
	case "tool_call":
		if toolCallID != "" && !isTerminalStatus(status) {
			t.tools[toolCallID] = true
		}
	case "tool_call_update":
		if isTerminalStatus(status) {
			delete(t.tools, toolCallID)
		}
	}
	if kind == "usage_update" {
		t.deadline = now.Add(agentTurnSettle)
	} else {
		t.deadline = now.Add(agentTurnFallback)
	}
}

// opened reports whether an agy-initiated turn is in progress.
func (t *agentTurnTracker) opened() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.open
}

// finish ends the turn if it is due, reporting whether it did.
func (t *agentTurnTracker) finish(now time.Time, pendingPermissions int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.open || len(t.tools) > 0 || pendingPermissions > 0 || now.Before(t.deadline) {
		return false
	}
	t.open = false
	return true
}

// reset drops any agy-initiated turn; a prompted turn takes over.
func (t *agentTurnTracker) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.open = false
	t.tools = nil
}

// toolTracker keeps the tool calls agy has started and not yet finished. Those
// still running between turns are background work (a long command, a dev
// server, a subagent), reported to the engine so it keeps the message in
// progress and does not close the agy process as idle.
type toolTracker struct {
	mu      sync.Mutex
	running map[string]toolRun
}

type toolRun struct {
	name, input, status string
}

func (t *toolTracker) observe(kind string, tc acpToolCall, info toolInfo) {
	if kind != "tool_call" && kind != "tool_call_update" || tc.ToolCallID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if isTerminalStatus(tc.Status) {
		delete(t.running, tc.ToolCallID)
		return
	}
	if t.running == nil {
		t.running = make(map[string]toolRun)
	}
	t.running[tc.ToolCallID] = toolRun{name: info.name, input: info.input, status: strings.ToLower(tc.Status)}
}

// turnEnded forgets tool calls still waiting to start: once their turn is
// over they will not run, and must not hold the message in progress.
func (t *toolTracker) turnEnded() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, run := range t.running {
		if run.status == "pending" || run.status == "" {
			delete(t.running, id)
		}
	}
}

func (t *toolTracker) tasks() []core.BackgroundTask {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.running) == 0 {
		return nil
	}
	out := make([]core.BackgroundTask, 0, len(t.running))
	for id, run := range t.running {
		desc := run.input
		if desc == "" {
			desc = run.name
		}
		out = append(out, core.BackgroundTask{ID: id, Type: run.name, Description: desc})
	}
	return out
}
