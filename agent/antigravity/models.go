package antigravity

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// Before any session has run, /model has no session-reported list to show, so
// the bot asks `agy models` instead. It takes several seconds (agy fetches the
// list from its server), longer than /model waits, so the engine preloads it
// at start and every Agent running the same agy shares the result.

// modelsArgs lists agy's models: one "<id>\t<name>" line per model, after a
// "Fetching available models..." line (agy 1.5.14).
var modelsArgs = []string{"models"}

const (
	modelsFetchTimeout = 30 * time.Second
	// modelsRefreshAfter is how old a listed catalog may get before the next
	// read refreshes it in the background.
	modelsRefreshAfter = time.Hour
)

// modelCatalog is the `agy models` list for one agy command line.
type modelCatalog struct {
	mu       sync.Mutex
	models   []core.ModelOption
	listedAt time.Time
	fetching chan struct{} // closed when the running fetch ends; nil when idle
}

var (
	catalogsMu sync.Mutex
	catalogs   = map[string]*modelCatalog{}
)

func catalogFor(key string) *modelCatalog {
	catalogsMu.Lock()
	defer catalogsMu.Unlock()
	c := catalogs[key]
	if c == nil {
		c = &modelCatalog{}
		catalogs[key] = c
	}
	return c
}

// get returns the listed models, starting a fetch when there are none or they
// are stale. With none listed yet it waits for the fetch until ctx ends.
func (c *modelCatalog) get(ctx context.Context, fetch func(context.Context) ([]core.ModelOption, error)) []core.ModelOption {
	c.mu.Lock()
	models := c.models
	stale := len(models) == 0 || time.Since(c.listedAt) > modelsRefreshAfter
	done := c.fetching
	if stale && done == nil {
		done = make(chan struct{})
		c.fetching = done
		go c.run(done, fetch)
	}
	c.mu.Unlock()

	if len(models) > 0 || done == nil {
		return models
	}
	select {
	case <-done:
	case <-ctx.Done():
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.models
}

// run fetches the list on its own deadline, so a caller that stops waiting
// still leaves the result for the next one.
func (c *modelCatalog) run(done chan struct{}, fetch func(context.Context) ([]core.ModelOption, error)) {
	ctx, cancel := context.WithTimeout(context.Background(), modelsFetchTimeout)
	defer cancel()
	models, err := fetch(ctx)

	c.mu.Lock()
	if err != nil {
		slog.Warn("antigravity: list models", "error", err)
	} else if len(models) > 0 {
		c.models = models
		c.listedAt = time.Now()
	}
	c.fetching = nil
	c.mu.Unlock()
	close(done)
}

// catalogKey identifies the agy command line, so agents running another agy
// binary or flags keep their own list.
func (a *Agent) catalogKey() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return strings.Join(append([]string{a.cmd}, a.cliExtraArgs...), "\x00")
}

// listedModels returns `agy models`, waiting for it until ctx ends when it has
// not been listed yet.
func (a *Agent) listedModels(ctx context.Context) []core.ModelOption {
	return catalogFor(a.catalogKey()).get(ctx, a.runModels)
}

// PreloadModels implements core.ModelPreloader.
func (a *Agent) PreloadModels(ctx context.Context) {
	a.listedModels(ctx)
}

func (a *Agent) runModels(ctx context.Context) ([]core.ModelOption, error) {
	a.mu.RLock()
	args := append(append([]string(nil), a.cliExtraArgs...), modelsArgs...)
	cmd := exec.CommandContext(ctx, a.cmd, args...)
	cmd.Env = core.MergeEnv(os.Environ(), append(append([]string(nil), a.configEnv...), a.sessionEnv...))
	a.mu.RUnlock()
	cmd.Dir = os.TempDir()
	cmd.WaitDelay = 2 * time.Second

	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("antigravity: run agy models: %w: %s", err, truncate(strings.TrimSpace(string(exitErr.Stderr)), 300))
		}
		return nil, fmt.Errorf("antigravity: run agy models: %w", err)
	}
	models := parseModels(out)
	if len(models) == 0 {
		return nil, fmt.Errorf("antigravity: agy models listed no models: %s", truncate(strings.TrimSpace(string(out)), 300))
	}
	return models, nil
}

// parseModels turns `agy models` output into the base models a session offers.
// agy lists each model once per reasoning effort (gemini-4-neon-low,
// gemini-4-neon-high, ...), while a session's "model" option names the base
// model and sets the effort separately, and /model switches through that
// option. So a model listed with two or more efforts becomes its base model;
// one listed once keeps its full id, as the session option does
// (gpt-oss-120b-medium).
func parseModels(out []byte) []core.ModelOption {
	type entry struct{ id, name string }
	var entries []entry
	variants := map[string]int{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		id, name, ok := strings.Cut(sc.Text(), "\t")
		id, name = strings.TrimSpace(id), strings.TrimSpace(name)
		if !ok || id == "" || strings.ContainsAny(id, " ") {
			continue
		}
		entries = append(entries, entry{id, name})
		if base, _, ok := splitEffort(id); ok {
			variants[base]++
		}
	}

	var models []core.ModelOption
	seen := map[string]bool{}
	for _, e := range entries {
		id, name := e.id, e.name
		if base, effort, ok := splitEffort(id); ok && variants[base] >= 2 {
			id = base
			name = strings.TrimSpace(strings.TrimSuffix(name, "("+effortLabel(effort)+")"))
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, core.ModelOption{Name: id, Desc: name})
	}
	return models
}

// splitEffort splits a reasoning-effort suffix off an agy model id.
func splitEffort(id string) (base, effort string, ok bool) {
	i := strings.LastIndexByte(id, '-')
	if i <= 0 {
		return "", "", false
	}
	switch suffix := id[i+1:]; suffix {
	case "low", "medium", "high", "xhigh", "max":
		return id[:i], suffix, true
	}
	return "", "", false
}

// effortLabel is how agy names an effort in a model name: "XHigh", "High".
func effortLabel(effort string) string {
	if effort == "xhigh" {
		return "XHigh"
	}
	return strings.ToUpper(effort[:1]) + effort[1:]
}
