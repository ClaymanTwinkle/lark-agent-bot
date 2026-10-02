package main

import (
	"strconv"
	"strings"
)

// supervisorRestartExitCode parses the daemon.RestartExitCodeEnv value. A
// service launcher sets it when it starts the bot again after the bot exits
// with that code, so a restart can exit with it and leave starting the new
// process to the launcher. Only 1-255 count: 0 is a normal stop.
func supervisorRestartExitCode(v string) (int, bool) {
	code, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || code < 1 || code > 255 {
		return 0, false
	}
	return code, true
}
