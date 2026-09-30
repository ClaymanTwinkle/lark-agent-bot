package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"sync"
	"testing"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// Regression: Claude Code removed --max-context-tokens ("unknown option"
// aborts the spawn). max_context_tokens now maps to --autocompact, whose
// parser only accepts 100k–1M.
func TestAutocompactArgs(t *testing.T) {
	cases := []struct {
		in   int
		want []string
	}{
		{0, nil},
		{-1, nil},
		{99_999, nil},
		{100_000, []string{"--autocompact", "100000"}},
		{400_000, []string{"--autocompact", "400000"}},
		{1_000_000, []string{"--autocompact", "1000000"}},
		{1_000_001, nil},
	}
	for _, tc := range cases {
		if got := autocompactArgs(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("autocompactArgs(%d) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// Regression: "xhigh" is a valid claude --effort level and must not be
// silently dropped.
func TestNormalizeEffort(t *testing.T) {
	cases := map[string]string{
		"":       "",
		"low":    "low",
		"med":    "medium",
		"HIGH":   "high",
		"xhigh":  "xhigh",
		"x-high": "xhigh",
		"max":    "max",
		"ultra":  "",
	}
	for in, want := range cases {
		if got := normalizeEffort(in); got != want {
			t.Errorf("normalizeEffort(%q) = %q, want %q", in, got, want)
		}
	}

	a := &Agent{}
	for _, e := range a.AvailableReasoningEfforts() {
		if normalizeEffort(e) != e {
			t.Errorf("AvailableReasoningEfforts lists %q but normalizeEffort does not accept it", e)
		}
	}
}

func TestModelUsageContextWindow(t *testing.T) {
	entry := func(w any) map[string]any { return map[string]any{"contextWindow": w} }
	cases := []struct {
		name   string
		raw    map[string]any
		active string
		want   int
	}{
		{name: "no modelUsage", raw: map[string]any{}, active: "claude-opus-5-5", want: 0},
		{name: "empty modelUsage", raw: map[string]any{"modelUsage": map[string]any{}}, active: "claude-opus-5-5", want: 0},
		{
			name: "active model matches despite [1m] suffix",
			raw: map[string]any{"modelUsage": map[string]any{
				"claude-opus-5-5":           entry(float64(1_000_000)),
				"claude-haiku-4-5-20251001": entry(float64(200_000)),
			}},
			active: "claude-opus-5-5[1m]",
			want:   1_000_000,
		},
		{
			name: "active model entry wins over a larger one",
			raw: map[string]any{"modelUsage": map[string]any{
				"claude-sonnet-5[1m]": entry(float64(200_000)),
				"claude-fable-5-1":    entry(float64(1_000_000)),
			}},
			active: "Claude-Sonnet-5",
			want:   200_000,
		},
		{
			name: "no match falls back to the largest window",
			raw: map[string]any{"modelUsage": map[string]any{
				"custom-a": entry(float64(128_000)),
				"custom-b": entry("256000"),
			}},
			active: "claude-opus-5-5",
			want:   256_000,
		},
		{
			name:   "zero window is ignored",
			raw:    map[string]any{"modelUsage": map[string]any{"claude-opus-5-5": entry(float64(0))}},
			active: "claude-opus-5-5",
			want:   0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := modelUsageContextWindow(tc.raw, tc.active); got != tc.want {
				t.Errorf("modelUsageContextWindow = %d, want %d", got, tc.want)
			}
		})
	}
}

func assistantUsageEvent(cacheRead float64) map[string]any {
	return map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []any{},
			"usage": map[string]any{
				"input_tokens":                float64(10),
				"output_tokens":               float64(1),
				"cache_creation_input_tokens": float64(0),
				"cache_read_input_tokens":     cacheRead,
			},
		},
	}
}

func resultWithModelUsage(model string, window float64) map[string]any {
	return map[string]any{
		"type":       "result",
		"result":     "done",
		"session_id": "test-session",
		"usage":      map[string]any{"output_tokens": float64(5)},
		"modelUsage": map[string]any{
			model:                       map[string]any{"contextWindow": window},
			"claude-haiku-4-5-20251001": map[string]any{"contextWindow": float64(200_000)},
		},
	}
}

// Regression: a 1M session whose model id lacks "[1m]" was sized with the
// 200K heuristic, overstating ctx% five-fold. The window Claude Code reports
// in modelUsage must replace it, for the current turn and later ones.
func TestHandleResultAppliesReportedContextWindow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cs := &claudeSession{events: make(chan core.Event, 8), ctx: ctx}
	cs.sessionID.Store("test-session")
	cs.alive.Store(true)
	cs.activeModel.Store("claude-opus-5-5")

	cs.handleAssistant(assistantUsageEvent(300_000))
	if got := cs.GetContextUsage().ContextWindow; got != 200_000 {
		t.Fatalf("before any result: ContextWindow = %d, want heuristic 200_000", got)
	}

	cs.handleResult(resultWithModelUsage("claude-opus-5-5", 1_000_000))
	if got := cs.GetContextUsage().ContextWindow; got != 1_000_000 {
		t.Fatalf("after result: ContextWindow = %d, want reported 1_000_000", got)
	}

	cs.handleAssistant(assistantUsageEvent(400_000))
	if got := cs.GetContextUsage().ContextWindow; got != 1_000_000 {
		t.Fatalf("next turn: ContextWindow = %d, want reported 1_000_000", got)
	}
}

func TestHandleResultConfiguredWindowBeatsReported(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cs := &claudeSession{events: make(chan core.Event, 8), ctx: ctx, ctxWindowOverride: 300_000}
	cs.sessionID.Store("test-session")
	cs.alive.Store(true)
	cs.activeModel.Store("claude-opus-5-5")

	cs.handleAssistant(assistantUsageEvent(100_000))
	cs.handleResult(resultWithModelUsage("claude-opus-5-5", 1_000_000))
	if got := cs.GetContextUsage().ContextWindow; got != 300_000 {
		t.Fatalf("ContextWindow = %d, want configured 300_000", got)
	}
}

// fakeModeCLI plays the Claude Code process on the other end of stdin: it
// answers set_permission_mode control requests the way CLI 2.1.283 does,
// refusing the modes listed in reject with the given error_code.
type fakeModeCLI struct {
	mu     sync.Mutex
	got    []string
	reject map[string]string
}

func (f *fakeModeCLI) requested() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.got...)
}

func newLiveModeSession(t *testing.T, cli *fakeModeCLI, initial string) *claudeSession {
	t.Helper()
	pr, pw := io.Pipe()
	cs := &claudeSession{stdin: pw, done: make(chan struct{})}
	cs.alive.Store(true)
	cs.setPermissionMode(initial)
	t.Cleanup(func() { _ = pw.Close() })

	go func() {
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			var msg map[string]any
			if json.Unmarshal(sc.Bytes(), &msg) != nil {
				continue
			}
			id, _ := msg["request_id"].(string)
			req, _ := msg["request"].(map[string]any)
			mode, _ := req["mode"].(string)

			cli.mu.Lock()
			cli.got = append(cli.got, mode)
			code, refuse := cli.reject[mode]
			cli.mu.Unlock()

			resp := map[string]any{"subtype": "success", "request_id": id, "response": map[string]any{"mode": mode}}
			if refuse {
				resp = map[string]any{"subtype": "error", "request_id": id, "error": "refused", "error_code": code}
			}
			line, _ := json.Marshal(map[string]any{"type": "control_response", "response": resp})
			cs.handleReadLoopLine(string(line))
		}
	}()
	return cs
}

func assertModeFlags(t *testing.T, cs *claudeSession, mode string) {
	t.Helper()
	if got := cs.permissionModeValue(); got != mode {
		t.Fatalf("permission mode = %q, want %q", got, mode)
	}
	if cs.autoApprove.Load() != (mode == "bypassPermissions") ||
		cs.acceptEditsOnly.Load() != (mode == "acceptEdits") ||
		cs.dontAsk.Load() != (mode == "dontAsk") {
		t.Fatalf("flags for %q: autoApprove=%v acceptEditsOnly=%v dontAsk=%v",
			mode, cs.autoApprove.Load(), cs.acceptEditsOnly.Load(), cs.dontAsk.Load())
	}
}

// Regression: SetLiveMode used to flip only lark-agent-bot's own flags, so a
// CLI launched in bypassPermissions / acceptEdits / dontAsk kept not asking
// after a "successful" switch to a stricter mode, and auto / plan needed a
// restart. The CLI itself must now be switched.
func TestSetLiveModeSwitchesCLIMode(t *testing.T) {
	cli := &fakeModeCLI{}
	cs := newLiveModeSession(t, cli, "bypassPermissions")

	for _, mode := range []string{"default", "acceptEdits", "auto", "plan", "dontAsk"} {
		if !cs.SetLiveMode(mode) {
			t.Fatalf("SetLiveMode(%q) = false, want true", mode)
		}
		assertModeFlags(t, cs, mode)
	}
	want := []string{"default", "acceptEdits", "auto", "plan", "dontAsk"}
	if got := cli.requested(); !reflect.DeepEqual(got, want) {
		t.Fatalf("CLI got set_permission_mode %v, want %v", got, want)
	}
}

func TestSetLiveModeNormalizesAliases(t *testing.T) {
	cli := &fakeModeCLI{}
	cs := newLiveModeSession(t, cli, "acceptEdits")

	if !cs.SetLiveMode("manual") {
		t.Fatal("SetLiveMode(manual) = false, want true")
	}
	assertModeFlags(t, cs, "default")
	if !cs.SetLiveMode("default") {
		t.Fatal("SetLiveMode(default) when already default = false, want true")
	}
	if got := cli.requested(); !reflect.DeepEqual(got, []string{"default"}) {
		t.Fatalf("CLI got %v, want a single default switch", got)
	}
}

// The CLI refuses bypassPermissions unless launched with
// --dangerously-skip-permissions; the switch must still work by putting the
// CLI in default mode and approving locally.
func TestSetLiveModeBypassFallsBackToLocalApproval(t *testing.T) {
	cli := &fakeModeCLI{reject: map[string]string{"bypassPermissions": "bypass_not_launched"}}
	cs := newLiveModeSession(t, cli, "dontAsk")

	if !cs.SetLiveMode("yolo") {
		t.Fatal("SetLiveMode(yolo) = false, want true")
	}
	assertModeFlags(t, cs, "bypassPermissions")
	want := []string{"bypassPermissions", "default"}
	if got := cli.requested(); !reflect.DeepEqual(got, want) {
		t.Fatalf("CLI got %v, want %v", got, want)
	}
}

func TestSetLiveModeRefusedKeepsCurrentMode(t *testing.T) {
	cli := &fakeModeCLI{reject: map[string]string{"auto": "auto_mode_settings"}}
	cs := newLiveModeSession(t, cli, "default")

	if cs.SetLiveMode("auto") {
		t.Fatal("SetLiveMode(auto) = true although the CLI refused it")
	}
	assertModeFlags(t, cs, "default")
}

func TestSetLiveModeFailsWhenProcessGone(t *testing.T) {
	cs := &claudeSession{}
	cs.setPermissionMode("default")
	if cs.SetLiveMode("acceptEdits") {
		t.Fatal("SetLiveMode on a dead session = true, want false")
	}

	// Alive flag still set but the process exits before answering.
	pr, pw := io.Pipe()
	go func() { _, _ = io.Copy(io.Discard, pr) }()
	t.Cleanup(func() { _ = pw.Close() })
	cs = &claudeSession{stdin: pw, done: make(chan struct{})}
	cs.alive.Store(true)
	cs.setPermissionMode("default")
	close(cs.done)
	if cs.SetLiveMode("acceptEdits") {
		t.Fatal("SetLiveMode after process exit = true, want false")
	}
	assertModeFlags(t, cs, "default")
}

// Claude Code streams the subscription quota as rate_limit_event (captured
// from CLI 2.1.283); the reply footer reads it through GetUsage instead of
// probing /usage.
func TestHandleRateLimitEventFeedsGetUsage(t *testing.T) {
	cs := &claudeSession{}
	if _, err := cs.GetUsage(context.Background()); err == nil {
		t.Fatal("GetUsage before any rate_limit_event should error")
	}

	cs.handleReadLoopLine(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1790658000,"rateLimitType":"five_hour","overageStatus":"rejected","isUsingOverage":false,"unifiedWindows":{"five_hour":{"utilization":0.09,"resetsAt":1790658000},"seven_day":{"utilization":0.02,"resetsAt":1791136800}}},"uuid":"u","session_id":"s"}`)

	report, err := cs.GetUsage(context.Background())
	if err != nil {
		t.Fatalf("GetUsage: %v", err)
	}
	want := []core.UsageWindow{
		{Name: "five_hour", UsedPercent: 9, WindowSeconds: 18000, ResetAtUnix: 1790658000},
		{Name: "seven_day", UsedPercent: 2, WindowSeconds: 604800, ResetAtUnix: 1791136800},
	}
	if len(report.Buckets) != 1 || !reflect.DeepEqual(report.Buckets[0].Windows, want) {
		t.Fatalf("windows = %+v, want %+v", report.Buckets, want)
	}
	if !report.Buckets[0].Allowed || report.Buckets[0].LimitReached {
		t.Fatalf("bucket status = %+v, want allowed", report.Buckets[0])
	}

	// An event without unifiedWindows updates only the window it names.
	cs.handleReadLoopLine(`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","rateLimitType":"five_hour","utilization":1.0,"resetsAt":1790660000}}`)
	report, _ = cs.GetUsage(context.Background())
	w := report.Buckets[0].Windows
	if len(w) != 2 || w[0].UsedPercent != 100 || w[1].UsedPercent != 2 {
		t.Fatalf("after single-window event windows = %+v", w)
	}
	if !report.Buckets[0].LimitReached {
		t.Fatal("rejected status should mark the limit reached")
	}
}
