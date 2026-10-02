package main

import (
	"os"
	"testing"
)

func TestTakeDaemonLogEnv_HidesLogSettingsUntilRestored(t *testing.T) {
	t.Setenv("CC_LOG_FILE", `C:\logs\bot.log`)
	t.Setenv("CC_LOG_MAX_SIZE", "1048576")
	t.Setenv("CC_LOG_MAX_BACKUPS", "") // restored after the test
	if err := os.Unsetenv("CC_LOG_MAX_BACKUPS"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CC_PROJECT", "claude-bot")

	taken := takeDaemonLogEnv()
	for _, key := range daemonLogEnvKeys {
		if _, ok := os.LookupEnv(key); ok {
			t.Errorf("%s still set; agents would inherit it", key)
		}
	}
	if os.Getenv("CC_PROJECT") != "claude-bot" {
		t.Error("takeDaemonLogEnv removed an unrelated variable")
	}
	if len(taken) != 2 || taken["CC_LOG_FILE"] != `C:\logs\bot.log` || taken["CC_LOG_MAX_SIZE"] != "1048576" {
		t.Fatalf("taken = %v", taken)
	}

	restoreEnv(taken)
	if os.Getenv("CC_LOG_FILE") != `C:\logs\bot.log` || os.Getenv("CC_LOG_MAX_SIZE") != "1048576" {
		t.Error("restoreEnv did not put the log settings back")
	}
	if _, ok := os.LookupEnv("CC_LOG_MAX_BACKUPS"); ok {
		t.Error("restoreEnv set a variable that was not there")
	}
}
