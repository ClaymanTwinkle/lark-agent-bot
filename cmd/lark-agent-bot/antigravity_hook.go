package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/ClaymanTwinkle/lark-agent-bot/agent/antigravityhook"
)

func runAntigravityPermissionHook() {
	err := antigravityhook.Relay(
		os.Stdin,
		os.Stdout,
		os.Getenv(antigravityhook.EnvAddress),
		os.Getenv(antigravityhook.EnvToken),
	)
	if err == nil {
		return
	}

	fmt.Fprintf(os.Stderr, "lark-agent-bot Agy permission hook: %v\n", err)
	_ = json.NewEncoder(os.Stdout).Encode(antigravityhook.BridgeResponse{
		Decision: "deny",
		Reason:   "lark-agent-bot permission bridge is unavailable",
	})
}
