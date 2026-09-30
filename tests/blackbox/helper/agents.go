//go:build blackbox

// This file registers all agent factories by importing the agent packages.
// Without these blank imports, core.CreateAgent would return "unknown agent".
// Each import triggers the package's init() function which calls core.RegisterAgent.
package helper

import (
	_ "github.com/ClaymanTwinkle/lark-agent-bot/agent/claudecode"
	_ "github.com/ClaymanTwinkle/lark-agent-bot/agent/codex"
	_ "github.com/ClaymanTwinkle/lark-agent-bot/agent/cursor"
	_ "github.com/ClaymanTwinkle/lark-agent-bot/agent/gemini"
	_ "github.com/ClaymanTwinkle/lark-agent-bot/agent/opencode"
	_ "github.com/ClaymanTwinkle/lark-agent-bot/agent/qoder"
)
