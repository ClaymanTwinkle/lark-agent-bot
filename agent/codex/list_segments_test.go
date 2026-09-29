package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const threadID = "01a0ea82-c175-7531-86f2-aeb040e27bba"

// writeThreadRollouts lays out one thread the way Codex does once it grows:
// the file it started in, then a continuation file named
// rollout-…-<thread>_<segment>.jsonl that repeats the thread's session_meta
// id and holds only the later turns. The first file also carries a line far
// longer than any fixed scanner buffer, as compactions and images do.
func writeThreadRollouts(t *testing.T, workDir, codexHome string) (first, second string) {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions", "2026", "09", "29")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd, _ := json.Marshal(workDir)
	meta := func(id string) string {
		return `{"type":"session_meta","payload":{"id":"` + id + `","cwd":` + string(cwd) + `,"source":"vscode"}}` + "\n"
	}
	msg := func(ts, role, kind, text string) string {
		return `{"timestamp":"` + ts + `","type":"response_item","payload":{"role":"` + role + `","content":[{"type":"` + kind + `","text":"` + text + `"}]}}` + "\n"
	}
	huge := `{"type":"response_item","payload":{"type":"compaction","encrypted_content":"` + strings.Repeat("x", 600*1024) + `"}}` + "\n"

	first = filepath.Join(dir, "rollout-2026-09-29T08-13-53-"+threadID+".jsonl")
	second = filepath.Join(dir, "rollout-2026-09-29T17-34-30-"+threadID+"_01a0ec84-0783-7ed0-a36e-9049624b1f51.jsonl")
	other := filepath.Join(dir, "rollout-2026-09-29T00-13-44-01a0e8cb-35a3-7091-b708-47c4ca3e2636.jsonl")

	files := map[string]string{
		first: meta(threadID) +
			msg("2026-09-29T00:13:54Z", "user", "input_text", "straighten the models") +
			huge +
			msg("2026-09-29T00:17:00Z", "assistant", "output_text", "done, here are the screenshots"),
		second: meta(threadID) +
			msg("2026-09-29T09:34:31Z", "user", "input_text", "continue the download") +
			msg("2026-09-29T09:40:00Z", "assistant", "output_text", "downloaded"),
		other: meta("01a0e8cb-35a3-7091-b708-47c4ca3e2636") +
			msg("2026-09-28T16:13:44Z", "user", "input_text", "split the unfinished models"),
	}
	now := time.Now()
	mtimes := map[string]time.Time{first: now.Add(-2 * time.Hour), second: now, other: now.Add(-time.Hour)}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mtimes[path], mtimes[path]); err != nil {
			t.Fatal(err)
		}
	}
	return first, second
}

// Regression: every rollout file became its own row, so a thread that Codex
// had continued in new files showed up several times in the session card —
// all marked current, each with a different title.
func TestListSessions_ShowsAThreadOnceAcrossItsRolloutFiles(t *testing.T) {
	workDir, codexHome := t.TempDir(), t.TempDir()
	writeThreadRollouts(t, workDir, codexHome)

	sessions, err := (&Agent{workDir: workDir, codexHome: codexHome}).ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("ListSessions() = %d rows %+v, want the thread once plus the other session", len(sessions), sessions)
	}
	thread := sessions[0]
	if thread.ID != threadID {
		t.Fatalf("first row = %q, want the most recently active thread %q", thread.ID, threadID)
	}
	if thread.Summary != "continue the download" {
		t.Fatalf("summary = %q, want the latest prompt from the newest file", thread.Summary)
	}
	if thread.MessageCount != 4 {
		t.Fatalf("message count = %d, want 4 across both files (the long line must not stop the count)", thread.MessageCount)
	}
}

func TestGetSessionHistory_ReadsEveryRolloutFileInOrder(t *testing.T) {
	workDir, codexHome := t.TempDir(), t.TempDir()
	writeThreadRollouts(t, workDir, codexHome)

	history, err := getSessionHistory(threadID, codexHome, 0)
	if err != nil {
		t.Fatalf("getSessionHistory() error = %v", err)
	}
	var got []string
	for _, h := range history {
		got = append(got, h.Content)
	}
	want := []string{"straighten the models", "done, here are the screenshots", "continue the download", "downloaded"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("history = %q, want %q", got, want)
	}
}

func TestDeleteSession_RemovesEveryRolloutFile(t *testing.T) {
	workDir, codexHome := t.TempDir(), t.TempDir()
	first, second := writeThreadRollouts(t, workDir, codexHome)

	if err := (&Agent{workDir: workDir, codexHome: codexHome}).DeleteSession(context.Background(), threadID); err != nil {
		t.Fatalf("DeleteSession() error = %v", err)
	}
	for _, path := range []string{first, second} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists (err=%v); the thread would reappear in the list", filepath.Base(path), err)
		}
	}
}
