package claudecode

import (
	"context"
	"reflect"
	"testing"

	"github.com/ClaymanTwinkle/lark-connect/core"
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
