package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// inflightTurn is a user turn that has started but not finished.
type inflightTurn struct {
	Platform   string    `json:"platform"`
	SessionKey string    `json:"session_key"`
	MessageID  string    `json:"message_id"`
	Preview    string    `json:"preview,omitempty"`
	StartedAt  time.Time `json:"started_at"`
}

const (
	inflightPreviewMaxRunes = 60
	// interruptedTurnReadyTimeout bounds how long a startup notice waits for
	// its platform to connect before it is dropped.
	interruptedTurnReadyTimeout = 30 * time.Second
)

// turnJournal persists the turns in progress so a process that dies mid-turn
// (crash, hard kill, restart) leaves a record behind. The next process start
// reads the leftovers and tells each chat that its reply was never completed.
// A turn is written when it starts and removed when it ends, so only turns
// cut off by the process exit survive on disk.
type turnJournal struct {
	mu    sync.Mutex
	path  string
	turns map[string]inflightTurn // interactive key → turn in progress
}

func turnJournalPath(dataDir, project string) string {
	name := strings.NewReplacer(
		"\\", "_", "/", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_",
	).Replace(strings.TrimSpace(project))
	if name == "" {
		name = "project"
	}
	return filepath.Join(dataDir, "run", "inflight_turns_"+name+".json")
}

// openTurnJournal returns an empty journal at path together with the turns the
// previous process left behind. The leftovers are removed from disk so each
// one is reported once.
func openTurnJournal(path string) (*turnJournal, []inflightTurn) {
	j := &turnJournal{path: path, turns: make(map[string]inflightTurn)}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("turn journal: read failed", "path", path, "error", err)
		}
		return j, nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("turn journal: remove leftover failed", "path", path, "error", err)
	}
	var saved map[string]inflightTurn
	if err := json.Unmarshal(data, &saved); err != nil {
		slog.Warn("turn journal: parse failed", "path", path, "error", err)
		return j, nil
	}
	leftover := make([]inflightTurn, 0, len(saved))
	for _, t := range saved {
		leftover = append(leftover, t)
	}
	sort.Slice(leftover, func(a, b int) bool {
		return leftover[a].StartedAt.Before(leftover[b].StartedAt)
	})
	return j, leftover
}

func (j *turnJournal) begin(key string, t inflightTurn) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.turns[key] = t
	j.saveLocked()
}

func (j *turnJournal) end(key string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, ok := j.turns[key]; !ok {
		return
	}
	delete(j.turns, key)
	j.saveLocked()
}

// endMessage ends key's entry only while it still records messageID; a newer
// turn that has replaced it keeps its entry.
func (j *turnJournal) endMessage(key, messageID string) {
	if j == nil || messageID == "" {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if t, ok := j.turns[key]; !ok || t.MessageID != messageID {
		return
	}
	delete(j.turns, key)
	j.saveLocked()
}

func (j *turnJournal) saveLocked() {
	if len(j.turns) == 0 {
		if err := os.Remove(j.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("turn journal: remove failed", "path", j.path, "error", err)
		}
		return
	}
	data, err := json.Marshal(j.turns)
	if err != nil {
		slog.Warn("turn journal: marshal failed", "error", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(j.path), 0o755); err != nil {
		slog.Warn("turn journal: mkdir failed", "path", j.path, "error", err)
		return
	}
	if err := AtomicWriteFile(j.path, data, 0o644); err != nil {
		slog.Warn("turn journal: write failed", "path", j.path, "error", err)
	}
}

// turnPreview flattens a user message into one short line for the
// interrupted-turn notice.
func turnPreview(content string) string {
	return truncateIf(strings.Join(strings.Fields(content), " "), inflightPreviewMaxRunes)
}

// beginTurnJournal records the turn now running under interactiveKey. Turns
// without a platform message ID (cron, timer, heartbeat) are not journaled —
// a notice about an interrupted heartbeat after every restart would be noise —
// and they clear the entry of the user turn they follow.
func (e *Engine) beginTurnJournal(interactiveKey, platformName, sessionKey, messageID, content string) {
	if e.turnJournal == nil {
		return
	}
	if messageID == "" {
		e.turnJournal.end(interactiveKey)
		return
	}
	e.turnJournal.begin(interactiveKey, inflightTurn{
		Platform:   platformName,
		SessionKey: sessionKey,
		MessageID:  messageID,
		Preview:    turnPreview(content),
		StartedAt:  time.Now(),
	})
}

// NotifyInterruptedTurns tells every chat whose turn was cut off by the
// previous process exit that the reply was never completed. Call once after
// Start; each notice waits for its platform to become ready.
func (e *Engine) NotifyInterruptedTurns() {
	turns := e.interruptedTurns
	e.interruptedTurns = nil
	for _, t := range turns {
		slog.Info("interrupted turn from previous run: notifying",
			"platform", t.Platform, "session", t.SessionKey, "started_at", t.StartedAt)
		go e.dispatchInterruptedTurn(t)
	}
}

func (e *Engine) dispatchInterruptedTurn(t inflightTurn) {
	defer func() {
		if r := recover(); r != nil {
			stack := make([]byte, 8192)
			n := runtime.Stack(stack, false)
			slog.Error("interrupted turn notice panic",
				"platform", t.Platform, "session", t.SessionKey, "panic", r, "stack", string(stack[:n]))
		}
	}()
	if err := e.sendInterruptedTurnNotice(t); err != nil {
		slog.Warn("interrupted turn notice not delivered",
			"platform", t.Platform, "session", t.SessionKey, "error", err)
	}
}

func (e *Engine) sendInterruptedTurnNotice(t inflightTurn) error {
	deadline := time.Now().Add(interruptedTurnReadyTimeout)
	p := e.lookupReadyPlatform(t.Platform)
	for p == nil {
		if e.ctx.Err() != nil {
			return e.ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("platform %q not ready after %v", t.Platform, interruptedTurnReadyTimeout)
		}
		time.Sleep(200 * time.Millisecond)
		p = e.lookupReadyPlatform(t.Platform)
	}
	rc, ok := p.(ReplyContextReconstructor)
	if !ok {
		return fmt.Errorf("platform %q does not support ReconstructReplyCtx", t.Platform)
	}
	rctx, err := rc.ReconstructReplyCtx(t.SessionKey)
	if err != nil {
		return fmt.Errorf("reconstruct reply ctx: %w", err)
	}

	text := e.i18n.Tf(MsgTurnInterrupted, t.StartedAt.Local().Format("01-02 15:04"))
	if t.Preview != "" {
		text += "\n> " + t.Preview
	}

	var lastErr error
	for attempt, wait := range []time.Duration{0, 500 * time.Millisecond, 1500 * time.Millisecond} {
		if wait > 0 {
			time.Sleep(wait)
		}
		if err := e.waitOutgoing(p); err != nil {
			lastErr = fmt.Errorf("wait outgoing: %w", err)
			continue
		}
		if err := p.Send(e.ctx, rctx, text); err != nil {
			lastErr = err
			slog.Warn("interrupted turn notice: send failed",
				"platform", t.Platform, "session", t.SessionKey, "attempt", attempt+1, "error", err)
			continue
		}
		return nil
	}
	return lastErr
}
