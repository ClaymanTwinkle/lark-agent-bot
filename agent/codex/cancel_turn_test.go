package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// interruptProbe records the requests an appServerSession writes and
// answers each with an empty result.
type interruptProbe struct {
	s      *appServerSession
	mu     sync.Mutex
	method string
	params map[string]any
}

func (p *interruptProbe) Close() error { return nil }
func (p *interruptProbe) Write(data []byte) (int, error) {
	var req struct {
		ID     any
		Method string
		Params map[string]any
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return 0, err
	}
	p.mu.Lock()
	p.method, p.params = req.Method, req.Params
	p.mu.Unlock()
	p.s.handleResponse(rpcResponseEnvelope{ID: req.ID, Result: json.RawMessage(`{}`)})
	return len(data), nil
}

func TestAppServerCancelTurnInterruptsCurrentTurn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &appServerSession{ctx: ctx, cancel: cancel, events: make(chan core.Event, 8)}
	s.alive.Store(true)
	probe := &interruptProbe{s: s}
	s.stdin = probe

	if err := s.CancelTurn(); err == nil {
		t.Fatal("CancelTurn with no turn succeeded")
	}
	s.threadID.Store("thread-1")
	s.currentTurn = "turn-7"
	if err := s.CancelTurn(); err != nil {
		t.Fatalf("CancelTurn: %v", err)
	}
	probe.mu.Lock()
	method, params := probe.method, probe.params
	probe.mu.Unlock()
	if method != "turn/interrupt" || params["threadId"] != "thread-1" || params["turnId"] != "turn-7" {
		t.Fatalf("sent %s %v, want turn/interrupt for thread-1/turn-7", method, params)
	}

	// Codex then reports the turn as interrupted, which ends it normally.
	s.handleNotification("turn/completed", json.RawMessage(`{"threadId":"thread-1","turn":{"id":"turn-7","status":"interrupted"}}`))
	select {
	case ev := <-s.events:
		if ev.Type != core.EventResult || !ev.Done {
			t.Fatalf("event = %+v, want a done EventResult", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no EventResult after the interrupted turn completed")
	}
}

// The exec backend runs a process per turn; CancelTurn kills it and the turn
// ends as a normal one, keeping the text already received.
func TestExecCancelTurnKillsTurnProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script as the fake codex")
	}
	workDir := t.TempDir()
	script := "#!/bin/sh\n" +
		"printf '%s\\n' '{\"type\":\"thread.started\",\"thread_id\":\"thread-cancel\"}'\n" +
		"printf '%s\\n' '{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"partial answer\"}}'\n" +
		"echo 'killed noise' >&2\n" +
		"(sleep 30) &\n" +
		"wait\n"
	bin := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cs, err := newCodexSession(context.Background(), bin, nil, workDir, "", "", "read-only", "", "", nil, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	if err := cs.CancelTurn(); err == nil {
		t.Fatal("CancelTurn with no turn succeeded")
	}
	if err := cs.Send("hello", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	waitForThreadID(t, cs, "thread-cancel")
	time.Sleep(100 * time.Millisecond) // let the partial message arrive
	if err := cs.CancelTurn(); err != nil {
		t.Fatalf("CancelTurn: %v", err)
	}

	var text strings.Builder
	deadline := time.After(3 * time.Second)
	for done := false; !done; {
		select {
		case ev := <-cs.Events():
			switch ev.Type {
			case core.EventText:
				text.WriteString(ev.Content)
			case core.EventError:
				t.Fatalf("cancel reported as an error: %v", ev.Error)
			case core.EventResult:
				done = true
			}
		case <-deadline:
			t.Fatal("no EventResult after CancelTurn")
		}
	}
	if !strings.Contains(text.String(), "partial answer") {
		t.Errorf("text = %q, want the partial answer kept", text.String())
	}
	if !cs.Alive() {
		t.Fatal("session died")
	}
	deadline = time.After(2 * time.Second)
	for cs.turnInFlight.Load() {
		select {
		case <-deadline:
			t.Fatal("turn still in flight after cancel")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
