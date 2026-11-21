package antigravity

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// listTimeout bounds the one-off `agy acp` run that lists conversations.
var listTimeout = 15 * time.Second

type acpSessionInfo struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updatedAt"`
}

// ListSessions lists agy's conversations for the work dir. It runs a short
// `agy acp` that only calls initialize and session/list, so no conversation
// is created.
func (a *Agent) ListSessions(ctx context.Context) ([]core.AgentSessionInfo, error) {
	a.mu.RLock()
	command := a.cmd
	args := append(append([]string(nil), a.cliExtraArgs...), "acp")
	env := append(append([]string(nil), a.configEnv...), a.sessionEnv...)
	workDir := a.workDir
	a.mu.RUnlock()
	if abs, err := filepath.Abs(workDir); err == nil {
		workDir = abs
	}

	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()

	cmd := exec.Command(command, args...)
	cmd.Dir = workDir
	cmd.Env = core.MergeEnv(os.Environ(), env)
	stderr := newTailBuffer(4 << 10)
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("antigravity: list: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("antigravity: list: stdout pipe: %w", err)
	}
	var tr *transport
	tr = newTransport(stdout, stdin, nil, func(_ string, id json.RawMessage, _ json.RawMessage) {
		_ = tr.respondError(id, -32601, "method not supported by lark-agent-bot")
	})
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("antigravity: list: start %s: %w", command, err)
	}
	exited := make(chan struct{})
	go func() {
		tr.readLoop()
		_ = cmd.Wait()
		close(exited)
	}()
	defer func() {
		_ = stdin.Close()
		select {
		case <-exited:
		case <-time.After(closeGrace):
			_ = cmd.Process.Kill()
			<-exited
		}
	}()

	if _, err := tr.call(ctx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{"fs": map[string]any{"readTextFile": false, "writeTextFile": false}, "terminal": false},
		"clientInfo":         map[string]any{"name": "lark-agent-bot", "version": "1"},
	}); err != nil {
		return nil, fmt.Errorf("antigravity: list: initialize: %w (agy stderr: %s)", err, truncate(strings.TrimSpace(stderr.String()), 300))
	}

	var all []acpSessionInfo
	params := map[string]any{"cwd": workDir}
	for page := 0; page < 20; page++ {
		raw, err := tr.call(ctx, "session/list", params)
		if err != nil {
			return nil, fmt.Errorf("antigravity: session/list: %w", err)
		}
		var res struct {
			Sessions   []acpSessionInfo `json:"sessions"`
			NextCursor string           `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, fmt.Errorf("antigravity: parse session/list: %w", err)
		}
		all = append(all, res.Sessions...)
		if res.NextCursor == "" {
			break
		}
		params = map[string]any{"cwd": workDir, "cursor": res.NextCursor}
	}
	return toSessionInfos(all, workDir), nil
}

// toSessionInfos converts session/list entries, keeping only the work dir's
// (in case agy ignores the cwd filter).
func toSessionInfos(entries []acpSessionInfo, workDir string) []core.AgentSessionInfo {
	out := make([]core.AgentSessionInfo, 0, len(entries))
	for _, e := range entries {
		if e.Cwd != "" && !samePath(e.Cwd, workDir) {
			continue
		}
		info := core.AgentSessionInfo{ID: e.SessionID, Summary: strings.TrimSpace(e.Title)}
		if t, err := time.Parse(time.RFC3339Nano, e.UpdatedAt); err == nil {
			info.ModifiedAt = t
		}
		out = append(out, info)
	}
	return out
}

// samePath reports whether two paths name the same directory, also after
// resolving symlinks (on macOS /var is a link to /private/var).
func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
