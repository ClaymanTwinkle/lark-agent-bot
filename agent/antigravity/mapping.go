package antigravity

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// toolCacheMax bounds the toolCallId → tool info cache kept per session.
const toolCacheMax = 1000

// toolInfo is what the bot remembers about a tool call between its
// tool_call announcement and later updates or permission requests.
type toolInfo struct {
	name  string
	input string
}

// updateMapper turns ACP session/update payloads into core events. It keeps
// per-tool-call state so a tool's result is reported under the same name as
// its call, which is how the engine pairs them into one step. It is only
// used from the transport's read loop, so it needs no locking.
type updateMapper struct {
	tools map[string]toolInfo
	order []string // insertion order, for eviction
}

func newUpdateMapper() *updateMapper {
	return &updateMapper{tools: make(map[string]toolInfo)}
}

func (m *updateMapper) remember(id string, info toolInfo) {
	if id == "" {
		return
	}
	if _, ok := m.tools[id]; !ok {
		m.order = append(m.order, id)
	}
	m.tools[id] = info
	for len(m.order) > toolCacheMax {
		delete(m.tools, m.order[0])
		m.order = m.order[1:]
	}
}

// lookup returns what is known about a tool call.
func (m *updateMapper) lookup(id string) (toolInfo, bool) {
	info, ok := m.tools[id]
	return info, ok
}

// acpContent is one ACP content block (text, image, ...). Only text matters here.
type acpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// acpToolCallContent is one entry of a tool call's content list. ACP wraps
// regular content as {"type":"content","content":{...}}; diffs and terminals
// carry other shapes that are summarized separately.
type acpToolCallContent struct {
	Type    string     `json:"type"`
	Content acpContent `json:"content"`
	Path    string     `json:"path"`
}

type acpToolCall struct {
	ToolCallID string               `json:"toolCallId"`
	Name       string               `json:"name"` // agy extension: the tool's own name, e.g. run_command
	Title      string               `json:"title"`
	Kind       string               `json:"kind"`
	Status     string               `json:"status"`
	RawInput   json.RawMessage      `json:"rawInput"`
	RawOutput  json.RawMessage      `json:"rawOutput"`
	Content    []acpToolCallContent `json:"content"`
}

// mapUpdate converts one session/update notification's params into events.
func (m *updateMapper) mapUpdate(params json.RawMessage) []core.Event {
	var wrap struct {
		Update json.RawMessage `json:"update"`
	}
	if err := json.Unmarshal(params, &wrap); err != nil || len(wrap.Update) == 0 {
		slog.Debug("antigravity: unparseable session/update", "params", truncate(string(params), 500))
		return nil
	}
	var head struct {
		Kind string `json:"sessionUpdate"`
	}
	if err := json.Unmarshal(wrap.Update, &head); err != nil {
		return nil
	}

	switch head.Kind {
	case "agent_message_chunk":
		if text := chunkText(wrap.Update); text != "" {
			return []core.Event{{Type: core.EventText, Content: text}}
		}
	case "agent_thought_chunk":
		if text := chunkText(wrap.Update); text != "" {
			return []core.Event{{Type: core.EventThinking, Content: text}}
		}
	case "tool_call":
		return m.mapToolCall(wrap.Update)
	case "tool_call_update":
		return m.mapToolCallUpdate(wrap.Update)
	case "plan":
		if text := planText(wrap.Update); text != "" {
			return []core.Event{{Type: core.EventThinking, Content: text}}
		}
	case "user_message_chunk", "current_mode_update", "available_commands_update",
		"session_info_update", "usage_update", "config_option_update":
		// Not part of the reply: user echoes, or state handled elsewhere.
	default:
		slog.Debug("antigravity: unhandled session/update", "kind", head.Kind)
	}
	return nil
}

func chunkText(update json.RawMessage) string {
	var u struct {
		Content acpContent `json:"content"`
	}
	if json.Unmarshal(update, &u) != nil || u.Content.Type != "text" {
		return ""
	}
	return u.Content.Text
}

func planText(update json.RawMessage) string {
	var u struct {
		Entries []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"entries"`
	}
	if json.Unmarshal(update, &u) != nil || len(u.Entries) == 0 {
		return ""
	}
	lines := make([]string, 0, len(u.Entries))
	for _, e := range u.Entries {
		line := e.Content
		if e.Status != "" {
			line = "[" + e.Status + "] " + line
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m *updateMapper) mapToolCall(update json.RawMessage) []core.Event {
	var tc acpToolCall
	if json.Unmarshal(update, &tc) != nil {
		return nil
	}
	info := toolInfo{name: toolName(tc), input: summarizeToolInput(tc.Kind, tc.RawInput)}
	if info.input == "" && tc.Title != info.name {
		info.input = tc.Title
	}
	m.remember(tc.ToolCallID, info)
	events := []core.Event{{Type: core.EventToolUse, ToolName: info.name, ToolInput: info.input}}
	// A tool call can be announced already finished.
	if isTerminalStatus(tc.Status) {
		events = append(events, toolResultEvent(info, tc))
	}
	return events
}

func (m *updateMapper) mapToolCallUpdate(update json.RawMessage) []core.Event {
	var tc acpToolCall
	if json.Unmarshal(update, &tc) != nil {
		return nil
	}
	info, known := m.lookup(tc.ToolCallID)
	if !known {
		info = toolInfo{name: toolName(tc)}
	}
	if input := summarizeToolInput(tc.Kind, tc.RawInput); input != "" {
		info.input = input
	}
	m.remember(tc.ToolCallID, info)
	if !isTerminalStatus(tc.Status) {
		return nil
	}
	return []core.Event{toolResultEvent(info, tc)}
}

func toolResultEvent(info toolInfo, tc acpToolCall) core.Event {
	status := strings.ToLower(strings.TrimSpace(tc.Status))
	exitCode := rawExitCode(tc.RawOutput)
	success := status == "completed" && (exitCode == nil || *exitCode == 0)
	return core.Event{
		Type:         core.EventToolResult,
		ToolName:     info.name,
		ToolInput:    info.input,
		ToolResult:   truncate(toolOutputText(tc), 800),
		ToolStatus:   status,
		ToolExitCode: exitCode,
		ToolSuccess:  &success,
	}
}

// rawExitCode reads a command's exit_code from rawOutput, when present.
func rawExitCode(raw json.RawMessage) *int {
	var out struct {
		ExitCode *int `json:"exit_code"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out.ExitCode
}

// commandOutputMarker precedes the actual output in agy's command results,
// after lines like "Created At: ..." and "Command exited with code 0.".
const commandOutputMarker = "Command output:\n"

// stripCommandHeader keeps only what a command printed.
func stripCommandHeader(text string) string {
	if i := strings.Index(text, commandOutputMarker); i >= 0 {
		text = text[i+len(commandOutputMarker):]
	}
	return strings.ReplaceAll(text, "\r\n", "\n")
}

func isTerminalStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed":
		return true
	}
	return false
}

// toolName names a tool call: agy's own tool name when it sends one (its
// title is often just the command line, which the input already shows),
// else the ACP title, else the kind.
func toolName(tc acpToolCall) string {
	for _, n := range []string{tc.Name, tc.Title, tc.Kind} {
		if n = strings.TrimSpace(n); n != "" {
			return n
		}
	}
	return "tool"
}

// toolOutputText collects a tool call's text output: its text content
// blocks, the paths of any diffs, or failing that its rawOutput.
func toolOutputText(tc acpToolCall) string {
	var parts []string
	for _, c := range tc.Content {
		switch {
		case c.Type == "content" && c.Content.Type == "text" && c.Content.Text != "":
			parts = append(parts, stripCommandHeader(c.Content.Text))
		case c.Type == "diff" && c.Path != "":
			parts = append(parts, "edited "+c.Path)
		}
	}
	if len(parts) > 0 {
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	return strings.TrimSpace(stripCommandHeader(rawJSONText(tc.RawOutput)))
}

// rawJSONText renders a raw JSON value for display: strings as-is, objects
// by their "output"/"stdout"/"result" field when present, else as JSON.
func rawJSONText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil {
		for _, key := range []string{"output", "stdout", "result", "error"} {
			if v, ok := obj[key].(string); ok && strings.TrimSpace(v) != "" {
				return v
			}
		}
	}
	return string(raw)
}

// summarizeToolInput turns a tool call's rawInput into a short readable line:
// the command for shell tools, the path for file tools, else sorted key: value pairs.
func summarizeToolInput(kind string, raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return truncate(string(raw), 500)
	}
	if len(m) == 0 {
		return ""
	}
	for _, key := range []string{"command", "CommandLine", "commandLine", "cmd"} {
		if v, ok := m[key].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	switch strings.ToLower(kind) {
	case "read", "edit", "delete", "move":
		for _, key := range []string{"path", "file_path", "filePath", "AbsolutePath", "TargetFile"} {
			if v, ok := m[key].(string); ok && v != "" {
				return v
			}
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		switch v := m[k].(type) {
		case string:
			parts = append(parts, k+": "+v)
		default:
			b, _ := json.Marshal(v)
			parts = append(parts, k+": "+string(b))
		}
	}
	return truncate(strings.Join(parts, ", "), 500)
}

// permissionOption is one choice offered by session/request_permission.
type permissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"` // allow_once, allow_always, reject_once, reject_always
}

// pickPermissionOption returns the option matching the user's decision,
// preferring the one-time variants so a single approval in chat never turns
// into a standing rule. It returns "" when no option fits.
func pickPermissionOption(allow bool, options []permissionOption) string {
	prefer := []string{"reject_once", "reject_always"}
	if allow {
		prefer = []string{"allow_once", "allow_always"}
	}
	for _, kind := range prefer {
		for _, o := range options {
			if strings.EqualFold(o.Kind, kind) {
				return o.OptionID
			}
		}
	}
	return ""
}

// permissionResponse builds the session/request_permission result.
// With no matching option a denial is sent as "cancelled".
func permissionResponse(optionID string) map[string]any {
	if optionID == "" {
		return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
	}
	return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": optionID}}
}

func truncate(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	return string([]rune(s)[:maxRunes]) + "..."
}

// stopReasonError reports a session/prompt stopReason that means the turn did
// not finish normally, or nil when it ended normally.
func stopReasonError(reason string) error {
	switch reason {
	case "", "end_turn", "cancelled":
		return nil
	case "max_tokens":
		return fmt.Errorf("antigravity: turn stopped: the model hit its output token limit")
	case "max_turn_requests":
		return fmt.Errorf("antigravity: turn stopped: too many model requests in one turn")
	case "refusal":
		return fmt.Errorf("antigravity: turn stopped: the model refused to continue")
	default:
		return fmt.Errorf("antigravity: turn stopped: %s", reason)
	}
}
