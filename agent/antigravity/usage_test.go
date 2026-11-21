package antigravity

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Captured from `agy -p /usage --output-format json` (agy 1.5.14), with the
// long descriptions shortened.
const realUsageJSON = `{"conversation_id":"","status":"SUCCESS","response":"Gemini Models\tWeekly Limit Remaining\t98%\t2026-11-28T11:58:53Z\n","duration_seconds":0,"num_turns":0,"usage":{"input_tokens":0,"output_tokens":0,"thinking_tokens":0,"cache_read_tokens":0,"total_tokens":0},"command":{"name":"usage","data":{"description":"Within each group, models share a weekly limit and a 5-hour limit.","groups":[{"name":"Gemini Models","description":"Models within this group: Gemini 4 Neon, Gemini 4 Hydrogen, Orchestrator (Neon), Gemini 3.8 Flash","buckets":[{"id":"gemini-weekly","name":"Weekly Limit Remaining","description":"You have used some of your weekly limit.","window":"weekly","remaining_fraction":0.9780411720275879,"reset_time":"2026-11-28T11:58:53Z"},{"id":"gemini-5h","name":"Five Hour Limit Remaining","window":"5h","remaining_fraction":0.9731438755989075,"reset_time":"2026-11-21T16:58:53Z"}]},{"name":"Claude and GPT models","description":"Models within this group: Claude Opus 5.5, Claude Sonnet 5.5, GPT-OSS 120B","buckets":[{"id":"3p-weekly","name":"Weekly Limit Remaining","window":"weekly","remaining_fraction":1,"reset_time":"2026-11-28T13:36:02Z"},{"id":"3p-5h","name":"Five Hour Limit Remaining","window":"5h","remaining_fraction":0,"reset_time":"2026-11-21T18:36:02Z"}]},{"name":"Gemini 4 Argon","buckets":[{"id":"gemini-argon-wtu-weekly","name":"Gemini 4 Argon Weekly Limit","window":"weekly","remaining_fraction":1,"reset_time":"2026-11-28T13:36:02Z"}]}]}}}`

func TestParseUsage_RealOutput(t *testing.T) {
	now := time.Date(2026, 11, 21, 13, 36, 0, 0, time.UTC)
	report, err := parseUsage([]byte(realUsageJSON), now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Provider != "Antigravity" || len(report.Buckets) != 3 {
		t.Fatalf("report = %+v", report)
	}
	gemini := report.Buckets[0]
	if gemini.Name != "Gemini Models" || len(gemini.Windows) != 2 || gemini.LimitReached {
		t.Fatalf("gemini bucket = %+v", gemini)
	}
	weekly, fiveHour := gemini.Windows[0], gemini.Windows[1]
	if weekly.Scope != "Gemini Models" || weekly.WindowSeconds != 7*24*3600 || weekly.UsedPercent != 2 {
		t.Errorf("weekly = %+v", weekly)
	}
	if fiveHour.WindowSeconds != 5*3600 || fiveHour.UsedPercent != 3 {
		t.Errorf("5h = %+v", fiveHour)
	}
	if want := time.Date(2026, 11, 21, 16, 58, 53, 0, time.UTC); fiveHour.ResetAtUnix != want.Unix() || fiveHour.ResetAfterSeconds != int(want.Sub(now).Seconds()) {
		t.Errorf("5h reset = %d / %ds", fiveHour.ResetAtUnix, fiveHour.ResetAfterSeconds)
	}
	thirdParty := report.Buckets[1]
	if !thirdParty.LimitReached || thirdParty.Windows[1].UsedPercent != 100 {
		t.Errorf("exhausted 5h window not reported: %+v", thirdParty)
	}
	if argon := report.Buckets[2]; argon.Name != "Gemini 4 Argon" || len(argon.Windows) != 1 {
		t.Errorf("argon bucket = %+v", argon)
	}
}

func TestParseUsage_Failures(t *testing.T) {
	cases := map[string]string{
		`not json`: "did not return JSON",
		`{"status":"ERROR","error":"Eligibility check failed: not available in your location."}`: "Eligibility check failed",
		`{"status":"SUCCESS","command":{"name":"credits","data":{}}}`:                            "no usage data",
		`{"status":"SUCCESS","command":{"name":"usage","data":{"groups":[]}}}`:                   "no quota",
	}
	for out, want := range cases {
		if _, err := parseUsage([]byte(out), time.Now()); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("parseUsage(%s) err = %v, want %q", out, err, want)
		}
	}
}

func TestGetUsage_RunsAgyUsage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script as the fake agy")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	bin := filepath.Join(dir, "agy")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n" +
		"if [ \"$FAKE_FAIL\" = 1 ]; then echo '{\"status\":\"ERROR\",\"error\":\"Authentication required\"}'; exit 1; fi\n" +
		"cat <<'JSON'\n" + realUsageJSON + "\nJSON\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	a := &Agent{cmd: bin, cliExtraArgs: []string{"--extra"}}
	report, err := a.GetUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Buckets) != 3 {
		t.Errorf("buckets = %d", len(report.Buckets))
	}
	args, _ := os.ReadFile(argsFile)
	if got := strings.Fields(string(args)); strings.Join(got, " ") != "--extra -p /usage --output-format json" {
		t.Errorf("agy args = %q", got)
	}

	a.configEnv = []string{"FAKE_FAIL=1"}
	if _, err := a.GetUsage(context.Background()); err == nil || !strings.Contains(err.Error(), "Authentication required") {
		t.Errorf("err = %v, want agy's reason", err)
	}
}
