package feishu

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"sort"
	"strings"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// Bots in the same group can hand work to each other with a real @: Feishu
// delivers a bot's text message to every bot it @-mentions. Three pieces make
// that usable:
//
//   - mention_map (with resolve_mentions) turns "@Name" in an outbound message
//     into a native @ of that bot, and PeerBotNames tells the agent who it can
//     @ this way.
//   - Messages from bots listed in peer_bots or mention_map are accepted even
//     though allow_from only lists people.
//   - A conversation a peer bot started never produces a native @, so a
//     delegated task cannot be handed on and two bots cannot @ each other in
//     a loop: delegation stops after one hop.

var _ core.PeerBotProvider = (*Platform)(nil)
var _ core.AtMentionSender = (*Platform)(nil)

// PeerBotNames implements core.PeerBotProvider: the mention_map names, which
// become native @ mentions when resolve_mentions is on.
func (p *Platform) PeerBotNames() []string {
	if !p.resolveMentions {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	names := make([]string, 0, len(p.mentionMap))
	for name, openID := range p.mentionMap {
		if openID != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// isPeerBotSender reports whether id identifies a bot this platform takes work
// from: an app_id listed in peer_bots or an open_id (as this app sees it)
// listed in mention_map. Both forms are accepted because Feishu does not
// document which one a bot sender's event carries.
func (p *Platform) isPeerBotSender(id string) bool {
	if id == "" {
		return false
	}
	if _, ok := p.peerBots[id]; ok {
		return true
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, openID := range p.mentionMap {
		if openID == id {
			return true
		}
	}
	return false
}

// rememberSessionTrigger records whether the latest accepted message in a
// session came from a peer bot. It backs sessionStartedByBot for session keys
// that do not carry the sender (thread isolation, shared channel sessions).
func (p *Platform) rememberSessionTrigger(sessionKey string, fromBot bool) {
	if sessionKey == "" {
		return
	}
	if fromBot {
		p.botTriggeredSessions.Store(sessionKey, true)
	} else {
		p.botTriggeredSessions.Delete(sessionKey)
	}
}

// sessionStartedByBot reports whether outbound messages in this session must
// not @ anyone: the session was started by a peer bot, or it is the relay
// visibility echo (which already addresses the other bot through relay).
func (p *Platform) sessionStartedByBot(sessionKey string) bool {
	parts := strings.SplitN(sessionKey, ":", 3)
	if len(parts) == 3 && (parts[2] == "relay" || p.isPeerBotSender(parts[2])) {
		return true
	}
	_, ok := p.botTriggeredSessions.Load(sessionKey)
	return ok
}

// resolveOutboundMentions turns "@Name" into native mentions, except in
// sessions a peer bot started, where "@Name" stays plain text.
func (p *Platform) resolveOutboundMentions(ctx context.Context, rc replyContext, content string) string {
	if p.sessionStartedByBot(rc.sessionKey) {
		return content
	}
	return p.resolveMentionsInContent(ctx, rc.chatID, content)
}

// ReplyWithAt implements core.AtMentionSender (`lark-agent-bot send --at-users
// / --at-all`): the IDs become native @ mentions in a text message.
func (p *Platform) ReplyWithAt(ctx context.Context, rctx any, content string, atUsers []string, atAll bool) error {
	rc, ok := rctx.(replyContext)
	if !ok {
		return fmt.Errorf("%s: invalid reply context type %T", p.tag(), rctx)
	}
	if p.sessionStartedByBot(rc.sessionKey) {
		slog.Info(p.tag()+": not mentioning anyone from a conversation another bot started",
			"session_key", rc.sessionKey)
	} else {
		content = atMentionPrefix(atUsers, atAll) + content
	}
	return p.Send(ctx, rc, content)
}

// atMentionPrefix builds Feishu text-message at tags for open_ids.
func atMentionPrefix(atUsers []string, atAll bool) string {
	var b strings.Builder
	for _, id := range atUsers {
		if id = strings.TrimSpace(id); id != "" {
			fmt.Fprintf(&b, `<at user_id="%s"></at> `, html.EscapeString(id))
		}
	}
	if atAll {
		b.WriteString(`<at user_id="all"></at> `)
	}
	return b.String()
}
