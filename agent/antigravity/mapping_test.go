package antigravity

import (
	"encoding/json"
	"testing"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

func TestMapUpdate_IgnoresNonReplyUpdates(t *testing.T) {
	m := newUpdateMapper()
	for _, kind := range []string{"user_message_chunk", "available_commands_update", "usage_update", "current_mode_update", "something_new"} {
		params := `{"sessionId":"s","update":{"sessionUpdate":"` + kind + `","content":{"type":"text","text":"x"}}}`
		if evs := m.mapUpdate(json.RawMessage(params)); len(evs) != 0 {
			t.Errorf("%s produced %+v", kind, evs)
		}
	}
}

func TestMapUpdate_ToolCallAnnouncedFinished(t *testing.T) {
	m := newUpdateMapper()
	evs := m.mapUpdate(json.RawMessage(`{"update":{"sessionUpdate":"tool_call","toolCallId":"t","title":"Read a.go","kind":"read","status":"completed","rawInput":{"path":"/w/a.go"},"rawOutput":{"output":"package a"}}}`))
	if len(evs) != 2 || evs[0].Type != core.EventToolUse || evs[1].Type != core.EventToolResult {
		t.Fatalf("events = %+v", evs)
	}
	if evs[0].ToolInput != "/w/a.go" || evs[1].ToolResult != "package a" || evs[1].ToolName != "Read a.go" {
		t.Errorf("events = %+v", evs)
	}
}

func TestMapUpdate_PlanBecomesThinking(t *testing.T) {
	evs := newUpdateMapper().mapUpdate(json.RawMessage(`{"update":{"sessionUpdate":"plan","entries":[{"content":"step one","status":"completed"},{"content":"step two","status":"pending"}]}}`))
	if len(evs) != 1 || evs[0].Type != core.EventThinking || evs[0].Content != "[completed] step one\n[pending] step two" {
		t.Errorf("events = %+v", evs)
	}
}

func TestSummarizeToolInput(t *testing.T) {
	cases := []struct{ kind, raw, want string }{
		{"execute", `{"command":"go test ./..."}`, "go test ./..."},
		{"other", `{"CommandLine":"ls -la"}`, "ls -la"},
		{"edit", `{"path":"/w/main.go","diff":"..."}`, "/w/main.go"},
		{"search", `{"query":"foo","limit":3}`, "limit: 3, query: foo"},
		{"x", `{}`, ""},
		{"x", ``, ""},
	}
	for _, c := range cases {
		if got := summarizeToolInput(c.kind, json.RawMessage(c.raw)); got != c.want {
			t.Errorf("summarizeToolInput(%s, %s) = %q, want %q", c.kind, c.raw, got, c.want)
		}
	}
}

func TestPickPermissionOption(t *testing.T) {
	opts := []permissionOption{
		{OptionID: "aa", Kind: "allow_always"},
		{OptionID: "ra", Kind: "reject_always"},
		{OptionID: "ao", Kind: "allow_once"},
	}
	if got := pickPermissionOption(true, opts); got != "ao" {
		t.Errorf("allow = %q, want the one-time option", got)
	}
	if got := pickPermissionOption(false, opts); got != "ra" {
		t.Errorf("deny = %q, want reject_always when no reject_once exists", got)
	}
	if got := pickPermissionOption(true, nil); got != "" {
		t.Errorf("no options = %q", got)
	}
	if r := permissionResponse(""); r["outcome"].(map[string]any)["outcome"] != "cancelled" {
		t.Errorf("empty option should cancel: %v", r)
	}
}

func TestNormalizeAndMatchMode(t *testing.T) {
	modes := []acpMode{{ID: "default", Name: "Default"}, {ID: "acceptEdits", Name: "Accept Edits"}, {ID: "plan", Name: "Plan"}}
	cases := map[string]string{"edit": "acceptEdits", "Accept-Edits": "acceptEdits", "PLAN": "plan", "manual": "default", "auto": ""}
	for in, want := range cases {
		if got := matchMode(normalizeMode(in), modes); got != want {
			t.Errorf("mode %q → %q, want %q", in, got, want)
		}
	}
	if normalizeMode("bypassPermissions") != modeYolo || normalizeMode("") != "" {
		t.Errorf("normalizeMode aliases broken")
	}
}

func TestStopReasonError(t *testing.T) {
	for _, ok := range []string{"", "end_turn", "cancelled"} {
		if err := stopReasonError(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"max_tokens", "max_turn_requests", "refusal", "weird"} {
		if stopReasonError(bad) == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

// Payloads as agy 1.5.14 sends them: a non-standard "name" field, the command
// line as the title, and command output behind a header.
func TestMapUpdate_RealAgyRunCommand(t *testing.T) {
	m := newUpdateMapper()
	use := m.mapUpdate(json.RawMessage(`{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"c1","name":"run_command","args":{"CommandLine":"echo lab-smoke-ok"},"status":"in_progress","kind":"execute","title":"echo lab-smoke-ok","rawInput":{"CommandLine":"echo lab-smoke-ok","WaitMsBeforeAsync":5000,"toolSummary":"Run echo smoke command"}}}`))
	if len(use) != 1 || use[0].ToolName != "run_command" || use[0].ToolInput != "echo lab-smoke-ok" {
		t.Fatalf("tool_call → %+v", use)
	}
	out := "Created At: 2026-11-21T19:59:35+08:00\nCompleted At: 2026-11-21T19:59:35+08:00\nCommand exited with code 0.\n\nCommand output:\nlab-smoke-ok\r\n"
	b, _ := json.Marshal(out)
	res := m.mapUpdate(json.RawMessage(`{"sessionId":"s","update":{"sessionUpdate":"tool_call_update","toolCallId":"c1","name":"run_command","status":"completed","kind":"execute","title":"echo lab-smoke-ok","rawOutput":{"output":` + string(b) + `,"exit_code":0},"content":[{"type":"content","content":{"type":"text","text":` + string(b) + `}}]}}`))
	if len(res) != 1 {
		t.Fatalf("tool_call_update → %+v", res)
	}
	r := res[0]
	if r.ToolName != "run_command" || r.ToolResult != "lab-smoke-ok" || r.ToolExitCode == nil || *r.ToolExitCode != 0 || !*r.ToolSuccess {
		t.Errorf("result = name %q result %q exit %v success %v", r.ToolName, r.ToolResult, r.ToolExitCode, *r.ToolSuccess)
	}

	failed := m.mapUpdate(json.RawMessage(`{"update":{"sessionUpdate":"tool_call_update","toolCallId":"c1","status":"completed","rawOutput":{"output":"Command exited with code 2.\n\nCommand output:\nboom","exit_code":2}}}`))
	if len(failed) != 1 || *failed[0].ToolSuccess || *failed[0].ToolExitCode != 2 || failed[0].ToolResult != "boom" {
		t.Errorf("non-zero exit → %+v", failed)
	}
}
