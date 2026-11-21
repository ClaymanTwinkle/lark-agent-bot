package antigravity

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// fakeModelsOutput is `agy models` as agy 1.5.14 prints it.
const fakeModelsOutput = "Fetching available models...\n" +
	"gemini-4-neon-high\tGemini 4 Neon (High)\n" +
	"gemini-4-neon-xhigh\tGemini 4 Neon (XHigh)\n" +
	"gemini-4-neon-medium\tGemini 4 Neon (Medium)\n" +
	"gemini-4-neon-low\tGemini 4 Neon (Low)\n" +
	"gemini-4-argon-medium\tGemini 4 Argon (Medium)\n" +
	"gemini-4-argon-high\tGemini 4 Argon (High)\n" +
	"gemini-4-argon-low\tGemini 4 Argon (Low)\n" +
	"gemini-4-hydrogen-high\tGemini 4 Hydrogen (High)\n" +
	"gemini-4-hydrogen-xhigh\tGemini 4 Hydrogen (XHigh)\n" +
	"gemini-4-hydrogen-medium\tGemini 4 Hydrogen (Medium)\n" +
	"gemini-4-hydrogen-low\tGemini 4 Hydrogen (Low)\n" +
	"gemini-pro-orchestrator-neon\tOrchestrator (Neon)\n" +
	"gemini-pro-orchestrator-g1-pro\tOrchestrator (Argon)\n" +
	"gemini-3.8-flash-high\tGemini 3.8 Flash (High)\n" +
	"gemini-3.8-flash-medium\tGemini 3.8 Flash (Medium)\n" +
	"gemini-3.8-flash-low\tGemini 3.8 Flash (Low)\n" +
	"claude-opus-5-5-low\tClaude Opus 5.5 (Low)\n" +
	"claude-opus-5-5-medium\tClaude Opus 5.5 (Medium)\n" +
	"claude-opus-5-5-high\tClaude Opus 5.5 (High)\n" +
	"claude-sonnet-5-5-low\tClaude Sonnet 5.5 (Low)\n" +
	"claude-sonnet-5-5-medium\tClaude Sonnet 5.5 (Medium)\n" +
	"claude-sonnet-5-5-high\tClaude Sonnet 5.5 (High)\n" +
	"gpt-oss-120b-medium\tGPT-OSS 120B (Medium)\n"

func resetCatalogs(t *testing.T) {
	t.Helper()
	catalogsMu.Lock()
	catalogs = map[string]*modelCatalog{}
	catalogsMu.Unlock()
}

func modelNames(models []core.ModelOption) []string {
	var names []string
	for _, m := range models {
		names = append(names, m.Name+"="+m.Desc)
	}
	return names
}

// The base ids must match the "model" option a real agy 1.5.14 session
// offers, which /model switches through.
func TestParseModels_MatchesSessionModelOption(t *testing.T) {
	got := modelNames(parseModels([]byte(fakeModelsOutput)))
	want := []string{
		"gemini-4-neon=Gemini 4 Neon",
		"gemini-4-argon=Gemini 4 Argon",
		"gemini-4-hydrogen=Gemini 4 Hydrogen",
		"gemini-pro-orchestrator-neon=Orchestrator (Neon)",
		"gemini-pro-orchestrator-g1-pro=Orchestrator (Argon)",
		"gemini-3.8-flash=Gemini 3.8 Flash",
		"claude-opus-5-5=Claude Opus 5.5",
		"claude-sonnet-5-5=Claude Sonnet 5.5",
		"gpt-oss-120b-medium=GPT-OSS 120B (Medium)",
	}
	if !slices.Equal(got, want) {
		t.Errorf("parseModels =\n%v\nwant\n%v", got, want)
	}
}

func TestParseModels_IgnoresNoise(t *testing.T) {
	if got := parseModels([]byte("Fetching available models...\n\nerror: not signed in\n")); len(got) != 0 {
		t.Errorf("parseModels = %v, want none", got)
	}
}

func TestAvailableModels_BeforeSessionListsAgyModels(t *testing.T) {
	resetCatalogs(t)
	a, logPath := newFakeAgent(t, "", "")
	got := modelNames(a.AvailableModels(context.Background()))
	if len(got) != 9 || got[0] != "gemini-4-neon=Gemini 4 Neon" {
		t.Fatalf("AvailableModels = %v", got)
	}
	if e, ok := find(readLog(t, logPath), "argv"); !ok || string(e.Params) != `["models"]` {
		t.Errorf("argv = %s, want [\"models\"]", e.Params)
	}
}

func TestAvailableModels_SessionListWins(t *testing.T) {
	resetCatalogs(t)
	a, _ := newFakeAgent(t, "", "")
	startFake(t, a, "")
	got := modelNames(a.AvailableModels(context.Background()))
	if !slices.Equal(got, []string{"gemini-a=Gemini A", "gemini-b=Gemini B"}) {
		t.Errorf("AvailableModels = %v, want the session's list", got)
	}
}

func TestModelCatalog_FetchesOnceForConcurrentCallers(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	fetch := func(context.Context) ([]core.ModelOption, error) {
		calls.Add(1)
		<-release
		return []core.ModelOption{{Name: "m"}}, nil
	}
	c := &modelCatalog{}
	var wg sync.WaitGroup
	results := make([][]core.ModelOption, 5)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = c.get(context.Background(), fetch)
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Errorf("fetches = %d, want 1", n)
	}
	for i, r := range results {
		if len(r) != 1 {
			t.Errorf("caller %d got %v", i, r)
		}
	}
	if r := c.get(context.Background(), fetch); len(r) != 1 || calls.Load() != 1 {
		t.Errorf("cached get = %v after %d fetches, want the cached list without a fetch", r, calls.Load())
	}
}

// A caller that gives up (the /model card waits only a few seconds) still
// leaves the result for the next one.
func TestModelCatalog_SlowFetchServesNextCaller(t *testing.T) {
	release := make(chan struct{})
	fetch := func(context.Context) ([]core.ModelOption, error) {
		<-release
		return []core.ModelOption{{Name: "m"}}, nil
	}
	c := &modelCatalog{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if r := c.get(ctx, fetch); len(r) != 0 {
		t.Fatalf("get before the fetch ended = %v, want none", r)
	}
	close(release)
	if r := c.get(context.Background(), fetch); len(r) != 1 {
		t.Errorf("get after the fetch = %v", r)
	}
}

func TestModelCatalog_FailedFetchIsRetried(t *testing.T) {
	var calls atomic.Int32
	fetch := func(context.Context) ([]core.ModelOption, error) {
		if calls.Add(1) == 1 {
			return nil, context.DeadlineExceeded
		}
		return []core.ModelOption{{Name: "m"}}, nil
	}
	c := &modelCatalog{}
	if r := c.get(context.Background(), fetch); len(r) != 0 {
		t.Fatalf("first get = %v, want none", r)
	}
	if r := c.get(context.Background(), fetch); len(r) != 1 {
		t.Errorf("second get = %v, want the retried list", r)
	}
}

func TestModelCatalog_StaleListRefreshesInBackground(t *testing.T) {
	var calls atomic.Int32
	fetch := func(context.Context) ([]core.ModelOption, error) {
		calls.Add(1)
		return []core.ModelOption{{Name: "new"}}, nil
	}
	c := &modelCatalog{models: []core.ModelOption{{Name: "old"}}, listedAt: time.Now().Add(-2 * modelsRefreshAfter)}
	if r := c.get(context.Background(), fetch); len(r) != 1 || r[0].Name != "old" {
		t.Fatalf("stale get = %v, want the old list right away", r)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		name := c.models[0].Name
		c.mu.Unlock()
		if name == "new" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("stale list was not refreshed (fetches = %d)", calls.Load())
}
