//go:build blackbox

// This file registers all agent factories by importing the agent packages.
// Without these blank imports, core.CreateAgent would return "unknown agent".
// Each import triggers the package's init() function which calls core.RegisterAgent.
package helper

import (
	_ "github.com/ClaymanTwinkle/lark-connect/agent/claudecode"
	_ "github.com/ClaymanTwinkle/lark-connect/agent/codex"
	_ "github.com/ClaymanTwinkle/lark-connect/agent/cursor"
	_ "github.com/ClaymanTwinkle/lark-connect/agent/gemini"
	_ "github.com/ClaymanTwinkle/lark-connect/agent/opencode"
	_ "github.com/ClaymanTwinkle/lark-connect/agent/qoder"
)
