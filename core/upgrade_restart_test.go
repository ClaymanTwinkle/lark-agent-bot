package core

import (
	"strings"
	"testing"
	"time"
)

// takeRestartRequest reads the request the engine sent, leaving RestartCh
// empty for other tests.
func takeRestartRequest(t *testing.T) RestartRequest {
	t.Helper()
	select {
	case req := <-RestartCh:
		return req
	case <-time.After(time.Second):
		t.Fatal("no restart request sent")
		return RestartRequest{}
	}
}

func TestWorkInProgress_CountsBusySessions(t *testing.T) {
	e := newTestEngine()
	turn := &interactiveState{}
	turn.beginTurn()
	queued := &interactiveState{pendingMessages: []queuedMessage{{content: "next"}}}
	e.interactiveMu.Lock()
	e.interactiveStates["turn"] = turn
	e.interactiveStates["queued"] = queued
	e.interactiveStates["idle"] = &interactiveState{}
	e.interactiveMu.Unlock()

	if got := e.WorkInProgress(); got != 2 {
		t.Fatalf("WorkInProgress = %d, want 2 (a running turn and queued messages)", got)
	}
}

func TestRestartAfterUpgrade_WaitsWhileTasksRun(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.SetUpgradeRestartWait(90*time.Minute, func() int { return 2 })

	e.restartAfterUpgrade(p, &Message{SessionKey: "s1", ReplyCtx: "ctx"}, "installed")

	req := takeRestartRequest(t)
	if !req.WaitIdle || req.MaxWait != 90*time.Minute || req.SessionKey != "s1" || req.Platform != "test" {
		t.Fatalf("restart request = %+v, want one that waits up to 90m", req)
	}
	sent := p.getSent()
	want := "installed\n" + e.i18n.Tf(MsgUpgradeRestartWaiting, 2, 90)
	if len(sent) != 1 || sent[0] != want {
		t.Fatalf("replies = %q, want %q", sent, want)
	}
}

func TestRestartAfterUpgrade_RestartsAtOnce(t *testing.T) {
	for _, tc := range []struct {
		name string
		wait time.Duration
		busy int
	}{
		{"idle", time.Hour, 0},
		{"waiting disabled", 0, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &stubPlatformEngine{n: "test"}
			e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
			e.SetUpgradeRestartWait(tc.wait, func() int { return tc.busy })

			e.restartAfterUpgrade(p, &Message{SessionKey: "s1", ReplyCtx: "ctx"}, "installed")

			if req := takeRestartRequest(t); req.WaitIdle {
				t.Fatalf("restart request = %+v, want an immediate restart", req)
			}
			sent := p.getSent()
			if len(sent) != 1 || !strings.HasSuffix(sent[0], e.i18n.T(MsgRestarting)) {
				t.Fatalf("replies = %q, want the restarting notice", sent)
			}
		})
	}
}

func TestRestartAfterUpgrade_CountsThisEngineByDefault(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	state := &interactiveState{}
	state.beginTurn()
	e.interactiveMu.Lock()
	e.interactiveStates["turn"] = state
	e.interactiveMu.Unlock()

	e.restartAfterUpgrade(p, &Message{SessionKey: "s1", ReplyCtx: "ctx"}, "installed")

	if req := takeRestartRequest(t); !req.WaitIdle || req.MaxWait != DefaultUpgradeRestartWait {
		t.Fatalf("restart request = %+v, want one that waits the default time", req)
	}
}
