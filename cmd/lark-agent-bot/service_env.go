package main

import "os"

// daemonLogEnvKeys are the log settings a service launcher (see daemon/)
// passes to the bot in its environment.
var daemonLogEnvKeys = []string{"CC_LOG_FILE", "CC_LOG_MAX_SIZE", "CC_LOG_MAX_BACKUPS"}

// takeDaemonLogEnv removes the daemon's log settings from this process's
// environment, once the bot has opened its log file, and returns them.
// Agents inherit the bot's environment; a lark-agent-bot command they run
// would otherwise open and rotate the bot's own log file. restoreEnv puts
// them back for a process the bot starts to restart itself.
func takeDaemonLogEnv() map[string]string {
	taken := make(map[string]string)
	for _, key := range daemonLogEnvKeys {
		if value, ok := os.LookupEnv(key); ok {
			taken[key] = value
			_ = os.Unsetenv(key)
		}
	}
	return taken
}

func restoreEnv(env map[string]string) {
	for key, value := range env {
		_ = os.Setenv(key, value)
	}
}
