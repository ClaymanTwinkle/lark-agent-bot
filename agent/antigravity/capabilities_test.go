package antigravity

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

func TestSession_ReasoningEffort(t *testing.T) {
	a, logPath := newFakeAgent(t, "", "")
	if got := a.AvailableReasoningEfforts(); !slices.Equal(got, []string{"low", "medium", "high", "xhigh", "max"}) {
		t.Errorf("efforts before any session = %v", got)
	}
	a.SetReasoningEffort("LOW")
	startFake(t, a, "")

	e, ok := find(readLog(t, logPath), "session/set_config_option")
	if !ok || !strings.Contains(string(e.Params), `"configId":"thought_level"`) || !strings.Contains(string(e.Params), `"value":"low"`) {
		t.Fatalf("session/set_config_option params = %s", e.Params)
	}
	if got := a.AvailableReasoningEfforts(); !slices.Equal(got, []string{"low", "medium", "high"}) {
		t.Errorf("efforts = %v, want agy's thought_level values", got)
	}
	if got := a.GetReasoningEffort(); got != "low" {
		t.Errorf("GetReasoningEffort = %q", got)
	}
}

func TestSession_EffortAlreadyCurrentIsNotResent(t *testing.T) {
	a, logPath := newFakeAgent(t, "", "")
	a.SetReasoningEffort("high")
	startFake(t, a, "")
	if _, ok := find(readLog(t, logPath), "session/set_config_option"); ok {
		t.Errorf("set_config_option sent although agy already uses high")
	}
}

func TestSession_StartupWarnings(t *testing.T) {
	a, _ := newFakeAgent(t, "auto", "bad-model") // the fake offers no auto mode
	a.SetReasoningEffort("bogus")
	a.lang = core.LangChinese
	s := startFake(t, a, "")

	warning := s.StartupWarning()
	for _, want := range []string{`模型 "bad-model"`, `推理强度设为 "bogus"`, `模式 "auto"`} {
		if !strings.Contains(warning, want) {
			t.Errorf("warning %q missing %q", warning, want)
		}
	}
	if s.CurrentSessionID() != "conv-1" {
		t.Errorf("session should still start with defaults")
	}

	ok, _ := newFakeAgent(t, "plan", "gemini-b")
	if w := startFake(t, ok, "").StartupWarning(); w != "" {
		t.Errorf("unexpected warning %q", w)
	}
}

func TestSession_ContextUsage(t *testing.T) {
	a, _ := newFakeAgent(t, "", "")
	s := startFake(t, a, "")
	if s.GetContextUsage() != nil {
		t.Errorf("usage before any usage_update")
	}
	runTurn(t, s, "usage please", nil, "allow")
	u := s.GetContextUsage()
	if u == nil || u.UsedTokens != 1200 || u.ContextWindow != 200000 {
		t.Fatalf("usage = %+v", u)
	}
}

func TestWorkspaceAgentOptions_RecreateEquivalentAgent(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{
		cmd: bin, cliExtraArgs: []string{"--flag"}, mode: modePlan, model: "gemini-b", effort: "low",
		configEnv: []string{"FOO=bar"},
	}
	opts := a.WorkspaceAgentOptions()
	opts["work_dir"] = t.TempDir()
	created, err := New(opts)
	if err != nil {
		t.Fatalf("New(snapshot): %v", err)
	}
	b := created.(*Agent)
	if b.cmd != bin || !slices.Equal(b.cliExtraArgs, []string{"--flag"}) || b.mode != modePlan || b.model != "gemini-b" ||
		b.effort != "low" || !slices.Equal(b.configEnv, []string{"FOO=bar"}) {
		t.Errorf("recreated agent = %+v", b)
	}
}

func TestDirsAndCompress(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	wd := t.TempDir()
	a := &Agent{workDir: wd}
	if got := a.SkillDirs(); !slices.Equal(got, []string{filepath.Join(wd, ".agents", "skills"), filepath.Join(home, ".gemini", "config", "skills")}) {
		t.Errorf("SkillDirs = %v", got)
	}
	if got := a.CommandDirs(); !slices.Equal(got, []string{filepath.Join(wd, ".agents", "workflows"), filepath.Join(home, ".gemini", "config", "workflows")}) {
		t.Errorf("CommandDirs = %v", got)
	}
	if a.CompressCommand() != "/compact" {
		t.Errorf("CompressCommand = %q", a.CompressCommand())
	}
}

func TestSamePathResolvesSymlinks(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if !samePath(link, real) || samePath(real, t.TempDir()) {
		t.Errorf("samePath wrong")
	}
}

func TestSession_LegacyModelIDUsesSetModel(t *testing.T) {
	a, logPath := newFakeAgent(t, "", "gemini-b-low")
	s := startFake(t, a, "")
	if w := s.StartupWarning(); w != "" {
		t.Errorf("warning %q", w)
	}
	if e, ok := find(readLog(t, logPath), "session/set_model"); !ok || !strings.Contains(string(e.Params), `"modelId":"gemini-b-low"`) {
		t.Errorf("session/set_model params = %s", e.Params)
	}
	if a.GetModel() != "gemini-b-low" {
		t.Errorf("GetModel = %q", a.GetModel())
	}
}

func TestSession_ModelListIsBaseModels(t *testing.T) {
	a, _ := newFakeAgent(t, "", "")
	startFake(t, a, "")
	var names []string
	for _, m := range a.AvailableModels(context.Background()) {
		names = append(names, m.Name+"="+m.Desc)
	}
	if !slices.Equal(names, []string{"gemini-a=Gemini A", "gemini-b=Gemini B"}) {
		t.Errorf("models = %v, want the model config option's base models", names)
	}
	if a.GetModel() != "gemini-a" {
		t.Errorf("GetModel = %q, want agy's current base model", a.GetModel())
	}
}

func TestSession_ResultCarriesTokenUsage(t *testing.T) {
	a, _ := newFakeAgent(t, "", "")
	s := startFake(t, a, "")
	events := runTurn(t, s, "usage please", nil, "allow")
	r := events[len(events)-1]
	if r.Type != core.EventResult || r.InputTokens != 900 || r.OutputTokens != 40 || r.CacheReadInputTokens != 300 {
		t.Errorf("result = %+v", r)
	}
}

func TestSession_CrashMidTurnReportedOnce(t *testing.T) {
	a, _ := newFakeAgent(t, "", "")
	s := startFake(t, a, "")
	events := runTurn(t, s, "crash now", nil, "allow")
	if events[len(events)-1].Type != core.EventError {
		t.Fatalf("events = %+v", events)
	}
	select {
	case <-s.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("agy did not exit")
	}
	time.Sleep(300 * time.Millisecond)
	select {
	case ev, ok := <-s.Events():
		if ok && ev.Type == core.EventError {
			t.Errorf("crash reported twice: %v", ev.Error)
		}
	default:
	}
}

// waitFor reads events until one of type typ, failing after timeout.
func waitFor(t *testing.T, s *session, typ core.EventType, timeout time.Duration) []core.Event {
	t.Helper()
	var got []core.Event
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-s.Events():
			got = append(got, ev)
			if ev.Type == typ {
				return got
			}
		case <-deadline:
			t.Fatalf("no %s within %s; got %+v", typ, timeout, got)
		}
	}
}

func withAgentTurnTimings(t *testing.T, settle, fallback time.Duration) {
	t.Helper()
	oldSettle, oldFallback, oldTick := agentTurnSettle, agentTurnFallback, agentTurnTick
	agentTurnSettle, agentTurnFallback, agentTurnTick = settle, fallback, 20*time.Millisecond
	t.Cleanup(func() { agentTurnSettle, agentTurnFallback, agentTurnTick = oldSettle, oldFallback, oldTick })
}

func TestSession_AgentWakesUpAfterBackgroundTask(t *testing.T) {
	withAgentTurnTimings(t, 150*time.Millisecond, 10*time.Second)
	a, _ := newFakeAgent(t, "", "")
	s := startFake(t, a, "")

	events := runTurn(t, s, "run it in the background", nil, "allow")
	if got := textOf(events, core.EventText); got != "started" {
		t.Fatalf("prompted turn text = %q", got)
	}
	tasks := s.BackgroundTasks()
	if len(tasks) != 1 || tasks[0].ID != "bg1" || tasks[0].Description != "sleep 1; echo bg-done" {
		t.Fatalf("background tasks after the turn = %+v", tasks)
	}

	start := time.Now()
	woke := waitFor(t, s, core.EventResult, 5*time.Second)
	if got := textOf(woke, core.EventText); got != "bg-done" {
		t.Errorf("agent turn text = %q", got)
	}
	if res := eventOf(t, woke, core.EventToolResult); res.ToolResult != "bg-done" {
		t.Errorf("background tool result = %+v", res)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("agent turn took %s to end; the usage_update should settle it quickly", elapsed)
	}
	if tasks := s.BackgroundTasks(); len(tasks) != 0 {
		t.Errorf("background tasks after completion = %+v", tasks)
	}

	// The session takes prompts normally afterwards.
	if got := textOf(runTurn(t, s, "plain", nil, "allow"), core.EventText); got != "plain reply" {
		t.Errorf("next turn text = %q", got)
	}
}

func TestSession_AgentTurnFallbackWithoutUsageUpdate(t *testing.T) {
	withAgentTurnTimings(t, 50*time.Millisecond, 700*time.Millisecond)
	a, _ := newFakeAgent(t, "", "")
	s := startFake(t, a, "")
	runTurn(t, s, "background silent", nil, "allow")

	start := time.Now()
	woke := waitFor(t, s, core.EventResult, 5*time.Second)
	if textOf(woke, core.EventText) != "bg-done" {
		t.Errorf("agent turn = %+v", woke)
	}
	if elapsed := time.Since(start); elapsed < 700*time.Millisecond {
		t.Errorf("turn ended after %s, before the fallback", elapsed)
	}
}

func TestAgentTurnTracker(t *testing.T) {
	withAgentTurnTimings(t, time.Second, 10*time.Second)
	now := time.Unix(1000, 0)
	var tr agentTurnTracker

	tr.observe("usage_update", "", "", false, now)
	if tr.opened() {
		t.Fatal("bookkeeping update opened a turn")
	}
	tr.observe("agent_message_chunk", "", "", true, now)
	tr.observe("tool_call", "t1", "in_progress", true, now)
	tr.observe("usage_update", "", "", false, now)
	if tr.finish(now.Add(time.Hour), 0) {
		t.Error("ended while its own tool call runs")
	}
	tr.observe("tool_call_update", "t1", "completed", true, now)
	if tr.finish(now.Add(5*time.Second), 0) {
		t.Error("ended before the fallback without a usage_update")
	}
	tr.observe("usage_update", "", "", false, now)
	if tr.finish(now.Add(500*time.Millisecond), 0) {
		t.Error("ended before settling")
	}
	if tr.finish(now.Add(2*time.Second), 1) {
		t.Error("ended with a permission request pending")
	}
	if !tr.finish(now.Add(2*time.Second), 0) || tr.opened() {
		t.Error("did not end once settled")
	}
}
