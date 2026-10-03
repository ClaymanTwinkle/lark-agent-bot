package core

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// warnLog collects slog warnings. Goroutines left over from other tests may
// log while a test reads it, so it locks around both.
type warnLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *warnLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// count returns how many records with the given message were logged for the
// session key.
func (l *warnLog) count(msg, sessionKey string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range strings.Split(l.buf.String(), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if rec["msg"] == msg && rec["session"] == sessionKey {
			n++
		}
	}
	return n
}

func captureWarnings(t *testing.T) *warnLog {
	t.Helper()
	l := &warnLog{}
	t.Cleanup(setDefaultSlog(slog.New(slog.NewJSONHandler(l, &slog.HandlerOptions{Level: slog.LevelWarn}))))
	return l
}

// Regression for #8: the warning used to time the whole turn, so every turn
// longer than slowAgentSend warned although Send had returned at once.
func TestProcessInteractiveMessage_LongTurnWithFastSendDoesNotWarn(t *testing.T) {
	logs := captureWarnings(t)

	p := &stubPlatformEngine{n: "test"}
	sess := newControllableSession("long-turn")
	e := NewEngine("test", &controllableAgent{nextSession: sess}, []Platform{p}, "", LangEnglish)

	sessionKey := "test:long-turn-user"
	session := e.sessions.GetOrCreateActive(sessionKey)
	lockGen, locked := session.TryLock()
	if !locked {
		t.Fatal("expected session lock")
	}

	done := make(chan struct{})
	go func() {
		e.processInteractiveMessageWith(p, &Message{
			SessionKey: sessionKey,
			UserID:     "user1",
			Content:    "long running task",
			ReplyCtx:   "ctx",
		}, session, e.agent, e.sessions, sessionKey, "", sessionKey, lockGen)
		close(done)
	}()

	time.Sleep(slowAgentSend + 200*time.Millisecond)
	sess.events <- Event{Type: EventResult, Content: "done", Done: true}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("processInteractiveMessageWith did not complete")
	}
	if n := logs.count("slow agent send", sessionKey); n != 0 {
		t.Fatalf("logged %d slow agent send warnings for a fast Send in a long turn, want 0", n)
	}
}

func TestSendToAgent_WarnsWhenSendItselfIsSlow(t *testing.T) {
	logs := captureWarnings(t)

	sess := newBlockingSendSession("slow-send")
	time.AfterFunc(slowAgentSend+100*time.Millisecond, func() { close(sess.unblock) })

	sessionKey := "test:slow-send-user"
	if err := sendToAgent(sess, sessionKey, "prompt", "m1", nil, nil); err != nil {
		t.Fatalf("sendToAgent: %v", err)
	}
	if n := logs.count("slow agent send", sessionKey); n != 1 {
		t.Fatalf("logged %d slow agent send warnings, want 1", n)
	}
}
