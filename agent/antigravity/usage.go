package antigravity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// usageArgs runs agy's own /usage in print mode. agy answers it locally: no
// model call, no quota used, no conversation saved.
var usageArgs = []string{"-p", "/usage", "--output-format", "json"}

// usageOutput is the part of `agy -p /usage --output-format json` the bot
// reads (agy 1.5.14):
//
//	{"status":"SUCCESS","error":"","command":{"name":"usage","data":{"groups":[
//	  {"name":"Gemini Models","buckets":[{"name":"Weekly Limit Remaining",
//	   "window":"weekly","remaining_fraction":0.97,"reset_time":"2026-11-28T11:58:53Z"}]}]}}}
type usageOutput struct {
	Status  string `json:"status"`
	Error   string `json:"error"`
	Command struct {
		Name string `json:"name"`
		Data struct {
			Groups []struct {
				Name    string `json:"name"`
				Buckets []struct {
					Name              string   `json:"name"`
					Window            string   `json:"window"`
					RemainingFraction *float64 `json:"remaining_fraction"`
					ResetTime         string   `json:"reset_time"`
				} `json:"buckets"`
			} `json:"groups"`
		} `json:"data"`
	} `json:"command"`
}

// GetUsage implements core.UsageReporter with agy's /usage. agy limits each
// model group (Gemini models, Claude and GPT models, ...) separately, with a
// weekly and a 5-hour window, so every window is reported scoped to its group.
func (a *Agent) GetUsage(ctx context.Context) (*core.UsageReport, error) {
	a.mu.RLock()
	args := append(append([]string(nil), a.cliExtraArgs...), usageArgs...)
	cmd := exec.CommandContext(ctx, a.cmd, args...)
	cmd.Env = core.MergeEnv(os.Environ(), append(append([]string(nil), a.configEnv...), a.sessionEnv...))
	a.mu.RUnlock()
	// A neutral directory keeps the work dir's rules and hooks out of the run.
	cmd.Dir = os.TempDir()
	cmd.WaitDelay = 2 * time.Second

	out, err := cmd.Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("antigravity: run /usage: %w", ctxErr)
		}
		// agy prints its JSON result, with the reason, even when it fails.
		if report, perr := parseUsage(out, time.Now()); perr == nil {
			return report, nil
		} else if len(out) > 0 {
			return nil, fmt.Errorf("antigravity: run /usage: %w: %w", err, perr)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("antigravity: run /usage: %w: %s", err, truncate(strings.TrimSpace(string(exitErr.Stderr)), 300))
		}
		return nil, fmt.Errorf("antigravity: run /usage: %w", err)
	}
	return parseUsage(out, time.Now())
}

func parseUsage(out []byte, now time.Time) (*core.UsageReport, error) {
	var res usageOutput
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("antigravity: /usage did not return JSON: %s", truncate(strings.TrimSpace(string(out)), 300))
	}
	if !strings.EqualFold(res.Status, "SUCCESS") {
		msg := strings.TrimSpace(res.Error)
		if msg == "" {
			msg = "status " + res.Status
		}
		return nil, fmt.Errorf("antigravity: /usage failed: %s", truncate(msg, 300))
	}
	if res.Command.Name != "usage" {
		return nil, fmt.Errorf("antigravity: /usage returned no usage data")
	}

	report := &core.UsageReport{Provider: "Antigravity"}
	for _, g := range res.Command.Data.Groups {
		bucket := core.UsageBucket{Name: g.Name, Allowed: true}
		for _, b := range g.Buckets {
			if b.RemainingFraction == nil {
				continue
			}
			remaining := math.Max(0, math.Min(1, *b.RemainingFraction))
			w := core.UsageWindow{
				Name:          b.Name,
				Scope:         g.Name,
				UsedPercent:   int(math.Round((1 - remaining) * 100)),
				WindowSeconds: windowSeconds(b.Window),
			}
			if t, err := time.Parse(time.RFC3339, b.ResetTime); err == nil {
				w.ResetAtUnix = t.Unix()
				if d := t.Sub(now); d > 0 {
					w.ResetAfterSeconds = int(d.Seconds())
				}
			}
			if remaining <= 0 {
				bucket.LimitReached = true
			}
			bucket.Windows = append(bucket.Windows, w)
		}
		if len(bucket.Windows) > 0 {
			report.Buckets = append(report.Buckets, bucket)
		}
	}
	if len(report.Buckets) == 0 {
		return nil, fmt.Errorf("antigravity: /usage reported no quota")
	}
	return report, nil
}

// windowSeconds maps agy's window names ("weekly", "5h") to their length.
func windowSeconds(window string) int {
	switch strings.ToLower(strings.TrimSpace(window)) {
	case "weekly", "week", "7d":
		return 7 * 24 * 60 * 60
	case "5h", "five_hour", "5_hour":
		return 5 * 60 * 60
	case "daily", "day", "24h":
		return 24 * 60 * 60
	}
	if d, err := time.ParseDuration(window); err == nil {
		return int(d.Seconds())
	}
	return 0
}
