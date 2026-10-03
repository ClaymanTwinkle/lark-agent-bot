package feishu

import (
	"fmt"
	"strings"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// This file decides which session an event belongs to and where the bot's
// messages for it go. The event handlers and the senders all ask it.
//
// Each conversation is stored under a session key. Users' sessions are saved
// under these strings, so their format must not change:
//
//	{platform}:{chat}:{user}       a user's session in a chat (the default)
//	{platform}:{chat}              the chat's session (share_session_in_channel)
//	{platform}:{chat}:root:{root}  a topic's session (thread_isolation, group chats)
//
// {chat} is a chat_id, or the user's open_id for events that carry no chat
// (bot menu clicks before the bot knows the user's chat, some card
// callbacks).
//
// Where a message goes follows from its reply context; see replyTarget.

// makeSessionKey returns the session an incoming message joins.
func (p *Platform) makeSessionKey(msg *larkim.EventMessage, chatID, userID string) string {
	return p.sessionKeyFor(chatID, userID, p.topicRoot(msg))
}

// topicRoot returns the message whose topic msg's session is scoped to, or ""
// when msg joins a user's or the chat's session. With thread_isolation, a
// group message inside a topic belongs to the topic's root, and a top-level
// group message to itself: the bot's reply opens a topic under it.
func (p *Platform) topicRoot(msg *larkim.EventMessage) string {
	if !p.threadIsolation || msg == nil || stringValue(msg.ChatType) != "group" {
		return ""
	}
	if rootID := stringValue(msg.RootId); rootID != "" {
		return rootID
	}
	return stringValue(msg.MessageId)
}

// sessionKeyFor builds the key of a topic's session, or with topicRoot "" of
// the user's or the chat's session depending on share_session_in_channel.
func (p *Platform) sessionKeyFor(chatID, userID, topicRoot string) string {
	switch {
	case topicRoot != "":
		return fmt.Sprintf("%s:%s:root:%s", p.tag(), chatID, topicRoot)
	case p.shareSessionInChannel:
		return fmt.Sprintf("%s:%s", p.tag(), chatID)
	default:
		return p.userSessionKey(chatID, userID)
	}
}

func (p *Platform) userSessionKey(chatID, userID string) string {
	return fmt.Sprintf("%s:%s:%s", p.tag(), chatID, userID)
}

// sessionKeyFromCardAction returns the session a card button belongs to: the
// one the card was rendered for, which renderCardMap puts in its buttons.
// Every card the bot renders for a session carries it, so the fallback, the
// clicker's or the chat's session, is only for cards rendered without one. A
// callback does not say which topic the card is in, so the fallback is never
// a topic session.
func (p *Platform) sessionKeyFromCardAction(chatID, userID string, value map[string]any) string {
	if value != nil {
		if sessionKey, _ := value["session_key"].(string); sessionKey != "" {
			return sessionKey
		}
	}
	return p.sessionKeyFor(chatID, userID, "")
}

// menuSession returns the session of a bot menu click and the chat its
// messages go to. The menu is shown in the user's chat with the bot, so the
// click joins the session the user's messages in that chat join. Menu events
// name only the user; until the bot has seen the user open or write in the
// chat, the click uses a session of its own keyed by the user's open_id, as
// all menu clicks did before, and messages go to the user.
func (p *Platform) menuSession(userID string) (sessionKey, chatID string) {
	if chatID := p.dmChats.chatOf(userID); chatID != "" {
		return p.sessionKeyFor(chatID, userID, ""), chatID
	}
	return p.userSessionKey(userID, userID), userID
}

// topicOfSessionKey splits a topic session key into its platform, chat and
// topic root. ok is false for any other key.
func topicOfSessionKey(sessionKey string) (platform, chatID, rootID string, ok bool) {
	parts := strings.SplitN(sessionKey, ":", 3)
	if len(parts) != 3 {
		return "", "", "", false
	}
	rootID, ok = parseThreadRootID(parts[2])
	if !ok {
		return "", "", "", false
	}
	return parts[0], parts[1], rootID, true
}

func parseThreadRootID(sessionTail string) (string, bool) {
	rootID, ok := strings.CutPrefix(sessionTail, "root:")
	if !ok || rootID == "" {
		return "", false
	}
	return rootID, true
}

// threadScoped reports whether sessionKey is a topic session and
// thread_isolation is on. Only then do replies stay inside the topic, does
// the bot remember that the topic is engaged, and does it stop quoting
// parent messages the topic session already holds.
func (p *Platform) threadScoped(sessionKey string) bool {
	if !p.threadIsolation {
		return false
	}
	_, _, _, ok := topicOfSessionKey(sessionKey)
	return ok
}

// replyTarget is where a message sent for a reply context goes.
type replyTarget struct {
	replyTo  string // message to reply to; "" sends a new message to the chat
	inThread bool   // keep the reply inside replyTo's topic
}

// replyTarget decides where the messages sent for rc go. They reply to the
// message in rc: the message that started the turn, the card that was
// clicked, or for messages rebuilt from a topic key the topic's root. In a
// topic session with thread_isolation the reply stays inside the topic.
// With reply_to_trigger off, or with no message to reply to (bot menu
// clicks, cron in a user's or the chat's session), they are new messages to
// rc.chatID.
//
// Send follows these rules like Reply: core sends a turn's answers with
// Send, and they quote the message that started the turn. Two senders
// differ. SendCard, used for permission and question cards among others,
// sends a new message as core asks of it, but in a topic session it replies
// inside the topic so the card stays there.
// NotifyMessageRecall always sends a new message: the message it would reply
// to is gone.
func (p *Platform) replyTarget(rc replyContext) replyTarget {
	if rc.messageID == "" || p.noReplyToTrigger {
		return replyTarget{}
	}
	return replyTarget{replyTo: rc.messageID, inThread: p.threadScoped(rc.sessionKey)}
}

// ReconstructReplyCtx rebuilds the reply context of a stored session key, for
// messages core sends without an incoming event (cron, timers, relay,
// restart notices). Messages for a topic session reply to the topic's root,
// so they land in the topic.
func (p *Platform) ReconstructReplyCtx(sessionKey string) (any, error) {
	parts := strings.SplitN(sessionKey, ":", 3)
	if len(parts) < 2 || parts[0] != p.platformName {
		return nil, fmt.Errorf("%s: invalid session key %q", p.tag(), sessionKey)
	}
	rc := replyContext{chatID: parts[1], sessionKey: sessionKey}
	if _, _, rootID, ok := topicOfSessionKey(sessionKey); ok {
		rc.messageID = rootID
	}
	return rc, nil
}

// RelayGroupVisibilityKey implements core.RelayGroupVisibilityTarget. When
// the caller's session is a topic session, the relay visibility echo goes
// back into that topic; otherwise the platform returns ("", false) so core
// falls back to the chat-level ":relay" key. Only this platform's own keys
// qualify: a lark platform's topic keys start with "lark:".
func (p *Platform) RelayGroupVisibilityKey(callerSessionKey string) (string, bool) {
	platform, _, _, ok := topicOfSessionKey(callerSessionKey)
	if !ok || platform != p.platformName {
		return "", false
	}
	return callerSessionKey, true
}

// populateWorkspaceChannelKeys keeps workspace binding scope aligned with the
// session scope. In Feishu topic mode the session key contains the root message
// ID, while the legacy chat-level binding remains the default for new topics.
func (p *Platform) populateWorkspaceChannelKeys(msg *core.Message) {
	if msg == nil || msg.ChannelKey != "" {
		return
	}
	rctx, ok := msg.ReplyCtx.(replyContext)
	if !ok || rctx.chatID == "" {
		return
	}
	msg.ChannelKey = rctx.chatID
	if !p.threadIsolation {
		return
	}
	platform, chatID, rootID, ok := topicOfSessionKey(rctx.sessionKey)
	if !ok || platform != p.platformName || chatID != rctx.chatID {
		return
	}
	msg.ChannelKey = rctx.chatID + ":topic:" + rootID
	msg.LegacyChannelKey = rctx.chatID
}
