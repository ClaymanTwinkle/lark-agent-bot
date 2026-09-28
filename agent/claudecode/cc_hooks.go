package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ccPermissionHookEntry represents one PermissionRequest hook entry from
// Claude Code's settings.json.
type ccPermissionHookEntry struct {
	Matcher string `json:"matcher"`
	Hooks   []struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	} `json:"hooks"`
}

// ccHooksSection is the "hooks" section of Claude Code settings.json.
type ccHooksSection struct {
	PermissionRequest []ccPermissionHookEntry `json:"PermissionRequest"`
}

// ccSettings is the relevant subset of Claude Code settings.json.
type ccSettings struct {
	Hooks ccHooksSection `json:"hooks"`
}

// ccHookDecision is the parsed decision from a hook's stdout.
type ccHookDecision struct {
	Behavior string // "allow", "deny", or "" (ask/fallthrough)
	Message  string // reason for deny (optional)
}

// hookContext carries the session context needed to build hook stdin.
// Fields mirror Claude Code's PermissionRequest hook input spec.
type hookContext struct {
	sessionID             string
	toolName              string
	toolInput             map[string]any
	cwd                   string
	permissionMode        string
	transcriptPath        string
	permissionSuggestions []any  // nil = omit from stdin
	agentID               string // empty = omit
	agentType             string // empty = omit
}

// ccPermissionHookRunner reads and executes Claude Code PermissionRequest
// hooks. Settings are loaded once on first use and reused for the session.
type ccPermissionHookRunner struct {
	workDir  string
	once     sync.Once
	settings *ccSettings
}

func newCCPermissionHookRunner(workDir string) *ccPermissionHookRunner {
	return &ccPermissionHookRunner{
		workDir: workDir,
	}
}

// tryHook finds and executes a matching PermissionRequest hook.
// Returns (decision, true) if a hook matched and produced allow/deny.
// Returns (_, false) if no hook matched, hook returned "ask", or any error.
func (r *ccPermissionHookRunner) tryHook(
	ctx context.Context,
	hctx hookContext,
) (ccHookDecision, bool) {
	r.once.Do(func() { r.settings = r.loadSettings() })
	if r.settings == nil {
		return ccHookDecision{}, false
	}
	settings := r.settings

	for _, entry := range settings.Hooks.PermissionRequest {
		if !matchHookEntry(entry.Matcher, hctx.toolName) {
			continue
		}
		for _, h := range entry.Hooks {
			if h.Type != "command" || h.Command == "" {
				continue
			}
			stdinData := buildHookStdin(hctx)
			decision, err := runHookCommand(ctx, h.Command, stdinData)
			if err != nil {
				slog.Warn("ccHooks: hook command failed",
					"command", truncateStr(h.Command, 80), "error", err)
				continue
			}
			if decision.Behavior == "allow" || decision.Behavior == "deny" {
				return decision, true
			}
			// "ask" or empty — fall through to next hook / platform
		}
	}
	return ccHookDecision{}, false
}

// loadSettings reads and merges settings.json files. Called once per session.
func (r *ccPermissionHookRunner) loadSettings() *ccSettings {
	merged := &ccSettings{}
	found := false
	for _, p := range settingsPaths(r.workDir) {
		s, err := readSettingsFile(p)
		if err != nil {
			if !os.IsNotExist(err) {
				slog.Debug("ccHooks: skipping settings file", "path", p, "error", err)
			}
			continue
		}
		merged.Hooks.PermissionRequest = append(merged.Hooks.PermissionRequest, s.Hooks.PermissionRequest...)
		found = true
	}
	if !found {
		slog.Debug("ccHooks: no settings files found")
		return nil
	}
	return merged
}

// settingsPaths returns the settings.json paths to read, in order.
func settingsPaths(workDir string) []string {
	configHome := claudeConfigHomeDir()
	paths := []string{
		filepath.Join(configHome, "settings.json"),
		filepath.Join(configHome, "settings.local.json"),
	}
	if workDir != "" {
		paths = append(paths,
			filepath.Join(workDir, ".claude", "settings.json"),
			filepath.Join(workDir, ".claude", "settings.local.json"),
		)
	}
	return paths
}

// readSettingsFile reads a single settings.json, stripping JSONC comments.
func readSettingsFile(path string) (*ccSettings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cleaned := stripJSONC(data)
	var s ccSettings
	if err := json.Unmarshal(cleaned, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &s, nil
}

// stripJSONC removes // and /* */ comments from JSONC, preserving strings.
func stripJSONC(data []byte) []byte {
	var out bytes.Buffer
	inString := false
	escaped := false
	i := 0
	for i < len(data) {
		ch := data[i]

		if escaped {
			out.WriteByte(ch)
			escaped = false
			i++
			continue
		}

		if inString {
			out.WriteByte(ch)
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			i++
			continue
		}

		// Not in string.
		if ch == '"' {
			inString = true
			out.WriteByte(ch)
			i++
			continue
		}

		if ch == '/' && i+1 < len(data) {
			next := data[i+1]
			if next == '/' {
				// Line comment — skip to end of line.
				i += 2
				for i < len(data) && data[i] != '\n' {
					i++
				}
				continue
			}
			if next == '*' {
				// Block comment — skip to closing */.
				i += 2
				closed := false
				for i+1 < len(data) {
					if data[i] == '*' && data[i+1] == '/' {
						i += 2
						closed = true
						break
					}
					i++
				}
				if !closed {
					return data // return original; json.Unmarshal will give a clear error
				}
				continue
			}
		}

		out.WriteByte(ch)
		i++
	}
	return out.Bytes()
}

// matchHookEntry checks if toolName matches the matcher.
// Empty or "*" matcher matches everything. Otherwise exact match.
func matchHookEntry(matcher, toolName string) bool {
	if matcher == "" || matcher == "*" {
		return true
	}
	return strings.EqualFold(matcher, toolName)
}

// runHookCommand executes a hook command with tool info on stdin.
// Returns the parsed decision. Timeout: 60s (matching Claude Code's own).
func runHookCommand(
	ctx context.Context,
	command string,
	stdinData map[string]any,
) (ccHookDecision, error) {
	stdinJSON, err := json.Marshal(stdinData)
	if err != nil {
		return ccHookDecision{}, fmt.Errorf("marshal stdin: %w", err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	shell, err := permissionHookShell()
	if err != nil {
		return ccHookDecision{}, fmt.Errorf("hook shell: %w", err)
	}
	cmd := exec.CommandContext(timeoutCtx, shell, "-c", command)
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(stdinJSON)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Strip the skip flag so the hook does real work when lark-connect
	// calls it (even if the host environment has it set).
	cmd.Env = filterEnv(os.Environ(), "LARK_CONNECT_PERMISSION_HOOK_SKIP")

	if err := cmd.Run(); err != nil {
		return ccHookDecision{}, fmt.Errorf("hook exec: %w (stderr: %s)", err, truncateStr(strings.TrimSpace(stderr.String()), 200))
	}

	return parseHookOutput(stdout.Bytes())
}

// Git for Windows normally adds only Git's cmd directory to PATH. Locate its
// shell explicitly so hooks keep their POSIX syntax on Windows as well.
func permissionHookShell() (string, error) {
	if runtime.GOOS == "windows" {
		if path := os.Getenv("CLAUDE_CODE_GIT_BASH_PATH"); path != "" {
			return exec.LookPath(path)
		}
	}
	if path, err := exec.LookPath("sh"); err == nil {
		return path, nil
	}
	if runtime.GOOS == "windows" {
		if git, err := exec.LookPath("git.exe"); err == nil {
			root := filepath.Dir(filepath.Dir(git))
			for _, path := range []string{filepath.Join(root, "bin", "bash.exe"), filepath.Join(root, "usr", "bin", "sh.exe")} {
				if shell, err := exec.LookPath(path); err == nil {
					return shell, nil
				}
			}
		}
	}
	return "", fmt.Errorf("POSIX shell not found; install sh or configure CLAUDE_CODE_GIT_BASH_PATH on Windows")
}

// parseHookOutput parses hook stdout into a decision.
func parseHookOutput(data []byte) (ccHookDecision, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return ccHookDecision{}, nil // empty = ask/fallthrough
	}

	// Try plain text first: "allow", "deny", "ask".
	text := strings.ToLower(string(trimmed))
	switch text {
	case "allow":
		return ccHookDecision{Behavior: "allow"}, nil
	case "deny":
		return ccHookDecision{Behavior: "deny"}, nil
	case "ask":
		return ccHookDecision{}, nil
	}

	// Try structured JSON output.
	var out struct {
		HookSpecificOutput struct {
			Decision struct {
				Behavior string `json:"behavior"`
				Message  string `json:"message"`
			} `json:"decision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(trimmed, &out); err != nil {
		return ccHookDecision{}, fmt.Errorf("parse hook output: %w", err)
	}
	behavior := strings.ToLower(out.HookSpecificOutput.Decision.Behavior)
	if behavior == "allow" || behavior == "deny" {
		return ccHookDecision{
			Behavior: behavior,
			Message:  out.HookSpecificOutput.Decision.Message,
		}, nil
	}
	return ccHookDecision{}, nil
}

// buildHookStdin constructs the JSON payload for the hook's stdin,
// matching Claude Code's PermissionRequest hook input spec.
func buildHookStdin(hctx hookContext) map[string]any {
	m := map[string]any{
		"session_id":      hctx.sessionID,
		"hook_event_name": "PermissionRequest",
		"tool_name":       hctx.toolName,
		"tool_input":      hctx.toolInput,
	}
	if hctx.cwd != "" {
		m["cwd"] = hctx.cwd
	}
	if hctx.permissionMode != "" {
		m["permission_mode"] = hctx.permissionMode
	}
	if hctx.transcriptPath != "" {
		m["transcript_path"] = hctx.transcriptPath
	}
	if hctx.permissionSuggestions != nil {
		m["permission_suggestions"] = hctx.permissionSuggestions
	}
	if hctx.agentID != "" {
		m["agent_id"] = hctx.agentID
	}
	if hctx.agentType != "" {
		m["agent_type"] = hctx.agentType
	}
	return m
}

// truncateStr truncates s to maxLen characters, appending "..." if truncated.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
