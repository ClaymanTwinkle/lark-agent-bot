package claudecode

import (
	"context"
	"reflect"
	"testing"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

func TestHandleSystem_BackgroundTasksChangedTracksRunningTasks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cs := &claudeSession{events: make(chan core.Event, 8), ctx: ctx}
	cs.recoverUsageOnce.Do(func() {}) // no transcript to recover in this test

	// Captured from Claude Code 2.1.286: a background shell and a background
	// subagent running at once.
	cs.handleSystem(apiRetryLine(t, `{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"bi72jvolr","task_type":"local_bash","description":"Background sleep and echo"},{"task_id":"aa572e2107103e1b1","task_type":"local_agent","description":"Run sleep 20 then report pong"}],"uuid":"u1","session_id":"sid"}`))

	want := []core.BackgroundTask{
		{ID: "bi72jvolr", Type: "local_bash", Description: "Background sleep and echo"},
		{ID: "aa572e2107103e1b1", Type: "local_agent", Description: "Run sleep 20 then report pong"},
	}
	if got := cs.BackgroundTasks(); !reflect.DeepEqual(got, want) {
		t.Fatalf("BackgroundTasks() = %+v, want %+v", got, want)
	}
	// The content-less event still goes out: it wakes the engine to look
	// at the new list.
	if ev := <-cs.events; ev.Type != core.EventText || ev.Content != "" || ev.SessionID != "sid" {
		t.Fatalf("event = %+v, want the content-less session event", ev)
	}

	cs.handleSystem(apiRetryLine(t, `{"type":"system","subtype":"background_tasks_changed","tasks":[],"uuid":"u2","session_id":"sid"}`))
	if got := cs.BackgroundTasks(); got != nil {
		t.Fatalf("BackgroundTasks() after the last task finished = %+v, want none", got)
	}
}

func TestHandleSystem_OtherSystemMessagesKeepBackgroundTasks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cs := &claudeSession{events: make(chan core.Event, 8), ctx: ctx}
	cs.recoverUsageOnce.Do(func() {})

	cs.handleSystem(apiRetryLine(t, `{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"t1","task_type":"local_agent","description":"survey"}],"session_id":"sid"}`))
	// task_started for a shell a subagent runs itself; not a main-session task.
	cs.handleSystem(apiRetryLine(t, `{"type":"system","subtype":"task_started","task_id":"bsacvjii5","owned_by_subagent":true,"is_backgrounded":false,"task_type":"local_bash","session_id":"sid"}`))

	if got := cs.BackgroundTasks(); len(got) != 1 || got[0].ID != "t1" {
		t.Fatalf("BackgroundTasks() = %+v, want only t1", got)
	}
}

func TestFinishReadLoop_ClearsBackgroundTasks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cs := &claudeSession{events: make(chan core.Event, 8), ctx: ctx, done: make(chan struct{})}
	cs.setBackgroundTasks([]core.BackgroundTask{{ID: "t1"}})

	waitErr := make(chan error, 1)
	waitErr <- nil
	cs.finishReadLoop(waitErr, nil)

	if got := cs.BackgroundTasks(); got != nil {
		t.Fatalf("BackgroundTasks() after the process exited = %+v, want none", got)
	}
}
