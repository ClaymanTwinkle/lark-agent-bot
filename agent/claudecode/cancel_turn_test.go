package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// CancelTurn sends the interrupt control request; the fake answers it the way
// Claude Code CLI 2.1.326 was observed to: a success control_response, the
// "[Request interrupted by user]" user message, then a result with subtype
// error_during_execution and no text. That result must end the turn.
func TestCancelTurnInterruptsAndEndsTurn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pr, pw := io.Pipe()
	cs := &claudeSession{stdin: pw, done: make(chan struct{}), events: make(chan core.Event, 8), ctx: ctx}
	cs.alive.Store(true)
	t.Cleanup(func() { _ = pw.Close() })

	subtypes := make(chan string, 4)
	go func() {
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			var msg map[string]any
			if json.Unmarshal(sc.Bytes(), &msg) != nil || msg["type"] != "control_request" {
				continue
			}
			id, _ := msg["request_id"].(string)
			req, _ := msg["request"].(map[string]any)
			subtype, _ := req["subtype"].(string)
			subtypes <- subtype
			for _, line := range []map[string]any{
				{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": id, "response": map[string]any{"still_queued": []any{}}}},
				{"type": "user", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "[Request interrupted by user]"}}}},
				{"type": "result", "subtype": "error_during_execution", "is_error": true, "result": nil, "session_id": "s1"},
			} {
				raw, _ := json.Marshal(line)
				cs.handleReadLoopLine(string(raw))
			}
		}
	}()

	if err := cs.CancelTurn(); err != nil {
		t.Fatalf("CancelTurn: %v", err)
	}
	if got := <-subtypes; got != "interrupt" {
		t.Fatalf("control request subtype = %q, want interrupt", got)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-cs.events:
			if ev.Type == core.EventError {
				t.Fatalf("interrupt reported as an error: %v", ev.Error)
			}
			if ev.Type == core.EventResult {
				if !ev.Done || ev.Content != "" {
					t.Fatalf("result = %+v, want a done result without text", ev)
				}
				return
			}
		case <-deadline:
			t.Fatal("no EventResult after the interrupt")
		}
	}
}

func TestCancelTurnRefusedWhenProcessGone(t *testing.T) {
	cs := &claudeSession{done: make(chan struct{})}
	if err := cs.CancelTurn(); err == nil {
		t.Fatal("CancelTurn succeeded with no process")
	}
}
