package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// updateCheckInterval is the least time between two background checks for
// a newer release by the lark-agent-bot processes on this host. A check is
// a GitHub API request, and without a token GitHub allows 60 of those per
// hour per IP, shared with /upgrade. Every CLI invocation used to check, so
// the agents' frequent `lark-agent-bot send/cron/timer` calls used up the
// quota and /upgrade lost its release notes (#10).
const updateCheckInterval = 6 * time.Hour

// updateCheckState is the record of the background update check, shared by
// all processes through updateCheckPath.
type updateCheckState struct {
	// Latest is the newest stable release tag found, CheckedAt when.
	Latest    string    `json:"latest,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
	// AttemptedAt is when a process last asked GitHub, whatever the outcome.
	AttemptedAt time.Time `json:"attempted_at"`
}

// updateCheckPath returns the file the update check record is kept in, or
// "" when the home directory is unknown. Tests replace it.
var updateCheckPath = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".lark-agent-bot", "update-check.json")
}

func readUpdateCheckState(path string) (updateCheckState, error) {
	var s updateCheckState
	data, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return updateCheckState{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return s, nil
}

func writeUpdateCheckState(path string, s updateCheckState) error {
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("encode update check state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// claimUpdateCheck reports whether this process should ask GitHub for the
// latest release now: when no process has in the last updateCheckInterval.
// It records the attempt before the request, so a failed check, or one cut
// off because a short-lived process exited, also waits out the interval.
// When the attempt cannot be recorded, it does not check at all: nothing
// would stop every invocation from asking.
func claimUpdateCheck(path string, now time.Time) bool {
	if path == "" {
		return false
	}
	s, err := readUpdateCheckState(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Debug("update check: ignoring unreadable state", "error", err)
	}
	// An attempt in the future means the clock was turned back; check.
	if last := s.AttemptedAt; !last.IsZero() && !last.After(now) && now.Sub(last) < updateCheckInterval {
		return false
	}
	s.AttemptedAt = now
	if err := writeUpdateCheckState(path, s); err != nil {
		slog.Warn("update check: cannot record the attempt, not checking", "error", err)
		return false
	}
	return true
}

// recordLatestVersion stores the latest release a check found.
func recordLatestVersion(path, tag string, checkedAt time.Time) {
	s, err := readUpdateCheckState(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Debug("update check: replacing unreadable state", "error", err)
	}
	s.Latest = tag
	s.CheckedAt = checkedAt
	if s.AttemptedAt.Before(checkedAt) {
		s.AttemptedAt = checkedAt
	}
	if err := writeUpdateCheckState(path, s); err != nil {
		slog.Warn("update check: cannot record the latest version", "error", err)
	}
}

// loadCachedLatestVersion fills cachedLatestVersion with the result of the
// last check by any process, so the update hint does not wait for this
// process's own check.
func loadCachedLatestVersion(path string) {
	if path == "" {
		return
	}
	s, err := readUpdateCheckState(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Debug("update check: ignoring unreadable state", "error", err)
		}
		return
	}
	if s.Latest == "" {
		return
	}
	cachedLatestVersion.mu.Lock()
	cachedLatestVersion.version = s.Latest
	cachedLatestVersion.timestamp = s.CheckedAt
	cachedLatestVersion.mu.Unlock()
}

// invokesSubcommand reports whether args (os.Args[1:]) run a top-level
// subcommand: the first argument that is not a flag names one. A flag
// value given as a separate argument ("--config x send") hides the
// subcommand, which costs at most one throttled check.
func invokesSubcommand(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		_, ok := topLevelCommandHandlers[arg]
		return ok
	}
	return false
}
