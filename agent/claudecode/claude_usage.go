package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

const (
	claudeUsageSessionWindowSeconds = 5 * 60 * 60
	claudeUsageWeekWindowSeconds    = 7 * 24 * 60 * 60

	// claudeUsageOutputLimit caps how much CLI output is quoted in errors.
	claudeUsageOutputLimit = 300
)

// claudeUsageArgs runs Claude Code's own /usage as a headless local command:
// it never calls the model, costs no quota, and leaves no session transcript.
var claudeUsageArgs = []string{"-p", "/usage", "--output-format", "json", "--no-session-persistence"}

var (
	// "Current week (all models): 45% used · resets Nov 23 at 2am (Asia/Singapore)"
	claudeUsageLineRe = regexp.MustCompile(`(?i)^\s*(current\s+[^:]+?)\s*:\s*(\d{1,3})\s*%\s*used\b(?:.*?\bresets\s+(.+?))?\s*$`)
	// "Current week (Fable)", "2am (Asia/Singapore)": text plus a trailing
	// parenthesized qualifier.
	claudeUsageParenRe = regexp.MustCompile(`^(.*?)\s*\(([^()]+)\)\s*$`)
)

// claudeUsageResult is the part of `claude -p --output-format json` output
// that GetUsage reads.
type claudeUsageResult struct {
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
}

// GetUsage reports the subscription quota by running
//
//	claude -p /usage --output-format json --no-session-persistence
//
// The CLI handles login and token refresh itself, so this works wherever the
// agent's sessions work. Since Claude Code 2.1 a run takes roughly 15 seconds
// because /usage also summarizes local transcripts; callers need a timeout
// well above that.
func (a *Agent) GetUsage(ctx context.Context) (*core.UsageReport, error) {
	cmd := a.claudeUsageCommand(ctx)
	out, err := cmd.Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("claudecode: run /usage: %w", ctxErr)
		}
		detail := strings.TrimSpace(string(out))
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			detail = strings.TrimSpace(string(exitErr.Stderr))
		}
		if res, ok := decodeClaudeUsageResult(out); ok && res.Result != "" {
			detail = res.Result
		}
		if detail != "" {
			return nil, fmt.Errorf("claudecode: run /usage: %w: %s", err, truncateClaudeUsageText(detail))
		}
		return nil, fmt.Errorf("claudecode: run /usage: %w", err)
	}
	return parseClaudeUsageOutput(out, time.Now())
}

// claudeUsageCommand builds the /usage command the same way sessions are
// spawned: same binary and wrapper args, same provider env, same run_as_user
// isolation.
func (a *Agent) claudeUsageCommand(ctx context.Context) *exec.Cmd {
	a.mu.RLock()
	bin := a.cmd
	args := append([]string(nil), a.cliExtraArgs...)
	argsFlag := a.cmdArgsFlag
	spawnOpts := a.spawnOpts
	a.mu.RUnlock()
	extraEnv := a.usageProbeEnv()

	if bin == "" {
		bin = "claude"
	}
	if argsFlag != "" {
		args = append(args, argsFlag, shellJoinArgs(claudeUsageArgs))
	} else {
		args = append(args, claudeUsageArgs...)
	}

	cmd := core.BuildSpawnCommand(ctx, spawnOpts, bin, args...)
	// A neutral directory keeps project settings and hooks of whatever
	// directory the bot was started from out of the run.
	cmd.Dir = os.TempDir()
	env := filterEnv(os.Environ(), "CLAUDECODE")
	if len(extraEnv) > 0 {
		env = core.MergeEnv(env, extraEnv)
	}
	cmd.Env = core.FilterEnvForSpawn(env, spawnOpts)
	// On timeout, kill the whole process group, not just the direct child.
	prepareCmdForKill(cmd)
	cmd.Cancel = func() error { return forceKillCmd(cmd) }
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

// usageProbeEnv returns the provider/runtime env the /usage command runs with.
func (a *Agent) usageProbeEnv() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.runtimeEnvLocked()
}

func decodeClaudeUsageResult(out []byte) (claudeUsageResult, bool) {
	var res claudeUsageResult
	if err := json.Unmarshal(out, &res); err != nil {
		return claudeUsageResult{}, false
	}
	return res, true
}

func parseClaudeUsageOutput(out []byte, now time.Time) (*core.UsageReport, error) {
	res, ok := decodeClaudeUsageResult(out)
	if !ok {
		return nil, fmt.Errorf("claudecode: /usage did not return JSON: %s", truncateClaudeUsageText(string(out)))
	}
	if res.IsError {
		return nil, fmt.Errorf("claudecode: /usage failed: %s", truncateClaudeUsageText(res.Result))
	}
	return parseClaudeUsageText(res.Result, now)
}

// parseClaudeUsageText reads the "Current …: N% used · resets …" lines of
// the /usage text. Which windows appear depends on the plan and the moment:
// the 5-hour "Current session" line is absent while no 5-hour window is
// running, and per-model weekly windows (e.g. "Current week (Fable)") come
// and go. Without any such line (API-key login, third-party provider) the
// CLI's own explanation is returned as the error.
func parseClaudeUsageText(text string, now time.Time) (*core.UsageReport, error) {
	var accountWide, scoped []core.UsageWindow
	for _, line := range strings.Split(text, "\n") {
		w, ok := parseClaudeUsageLine(line, now)
		if !ok {
			continue
		}
		if w.Scope == "" {
			accountWide = append(accountWide, w)
		} else {
			scoped = append(scoped, w)
		}
	}
	if len(accountWide)+len(scoped) == 0 {
		msg := strings.TrimSpace(text)
		if msg == "" {
			return nil, fmt.Errorf("claudecode: /usage returned no output")
		}
		return nil, fmt.Errorf("claudecode: /usage reported no quota windows: %s", truncateClaudeUsageText(msg))
	}
	// Account-wide windows first: the usage card and reply footer pick the
	// first 5-hour and the first weekly window as the headline numbers.
	return &core.UsageReport{
		Provider: "claudecode",
		Buckets: []core.UsageBucket{{
			Name:    "Usage",
			Allowed: true,
			Windows: append(accountWide, scoped...),
		}},
	}, nil
}

func parseClaudeUsageLine(line string, now time.Time) (core.UsageWindow, bool) {
	m := claudeUsageLineRe.FindStringSubmatch(line)
	if m == nil {
		return core.UsageWindow{}, false
	}
	name := strings.TrimSpace(m[1])
	base, scope := name, ""
	if sm := claudeUsageParenRe.FindStringSubmatch(name); sm != nil {
		base = strings.TrimSpace(sm[1])
		if s := strings.TrimSpace(sm[2]); !strings.EqualFold(s, "all models") {
			scope = s
		}
	}

	var seconds int
	switch lower := strings.ToLower(base); {
	case strings.HasPrefix(lower, "current session"):
		seconds = claudeUsageSessionWindowSeconds
	case strings.HasPrefix(lower, "current week"):
		seconds = claudeUsageWeekWindowSeconds
	default:
		slog.Debug("claudecode: skipping unknown /usage window", "name", name)
		return core.UsageWindow{}, false
	}

	pct, _ := strconv.Atoi(m[2])
	w := core.UsageWindow{
		Name:          name,
		Scope:         scope,
		UsedPercent:   pct,
		WindowSeconds: seconds,
	}
	if raw := strings.TrimSpace(m[3]); raw != "" {
		resetAt, err := parseClaudeUsageResetTime(raw, now)
		if err != nil {
			slog.Debug("claudecode: unparsed /usage reset time", "raw", raw, "error", err)
		} else {
			w.ResetAtUnix = resetAt.Unix()
			if after := int(resetAt.Sub(now).Round(time.Second).Seconds()); after > 0 {
				w.ResetAfterSeconds = after
			}
		}
	}
	return w, true
}

// parseClaudeUsageResetTime parses reset labels such as "Nov 23 at 2am
// (Asia/Singapore)", "Nov 23 at 1:59am (Asia/Singapore)", "Dec 23, 4:59pm"
// and "5pm (Asia/Singapore)". Labels carry no year; a date already past is
// taken to be next year's, a time already past today is taken as tomorrow.
func parseClaudeUsageResetTime(raw string, now time.Time) (time.Time, error) {
	label := strings.TrimSpace(raw)
	loc := now.Location()
	if m := claudeUsageParenRe.FindStringSubmatch(label); m != nil {
		tz, err := time.LoadLocation(strings.TrimSpace(m[2]))
		if err != nil {
			return time.Time{}, fmt.Errorf("unknown timezone %q", m[2])
		}
		label, loc = strings.TrimSpace(m[1]), tz
	}
	label = strings.ReplaceAll(label, ",", " ")
	label = strings.Join(strings.Fields(label), " ")
	label = strings.Replace(label, " at ", " ", 1)

	nowIn := now.In(loc)
	for _, layout := range []string{"Jan 2 3:04pm", "Jan 2 3pm"} {
		if t, err := time.ParseInLocation(layout, label, loc); err == nil {
			resetAt := time.Date(nowIn.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, loc)
			if !resetAt.After(nowIn) {
				resetAt = resetAt.AddDate(1, 0, 0)
			}
			return resetAt, nil
		}
	}
	for _, layout := range []string{"3:04pm", "3pm"} {
		if t, err := time.ParseInLocation(layout, label, loc); err == nil {
			resetAt := time.Date(nowIn.Year(), nowIn.Month(), nowIn.Day(), t.Hour(), t.Minute(), 0, 0, loc)
			if !resetAt.After(nowIn) {
				resetAt = resetAt.Add(24 * time.Hour)
			}
			return resetAt, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported reset time %q", raw)
}

func truncateClaudeUsageText(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= claudeUsageOutputLimit {
		return s
	}
	cut := claudeUsageOutputLimit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
