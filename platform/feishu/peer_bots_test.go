package feishu

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ClaymanTwinkle/lark-connect/core"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const (
	peerTestBotOpenID = "ou_self_bot"
	peerTestChatID    = "oc_group"
)

// newPeerBotTestPlatform returns a platform backed by a fake Feishu API.
// Bodies of outbound message creates and replies are sent to the returned
// channel; any other outbound call fails the test.
func newPeerBotTestPlatform(t *testing.T, handler core.MessageHandler) (*Platform, chan string) {
	t.Helper()
	outbound := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			writeJSON(t, w, map[string]any{"code": 0, "msg": "success", "expire": 7200, "tenant_access_token": "tenant-token"})
		case strings.HasPrefix(r.URL.Path, "/open-apis/contact/v3/users/"),
			strings.HasPrefix(r.URL.Path, "/open-apis/im/v1/chats/"):
			writeJSON(t, w, map[string]any{"code": 0, "msg": "success"})
		case r.Method == http.MethodPost && (r.URL.Path == "/open-apis/im/v1/messages" || strings.HasSuffix(r.URL.Path, "/reply")):
			body, _ := io.ReadAll(r.Body)
			outbound <- string(body)
			writeJSON(t, w, map[string]any{"code": 0, "msg": "success", "data": map[string]any{"message_id": "om_sent"}})
		default:
			t.Errorf("unexpected Feishu API call %s %s", r.Method, r.URL.Path)
			writeJSON(t, w, map[string]any{"code": 0, "msg": "success"})
		}
	}))
	t.Cleanup(srv.Close)

	p := &Platform{
		platformName:    "feishu",
		domain:          srv.URL,
		appID:           "cli_self",
		appSecret:       "secret",
		allowFrom:       "ou_owner",
		botOpenID:       peerTestBotOpenID,
		resolveMentions: true,
		peerBots:        map[string]string{"cli_codex": "Codex"},
		mentionMap:      map[string]string{"Codex": "ou_codex_seen_here"},
		dedup:           &core.MessageDedup{},
		client: lark.NewClient("cli_self", "secret",
			lark.WithOpenBaseUrl(srv.URL),
			lark.WithHttpClient(srv.Client()),
		),
		handler: handler,
	}
	// No chat-member lookup: only mention_map names resolve.
	p.chatMemberCache.Store(peerTestChatID, &chatMemberEntry{members: map[string]string{}, fetchedAt: time.Now()})
	return p, outbound
}

// groupMessageFrom builds a group message event that @-mentions this bot.
func groupMessageFrom(senderID, senderType, text string) *larkim.P2MessageReceiveV1 {
	chatType, msgType := "group", "text"
	content := `{"text":"@_user_1 ` + text + `"}`
	createTime := strconv.FormatInt(time.Now().UnixMilli(), 10)
	return &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderId:   &larkim.UserId{OpenId: stringPtr(senderID)},
				SenderType: &senderType,
			},
			Message: &larkim.EventMessage{
				MessageId:   stringPtr("om_" + senderID + "_" + createTime),
				ChatId:      stringPtr(peerTestChatID),
				ChatType:    &chatType,
				MessageType: &msgType,
				Content:     &content,
				CreateTime:  &createTime,
				Mentions: []*larkim.MentionEvent{{
					Key:  stringPtr("@_user_1"),
					Id:   &larkim.UserId{OpenId: stringPtr(peerTestBotOpenID)},
					Name: stringPtr("Self"),
				}},
			},
		},
	}
}

func TestOnMessageAcceptsTaskFromPeerBot(t *testing.T) {
	for _, senderID := range []string{"cli_codex", "ou_codex_seen_here"} {
		t.Run(senderID, func(t *testing.T) {
			got := make(chan *core.Message, 1)
			p, _ := newPeerBotTestPlatform(t, func(_ core.Platform, msg *core.Message) { got <- msg })

			if err := p.onMessage(context.Background(), groupMessageFrom(senderID, "app", "review the diff")); err != nil {
				t.Fatalf("onMessage() error = %v", err)
			}
			select {
			case msg := <-got:
				if msg.Content != "review the diff" {
					t.Fatalf("Content = %q, want the task text", msg.Content)
				}
				if !p.sessionStartedByBot(msg.SessionKey) {
					t.Fatalf("session %q not marked as started by a bot", msg.SessionKey)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("task from a peer bot was not dispatched")
			}
		})
	}
}

func TestOnMessageIgnoresUnknownBotWithoutReplying(t *testing.T) {
	p, outbound := newPeerBotTestPlatform(t, func(core.Platform, *core.Message) {
		t.Error("handler ran for a bot that is not a peer")
	})

	if err := p.onMessage(context.Background(), groupMessageFrom("cli_stranger", "app", "hello")); err != nil {
		t.Fatalf("onMessage() error = %v", err)
	}
	select {
	case body := <-outbound:
		t.Fatalf("replied %q to an unknown bot, want silence", body)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestOnMessageStillRepliesUnauthorizedToPeople(t *testing.T) {
	p, outbound := newPeerBotTestPlatform(t, func(core.Platform, *core.Message) {
		t.Error("handler ran for an unauthorized person")
	})
	// A person whose ID happens to be listed as a peer bot is still a person.
	if err := p.onMessage(context.Background(), groupMessageFrom("ou_codex_seen_here", "user", "hello")); err != nil {
		t.Fatalf("onMessage() error = %v", err)
	}
	select {
	case body := <-outbound:
		if !strings.Contains(body, core.UnauthorizedAccessMessage) {
			t.Fatalf("reply = %q, want the unauthorized notice", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no unauthorized reply to a person")
	}
}

func TestSessionStartedByBot(t *testing.T) {
	p, _ := newPeerBotTestPlatform(t, nil)
	p.rememberSessionTrigger("feishu:oc_group:root:om_thread", true)

	for key, want := range map[string]bool{
		"feishu:oc_group:ou_owner":           false,
		"feishu:oc_group:cli_codex":          true,
		"feishu:oc_group:ou_codex_seen_here": true,
		"feishu:oc_group:relay":              true,
		"feishu:oc_group:root:om_thread":     true,
		"feishu:oc_group:root:om_other":      false,
		"":                                   false,
	} {
		if got := p.sessionStartedByBot(key); got != want {
			t.Errorf("sessionStartedByBot(%q) = %v, want %v", key, got, want)
		}
	}

	p.rememberSessionTrigger("feishu:oc_group:root:om_thread", false)
	if p.sessionStartedByBot("feishu:oc_group:root:om_thread") {
		t.Error("a person's message must clear the bot-started mark")
	}
}

func TestResolveOutboundMentionsOnlyOutsideBotStartedSessions(t *testing.T) {
	p, _ := newPeerBotTestPlatform(t, nil)
	ctx := context.Background()

	human := p.resolveOutboundMentions(ctx, replyContext{chatID: peerTestChatID, sessionKey: "feishu:oc_group:ou_owner"}, "@Codex review it")
	if !strings.Contains(human, `<at user_id="ou_codex_seen_here">Codex</at>`) {
		t.Fatalf("human session: %q, want a native @ of Codex", human)
	}

	fromBot := p.resolveOutboundMentions(ctx, replyContext{chatID: peerTestChatID, sessionKey: "feishu:oc_group:cli_codex"}, "@Codex review it")
	if fromBot != "@Codex review it" {
		t.Fatalf("bot-started session: %q, want the @ left as plain text", fromBot)
	}
}

func TestReplyWithAtSendsNativeMentions(t *testing.T) {
	p, outbound := newPeerBotTestPlatform(t, nil)

	rc := replyContext{chatID: peerTestChatID, sessionKey: "feishu:oc_group:ou_owner"}
	if err := p.ReplyWithAt(context.Background(), rc, "please look", []string{"ou_codex_seen_here"}, false); err != nil {
		t.Fatalf("ReplyWithAt() error = %v", err)
	}
	msgType, text := decodeSentMessage(t, <-outbound)
	if msgType != "text" {
		t.Fatalf("msg_type = %q, want text so Feishu notifies the target", msgType)
	}
	if text != `<at user_id="ou_codex_seen_here"></at> please look` {
		t.Fatalf("text = %q, want a native @ before the message", text)
	}
}

// decodeSentMessage unwraps a Feishu send/reply body: the text lives in a
// JSON string inside the JSON body.
func decodeSentMessage(t *testing.T, body string) (msgType, text string) {
	t.Helper()
	var outer struct {
		MsgType string `json:"msg_type"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(body), &outer); err != nil {
		t.Fatalf("decode body %s: %v", body, err)
	}
	var inner struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(outer.Content), &inner); err != nil {
		t.Fatalf("decode content %s: %v", outer.Content, err)
	}
	return outer.MsgType, inner.Text
}

func TestReplyWithAtDoesNotMentionFromBotStartedSession(t *testing.T) {
	p, outbound := newPeerBotTestPlatform(t, nil)

	rc := replyContext{chatID: peerTestChatID, sessionKey: "feishu:oc_group:cli_codex"}
	if err := p.ReplyWithAt(context.Background(), rc, "done", []string{"ou_someone"}, true); err != nil {
		t.Fatalf("ReplyWithAt() error = %v", err)
	}
	if _, text := decodeSentMessage(t, <-outbound); text != "done" {
		t.Fatalf("text = %q, want no @ from a conversation a bot started", text)
	}
}

func TestPeerBotNames(t *testing.T) {
	p := &Platform{mentionMap: map[string]string{"Codex": "ou_1", "Claude Code": "ou_2", "Empty": ""}}
	if got := p.PeerBotNames(); got != nil {
		t.Fatalf("PeerBotNames() = %#v without resolve_mentions, want nil", got)
	}
	p.resolveMentions = true
	if got := p.PeerBotNames(); !reflect.DeepEqual(got, []string{"Claude Code", "Codex"}) {
		t.Fatalf("PeerBotNames() = %#v", got)
	}
}

func TestAtMentionPrefix(t *testing.T) {
	if got := atMentionPrefix([]string{"ou_1", " ", "ou_2"}, true); got != `<at user_id="ou_1"></at> <at user_id="ou_2"></at> <at user_id="all"></at> ` {
		t.Fatalf("atMentionPrefix() = %q", got)
	}
	if got := atMentionPrefix(nil, false); got != "" {
		t.Fatalf("atMentionPrefix() = %q, want empty", got)
	}
}
