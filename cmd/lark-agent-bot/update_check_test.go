package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// useUpdateCheckPath points the update check record at path for the test.
func useUpdateCheckPath(t *testing.T, path string) {
	t.Helper()
	orig := updateCheckPath
	updateCheckPath = func() string { return path }
	t.Cleanup(func() { updateCheckPath = orig })
}

// resetCachedLatestVersion clears the in-memory cache for the test.
func resetCachedLatestVersion(t *testing.T) {
	t.Helper()
	reset := func() {
		cachedLatestVersion.mu.Lock()
		cachedLatestVersion.version = ""
		cachedLatestVersion.timestamp = time.Time{}
		cachedLatestVersion.mu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

// Regression (#10): every CLI invocation asked GitHub, which used up the
// unauthenticated API quota. Processes now check at most once per interval,
// and a failed or cut-off check counts too.
func TestClaimUpdateCheck_AtMostOncePerInterval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-check.json")
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	if !claimUpdateCheck(path, now) {
		t.Fatal("first claim = false, want true")
	}
	// The process that claimed never recorded a result (failed, or exited).
	if claimUpdateCheck(path, now.Add(time.Minute)) {
		t.Fatal("claim right after an attempt = true, want false")
	}
	if claimUpdateCheck(path, now.Add(updateCheckInterval-time.Second)) {
		t.Fatal("claim within the interval = true, want false")
	}
	if !claimUpdateCheck(path, now.Add(updateCheckInterval)) {
		t.Fatal("claim after the interval = false, want true")
	}
}

func TestClaimUpdateCheck_ClockTurnedBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-check.json")
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	if !claimUpdateCheck(path, now) {
		t.Fatal("first claim = false, want true")
	}
	if !claimUpdateCheck(path, now.Add(-time.Hour)) {
		t.Fatal("claim before the recorded attempt = false, want true")
	}
}

func TestClaimUpdateCheck_NoRecordNoCheck(t *testing.T) {
	if claimUpdateCheck("", time.Now()) {
		t.Error("claim without a record path = true, want false")
	}

	// The record's directory cannot be created: a file is in the way.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if claimUpdateCheck(filepath.Join(blocker, "update-check.json"), time.Now()) {
		t.Error("claim with an unwritable record = true, want false")
	}
}

func TestClaimUpdateCheck_UnreadableRecordIsReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-check.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if !claimUpdateCheck(path, now) {
		t.Fatal("claim over a corrupt record = false, want true")
	}
	if claimUpdateCheck(path, now) {
		t.Fatal("second claim = true, want false")
	}
}

// The hint is shown from another process's check, even one older than the
// check interval.
func TestUpdateHint_FromRecordedCheck(t *testing.T) {
	origVersion := version
	t.Cleanup(func() { version = origVersion })
	version = "v1.0.0"
	resetCachedLatestVersion(t)

	path := filepath.Join(t.TempDir(), "update-check.json")
	useUpdateCheckPath(t, path)
	checkedAt := time.Now().Add(-2 * 24 * time.Hour)
	if !claimUpdateCheck(path, checkedAt) {
		t.Fatal("claim = false, want true")
	}
	recordLatestVersion(path, "v1.2.0", checkedAt)

	loadCachedLatestVersion(path)
	if hint := getUpdateHintIfAvailable(); !strings.Contains(hint, "v1.0.0 → v1.2.0") {
		t.Fatalf("hint = %q, want v1.0.0 → v1.2.0", hint)
	}

	s, err := readUpdateCheckState(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Latest != "v1.2.0" || !s.CheckedAt.Equal(checkedAt) || !s.AttemptedAt.Equal(checkedAt) {
		t.Fatalf("state = %+v, want latest v1.2.0 checked and attempted at %v", s, checkedAt)
	}
}

func TestInvokesSubcommand(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"--config", "config.toml"}, false},
		{[]string{"--help"}, false},
		{[]string{"--version"}, false},
		{[]string{"unknown"}, false},
		{[]string{"send", "--message", "hi"}, true},
		{[]string{"cron", "list"}, true},
		{[]string{"timer", "add"}, true},
		{[]string{"update"}, true},
		{[]string{"check-update"}, true},
		{[]string{"--config=config.toml", "relay", "send"}, true},
	}
	for _, tt := range tests {
		if got := invokesSubcommand(tt.args); got != tt.want {
			t.Errorf("invokesSubcommand(%q) = %v, want %v", tt.args, got, tt.want)
		}
	}
}

// A subcommand neither asks GitHub nor touches the record.
func TestCheckUpdateAsync_SkipsSubcommands(t *testing.T) {
	origVersion, origArgs := version, os.Args
	t.Cleanup(func() { version, os.Args = origVersion, origArgs })
	version = "v1.0.0"
	resetCachedLatestVersion(t)
	path := filepath.Join(t.TempDir(), "update-check.json")
	useUpdateCheckPath(t, path)

	os.Args = []string{"lark-agent-bot", "send", "--message", "hi"}
	checkUpdateAsync()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("record exists after a subcommand (stat err %v), want none", err)
	}
}

// Running the bot or --help loads the recorded result and, within the
// interval, does not ask GitHub again.
func TestCheckUpdateAsync_UsesRecordWithinInterval(t *testing.T) {
	origVersion, origArgs := version, os.Args
	t.Cleanup(func() { version, os.Args = origVersion, origArgs })
	version = "v1.0.0"
	resetCachedLatestVersion(t)
	path := filepath.Join(t.TempDir(), "update-check.json")
	useUpdateCheckPath(t, path)

	attemptedAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	if !claimUpdateCheck(path, attemptedAt) {
		t.Fatal("claim = false, want true")
	}
	recordLatestVersion(path, "v1.2.0", attemptedAt)

	os.Args = []string{"lark-agent-bot", "--help"}
	checkUpdateAsync()

	cachedLatestVersion.mu.RLock()
	cached := cachedLatestVersion.version
	cachedLatestVersion.mu.RUnlock()
	if cached != "v1.2.0" {
		t.Fatalf("cached latest = %q, want v1.2.0 from the record", cached)
	}
	s, err := readUpdateCheckState(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s.AttemptedAt.Equal(attemptedAt) {
		t.Fatalf("attempted at = %v, want unchanged %v (no new check)", s.AttemptedAt, attemptedAt)
	}
}
