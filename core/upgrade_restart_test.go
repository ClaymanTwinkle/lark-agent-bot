package core

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestRequestRestart_WaitsForTheAskingTurn(t *testing.T) {
	p := &stubPlatformEngine{n: "feishu"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.SetUpgradeRestartWait(2*time.Hour, func() int { return 1 })

	busy, maxWait, err := e.RequestRestart("feishu:oc_chat:ou_user", false)
	if err != nil || busy != 1 || maxWait != 2*time.Hour {
		t.Fatalf("RequestRestart() = %d, %s, %v; want 1 task, 2h", busy, maxWait, err)
	}
	req := takeRestartRequest(t)
	if !req.WaitIdle || req.MaxWait != 2*time.Hour || req.Platform != "feishu" || req.SessionKey != "feishu:oc_chat:ou_user" {
		t.Fatalf("restart request = %+v, want one that waits and notifies the feishu chat", req)
	}
}

func TestRequestRestart_NowAndWorkspacePrefix(t *testing.T) {
	p := &stubPlatformEngine{n: "feishu"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.SetUpgradeRestartWait(2*time.Hour, func() int { return 3 })

	busy, maxWait, err := e.RequestRestart(`D:\work:feishu:oc_chat:ou_user`, true)
	if err != nil || busy != 0 || maxWait != 0 {
		t.Fatalf("RequestRestart(now) = %d, %s, %v; want an immediate restart", busy, maxWait, err)
	}
	req := takeRestartRequest(t)
	if req.WaitIdle || req.Platform != "feishu" || req.SessionKey != "feishu:oc_chat:ou_user" {
		t.Fatalf("restart request = %+v, want an immediate one with the workspace prefix stripped", req)
	}
}

func TestRequestRestart_RefusesWhileOneIsPending(t *testing.T) {
	e := newTestEngine()
	RestartCh <- RestartRequest{SessionKey: "earlier"}
	t.Cleanup(func() {
		select {
		case <-RestartCh:
		default:
		}
	})
	if _, _, err := e.RequestRestart("test:chat:user", true); !errors.Is(err, ErrRestartPending) {
		t.Fatalf("RequestRestart() error = %v, want ErrRestartPending", err)
	}
}

func TestHandleRestart(t *testing.T) {
	p := &stubPlatformEngine{n: "feishu"}
	e := NewEngine("claude-bot", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.SetUpgradeRestartWait(time.Hour, func() int { return 2 })
	api := &APIServer{engines: map[string]*Engine{"claude-bot": e}}

	rec := httptest.NewRecorder()
	api.handleRestart(rec, httptest.NewRequest(http.MethodPost, "/restart", strings.NewReader(`{"session_key":"feishu:oc_chat:ou_user"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp RestartAPIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Busy != 2 || resp.MaxWaitSecs != 3600 {
		t.Fatalf("response = %+v, %v; want 2 busy, 3600s", resp, err)
	}
	if req := takeRestartRequest(t); !req.WaitIdle || req.Platform != "feishu" {
		t.Fatalf("restart request = %+v", req)
	}

	rec = httptest.NewRecorder()
	api.handleRestart(rec, httptest.NewRequest(http.MethodPost, "/restart", strings.NewReader(`{"project":"nope"}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown project: status = %d, want 404", rec.Code)
	}
	rec = httptest.NewRecorder()
	api.handleRestart(rec, httptest.NewRequest(http.MethodGet, "/restart", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: status = %d, want 405", rec.Code)
	}
}
