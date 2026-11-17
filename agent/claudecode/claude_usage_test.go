package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// Trimmed `claude -p /usage --output-format json` output from Claude Code
// 2.1.325 on a Max plan: no 5-hour line, plus a per-model weekly window.
const claudeUsageJSON2_1_325 = `{"is_error":false,"duration_api_ms":0,"num_turns":0,"session_id":"748892fa-7e9e-49f4-a787-80a3c1aac8b2","total_cost_usd":0,"subtype":"success","result":"You are currently using your subscription to power your Claude Code usage\n\nCurrent week (all models): 45% used · resets Nov 23 at 1:59am (Asia/Singapore)\nCurrent week (Fable): 82% used · resets Nov 23 at 1:59am (Asia/Singapore)\n\nWhat's contributing to your usage?\nApproximate · sessions on this machine only · shares overlap\n\nLast 24h · 1918 requests · 7 sessions\n  100%  Check which models your subagents use · share covers whole sessions, not only subagents\n  89%  Start fresh when you switch tasks · run /clear\n","local_command":"usage","type":"result"}`

func TestParseClaudeUsageOutput_CLI2_1_325(t *testing.T) {
	sg := mustLoadLocation(t, "Asia/Singapore")
	now := time.Date(2026, time.November, 17, 16, 0, 0, 0, sg)

	report, err := parseClaudeUsageOutput([]byte(claudeUsageJSON2_1_325), now)
	if err != nil {
		t.Fatalf("parseClaudeUsageOutput: %v", err)
	}
	if len(report.Buckets) != 1 {
		t.Fatalf("buckets = %+v, want 1", report.Buckets)
	}
	resetAt := time.Date(2026, time.November, 23, 1, 59, 0, 0, sg)
	want := []core.UsageWindow{
		{Name: "Current week (all models)", UsedPercent: 45, WindowSeconds: 604800, ResetAtUnix: resetAt.Unix(), ResetAfterSeconds: int(resetAt.Sub(now).Seconds())},
		{Name: "Current week (Fable)", Scope: "Fable", UsedPercent: 82, WindowSeconds: 604800, ResetAtUnix: resetAt.Unix(), ResetAfterSeconds: int(resetAt.Sub(now).Seconds())},
	}
	got := report.Buckets[0].Windows
	if len(got) != len(want) {
		t.Fatalf("windows = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("window[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Account-wide windows come first whatever order the CLI prints them in, so
// the card's headline numbers are the account's, not one model's.
func TestParseClaudeUsageText_SessionLineAndOrdering(t *testing.T) {
	sg := mustLoadLocation(t, "Asia/Singapore")
	now := time.Date(2026, time.November, 17, 16, 0, 0, 0, sg)
	text := "Current week (Sonnet only): 3% used · resets Nov 23 at 2am (Asia/Singapore)\n" +
		"Current session: 12% used · resets 5pm (Asia/Singapore)\n" +
		"Current week (all models): 45% used · resets Nov 23 at 2am (Asia/Singapore)\n"

	report, err := parseClaudeUsageText(text, now)
	if err != nil {
		t.Fatalf("parseClaudeUsageText: %v", err)
	}
	w := report.Buckets[0].Windows
	if len(w) != 3 {
		t.Fatalf("windows = %+v, want 3", w)
	}
	if w[0].WindowSeconds != 18000 || w[0].UsedPercent != 12 || w[0].Scope != "" || w[0].ResetAfterSeconds != 3600 {
		t.Fatalf("window[0] = %+v, want 5-hour 12%% resetting in 1h", w[0])
	}
	if w[1].WindowSeconds != 604800 || w[1].UsedPercent != 45 || w[1].Scope != "" {
		t.Fatalf("window[1] = %+v, want account-wide weekly 45%%", w[1])
	}
	if w[2].Scope != "Sonnet only" || w[2].UsedPercent != 3 {
		t.Fatalf("window[2] = %+v, want Sonnet-only weekly 3%%", w[2])
	}
}

func TestParseClaudeUsageText_UnparsedResetKeepsWindow(t *testing.T) {
	report, err := parseClaudeUsageText("Current week (all models): 45% used · resets soon-ish", time.Now())
	if err != nil {
		t.Fatalf("parseClaudeUsageText: %v", err)
	}
	w := report.Buckets[0].Windows[0]
	if w.UsedPercent != 45 || w.ResetAtUnix != 0 || w.ResetAfterSeconds != 0 {
		t.Fatalf("window = %+v, want 45%% without reset time", w)
	}
}

// API-key logins and third-party providers have no subscription windows; the
// CLI explains why, and that text is what the user should see.
func TestParseClaudeUsageOutput_NoWindowsQuotesCLI(t *testing.T) {
	out := `{"type":"result","subtype":"success","is_error":false,"result":"You are currently using API usage billing. /usage shows plan limits for Claude subscriptions only."}`
	_, err := parseClaudeUsageOutput([]byte(out), time.Now())
	if err == nil || !strings.Contains(err.Error(), "API usage billing") {
		t.Fatalf("err = %v, want the CLI's explanation", err)
	}
}

func TestParseClaudeUsageOutput_Errors(t *testing.T) {
	cases := map[string]struct {
		out  string
		want string
	}{
		"is_error": {`{"type":"result","is_error":true,"result":"Not logged in · Please run /login"}`, "Not logged in"},
		"not json": {"Error: unknown option '--no-session-persistence'", "unknown option"},
		"empty":    {`{"type":"result","is_error":false,"result":""}`, "no output"},
	}
	for name, tc := range cases {
		_, err := parseClaudeUsageOutput([]byte(tc.out), time.Now())
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want it to mention %q", name, err, tc.want)
		}
	}
}

func TestParseClaudeUsageResetTime(t *testing.T) {
	sg := mustLoadLocation(t, "Asia/Singapore")
	seoul := mustLoadLocation(t, "Asia/Seoul")
	now := time.Date(2026, time.November, 17, 16, 0, 0, 0, sg)
	cases := []struct {
		raw  string
		want time.Time
	}{
		{"Nov 23 at 2am (Asia/Singapore)", time.Date(2026, time.November, 23, 2, 0, 0, 0, sg)},
		{"Nov 23 at 1:59am (Asia/Singapore)", time.Date(2026, time.November, 23, 1, 59, 0, 0, sg)},
		{"Dec 23, 4:59pm (Asia/Seoul)", time.Date(2026, time.December, 23, 16, 59, 0, 0, seoul)},
		{"5pm (Asia/Singapore)", time.Date(2026, time.November, 17, 17, 0, 0, 0, sg)},
		// Already past today: tomorrow. Already past this year: next year.
		{"3pm (Asia/Singapore)", time.Date(2026, time.November, 18, 15, 0, 0, 0, sg)},
		{"Jan 2 at 3am (Asia/Singapore)", time.Date(2027, time.January, 2, 3, 0, 0, 0, sg)},
	}
	for _, tc := range cases {
		got, err := parseClaudeUsageResetTime(tc.raw, now)
		if err != nil {
			t.Fatalf("%q: %v", tc.raw, err)
		}
		if !got.Equal(tc.want) {
			t.Fatalf("%q = %v, want %v", tc.raw, got, tc.want)
		}
	}
	if _, err := parseClaudeUsageResetTime("Nov 23 at 2am (Mars/Base)", now); err == nil {
		t.Fatal("unknown timezone should fail")
	}
}

// The /usage command must run the same CLI the sessions run, with the same
// wrapper args and provider env, and without the nested-session marker.
func TestClaudeUsageCommand_MatchesSessionSpawn(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")

	a := &Agent{cmd: "my-claude", cliExtraArgs: []string{"code"}, configEnv: []string{"FOO=bar"}}
	cmd := a.claudeUsageCommand(context.Background())
	if got, want := strings.Join(cmd.Args[1:], " "), "code -p /usage --output-format json --no-session-persistence"; got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
	env := envSliceToMap(cmd.Env)
	if env["FOO"] != "bar" {
		t.Fatalf("configured env missing: FOO=%q", env["FOO"])
	}
	if _, ok := env["CLAUDECODE"]; ok {
		t.Fatal("CLAUDECODE must be filtered out")
	}

	wrapped := &Agent{cmd: "my-cli", cliExtraArgs: []string{"code"}, cmdArgsFlag: "-a"}
	cmd = wrapped.claudeUsageCommand(context.Background())
	if got, want := strings.Join(cmd.Args[1:], "|"), "code|-a|-p /usage --output-format json --no-session-persistence"; got != want {
		t.Fatalf("wrapped args = %q, want %q", got, want)
	}

	if cmd := (&Agent{}).claudeUsageCommand(context.Background()); filepath.Base(cmd.Args[0]) != "claude" {
		t.Fatalf("default binary = %q, want claude", cmd.Args[0])
	}
}

func writeFakeClaude(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in for the claude CLI")
	}
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAgentGetUsage_RunsCLI(t *testing.T) {
	jsonFile := filepath.Join(t.TempDir(), "out.json")
	if err := os.WriteFile(jsonFile, []byte(claudeUsageJSON2_1_325), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := writeFakeClaude(t, `[ "$1 $2" = "-p /usage" ] || { echo "unexpected args: $*" >&2; exit 2; }
cat '`+jsonFile+`'
`)

	report, err := (&Agent{cmd: bin}).GetUsage(context.Background())
	if err != nil {
		t.Fatalf("GetUsage: %v", err)
	}
	if w := report.Buckets[0].Windows; len(w) != 2 || w[1].Scope != "Fable" || w[1].UsedPercent != 82 {
		t.Fatalf("windows = %+v", w)
	}
}

func TestAgentGetUsage_CLIFailureQuotesStderr(t *testing.T) {
	bin := writeFakeClaude(t, "echo 'Invalid API key · Please run /login' >&2\nexit 1\n")

	_, err := (&Agent{cmd: bin}).GetUsage(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Invalid API key") {
		t.Fatalf("err = %v, want stderr quoted", err)
	}
}

func TestAgentGetUsage_TimeoutKillsCLI(t *testing.T) {
	bin := writeFakeClaude(t, "sleep 30\n")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := (&Agent{cmd: bin}).GetUsage(ctx)
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("GetUsage returned after %v; the CLI was not killed on timeout", elapsed)
	}
}

func TestAgentGetUsageSmoke(t *testing.T) {
	if os.Getenv("LARK_AGENT_BOT_SMOKE_CLAUDE_USAGE") == "" {
		t.Skip("set LARK_AGENT_BOT_SMOKE_CLAUDE_USAGE=1 to run")
	}
	// TestMain points HOME at an empty dir; the CLI would then see no login
	// and print the API-billing cost view instead of plan limits.
	useRealUserEnv(t)

	a := &Agent{workDir: ".", mode: "plan"}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	report, err := a.GetUsage(ctx)
	if err != nil {
		t.Fatalf("GetUsage returned error: %v", err)
	}
	if report == nil || len(report.Buckets) == 0 || len(report.Buckets[0].Windows) == 0 {
		t.Fatalf("unexpected empty report: %+v", report)
	}
	t.Logf("windows: %+v", report.Buckets[0].Windows)
}

func mustLoadLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return loc
}
