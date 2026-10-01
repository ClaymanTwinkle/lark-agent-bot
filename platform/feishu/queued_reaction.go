package feishu

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// defaultQueuedEmoji marks a message waiting behind a running turn: Feishu's
// "稍等" (one second) emoji.
const defaultQueuedEmoji = "OneSecond"

// parseQueuedEmoji reads the queued_emoji option: the default when omitted
// or empty, disabled when "none". It is also disabled when it equals the
// processing emoji: Feishu identifies a bot's reaction by message and emoji,
// so clearing the queued mark as processing starts would remove the
// processing reaction as well.
func parseQueuedEmoji(name string, opts map[string]any, reactionEmoji string) string {
	v, _ := opts["queued_emoji"].(string)
	v = strings.TrimSpace(v)
	switch {
	case v == "":
		v = defaultQueuedEmoji
	case strings.EqualFold(v, "none"):
		return ""
	}
	if v == reactionEmoji {
		slog.Warn(name+": queued_emoji equals reaction_emoji; queued messages get a text notice instead", "emoji", v)
		return ""
	}
	return v
}

// MarkQueued adds the queued emoji to a message waiting behind a running
// turn, in place of the engine's text notice. Like the processing emoji, the
// reaction is recorded in the typing ledger until it is removed, so one left
// behind by a crash is cleaned up on the next start.
func (p *Platform) MarkQueued(ctx context.Context, rctx any) (clear func(), ok bool) {
	rc, isReply := rctx.(replyContext)
	if p.queuedEmoji == "" || !isReply || rc.messageID == "" {
		return nil, false
	}
	// A receipt with the same emoji owns it (see StartTyping). It already
	// marks the message, and deleting a queued reaction would remove it.
	if rc.receiptMessageID == rc.messageID && rc.receiptEmoji == p.queuedEmoji {
		return nil, true
	}
	reactionID := p.addReactionWithEmojiContext(ctx, rc.messageID, p.queuedEmoji)
	if reactionID == "" {
		return nil, false
	}
	r := typingReaction{MessageID: rc.messageID, ReactionID: reactionID, AddedAt: time.Now()}
	p.typingLedger.add(r)
	return func() {
		go func() {
			if err := p.removeTypingReaction(context.Background(), r); err != nil {
				slog.Warn(p.tag()+": queued reaction not removed, will retry on next start", "message_id", r.MessageID, "error", err)
			}
		}()
	}, true
}
