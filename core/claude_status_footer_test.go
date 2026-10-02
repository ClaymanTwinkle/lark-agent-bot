package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newClaudeFooterEngine returns an Engine with all three footer-related flags
// turned on, suitable for invoking buildClaudeStatusLineFooter as a method.
func newClaudeFooterEngine() *Engine {
	e := &Engine{i18n: NewI18n(LangEnglish)}
	e.SetReplyFooterEnabled(true)
	e.SetShowContextIndicator(true)
	e.SetShowWorkdirIndicator(true)
	return e
}

func quotaReport(fiveHour, week int) *UsageReport {
	return &UsageReport{Buckets: []UsageBucket{{Name: "claude", Windows: []UsageWindow{
		{Name: "five_hour", UsedPercent: fiveHour, WindowSeconds: 18000},
		{Name: "seven_day", UsedPercent: week, WindowSeconds: 604800},
	}}}}
}

func TestBuildClaudeStatusLineFooter_NilUsage(t *testing.T) {
	session := &controllableAgentSession{
		model:   "claude-opus-4-7[1m]",
		workDir: "/tmp/ws",
	}
	e := newClaudeFooterEngine()
	if got := e.buildClaudeStatusLineFooter(nil, session, "/tmp/ws"); got != "" {
		t.Errorf("expected empty footer for nil usage, got %q", got)
	}
}

func TestBuildClaudeStatusLineFooter_NoCacheTokens(t *testing.T) {
	// Other agents (codex/gemini) populate ContextUsage without cache
	// tokens; we must NOT emit the claude-style footer for them.
	session := &controllableAgentSession{
		model:   "claude-opus-4-7[1m]",
		workDir: "/tmp/ws",
		contextUsage: &ContextUsage{
			InputTokens:   1000,
			OutputTokens:  200,
			ContextWindow: 1_000_000,
			UsedTokens:    1000,
		},
	}
	e := newClaudeFooterEngine()
	if got := e.buildClaudeStatusLineFooter(nil, session, "/tmp/ws"); got != "" {
		t.Errorf("expected empty footer when cache tokens absent, got %q", got)
	}
}

func TestBuildClaudeStatusLineFooter_FullRender(t *testing.T) {
	session := &controllableAgentSession{
		model:   "claude-opus-4-7[1m]",
		workDir: "/tmp/ws",
		contextUsage: &ContextUsage{
			InputTokens:              1,
			OutputTokens:             168,
			CacheCreationInputTokens: 971,
			CachedInputTokens:        40800,
			ContextWindow:            1_000_000,
			UsedTokens:               1 + 971 + 40800,
		},
		report: quotaReport(9, 2),
	}
	e := newClaudeFooterEngine()
	got := e.buildClaudeStatusLineFooter(nil, session, "/tmp/ws")
	// 41772 / 1_000_000 = 4.17% → rounds to 4%.
	// Output is two lines:
	//   line 1: <model id> · 5h N% used · week N% used · ctx N%
	//   line 2: <workspace dir>
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (metrics + dir), got %d: %q", len(lines), got)
	}
	want := []string{"claude-opus-4-7[1m]", "5h 9% used", "week 2% used", "ctx 4%"}
	if parts := strings.Split(lines[0], " · "); strings.Join(parts, "|") != strings.Join(want, "|") {
		t.Fatalf("line 1 segments = %q, want %q", parts, want)
	}
	if lines[1] == "" || !strings.Contains(lines[1], "ws") {
		t.Errorf("line 2 = %q, want workspace path containing 'ws'", lines[1])
	}
}

// Token counts are no longer shown; without quota data line 1 keeps only
// model and ctx.
func TestBuildClaudeStatusLineFooter_NoQuotaNoTokens(t *testing.T) {
	session := &controllableAgentSession{
		model:   "gpt-6-astra",
		workDir: "/tmp/ws",
		contextUsage: &ContextUsage{
			InputTokens:       176300,
			OutputTokens:      35,
			CachedInputTokens: 175900,
			ContextWindow:     258000,
			UsedTokens:        176335,
		},
	}
	e := newClaudeFooterEngine()
	e.SetShowWorkdirIndicator(false)
	got := e.buildClaudeStatusLineFooter(nil, session, "/tmp/ws")
	if got != "gpt-6-astra · ctx 68%" {
		t.Fatalf("footer = %q, want %q", got, "gpt-6-astra · ctx 68%")
	}
}

func TestFormatReplyFooterQuota(t *testing.T) {
	zh := NewI18n(LangChinese)
	if got := formatReplyFooterQuota(quotaReport(9, 2), zh); got != "5小时已用 9% · 本周已用 2%" {
		t.Fatalf("zh quota = %q", got)
	}
	weekOnly := &UsageReport{Buckets: []UsageBucket{{Windows: []UsageWindow{{UsedPercent: 40, WindowSeconds: 604800}}}}}
	if got := formatReplyFooterQuota(weekOnly, zh); got != "本周已用 40%" {
		t.Fatalf("week-only quota = %q", got)
	}
	// A window of another length is never labeled as 5h or week.
	other := &UsageReport{Buckets: []UsageBucket{{Windows: []UsageWindow{{UsedPercent: 50, WindowSeconds: 3600}}}}}
	if got := formatReplyFooterQuota(other, zh); got != "" {
		t.Fatalf("unknown window quota = %q, want empty", got)
	}
}

func TestComposeRichStatusFooterShowsQuota(t *testing.T) {
	session := &controllableAgentSession{
		model: "claude-opus-5-5",
		contextUsage: &ContextUsage{
			CachedInputTokens: 40000, OutputTokens: 10, ContextWindow: 1_000_000, UsedTokens: 40000,
		},
		report: quotaReport(12, 30),
	}
	e := newClaudeFooterEngine()
	e.SetShowWorkdirIndicator(false)
	got := e.composeRichStatusFooter(false, time.Now(), nil, session, "")
	if !strings.Contains(got, "claude-opus-5-5 · 5h 12% used · week 30% used · ctx 4%") {
		t.Fatalf("rich footer = %q, want the quota status line", got)
	}
	if strings.Contains(got, "out ") || strings.Contains(got, " cr ") {
		t.Fatalf("rich footer still shows token counts: %q", got)
	}
}

// TestBuildClaudeStatusLineFooter_FooterDisabled verifies that the master
// reply_footer toggle suppresses the footer outright.
func TestBuildClaudeStatusLineFooter_FooterDisabled(t *testing.T) {
	session := &controllableAgentSession{
		model:   "claude-opus-4-7[1m]",
		workDir: "/tmp/ws",
		contextUsage: &ContextUsage{
			InputTokens:              1,
			OutputTokens:             168,
			CacheCreationInputTokens: 971,
			CachedInputTokens:        40800,
			ContextWindow:            1_000_000,
			UsedTokens:               1 + 971 + 40800,
		},
	}
	e := newClaudeFooterEngine()
	e.SetReplyFooterEnabled(false)
	if got := e.buildClaudeStatusLineFooter(nil, session, "/tmp/ws"); got != "" {
		t.Errorf("reply_footer=false must suppress footer, got %q", got)
	}
}

// TestBuildClaudeStatusLineFooter_HideContextLine hides line 1 (model/tokens/ctx)
// but keeps line 2 (workdir) — only the workspace path should appear.
func TestBuildClaudeStatusLineFooter_HideContextLine(t *testing.T) {
	session := &controllableAgentSession{
		model:   "claude-opus-4-7[1m]",
		workDir: "/tmp/ws",
		contextUsage: &ContextUsage{
			InputTokens:              1,
			OutputTokens:             168,
			CacheCreationInputTokens: 971,
			CachedInputTokens:        40800,
			ContextWindow:            1_000_000,
			UsedTokens:               1 + 971 + 40800,
		},
	}
	e := newClaudeFooterEngine()
	e.SetShowContextIndicator(false)
	got := e.buildClaudeStatusLineFooter(nil, session, "/tmp/ws")
	if strings.Contains(got, "\n") {
		t.Errorf("line 1 should be hidden — got multi-line footer: %q", got)
	}
	if got == "" || !strings.Contains(got, "ws") {
		t.Errorf("expected workdir-only footer containing 'ws', got %q", got)
	}
	if strings.Contains(got, "ctx ") || strings.Contains(got, "out ") {
		t.Errorf("metrics segments must not appear when show_context_indicator=false: %q", got)
	}
}

// TestBuildClaudeStatusLineFooter_HideWorkdirLine hides line 2 (workdir) but
// keeps line 1 (metrics) — no newline, no path.
func TestBuildClaudeStatusLineFooter_HideWorkdirLine(t *testing.T) {
	session := &controllableAgentSession{
		model:   "claude-opus-4-7[1m]",
		workDir: "/tmp/ws",
		contextUsage: &ContextUsage{
			InputTokens:              1,
			OutputTokens:             168,
			CacheCreationInputTokens: 971,
			CachedInputTokens:        40800,
			ContextWindow:            1_000_000,
			UsedTokens:               1 + 971 + 40800,
		},
	}
	e := newClaudeFooterEngine()
	e.SetShowWorkdirIndicator(false)
	got := e.buildClaudeStatusLineFooter(nil, session, "/tmp/ws")
	if strings.Contains(got, "\n") {
		t.Errorf("line 2 should be hidden — got multi-line footer: %q", got)
	}
	if !strings.Contains(got, "ctx ") {
		t.Errorf("metrics line missing ctx segment: %q", got)
	}
	if strings.Contains(got, "ws") {
		t.Errorf("workspace path must not appear when show_workdir_indicator=false: %q", got)
	}
}

// TestBuildClaudeStatusLineFooter_HideBothLines disables both per-line flags
// while keeping the master reply_footer on — footer collapses to empty.
func TestBuildClaudeStatusLineFooter_HideBothLines(t *testing.T) {
	session := &controllableAgentSession{
		model:   "claude-opus-4-7[1m]",
		workDir: "/tmp/ws",
		contextUsage: &ContextUsage{
			InputTokens:              1,
			OutputTokens:             168,
			CacheCreationInputTokens: 971,
			CachedInputTokens:        40800,
			ContextWindow:            1_000_000,
			UsedTokens:               1 + 971 + 40800,
		},
	}
	e := newClaudeFooterEngine()
	e.SetShowContextIndicator(false)
	e.SetShowWorkdirIndicator(false)
	if got := e.buildClaudeStatusLineFooter(nil, session, "/tmp/ws"); got != "" {
		t.Errorf("both lines hidden should yield empty footer, got %q", got)
	}
}

// ── buildReplyFooter (legacy single-line) toggle matrix ───────────────────────

// stubFooterAgent is a minimal Agent that exposes GetModel/GetReasoningEffort/
// GetWorkDir but never reports usage — so buildReplyFooter renders only
// model · effort · contextLeft · cwd segments deterministically.
type stubFooterAgent struct {
	stubAgent
	model   string
	effort  string
	workDir string
}

func (a *stubFooterAgent) GetModel() string           { return a.model }
func (a *stubFooterAgent) GetReasoningEffort() string { return a.effort }
func (a *stubFooterAgent) GetWorkDir() string         { return a.workDir }

// newLegacyFooterEngine returns an Engine with all three footer flags on,
// suitable for invoking buildReplyFooter directly against the legacy single-
// line footer path.
func newLegacyFooterEngine() *Engine {
	e := &Engine{}
	e.SetReplyFooterEnabled(true)
	e.SetShowContextIndicator(true)
	e.SetShowWorkdirIndicator(true)
	return e
}

func TestBuildReplyFooter_LegacyAllSegments(t *testing.T) {
	e := newLegacyFooterEngine()
	e.i18n = NewI18n(LangEnglish)
	agent := &stubFooterAgent{model: "gpt-5.4", effort: "xhigh", workDir: "/tmp/ws"}
	got := e.buildReplyFooter(agent, nil, "/tmp/ws", "100% left")
	wantSubs := []string{"gpt-5.4", "xhigh", "100% left", "ws"}
	for _, sub := range wantSubs {
		if !strings.Contains(got, sub) {
			t.Errorf("legacy footer = %q, missing %q", got, sub)
		}
	}
	// Segments joined by " · ".
	if !strings.Contains(got, " · ") {
		t.Errorf("legacy footer = %q, expected ' · ' separators", got)
	}
}

func TestCompactReplyFooterPath_HomeRelativeDeepPathStaysFull(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	shortPath := filepath.Join(homeDir, "codes", "lark-agent-bot")
	if got, want := compactReplyFooterPath(shortPath), "~/codes/lark-agent-bot"; got != want {
		t.Fatalf("short home path = %q, want %q", got, want)
	}

	deepPath := filepath.Join(homeDir, "code", "TechStudio", "projects", "core", "agents", "ceo")
	if got, want := compactReplyFooterPath(deepPath), "~/code/TechStudio/projects/core/agents/ceo"; got != want {
		t.Fatalf("deep home path = %q, want %q", got, want)
	}
}

func TestBuildReplyFooter_LegacyHidesContextSegments(t *testing.T) {
	e := newLegacyFooterEngine()
	e.SetShowContextIndicator(false)
	e.i18n = NewI18n(LangEnglish)
	agent := &stubFooterAgent{model: "gpt-5.4", effort: "xhigh", workDir: "/tmp/ws"}
	// With model/effort/contextLeft all suppressed, only cwd would remain —
	// and a workdir-only footer is suppressed entirely (regression cc-connect#701).
	if got := e.buildReplyFooter(agent, nil, "/tmp/ws", "100% left"); got != "" {
		t.Errorf("legacy footer with show_context_indicator=false = %q, want empty (workdir-only suppressed)", got)
	}
}

func TestBuildReplyFooter_LegacyHidesWorkdirSegment(t *testing.T) {
	e := newLegacyFooterEngine()
	e.SetShowWorkdirIndicator(false)
	e.i18n = NewI18n(LangEnglish)
	agent := &stubFooterAgent{model: "gpt-5.4", effort: "xhigh", workDir: "/tmp/ws"}
	got := e.buildReplyFooter(agent, nil, "/tmp/ws", "100% left")
	if got == "" {
		t.Fatalf("legacy footer should still render line-1 segments")
	}
	if strings.Contains(got, "ws") {
		t.Errorf("legacy footer with show_workdir_indicator=false = %q, must not contain workdir", got)
	}
	for _, sub := range []string{"gpt-5.4", "xhigh", "100% left"} {
		if !strings.Contains(got, sub) {
			t.Errorf("legacy footer = %q, missing %q (line-1 must remain)", got, sub)
		}
	}
}

func TestBuildReplyFooter_LegacyMasterToggleOff(t *testing.T) {
	e := newLegacyFooterEngine()
	e.SetReplyFooterEnabled(false)
	e.i18n = NewI18n(LangEnglish)
	agent := &stubFooterAgent{model: "gpt-5.4", effort: "xhigh", workDir: "/tmp/ws"}
	if got := e.buildReplyFooter(agent, nil, "/tmp/ws", "100% left"); got != "" {
		t.Errorf("reply_footer=false must short-circuit, got %q", got)
	}
}

// ── sendChunksWithStatusFooter helper ─────────────────────────────────────────

// stubFooterSendingPlatform implements StatusFooterSender, capturing every
// call so tests can verify the routing decision (footer-aware vs plain Send).
type stubFooterSendingPlatform struct {
	stubPlatformEngine
	footerCalls []string // "<content>|FOOTER|<footer>"
	failFooter  bool     // when true, SendWithStatusFooter returns an error
}

func (p *stubFooterSendingPlatform) SendWithStatusFooter(_ context.Context, _ any, content, footer string) error {
	p.footerCalls = append(p.footerCalls, content+"|FOOTER|"+footer)
	if p.failFooter {
		return fmt.Errorf("simulated SendWithStatusFooter failure")
	}
	return nil
}

func TestSendChunksWithStatusFooter_RoutesToFooterSender(t *testing.T) {
	p := &stubFooterSendingPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}
	send := func(pl Platform, rctx any, content string) error {
		return pl.Send(context.Background(), rctx, content)
	}
	ok := sendChunksWithStatusFooter(context.Background(), p, "ctx", "hello body", "model · ctx 5%", send)
	if !ok {
		t.Fatal("expected success")
	}
	if len(p.footerCalls) != 1 {
		t.Fatalf("expected 1 SendWithStatusFooter call, got %d", len(p.footerCalls))
	}
	if !strings.Contains(p.footerCalls[0], "hello body") || !strings.Contains(p.footerCalls[0], "model · ctx 5%") {
		t.Errorf("footer call payload = %q, missing body or footer", p.footerCalls[0])
	}
	// Plain Send must NOT be called when footer routing succeeds.
	if got := p.getSent(); len(got) != 0 {
		t.Errorf("plain Send fallthrough = %#v, want empty when footer routing succeeded", got)
	}
}

func TestSendChunksWithStatusFooter_FallsBackInlineWhenFooterSenderFails(t *testing.T) {
	p := &stubFooterSendingPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}, failFooter: true}
	send := func(pl Platform, rctx any, content string) error {
		return pl.Send(context.Background(), rctx, content)
	}
	ok := sendChunksWithStatusFooter(context.Background(), p, "ctx", "hello body", "model · ctx 5%", send)
	if !ok {
		t.Fatal("expected success after fallback")
	}
	if len(p.footerCalls) != 1 {
		t.Fatalf("expected 1 attempted footer call, got %d", len(p.footerCalls))
	}
	sent := p.getSent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 inline-fallback Send, got %#v", sent)
	}
	// Inline fallback applies appendReplyFooter using the upstream core footer shape.
	if !strings.Contains(sent[0], "hello body") || !strings.Contains(sent[0], "*model · ctx 5%*") {
		t.Errorf("inline fallback content = %q, missing body or footer", sent[0])
	}
}

func TestSendChunksWithStatusFooter_NoFooter(t *testing.T) {
	p := &stubFooterSendingPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}
	send := func(pl Platform, rctx any, content string) error {
		return pl.Send(context.Background(), rctx, content)
	}
	ok := sendChunksWithStatusFooter(context.Background(), p, "ctx", "hello body", "", send)
	if !ok {
		t.Fatal("expected success")
	}
	// No footer → routes via plain Send only, never tries SendWithStatusFooter.
	if len(p.footerCalls) != 0 {
		t.Errorf("expected 0 footer calls when statusFooter empty, got %d", len(p.footerCalls))
	}
	if got := p.getSent(); len(got) != 1 || got[0] != "hello body" {
		t.Errorf("plain Send sequence = %#v, want one chunk verbatim", got)
	}
}

func TestSendChunksWithStatusFooter_PlatformWithoutFooterSender(t *testing.T) {
	// stubPlatformEngine does NOT implement StatusFooterSender → must fall
	// through to inline appendReplyFooter on the last chunk.
	p := &stubPlatformEngine{n: "test"}
	send := func(pl Platform, rctx any, content string) error {
		return pl.Send(context.Background(), rctx, content)
	}
	ok := sendChunksWithStatusFooter(context.Background(), p, "ctx", "hello body", "model · ctx 5%", send)
	if !ok {
		t.Fatal("expected success")
	}
	sent := p.getSent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 send, got %#v", sent)
	}
	if !strings.Contains(sent[0], "*model · ctx 5%*") {
		t.Errorf("expected wrapped footer inline, got %q", sent[0])
	}
}

func TestAppendReplyFooter_UsesUpstreamWrappedFooterShape(t *testing.T) {
	got := appendReplyFooter("hello world", "line1 metrics\n~/path/to/ws")
	wantSubs := []string{
		"hello world",
		"\n\n*line1 metrics\n~/path/to/ws*",
	}
	for _, sub := range wantSubs {
		if !strings.Contains(got, sub) {
			t.Errorf("appendReplyFooter result missing %q\nfull: %q", sub, got)
		}
	}
	// Single-line footer keeps the platform-agnostic wrapped shape.
	got = appendReplyFooter("body", "single")
	if got != "body\n\n*single*" {
		t.Errorf("single-line append = %q, want %q", got, "body\n\n*single*")
	}
	// Empty footer is a no-op.
	if got := appendReplyFooter("body", ""); got != "body" {
		t.Errorf("empty footer should be no-op, got %q", got)
	}
}
