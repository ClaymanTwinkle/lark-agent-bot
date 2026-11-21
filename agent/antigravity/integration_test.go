package antigravity

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// Opt-in test against the real, signed-in Antigravity CLI. It spends model
// quota and creates conversations, so it only runs when
// LARK_AGY_INTEGRATION=1. It works in a fresh temp dir:
//
//	LARK_AGY_INTEGRATION=1 go test ./agent/antigravity/ -run TestIntegration -v
func TestIntegration_RealAgy(t *testing.T) {
	if os.Getenv("LARK_AGY_INTEGRATION") != "1" {
		t.Skip("real agy test not requested (set LARK_AGY_INTEGRATION=1)")
	}
	bin, err := exec.LookPath("agy")
	if err != nil {
		t.Fatalf("agy not in PATH: %v", err)
	}
	if os.Getenv("LARK_AGY_DEBUG") == "1" {
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
		t.Cleanup(func() { slog.SetDefault(prev) })
	}
	a := &Agent{
		workDir:      t.TempDir(),
		cmd:          bin,
		mode:         modeDefault,
		effort:       "low",
		instructions: "When asked for the bot codeword, answer exactly: PELICAN-42.",
	}
	ctx := context.Background()

	s := startReal(t, a, ctx, "")
	id := s.CurrentSessionID()
	var models []string
	for _, m := range a.AvailableModels(ctx) {
		models = append(models, m.Name)
	}
	t.Logf("session id %s, mode %q, model %q of %v, effort %q of %v", id, a.GetMode(), a.GetModel(), models, a.GetReasoningEffort(), a.AvailableReasoningEfforts())
	if w := s.StartupWarning(); w != "" {
		t.Errorf("startup warning: %s", w)
	}
	if report, err := a.GetUsage(ctx); err != nil {
		t.Errorf("GetUsage: %v", err)
	} else {
		for _, b := range report.Buckets {
			for _, w := range b.Windows {
				t.Logf("usage %s / %s: %d%% used, resets in %ds", w.Scope, w.Name, w.UsedPercent, w.ResetAfterSeconds)
			}
		}
	}

	events := realTurn(t, s, "Run the shell command `echo lab-smoke-ok` and tell me exactly what it printed.")
	if !strings.Contains(textOf(events, core.EventText), "lab-smoke-ok") {
		t.Errorf("reply does not contain the command output")
	}

	if u := s.GetContextUsage(); u == nil {
		t.Errorf("no context usage after a turn")
	} else {
		t.Logf("context usage: %d of %d tokens", u.UsedTokens, u.ContextWindow)
	}

	events = realTurn(t, s, "What is the bot codeword? Reply with the codeword only.")
	if !strings.Contains(textOf(events, core.EventText), "PELICAN-42") {
		t.Errorf("system prompt append did not reach agy")
	}

	// agy waits up to 5s for a command before moving it to the background,
	// so a 25s command ends up there unless the model chooses to wait for it.
	bg := realTurn(t, s, "Start the shell command `sleep 25; echo bg-done` as a background task (do not wait for it), then immediately end your turn replying only: started. When the background task finishes later, reply with its output.")
	t.Logf("background tasks after the turn: %+v", s.BackgroundTasks())
	if strings.Contains(textOf(bg, core.EventText), "bg-done") {
		t.Logf("the model waited for the command within its turn; no wake-up to check this run")
	} else {
		woke := agentTurn(t, s)
		if !strings.Contains(textOf(woke, core.EventText), "bg-done") {
			t.Errorf("agy did not report the background task's output on its own")
		}
		t.Logf("background tasks after waking: %+v", s.BackgroundTasks())
	}

	// /stop: cancel a long answer mid-way; the session must stay usable.
	t.Logf(">>> (cancel mid-turn)")
	if err := s.Send("Write a 1500-word essay on the history of bridges. Do not use tools.", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	for ev := range s.Events() {
		if ev.Type == core.EventText {
			break
		}
	}
	cancelAt := time.Now()
	if err := s.CancelTurn(); err != nil {
		t.Fatalf("CancelTurn: %v", err)
	}
	for ev := range s.Events() {
		if ev.Type == core.EventError {
			t.Fatalf("cancel ended the turn with an error: %v", ev.Error)
		}
		if ev.Type == core.EventResult {
			t.Logf("  cancelled turn ended after %s", time.Since(cancelAt))
			break
		}
	}
	if got := textOf(realTurn(t, s, "Reply with exactly: ALIVE"), core.EventText); !strings.Contains(got, "ALIVE") {
		t.Errorf("session not usable after cancel: %q", got)
	}

	realTurn(t, s, a.CompressCommand())
	if u := s.GetContextUsage(); u != nil {
		t.Logf("context usage after compact: %d of %d tokens", u.UsedTokens, u.ContextWindow)
	}
	if !s.SetLiveMode(modePlan) || !s.SetLiveMode(modeDefault) {
		t.Errorf("live mode switch failed")
	}
	_ = s.Close()

	resumed := startReal(t, a, ctx, id)
	if got := resumed.CurrentSessionID(); got != id {
		t.Errorf("resumed session id = %q, want %q", got, id)
	}
	events = realTurn(t, resumed, "What exact text did the shell command print earlier in this conversation? Reply with that text only.")
	if !strings.Contains(textOf(events, core.EventText), "lab-smoke-ok") {
		t.Errorf("resumed conversation lost its history")
	}
	_ = resumed.Close()

	sessions, err := a.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	found := false
	for _, info := range sessions {
		t.Logf("listed %s %q %s", info.ID, info.Summary, info.ModifiedAt.Format(time.RFC3339))
		found = found || info.ID == id
	}
	if !found {
		t.Errorf("ListSessions did not include %s", id)
	}
}

func startReal(t *testing.T, a *Agent, ctx context.Context, resumeID string) *session {
	t.Helper()
	as, err := a.StartSession(ctx, resumeID)
	if err != nil {
		t.Fatalf("StartSession(%q): %v", resumeID, err)
	}
	s := as.(*session)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// realTurn runs one turn, approving any permission request, and logs every event.
func realTurn(t *testing.T, s *session, prompt string) []core.Event {
	t.Helper()
	t.Logf(">>> %s", prompt)
	if err := s.Send(prompt, "", nil, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var got []core.Event
	timeout := time.After(5 * time.Minute)
	for {
		select {
		case ev := <-s.Events():
			got = append(got, ev)
			switch ev.Type {
			case core.EventText, core.EventThinking:
				t.Logf("  %s %q", ev.Type, truncate(ev.Content, 200))
			case core.EventToolUse:
				t.Logf("  tool_use name=%q input=%q", ev.ToolName, ev.ToolInput)
			case core.EventToolResult:
				ok := ev.ToolSuccess != nil && *ev.ToolSuccess
				t.Logf("  tool_result name=%q status=%q success=%v result=%q", ev.ToolName, ev.ToolStatus, ok, truncate(ev.ToolResult, 200))
			case core.EventPermissionRequest:
				t.Logf("  permission id=%s name=%q input=%q", ev.RequestID, ev.ToolName, ev.ToolInput)
				if err := s.RespondPermission(ev.RequestID, core.PermissionResult{Behavior: "allow"}); err != nil {
					t.Errorf("RespondPermission: %v", err)
				}
			case core.EventResult:
				t.Logf("  result session=%s", ev.SessionID)
				return got
			case core.EventError:
				t.Fatalf("turn failed: %v", ev.Error)
			default:
				t.Logf("  %s %+v", ev.Type, ev)
			}
		case <-timeout:
			t.Fatalf("turn timed out; got %d events", len(got))
		}
	}
}

// agentTurn waits for a turn agy starts on its own and collects it,
// approving any permission request.
func agentTurn(t *testing.T, s *session) []core.Event {
	t.Helper()
	t.Logf("<<< waiting for agy to wake up on its own")
	var got []core.Event
	timeout := time.After(3 * time.Minute)
	for {
		select {
		case ev := <-s.Events():
			got = append(got, ev)
			t.Logf("  %s %q %q %q", ev.Type, truncate(ev.Content, 120), ev.ToolName, truncate(ev.ToolResult, 120))
			switch ev.Type {
			case core.EventPermissionRequest:
				_ = s.RespondPermission(ev.RequestID, core.PermissionResult{Behavior: "allow"})
			case core.EventResult:
				return got
			case core.EventError:
				t.Fatalf("agent turn failed: %v", ev.Error)
			}
		case <-timeout:
			t.Fatalf("agent turn did not end; got %d events", len(got))
		}
	}
}
