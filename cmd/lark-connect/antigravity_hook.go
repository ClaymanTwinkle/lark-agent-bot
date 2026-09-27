package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/ClaymanTwinkle/lark-connect/agent/antigravityhook"
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

	fmt.Fprintf(os.Stderr, "lark-connect Agy permission hook: %v\n", err)
	_ = json.NewEncoder(os.Stdout).Encode(antigravityhook.BridgeResponse{
		Decision: "deny",
		Reason:   "lark-connect permission bridge is unavailable",
	})
}
