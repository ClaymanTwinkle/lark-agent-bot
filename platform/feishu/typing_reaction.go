package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// typingReactionMaxAge bounds how long a typing reaction whose delete keeps
// failing is kept for the next start. Past it the entry is dropped, so a
// reaction Feishu never lets us remove does not cost requests forever.
const typingReactionMaxAge = 24 * time.Hour

// typingReactionDeleteTimeout bounds a single delete request.
const typingReactionDeleteTimeout = 10 * time.Second

// defaultTypingDeleteBackoff spaces the delete attempts for one reaction.
var defaultTypingDeleteBackoff = []time.Duration{0, time.Second, 3 * time.Second}

// reactionGoneCodes are delete-reaction error codes after which retrying can
// never succeed: the message, chat or reaction no longer exists, or the bot
// lost access to it. The reaction is either gone already or out of reach.
var reactionGoneCodes = map[int]bool{
	231002: true, // no permission to react on the message (bot left the chat)
	231003: true, // message not found, recalled or deleted
	231004: true, // chat not found, disbanded or archived
	231005: true, // thread removed without trace
	231007: true, // reaction was added by someone else
	231008: true, // no access to the message
	231010: true, // reaction does not belong to the message
	231011: true, // reaction_id does not exist (already deleted)
}

// reactionAPIError is a non-success response from the reaction API.
type reactionAPIError struct {
	code int
	msg  string
}

func (e *reactionAPIError) Error() string {
	return fmt.Sprintf("code=%d msg=%s", e.code, e.msg)
}

func isReactionGone(err error) bool {
	var apiErr *reactionAPIError
	return errors.As(err, &apiErr) && reactionGoneCodes[apiErr.code]
}

type typingReaction struct {
	MessageID  string    `json:"message_id"`
	ReactionID string    `json:"reaction_id"`
	AddedAt    time.Time `json:"added_at"`
}

func (r typingReaction) key() string { return r.MessageID + "/" + r.ReactionID }

// typingReactionLedger persists the typing reactions that were added but not
// yet removed. A process that dies mid-turn, or a delete request that fails,
// would otherwise leave the processing emoji on the user's message for good:
// users cannot remove a bot's reaction from the Feishu client. Entries left
// by the previous process are removed by the next start.
type typingReactionLedger struct {
	mu       sync.Mutex
	path     string
	entries  map[string]typingReaction // key() → reaction
	leftover []typingReaction          // entries the previous process left behind
}

func typingReactionLedgerPath(dataDir, platformName, project, appID string) string {
	if strings.TrimSpace(dataDir) == "" {
		return ""
	}
	safe := strings.NewReplacer(
		"\\", "_", "/", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_",
	)
	name := fmt.Sprintf("%s_typing_reactions_%s_%s.json", platformName, strings.TrimSpace(project), appID)
	return filepath.Join(dataDir, "run", safe.Replace(name))
}

// openTypingReactionLedger loads the ledger at path. It returns nil when path
// is empty, which disables persistence; every method accepts a nil ledger.
func openTypingReactionLedger(path string) *typingReactionLedger {
	if path == "" {
		return nil
	}
	l := &typingReactionLedger{path: path, entries: make(map[string]typingReaction)}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("typing reaction ledger: read failed", "path", path, "error", err)
		}
		return l
	}
	var saved []typingReaction
	if err := json.Unmarshal(data, &saved); err != nil {
		slog.Warn("typing reaction ledger: parse failed", "path", path, "error", err)
		return l
	}
	for _, r := range saved {
		if r.MessageID == "" || r.ReactionID == "" {
			continue
		}
		l.entries[r.key()] = r
		l.leftover = append(l.leftover, r)
	}
	return l
}

func (l *typingReactionLedger) add(r typingReaction) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries[r.key()] = r
	l.saveLocked()
}

func (l *typingReactionLedger) remove(r typingReaction) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.entries[r.key()]; !ok {
		return
	}
	delete(l.entries, r.key())
	l.saveLocked()
}

// takeLeftover returns the entries the previous process left behind, once.
// Reactions added by this process are never included, so a sweep cannot
// remove the indicator of a turn that is still running.
func (l *typingReactionLedger) takeLeftover() []typingReaction {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	leftover := l.leftover
	l.leftover = nil
	return leftover
}

func (l *typingReactionLedger) saveLocked() {
	if len(l.entries) == 0 {
		if err := os.Remove(l.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("typing reaction ledger: remove failed", "path", l.path, "error", err)
		}
		return
	}
	list := make([]typingReaction, 0, len(l.entries))
	for _, r := range l.entries {
		list = append(list, r)
	}
	data, err := json.Marshal(list)
	if err != nil {
		slog.Warn("typing reaction ledger: marshal failed", "error", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		slog.Warn("typing reaction ledger: mkdir failed", "path", l.path, "error", err)
		return
	}
	if err := core.AtomicWriteFile(l.path, data, 0o644); err != nil {
		slog.Warn("typing reaction ledger: write failed", "path", l.path, "error", err)
	}
}

// deleteReaction removes one reaction from a message.
func (p *Platform) deleteReaction(ctx context.Context, messageID, reactionID string) error {
	ctx, cancel := context.WithTimeout(ctx, typingReactionDeleteTimeout)
	defer cancel()
	resp, err := p.client.Im.MessageReaction.Delete(ctx,
		larkim.NewDeleteMessageReactionReqBuilder().
			MessageId(messageID).
			ReactionId(reactionID).
			Build())
	if err != nil {
		return fmt.Errorf("%s: delete reaction: %w", p.tag(), err)
	}
	if !resp.Success() {
		return fmt.Errorf("%s: delete reaction: %w", p.tag(), &reactionAPIError{code: resp.Code, msg: resp.Msg})
	}
	return nil
}

// removeTypingReaction deletes a typing reaction, retrying failures that may
// be temporary. The ledger entry is dropped once the reaction is gone; when
// every attempt fails it stays for the next start and the error is returned.
func (p *Platform) removeTypingReaction(ctx context.Context, r typingReaction) error {
	backoff := p.typingDeleteBackoff
	if backoff == nil {
		backoff = defaultTypingDeleteBackoff
	}
	var err error
	for _, wait := range backoff {
		if wait > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}
		err = p.deleteReaction(ctx, r.MessageID, r.ReactionID)
		if err == nil || isReactionGone(err) {
			if err != nil {
				slog.Debug(p.tag()+": typing reaction already gone", "message_id", r.MessageID, "error", err)
			}
			p.typingLedger.remove(r)
			return nil
		}
	}
	return err
}

// sweepTypingReactions removes the typing reactions the previous process
// left behind, for example when it was killed mid-turn.
func (p *Platform) sweepTypingReactions(ctx context.Context) {
	leftover := p.typingLedger.takeLeftover()
	if len(leftover) == 0 {
		return
	}
	removed := 0
	for _, r := range leftover {
		err := p.removeTypingReaction(ctx, r)
		if err == nil {
			removed++
			continue
		}
		if time.Since(r.AddedAt) > typingReactionMaxAge {
			p.typingLedger.remove(r)
			slog.Warn(p.tag()+": giving up on stale typing reaction", "message_id", r.MessageID, "added_at", r.AddedAt, "error", err)
			continue
		}
		slog.Warn(p.tag()+": stale typing reaction not removed, will retry on next start", "message_id", r.MessageID, "error", err)
	}
	slog.Info(p.tag()+": cleaned up typing reactions from previous run", "removed", removed, "total", len(leftover))
}
