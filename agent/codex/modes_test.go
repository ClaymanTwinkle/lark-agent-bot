package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

func TestPermissionModes_MatchCurrentPermissionsWithoutAliases(t *testing.T) {
	a := &Agent{backend: "app_server"}
	want := []string{"default", "auto-review", "read-only", "full-access"}
	got := a.PermissionModes()
	if len(got) != len(want) {
		t.Fatalf("modes = %+v", got)
	}
	for i, key := range want {
		if got[i].Key != key {
			t.Errorf("mode %d = %q, want %q", i, got[i].Key, key)
		}
	}
}

// full-access drops approvals and the sandbox, so only it needs admin_from.
func TestPermissionModes_OnlyFullAccessIsPrivileged(t *testing.T) {
	for _, backend := range []string{"app_server", "exec"} {
		a := &Agent{backend: backend}
		for _, mode := range a.PermissionModes() {
			if want := mode.Key == "full-access"; mode.Privileged != want {
				t.Errorf("%s %q: Privileged = %v, want %v", backend, mode.Key, mode.Privileged, want)
			}
		}
		if got := a.NormalizeMode(" Full-Access "); got != "full-access" {
			t.Errorf("NormalizeMode = %q, want full-access", got)
		}
	}
}

func TestPermissionModes_DefaultAndAutoReviewHaveDifferentReviewers(t *testing.T) {
	a := &Agent{backend: "app_server"}
	for _, tc := range []struct{ mode, approval, sandbox, reviewer string }{
		{"default", "on-request", "workspace-write", "user"},
		{"auto-review", "on-request", "workspace-write", "auto_review"},
		{"read-only", "on-request", "read-only", "user"},
		{"full-access", "never", "danger-full-access", "user"},
		{"default", "on-request", "workspace-write", "user"},
	} {
		a.SetMode(tc.mode)
		if a.GetMode() != tc.mode {
			t.Fatalf("mode = %q, want %q", a.GetMode(), tc.mode)
		}
		opts := a.WorkspaceAgentOptions()
		if opts["mode"] != tc.mode {
			t.Fatalf("workspace lost mode: %v", opts)
		}
		params := (&appServerSession{mode: tc.mode}).threadRequestParams()
		if params["approvalPolicy"] != tc.approval || params["sandbox"] != tc.sandbox || params["approvalsReviewer"] != tc.reviewer {
			t.Errorf("%s settings = %v", tc.mode, params)
		}
	}
}

func TestNew_RejectsRemovedPermissionModesAndReviewerOverride(t *testing.T) {
	for _, mode := range []string{"suggest", "auto-edit", "full-auto", "yolo", "auto", "edit", "bypass", "auto_review", "unknown"} {
		if _, err := New(map[string]any{"cmd": "go", "mode": mode}); err == nil {
			t.Errorf("removed/invalid mode %q accepted", mode)
		}
	}
	for _, reviewer := range []string{"", "user", "auto_review"} {
		if _, err := New(map[string]any{"cmd": "go", "mode": "default", "backend": "app_server", "approvals_reviewer": reviewer}); err == nil {
			t.Errorf("independent reviewer override %q accepted", reviewer)
		}
	}
}

func TestNew_PermissionDefaultsAndExecCapabilities(t *testing.T) {
	agent, err := New(map[string]any{"cmd": "go"})
	if err != nil {
		t.Fatal(err)
	}
	a := agent.(*Agent)
	if a.GetMode() != "default" || a.backend != "app_server" {
		t.Fatalf("defaults = mode %q backend %q", a.GetMode(), a.backend)
	}
	for _, mode := range []string{"default", "auto-review"} {
		if _, err := New(map[string]any{"cmd": "go", "backend": "exec", "mode": mode}); err == nil {
			t.Errorf("exec accepted mode requiring approvals: %s", mode)
		}
	}
	for _, mode := range []string{"read-only", "full-access"} {
		if _, err := New(map[string]any{"cmd": "go", "backend": "exec", "mode": mode}); err != nil {
			t.Fatal(err)
		}
	}
	execAgent := &Agent{backend: "exec", mode: "read-only"}
	if modes := execAgent.PermissionModes(); len(modes) != 2 || modes[0].Key != "read-only" || modes[1].Key != "full-access" {
		t.Fatalf("exec advertises unsupported modes: %+v", modes)
	}
	execAgent.SetMode("auto-review")
	if execAgent.GetMode() != "read-only" {
		t.Fatal("invalid mode changed the agent")
	}
	a.SetMode("auto-review")
	a.SetMode("full-auto")
	if a.GetMode() != "auto-review" {
		t.Fatal("obsolete alias changed the agent")
	}
}

type permissionRPCProbe struct {
	s     *appServerSession
	mu    sync.Mutex
	calls []struct {
		Method string
		Params map[string]any
	}
}

func (p *permissionRPCProbe) Close() error { return nil }
func (p *permissionRPCProbe) Write(data []byte) (int, error) {
	var req struct {
		ID     any
		Method string
		Params map[string]any
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return 0, err
	}
	p.mu.Lock()
	p.calls = append(p.calls, struct {
		Method string
		Params map[string]any
	}{req.Method, req.Params})
	p.mu.Unlock()
	var result any
	switch req.Method {
	case "thread/start", "thread/resume":
		result = map[string]any{"thread": map[string]string{"id": "kept-thread"}, "approvalsReviewer": req.Params["approvalsReviewer"]}
	case "turn/start":
		result = map[string]any{"turn": map[string]string{"id": "turn-1"}}
	default:
		return 0, fmt.Errorf("unexpected RPC: %s", req.Method)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return 0, err
	}
	p.s.handleResponse(rpcResponseEnvelope{ID: req.ID, Result: raw})
	return len(data), nil
}

func TestPermissionModes_StartResumeAndTurnPreserveExplicitPolicy(t *testing.T) {
	for _, tc := range []struct{ mode, approval, sandbox, reviewer string }{
		{"default", "on-request", "workspace-write", "user"},
		{"auto-review", "on-request", "workspace-write", "auto_review"},
		{"read-only", "on-request", "read-only", "user"},
		{"full-access", "never", "danger-full-access", "user"},
	} {
		for _, resume := range []string{"", "kept-thread"} {
			t.Run(tc.mode+"/"+resume, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				s := &appServerSession{ctx: ctx, cancel: cancel, mode: tc.mode}
				s.alive.Store(true)
				probe := &permissionRPCProbe{s: s}
				s.stdin = probe
				if err := s.ensureThread(resume); err != nil {
					t.Fatal(err)
				}
				if err := s.Send("hello", "message-1", nil, nil); err != nil {
					t.Fatal(err)
				}
				probe.mu.Lock()
				defer probe.mu.Unlock()
				if len(probe.calls) != 2 {
					t.Fatalf("RPC calls: %+v", probe.calls)
				}
				wantMethod := "thread/start"
				if resume != "" {
					wantMethod = "thread/resume"
				}
				if probe.calls[0].Method != wantMethod || probe.calls[1].Method != "turn/start" {
					t.Fatalf("RPC calls: %+v", probe.calls)
				}
				for _, call := range probe.calls {
					if call.Params["approvalPolicy"] != tc.approval || call.Params["approvalsReviewer"] != tc.reviewer {
						t.Fatalf("%s lost permission settings: %v", call.Method, call.Params)
					}
				}
				if probe.calls[0].Params["sandbox"] != tc.sandbox || probe.calls[1].Params["threadId"] != "kept-thread" {
					t.Fatalf("sandbox or conversation lost: %+v", probe.calls)
				}
			})
		}
	}
}
