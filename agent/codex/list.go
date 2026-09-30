package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// resolveCodexHomeDir returns the effective CODEX_HOME directory.
// Priority: explicit config value > CODEX_HOME env > ~/.codex
func resolveCodexHomeDir(explicit string) string {
	if h := strings.TrimSpace(explicit); h != "" {
		return h
	}
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(homeDir, ".codex")
}

// listCodexSessions scans the codex sessions directory for JSONL transcript
// files whose cwd matches workDir.
func listCodexSessions(workDir, codexHome string) ([]core.AgentSessionInfo, error) {
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		absWorkDir = workDir
	}

	sessionsDir := filepath.Join(resolveCodexHomeDir(codexHome), "sessions")

	var files []string
	_ = filepath.Walk(sessionsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".jsonl") {
			files = append(files, path)
		}
		return nil
	})

	if len(files) == 0 {
		return nil, nil
	}

	sessionTitles := loadCodexSessionTitles(codexHome)
	var sessions []core.AgentSessionInfo
	byID := make(map[string]int)
	for _, f := range files {
		info := parseCodexSessionFile(f, absWorkDir)
		if info == nil {
			continue
		}
		// Codex continues a long-running thread in further rollout files
		// (rollout-…-<thread>_<segment>.jsonl) that share the thread's
		// session_meta id. List the thread once: one row per file showed the
		// same session several times, each with a different title.
		if i, ok := byID[info.ID]; ok {
			mergeCodexSessionInfo(&sessions[i], *info)
			continue
		}
		byID[info.ID] = len(sessions)
		sessions = append(sessions, *info)
	}
	for i := range sessions {
		if title := sessionTitles[sessions[i].ID]; title != "" {
			if titleRunes := []rune(title); len(titleRunes) > 60 {
				title = string(titleRunes[:60]) + "..."
			}
			sessions[i].Summary = title
		}
		patchSessionSource(sessions[i].ID, codexHome)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ModifiedAt.After(sessions[j].ModifiedAt)
	})

	return sessions, nil
}

// mergeCodexSessionInfo folds another rollout file of the same thread into
// dst. Continuation files hold only the turns written to them, so message
// counts add up; the newest file supplies the time and the latest prompt.
func mergeCodexSessionInfo(dst *core.AgentSessionInfo, src core.AgentSessionInfo) {
	dst.MessageCount += src.MessageCount
	if src.ModifiedAt.After(dst.ModifiedAt) {
		dst.ModifiedAt = src.ModifiedAt
		if src.Summary != "" {
			dst.Summary = src.Summary
		}
	} else if dst.Summary == "" {
		dst.Summary = src.Summary
	}
}

// forEachLine calls fn with every non-empty line of r until fn returns false.
// Lines have no size cap: one rollout line (a compaction, an image, a large
// tool output) can be many megabytes, and a capped bufio.Scanner silently
// stopped reading at the first such line.
func forEachLine(r io.Reader, fn func(line []byte) bool) error {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			if !fn(trimmed) {
				return nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// loadCodexSessionTitles reads the same generated thread names that Codex uses
// in its session picker. Later entries win because renames append a new record.
func loadCodexSessionTitles(codexHome string) map[string]string {
	path := filepath.Join(resolveCodexHomeDir(codexHome), "session_index.jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Warn("codex: failed to close session index", "path", path, "error", err)
		}
	}()

	titles := make(map[string]string)
	err = forEachLine(f, func(line []byte) bool {
		var entry struct {
			ID         string `json:"id"`
			ThreadName string `json:"thread_name"`
		}
		if json.Unmarshal(line, &entry) == nil && entry.ID != "" && strings.TrimSpace(entry.ThreadName) != "" {
			titles[entry.ID] = entry.ThreadName
		}
		return true
	})
	if err != nil {
		slog.Warn("codex: failed to read session index", "path", path, "error", err)
	}
	return titles
}

// parseCodexSessionFile reads a Codex JSONL transcript.
// Returns nil if the session's cwd doesn't match filterCwd.
func parseCodexSessionFile(path, filterCwd string) *core.AgentSessionInfo {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil
	}

	var sessionID string
	var sessionCwd string
	var sessionSource json.RawMessage
	var summary string
	var msgCount int

	readErr := forEachLine(f, func(line []byte) bool {
		var entry struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(line, &entry); err != nil {
			return true
		}

		switch entry.Type {
		case "session_meta":
			if sessionID != "" {
				return true
			}
			var meta struct {
				ID     string          `json:"id"`
				Cwd    string          `json:"cwd"`
				Source json.RawMessage `json:"source"`
			}
			if json.Unmarshal(entry.Payload, &meta) == nil {
				sessionID = meta.ID
				sessionCwd = meta.Cwd
				sessionSource = meta.Source
			}

		case "response_item":
			var item struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if json.Unmarshal(entry.Payload, &item) == nil {
				if item.Role == "user" {
					msgCount++
					// The actual user prompt is the last user response_item
					// (earlier ones are system/AGENTS.md instructions).
					// Pick the last content block that looks like a real prompt.
					for _, c := range item.Content {
						if c.Type == "input_text" && c.Text != "" && isUserPrompt(c.Text) {
							summary = c.Text
						}
					}
				} else if item.Role == "assistant" {
					msgCount++
				}
			}
		}
		return true
	})
	if readErr != nil {
		slog.Warn("codex: failed to read session transcript", "path", path, "error", readErr)
	}

	// Filter by cwd
	if filterCwd != "" && sessionCwd != "" && sessionCwd != filterCwd {
		return nil
	}

	if sessionID == "" {
		return nil
	}
	if isSubagentSessionSource(sessionSource) {
		return nil
	}

	if len([]rune(summary)) > 60 {
		summary = string([]rune(summary)[:60]) + "..."
	}

	return &core.AgentSessionInfo{
		ID:           sessionID,
		Summary:      summary,
		MessageCount: msgCount,
		ModifiedAt:   stat.ModTime(),
	}
}

// isSubagentSessionSource reports whether Codex recorded the rollout as an
// internal subagent thread rather than a top-level user session.
func isSubagentSessionSource(source json.RawMessage) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(source, &object) != nil {
		return false
	}
	_, ok := object["subagent"]
	return ok
}

// findSessionFiles returns every JSONL transcript of a session: the file it
// started in and the continuation files Codex writes later, oldest first
// (rollout file names start with their creation time).
func findSessionFiles(sessionID, codexHome string) []string {
	if sessionID == "" {
		return nil
	}
	sessionsDir := filepath.Join(resolveCodexHomeDir(codexHome), "sessions")

	var found []string
	_ = filepath.Walk(sessionsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".jsonl") && strings.Contains(filepath.Base(path), sessionID) {
			found = append(found, path)
		}
		return nil
	})
	sort.Slice(found, func(i, j int) bool { return filepath.Base(found[i]) < filepath.Base(found[j]) })
	return found
}

// getSessionHistory reads a session's transcripts and returns user/assistant
// messages in order.
func getSessionHistory(sessionID, codexHome string, limit int) ([]core.HistoryEntry, error) {
	paths := findSessionFiles(sessionID, codexHome)
	if len(paths) == 0 {
		return nil, fmt.Errorf("session file not found for %s", sessionID)
	}

	var entries []core.HistoryEntry
	for _, path := range paths {
		fileEntries, err := readSessionHistoryFile(path)
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
	}

	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

func readSessionHistoryFile(path string) ([]core.HistoryEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var entries []core.HistoryEntry
	err = forEachLine(f, func(line []byte) bool {
		var raw struct {
			Timestamp string          `json:"timestamp"`
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(line, &raw) != nil || raw.Type != "response_item" {
			return true
		}

		var item struct {
			Role    string `json:"role"`
			Type    string `json:"type"`
			Text    string `json:"text"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(raw.Payload, &item) != nil {
			return true
		}

		ts, _ := time.Parse(time.RFC3339Nano, raw.Timestamp)

		switch {
		case item.Role == "user" && len(item.Content) > 0:
			for _, c := range item.Content {
				if c.Type == "input_text" && c.Text != "" && isUserPrompt(c.Text) {
					entries = append(entries, core.HistoryEntry{
						Role: "user", Content: c.Text, Timestamp: ts,
					})
				}
			}
		case item.Role == "assistant" && len(item.Content) > 0:
			for _, c := range item.Content {
				if c.Type == "output_text" && c.Text != "" {
					entries = append(entries, core.HistoryEntry{
						Role: "assistant", Content: c.Text, Timestamp: ts,
					})
				}
			}
		case item.Type == "reasoning" && item.Text != "":
			// skip reasoning items
		}
		return true
	})
	return entries, err
}

// patchSessionSource rewrites the session_meta line in a session's Codex JSONL
// transcripts so that source="cli" and originator="codex_cli_rs", making the
// session visible in the interactive `codex` terminal.
func patchSessionSource(sessionID, codexHome string) {
	for _, path := range findSessionFiles(sessionID, codexHome) {
		patchSessionSourceFile(path)
	}
}

func patchSessionSourceFile(path string) {
	// Only exec-sourced transcripts need the rewrite; check the first line
	// before reading what can be a many-megabyte file.
	if !firstLineContains(path, []byte(`"source":"exec"`)) {
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	idx := bytes.IndexByte(data, '\n')
	if idx < 0 {
		return
	}
	firstLine := data[:idx]
	if !bytes.Contains(firstLine, []byte(`"source":"exec"`)) {
		return
	}

	patched := bytes.Replace(firstLine, []byte(`"source":"exec"`), []byte(`"source":"cli"`), 1)
	patched = bytes.Replace(patched, []byte(`"originator":"codex_exec"`), []byte(`"originator":"codex_cli_rs"`), 1)

	if bytes.Equal(patched, firstLine) {
		return
	}

	out := make([]byte, 0, len(patched)+len(data)-idx)
	out = append(out, patched...)
	out = append(out, data[idx:]...)

	_ = os.WriteFile(path, out, 0o644)
}

func firstLineContains(path string, needle []byte) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	var found bool
	_ = forEachLine(f, func(line []byte) bool {
		found = bytes.Contains(line, needle)
		return false
	})
	return found
}

// isUserPrompt returns true if the text looks like an actual user prompt
// rather than system context (AGENTS.md, environment_context, permissions, etc.)
func isUserPrompt(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	// Skip XML-style system context
	if strings.HasPrefix(t, "<") {
		return false
	}
	// Skip AGENTS.md instructions injected by Codex
	if strings.HasPrefix(t, "# AGENTS.md") || strings.HasPrefix(t, "#AGENTS.md") {
		return false
	}
	return true
}
