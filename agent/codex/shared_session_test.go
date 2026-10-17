package codex

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
	"github.com/gorilla/websocket"
)

type sharedRPCRequest struct {
	ID     any            `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

// Simulates a backend owned by an already connected CLI. Starting a second
// turn while the CLI works fails; only steer can add input to that turn.
type sharedRPCServer struct {
	mu                   sync.Mutex
	conn                 *websocket.Conn
	requests             []sharedRPCRequest
	active               bool
	finishDuringSnapshot bool
	finishDuringStart    bool
	loaded               bool
	emptyResume          bool
	unmaterialized       bool
	disconnected         chan struct{}
}

func (p *sharedRPCServer) serve(w http.ResponseWriter, r *http.Request) {
	conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	defer close(p.disconnected)
	p.mu.Lock()
	p.conn = conn
	p.mu.Unlock()
	for {
		var req sharedRPCRequest
		if conn.ReadJSON(&req) != nil {
			return
		}
		p.mu.Lock()
		p.requests = append(p.requests, req)
		if req.ID == nil {
			p.mu.Unlock()
			continue
		}
		var result any = map[string]any{}
		switch req.Method {
		case "thread/read":
			status := "idle"
			if p.active {
				status = "active"
			}
			result = map[string]any{"thread": map[string]any{"status": map[string]any{"type": status}}}
		case "thread/loaded/list":
			data := []string{}
			if p.loaded {
				data = append(data, "cli-thread")
			}
			result = map[string]any{"data": data, "nextCursor": nil}
		case "thread/resume":
			if p.emptyResume {
				p.emptyResume = false
				_ = conn.WriteJSON(map[string]any{"id": req.ID, "error": map[string]any{"code": -32603, "message": "rollout at test.jsonl is empty"}})
				p.mu.Unlock()
				continue
			}
			result = map[string]any{"thread": map[string]any{"id": "cli-thread"}, "cwd": "cli-workdir", "model": "cli-model", "reasoningEffort": "high", "approvalsReviewer": "user"}
		case "thread/turns/list":
			if p.unmaterialized {
				_ = conn.WriteJSON(map[string]any{"id": req.ID, "error": map[string]any{"code": -32600, "message": "thread is not materialized yet"}})
				p.mu.Unlock()
				continue
			}
			status := "completed"
			if p.active {
				status = "inProgress"
			}
			result = map[string]any{"data": []any{map[string]any{"id": "cli-turn", "status": status}}}
			if p.finishDuringSnapshot {
				p.finishDuringSnapshot = false
				p.finishLocked("finished during snapshot")
			}
		case "turn/steer":
			if !p.active || req.Params["expectedTurnId"] != "cli-turn" {
				_ = conn.WriteJSON(map[string]any{"id": req.ID, "error": map[string]any{"code": -32600, "message": "no matching active turn"}})
				p.mu.Unlock()
				continue
			}
		case "turn/start":
			if p.active {
				_ = conn.WriteJSON(map[string]any{"id": req.ID, "error": map[string]any{"code": -32600, "message": "thread already has an active turn"}})
				p.mu.Unlock()
				continue
			}
			p.active = true
			p.unmaterialized = false
			p.notifyLocked("turn/started", map[string]any{"threadId": "cli-thread", "turn": map[string]any{"id": "cli-turn", "status": "inProgress"}})
			result = map[string]any{"turn": map[string]any{"id": "cli-turn"}}
			if p.finishDuringStart {
				p.finishLocked("fast response")
			}
		}
		_ = conn.WriteJSON(map[string]any{"id": req.ID, "result": result})
		p.mu.Unlock()
	}
}

func (p *sharedRPCServer) notifyLocked(method string, params any) {
	_ = p.conn.WriteJSON(map[string]any{"method": method, "params": params})
}

func (p *sharedRPCServer) finishLocked(text string) {
	p.notifyLocked("item/completed", map[string]any{"threadId": "cli-thread", "turnId": "cli-turn", "item": map[string]any{"type": "agentMessage", "text": text}})
	p.notifyLocked("turn/completed", map[string]any{"threadId": "cli-thread", "turn": map[string]any{"id": "cli-turn", "status": "completed"}})
	p.active = false
}

func (p *sharedRPCServer) finish(text string) { p.mu.Lock(); defer p.mu.Unlock(); p.finishLocked(text) }

func newSharedRPCServer(t *testing.T) (*sharedRPCServer, string) {
	t.Helper()
	p := &sharedRPCServer{active: true, loaded: true, disconnected: make(chan struct{})}
	srv := httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(srv.Close)
	return p, "ws" + strings.TrimPrefix(srv.URL, "http")
}

func attachTestSession(t *testing.T, endpoint string) *appServerSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	s, err := newAppServerSession(ctx, "missing-codex", endpoint, t.TempDir(), "bot-model", "low", "auto-review", "cli-thread", "", "", nil, "", "", "", "bot instructions")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func awaitSharedResult(t *testing.T, s *appServerSession) string {
	t.Helper()
	text := ""
	for {
		select {
		case ev, ok := <-s.Events():
			if !ok {
				t.Fatal("connection closed before result")
			}
			if ev.Type == core.EventError {
				t.Fatal(ev.Error)
			}
			if ev.Type == core.EventText {
				text += ev.Content
			}
			if ev.Type == core.EventResult {
				return text + ev.Content
			}
		case <-time.After(3 * time.Second):
			t.Fatal("no result")
		}
	}
}

func TestSharedSession_AttachActiveSteerAndDetachPreservesOwner(t *testing.T) {
	p, endpoint := newSharedRPCServer(t)
	s := attachTestSession(t, endpoint)
	if s.cmd != nil {
		t.Fatal("attach spawned a second process")
	}
	if s.GetModel() != "cli-model" || s.GetWorkDir() != "cli-workdir" || s.GetReasoningEffort() != "high" {
		t.Fatal("CLI settings were overwritten")
	}
	if err := s.Send("use the updated requirement", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	for _, req := range p.requests {
		if req.Method == "thread/resume" && (len(req.Params) != 2 || req.Params["excludeTurns"] != true) {
			t.Errorf("resume overrides CLI settings: %#v", req.Params)
		}
		if req.Method == "turn/start" {
			t.Error("started another turn instead of steering")
		}
	}
	// Foreign events must not complete, pollute, or change the subscribed turn.
	p.notifyLocked("item/completed", map[string]any{"threadId": "other-thread", "item": map[string]any{"type": "agentMessage", "text": "foreign text"}})
	p.notifyLocked("turn/completed", map[string]any{"threadId": "other-thread", "turn": map[string]any{"id": "other-turn", "status": "completed"}})
	p.mu.Unlock()
	p.finish("CLI final response")
	if got := awaitSharedResult(t, s); got != "CLI final response" {
		t.Fatalf("result=%q", got)
	}
	if err := s.Send("next turn", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.disconnected:
	case <-time.After(time.Second):
		t.Fatal("client did not detach")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.active {
		t.Error("detaching stopped the CLI's task")
	}
	found := false
	for _, req := range p.requests {
		if req.Method == "turn/start" {
			found = true
			if len(req.Params) != 2 {
				t.Errorf("start overrides CLI settings: %#v", req.Params)
			}
		}
		if req.Method == "turn/interrupt" || req.Method == "thread/close" {
			t.Errorf("detach sent %s", req.Method)
		}
	}
	if !found {
		t.Error("idle send did not start next turn")
	}
}

func TestSharedSession_CompletionDuringSnapshotIsNotResurrected(t *testing.T) {
	p, url := newSharedRPCServer(t)
	p.finishDuringSnapshot = true
	s := attachTestSession(t, url)
	if got := awaitSharedResult(t, s); got != "finished during snapshot" {
		t.Fatal(got)
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.currentTurn != "" {
		t.Fatalf("restored completed turn %q", s.currentTurn)
	}
}

func TestSharedSession_FastTurnResponseDoesNotLoseFinalText(t *testing.T) {
	p, url := newSharedRPCServer(t)
	p.active = false
	p.finishDuringStart = true
	s := attachTestSession(t, url)
	if err := s.Send("fast turn", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := awaitSharedResult(t, s); got != "fast response" {
		t.Fatal(got)
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.currentTurn != "" {
		t.Fatalf("restored completed turn %q", s.currentTurn)
	}
}

func TestSharedSession_ResumeWaitsForFirstRolloutHeader(t *testing.T) {
	p, url := newSharedRPCServer(t)
	p.emptyResume = true
	s := attachTestSession(t, url)
	if err := s.Send("steer after header flush", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	p.finish("resumed successfully")
	if got := awaitSharedResult(t, s); got != "resumed successfully" {
		t.Fatal(got)
	}
}

func TestSharedSession_UnmaterializedThreadCanSendFirstMessage(t *testing.T) {
	p, url := newSharedRPCServer(t)
	p.active, p.unmaterialized = false, true
	s := attachTestSession(t, url)
	if err := s.Send("first message", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	p.finish("first response")
	if got := awaitSharedResult(t, s); got != "first response" {
		t.Fatal(got)
	}
}

func TestSharedSession_ApprovalResolvedInCLI(t *testing.T) {
	p, url := newSharedRPCServer(t)
	s := attachTestSession(t, url)
	p.mu.Lock()
	_ = p.conn.WriteJSON(map[string]any{"id": "approval-1", "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": "cli-thread", "command": "echo test"}})
	p.mu.Unlock()
	var approval core.Event
	for approval.Type != core.EventPermissionRequest {
		select {
		case approval = <-s.Events():
		case <-time.After(time.Second):
			t.Fatal("missing approval")
		}
	}
	p.mu.Lock()
	p.notifyLocked("serverRequest/resolved", map[string]any{"threadId": "cli-thread", "requestId": "approval-1"})
	p.mu.Unlock()
	select {
	case <-approval.PermissionResolved:
	case <-time.After(time.Second):
		t.Fatal("approval was not cleared")
	}
	if err := s.RespondPermission(approval.RequestID, core.PermissionResult{Behavior: "deny"}); err == nil {
		t.Fatal("accepted a second decision")
	}
	p.finish("continued after terminal approval")
	if got := awaitSharedResult(t, s); got != "continued after terminal approval" {
		t.Fatal(got)
	}
}

func TestSharedSession_ContextCancellationDisconnects(t *testing.T) {
	p, url := newSharedRPCServer(t)
	s := attachTestSession(t, url)
	s.cancel()
	select {
	case <-p.disconnected:
	case <-time.After(time.Second):
		t.Fatal("cancel did not close socket")
	}
	for range s.Events() {
	}
	if s.Alive() {
		t.Fatal("disconnected session still alive")
	}
}

func TestSharedSession_AutoDiscoversDaemonSocket(t *testing.T) {
	t.Run("loaded", func(t *testing.T) { testAutoDaemonSocket(t, true) })
	t.Run("not loaded", func(t *testing.T) { testAutoDaemonSocket(t, false) })
}

func testAutoDaemonSocket(t *testing.T, loaded bool) {
	home, err := os.MkdirTemp("", "cdx-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	path := daemonSocketPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Skipf("unix sockets unavailable: %v", err)
	}
	p := &sharedRPCServer{active: true, loaded: loaded, disconnected: make(chan struct{})}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(p.serve))
	srv.Listener.Close()
	srv.Listener = listener
	srv.Start()
	defer srv.Close()
	a := &Agent{backend: "app_server", codexHome: home, workDir: t.TempDir(), mode: "default", cmd: "must-not-spawn"}
	if !loaded {
		s, err := a.AttachSession(context.Background(), "unowned")
		if s != nil || err != nil {
			t.Fatalf("unowned attach: %v %v", s, err)
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		for _, req := range p.requests {
			if req.Method == "thread/resume" {
				t.Fatal("auto loaded a thread owned elsewhere")
			}
		}
		return
	}
	s, err := a.StartSession(context.Background(), "cli-thread")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !s.(*appServerSession).shared {
		t.Fatal("auto did not attach")
	}
	p.finish("auto attached")
	if got := awaitSharedResult(t, s.(*appServerSession)); got != "auto attached" {
		t.Fatal(got)
	}
}

func TestSharedSession_MissingDaemonFallsBackButExplicitFailureDoesNot(t *testing.T) {
	a := &Agent{backend: "app_server", codexHome: t.TempDir(), mode: "default"}
	if s, err := a.AttachSession(context.Background(), "cli-thread"); s != nil || err != nil {
		t.Fatalf("missing daemon: %v %v", s, err)
	}
	a.appServerURL = "unix://" + filepath.Join(a.codexHome, "missing.sock")
	if _, err := a.StartSession(context.Background(), "cli-thread"); !errors.Is(err, core.ErrSessionResumeUnavailable) {
		t.Fatalf("explicit connection error lost: %v", err)
	}
}

// A private process has no other client to answer its requests, so a request
// from another thread it runs (e.g. a subagent) must still reach the user.
func TestPrivateSession_ForwardsRequestsFromOtherThreads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &appServerSession{ctx: ctx, events: make(chan core.Event, 4), stdin: &lockedWriteCloser{},
		pendingApprovals: make(map[string]chan core.PermissionResult)}
	s.threadID.Store("main-thread")
	s.handleServerRequest(serverRequestProbe(t, `"child-approval"`, "item/commandExecution/requestApproval", map[string]any{
		"threadId": "child-thread", "command": "echo child",
	}))
	select {
	case ev := <-s.events:
		if ev.Type != core.EventPermissionRequest {
			t.Fatalf("expected a permission request: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("request from another thread was dropped without an answer")
	}
}

// Verify raw IDs remain JSON-compatible (server IDs may be strings or numbers).
func TestSharedSession_NumericApprovalResolution(t *testing.T) {
	s := &appServerSession{}
	_, resolved := s.registerApproval("42")
	s.handleNotification("serverRequest/resolved", json.RawMessage(`{"requestId":42}`))
	select {
	case <-resolved:
	default:
		t.Fatal("numeric request not resolved")
	}
}
