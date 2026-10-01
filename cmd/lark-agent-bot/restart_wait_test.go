package main

import (
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

const testPoll = 5 * time.Millisecond

// awaitResult runs awaitIdleForRestart in the background.
func awaitResult(req core.RestartRequest, busy func() int, restartCh chan core.RestartRequest, sigCh chan os.Signal) <-chan *core.RestartRequest {
	done := make(chan *core.RestartRequest, 1)
	go func() { done <- awaitIdleForRestart(req, busy, restartCh, sigCh, testPoll) }()
	return done
}

func waitResult(t *testing.T, done <-chan *core.RestartRequest) *core.RestartRequest {
	t.Helper()
	select {
	case got := <-done:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("awaitIdleForRestart did not return")
		return nil
	}
}

func assertWaiting(t *testing.T, done <-chan *core.RestartRequest) {
	t.Helper()
	select {
	case got := <-done:
		t.Fatalf("returned %+v while tasks are still in progress", got)
	case <-time.After(10 * testPoll):
	}
}

func TestAwaitIdleForRestart_IdleRestartsAtOnce(t *testing.T) {
	req := core.RestartRequest{SessionKey: "s1", WaitIdle: true, MaxWait: time.Hour}
	got := awaitIdleForRestart(req, func() int { return 0 }, nil, nil, time.Hour)
	if got == nil || got.SessionKey != "s1" {
		t.Fatalf("got %+v, want the request back", got)
	}
}

func TestAwaitIdleForRestart_WaitsUntilTasksFinish(t *testing.T) {
	var busy atomic.Int32
	busy.Store(2)
	done := awaitResult(core.RestartRequest{SessionKey: "s1", WaitIdle: true, MaxWait: time.Hour},
		func() int { return int(busy.Load()) }, make(chan core.RestartRequest), make(chan os.Signal))

	assertWaiting(t, done)
	busy.Store(1)
	assertWaiting(t, done)
	busy.Store(0)
	if got := waitResult(t, done); got == nil || got.SessionKey != "s1" {
		t.Fatalf("got %+v, want the request back once idle", got)
	}
}

func TestAwaitIdleForRestart_RestartsAfterMaxWait(t *testing.T) {
	done := awaitResult(core.RestartRequest{SessionKey: "s1", WaitIdle: true, MaxWait: 50 * time.Millisecond},
		func() int { return 1 }, make(chan core.RestartRequest), make(chan os.Signal))

	if got := waitResult(t, done); got == nil || got.SessionKey != "s1" {
		t.Fatalf("got %+v, want the request back after the max wait", got)
	}
}

func TestAwaitIdleForRestart_ImmediateRequestRestartsNow(t *testing.T) {
	restartCh := make(chan core.RestartRequest)
	done := awaitResult(core.RestartRequest{SessionKey: "s1", WaitIdle: true, MaxWait: time.Hour},
		func() int { return 1 }, restartCh, make(chan os.Signal))

	restartCh <- core.RestartRequest{SessionKey: "s2", Platform: "feishu"}
	if got := waitResult(t, done); got == nil || got.SessionKey != "s2" || got.WaitIdle {
		t.Fatalf("got %+v, want the /restart request", got)
	}
}

func TestAwaitIdleForRestart_LaterWaitingRequestKeepsWaiting(t *testing.T) {
	var busy atomic.Int32
	busy.Store(1)
	restartCh := make(chan core.RestartRequest)
	done := awaitResult(core.RestartRequest{SessionKey: "s1", Platform: "feishu", WaitIdle: true, MaxWait: time.Hour},
		func() int { return int(busy.Load()) }, restartCh, make(chan os.Signal))

	restartCh <- core.RestartRequest{SessionKey: "s2", Platform: "feishu", WaitIdle: true, MaxWait: time.Hour}
	assertWaiting(t, done)
	busy.Store(0)
	if got := waitResult(t, done); got == nil || got.SessionKey != "s2" {
		t.Fatalf("got %+v, want the latest request to receive the notice", got)
	}
}

func TestAwaitIdleForRestart_SignalCancelsRestart(t *testing.T) {
	sigCh := make(chan os.Signal, 1)
	done := awaitResult(core.RestartRequest{SessionKey: "s1", WaitIdle: true, MaxWait: time.Hour},
		func() int { return 1 }, make(chan core.RestartRequest), sigCh)

	sigCh <- syscall.SIGTERM
	if got := waitResult(t, done); got != nil {
		t.Fatalf("got %+v, want no restart after a signal", got)
	}
}
