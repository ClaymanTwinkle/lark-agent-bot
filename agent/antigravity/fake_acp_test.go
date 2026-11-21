package antigravity

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// The test binary doubles as a fake `agy acp`: when fakeAgyEnv is set,
// TestMain runs runFakeAgy instead of the tests. The fake speaks ACP over
// stdio and appends every request it receives, plus its argv, to the file
// named by fakeLogEnv, one JSON object per line.
const (
	fakeAgyEnv      = "LAB_FAKE_AGY"
	fakeLogEnv      = "LAB_FAKE_AGY_LOG"
	fakeNoResumeEnv = "LAB_FAKE_AGY_NO_RESUME" // advertise load but not resume
	fakeNoAuthEnv   = "LAB_FAKE_AGY_NO_AUTH"   // session/new answers "Authentication required"
	fakeAutoEnv     = "LAB_FAKE_AGY_AUTO"      // also offer agy's auto mode
)

func TestMain(m *testing.M) {
	if os.Getenv(fakeAgyEnv) == "1" {
		runFakeAgy()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type fakeMsg struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

type fakeAgy struct {
	outMu   sync.Mutex
	out     *json.Encoder
	logMu   sync.Mutex
	log     *os.File
	replies chan fakeMsg // responses to the fake's own requests

	cancelOnce sync.Once
	cancelled  chan struct{} // closed by session/cancel
}

func runFakeAgy() {
	logFile, err := os.OpenFile(os.Getenv(fakeLogEnv), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake agy: open log:", err)
		os.Exit(1)
	}
	defer logFile.Close()
	f := &fakeAgy{out: json.NewEncoder(os.Stdout), log: logFile, replies: make(chan fakeMsg, 4), cancelled: make(chan struct{})}
	f.record("argv", os.Args[1:])
	if len(os.Args) > 1 && os.Args[1] == "models" {
		fmt.Print(fakeModelsOutput)
		return
	}

	var wg sync.WaitGroup
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		var msg fakeMsg
		if json.Unmarshal(sc.Bytes(), &msg) != nil {
			continue
		}
		if msg.Method == "" {
			f.replies <- msg
			continue
		}
		f.record(msg.Method, msg.Params)
		if msg.Method == "session/cancel" {
			f.cancelOnce.Do(func() { close(f.cancelled) })
			continue
		}
		if msg.Method == "session/prompt" {
			wg.Add(1)
			go func() {
				defer wg.Done()
				f.prompt(msg)
			}()
			continue
		}
		f.handle(msg)
	}
	wg.Wait()
	fmt.Fprintln(os.Stderr, "fake agy: stdin closed")
}

func (f *fakeAgy) record(method string, params any) {
	f.logMu.Lock()
	defer f.logMu.Unlock()
	line, _ := json.Marshal(map[string]any{"method": method, "params": params})
	_, _ = f.log.Write(append(line, '\n'))
}

func (f *fakeAgy) send(v any) {
	f.outMu.Lock()
	defer f.outMu.Unlock()
	_ = f.out.Encode(v)
}

func (f *fakeAgy) result(id json.RawMessage, result any) {
	f.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (f *fakeAgy) fail(id json.RawMessage, code int, message string) {
	f.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}

func (f *fakeAgy) update(sessionID string, update map[string]any) {
	f.send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": sessionID, "update": update}})
}

// fakeModes is what the fake offers: agy's modes without auto, which agy
// offers only to some accounts, unless fakeAutoEnv asks for it.
func fakeModes() map[string]any {
	modes := map[string]any{
		"currentModeId": "default",
		"availableModes": []any{
			map[string]any{"id": "default", "name": "Default"},
			map[string]any{"id": "accept-edits", "name": "Accept Edits"},
			map[string]any{"id": "plan", "name": "Plan"},
		},
	}
	if os.Getenv(fakeAutoEnv) == "1" {
		modes["availableModes"] = append(modes["availableModes"].([]any), map[string]any{"id": "auto", "name": "Auto"})
	}
	return modes
}

// Like agy, the legacy models list combines model and effort, while the
// config options list base models and efforts separately.
var fakeModels = map[string]any{
	"currentModelId": "gemini-a-high",
	"availableModels": []any{
		map[string]any{"modelId": "gemini-a-high", "name": "Gemini A (High)"},
		map[string]any{"modelId": "gemini-b-low", "name": "Gemini B (Low)"},
	},
}

// fakeConfig is the fake's current model and effort config values.
var fakeConfig = struct {
	sync.Mutex
	model, effort string
}{model: "gemini-a", effort: "high"}

func fakeConfigOptions() []any {
	fakeConfig.Lock()
	defer fakeConfig.Unlock()
	return []any{
		map[string]any{
			"id": "model", "name": "Model", "category": "model", "type": "select",
			"currentValue": fakeConfig.model,
			"options": []any{
				map[string]any{"value": "gemini-a", "name": "Gemini A"},
				map[string]any{"value": "gemini-b", "name": "Gemini B"},
			},
		},
		map[string]any{
			"id": "thought_level", "name": "Thinking Level", "category": "thought_level", "type": "select",
			"currentValue": fakeConfig.effort,
			"options": []any{
				map[string]any{"value": "low", "name": "Low"},
				map[string]any{"value": "medium", "name": "Medium"},
				map[string]any{"value": "high", "name": "High"},
			},
		},
	}
}

func (f *fakeAgy) handle(msg fakeMsg) {
	var p struct {
		SessionID string `json:"sessionId"`
		Cwd       string `json:"cwd"`
		ModelID   string `json:"modelId"`
		ConfigID  string `json:"configId"`
		Value     string `json:"value"`
	}
	_ = json.Unmarshal(msg.Params, &p)
	switch msg.Method {
	case "initialize":
		caps := map[string]any{"list": map[string]any{}}
		if os.Getenv(fakeNoResumeEnv) != "1" {
			caps["resume"] = map[string]any{}
		}
		f.result(msg.ID, map[string]any{
			"protocolVersion":   1,
			"agentCapabilities": map[string]any{"loadSession": true, "sessionCapabilities": caps},
		})
	case "session/new":
		if os.Getenv(fakeNoAuthEnv) == "1" {
			f.fail(msg.ID, -32000, "Authentication required")
			return
		}
		f.result(msg.ID, map[string]any{"sessionId": "conv-1", "modes": fakeModes(), "models": fakeModels, "configOptions": fakeConfigOptions()})
	case "session/resume":
		f.result(msg.ID, map[string]any{"modes": fakeModes(), "models": fakeModels})
	case "session/load":
		// Replay history before answering, as ACP load does.
		f.update(p.SessionID, map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "old question"}})
		f.update(p.SessionID, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "OLD ANSWER"}})
		f.result(msg.ID, map[string]any{"modes": fakeModes()})
	case "session/set_model":
		if p.ModelID == "bad-model" {
			f.fail(msg.ID, -32602, "unknown model")
			return
		}
		f.result(msg.ID, map[string]any{})
	case "session/set_mode":
		f.result(msg.ID, map[string]any{})
	case "session/set_config_option":
		if p.Value == "bogus" {
			f.fail(msg.ID, -32602, "invalid value")
			return
		}
		fakeConfig.Lock()
		if p.ConfigID == "model" {
			fakeConfig.model = p.Value
		} else {
			fakeConfig.effort = p.Value
		}
		fakeConfig.Unlock()
		f.result(msg.ID, map[string]any{"configOptions": fakeConfigOptions()})
	case "session/list":
		f.result(msg.ID, map[string]any{"sessions": []any{
			map[string]any{"sessionId": "conv-1", "cwd": p.Cwd, "title": "First chat", "updatedAt": "2026-11-21T08:00:00Z"},
			map[string]any{"sessionId": "elsewhere", "cwd": "/some/other/dir", "title": "Other"},
		}})
	default:
		f.fail(msg.ID, -32601, "method not found")
	}
}

// prompt plays one turn. The prompt text picks the script:
// "fail" → JSON-RPC error, "refuse" → stopReason refusal, "plain" → a
// text-only reply, anything else → thinking, text, a tool call that needs
// permission, its result, more text.
func (f *fakeAgy) prompt(msg fakeMsg) {
	var p struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"prompt"`
	}
	_ = json.Unmarshal(msg.Params, &p)
	text := ""
	if len(p.Prompt) > 0 {
		text = p.Prompt[0].Text
	}
	sid := p.SessionID
	chunk := func(kind, s string) {
		f.update(sid, map[string]any{"sessionUpdate": kind, "content": map[string]any{"type": "text", "text": s}})
	}
	switch {
	case strings.Contains(text, "fail"):
		fmt.Fprintln(os.Stderr, "model backend exploded")
		f.fail(msg.ID, -32603, "internal error")
		return
	case strings.Contains(text, "refuse"):
		f.result(msg.ID, map[string]any{"stopReason": "refusal"})
		return
	case strings.Contains(text, "slow"):
		// Streams a little, then waits until session/cancel arrives.
		chunk("agent_message_chunk", "partial answer")
		select {
		case <-f.cancelled:
			f.result(msg.ID, map[string]any{"stopReason": "cancelled"})
		case <-time.After(10 * time.Second):
			f.result(msg.ID, map[string]any{"stopReason": "end_turn"})
		}
		return
	case strings.Contains(text, "crash"):
		chunk("agent_message_chunk", "about to crash")
		os.Exit(3)
	case strings.Contains(text, "usage"):
		f.update(sid, map[string]any{"sessionUpdate": "usage_update", "used": 1200, "size": 200000})
		chunk("agent_message_chunk", "counted")
		f.result(msg.ID, map[string]any{"stopReason": "end_turn", "usage": map[string]any{"inputTokens": 900, "outputTokens": 40, "cachedReadTokens": 300}})
		return
	case strings.Contains(text, "background"):
		// Like agy: a background command outlives the turn, then agy wakes
		// up on its own, reports it and replies, with no session/prompt.
		f.update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "bg1", "name": "run_command", "title": "sleep 1; echo bg-done", "kind": "execute", "status": "in_progress", "rawInput": map[string]any{"CommandLine": "sleep 1; echo bg-done"}})
		chunk("agent_message_chunk", "started")
		f.update(sid, map[string]any{"sessionUpdate": "usage_update", "used": 100, "size": 1000})
		f.result(msg.ID, map[string]any{"stopReason": "end_turn"})
		go func() {
			time.Sleep(400 * time.Millisecond)
			f.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "bg1", "name": "run_command", "status": "completed", "rawOutput": map[string]any{"output": "Command output:\nbg-done", "exit_code": 0}})
			if strings.Contains(text, "silent") {
				// No usage_update: only the fallback timer can end the turn.
				chunk("agent_message_chunk", "bg-done")
				return
			}
			chunk("agent_message_chunk", "bg-done")
			f.update(sid, map[string]any{"sessionUpdate": "usage_update", "used": 120, "size": 1000})
		}()
		return
	case strings.Contains(text, "plain"):
		chunk("agent_message_chunk", "plain reply")
		f.result(msg.ID, map[string]any{"stopReason": "end_turn"})
		return
	}

	chunk("agent_thought_chunk", "thinking about it")
	chunk("agent_message_chunk", "Let me ")
	f.update(sid, map[string]any{
		"sessionUpdate": "tool_call", "toolCallId": "t1", "title": "Run echo", "kind": "execute",
		"status": "pending", "rawInput": map[string]any{"command": "echo hi"},
	})
	f.send(map[string]any{"jsonrpc": "2.0", "id": 900, "method": "session/request_permission", "params": map[string]any{
		"sessionId": sid,
		"toolCall":  map[string]any{"toolCallId": "t1", "title": "Run echo", "kind": "execute"},
		"options": []any{
			map[string]any{"optionId": "always", "name": "Always allow", "kind": "allow_always"},
			map[string]any{"optionId": "once", "name": "Allow", "kind": "allow_once"},
			map[string]any{"optionId": "no", "name": "Reject", "kind": "reject_once"},
		},
	}})
	reply := <-f.replies
	f.record("permission_reply", reply.Result)
	var outcome struct {
		Outcome struct {
			OptionID string `json:"optionId"`
		} `json:"outcome"`
	}
	_ = json.Unmarshal(reply.Result, &outcome)
	status, output := "completed", "hi"
	if outcome.Outcome.OptionID != "once" && outcome.Outcome.OptionID != "always" {
		status, output = "failed", "denied by user"
	}
	f.update(sid, map[string]any{
		"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": status,
		"content": []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": output}}},
	})
	chunk("agent_message_chunk", "done.")
	f.result(msg.ID, map[string]any{"stopReason": "end_turn"})
}
