package main

import (
	"os"
	"strings"
)

// sessionKeyFromEnv returns the session key the engine injected for the agent
// that runs this command. CC_SESSION comes first: Codex removes variables
// whose names contain KEY, SECRET or TOKEN from the commands it runs, so
// CC_SESSION_KEY never reaches them there. CC_SESSION_KEY is still read for
// engines that only inject the old name.
func sessionKeyFromEnv() string {
	if v := strings.TrimSpace(os.Getenv("CC_SESSION")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("CC_SESSION_KEY"))
}
