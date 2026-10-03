package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
	callback "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkapplication "github.com/larksuite/oapi-sdk-go/v3/service/application/v6"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// These tests pin down, for every path that receives an event or sends a
// message, which session key it uses and where the reply goes. Users'
// sessions are stored under these keys, and with thread_isolation off (the
// default) replies must keep landing where they always have. They talk to
// a fake Feishu API and only look at what the bot dispatches and sends, so
// they do not depend on how the platform computes either.

// routeRecorder is a fake Feishu API that records, in order, the calls that
// decide where a conversation goes:
//
//	"create chat_id:oc_x"         new message in a chat
//	"create open_id:ou_x"         new message to a user
//	"reply om_x"                  reply quoting om_x in the main chat
//	"reply om_x in_thread"        reply inside om_x's topic
//	"fetch om_x"                  read om_x to quote it to the agent
//
// User and chat name lookups and uploads are answered but not recorded.
type routeRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *routeRecorder) record(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

// take returns the calls recorded so far and forgets them.
func (r *routeRecorder) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	calls := r.calls
	r.calls = nil
	return calls
}

func (r *routeRecorder) serveHTTP(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := req.URL.Path
	const messages = "/open-apis/im/v1/messages"
	switch {
	case path == "/open-apis/auth/v3/tenant_access_token/internal":
		_, _ = fmt.Fprint(w, `{"code":0,"msg":"success","expire":7200,"tenant_access_token":"routing-token"}`)
	case strings.HasPrefix(path, "/open-apis/contact/v3/users/"),
		strings.HasPrefix(path, "/open-apis/im/v1/chats/"):
		_, _ = fmt.Fprint(w, `{"code":0,"msg":"success"}`)
	case req.Method == http.MethodPost && path == "/open-apis/im/v1/images":
		_, _ = fmt.Fprint(w, `{"code":0,"msg":"success","data":{"image_key":"img_routing"}}`)
	case req.Method == http.MethodPost && path == messages:
		var body struct {
			ReceiveID string `json:"receive_id"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		r.record("create " + req.URL.Query().Get("receive_id_type") + ":" + body.ReceiveID)
		_, _ = fmt.Fprint(w, `{"code":0,"msg":"success","data":{"message_id":"om_sent"}}`)
	case req.Method == http.MethodPost && strings.HasPrefix(path, messages+"/") && strings.HasSuffix(path, "/reply"):
		var body struct {
			ReplyInThread *bool `json:"reply_in_thread"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		call := "reply " + strings.TrimSuffix(strings.TrimPrefix(path, messages+"/"), "/reply")
		if body.ReplyInThread != nil && *body.ReplyInThread {
			call += " in_thread"
		}
		r.record(call)
		_, _ = fmt.Fprint(w, `{"code":0,"msg":"success","data":{"message_id":"om_sent"}}`)
	case req.Method == http.MethodGet && strings.HasPrefix(path, messages+"/"):
		r.record("fetch " + strings.TrimPrefix(path, messages+"/"))
		_, _ = fmt.Fprint(w, `{"code":0,"msg":"success","data":{"items":[]}}`)
	default:
		r.record("unexpected " + req.Method + " " + path)
		w.WriteHeader(http.StatusNotFound)
	}
}

// newRoutingTestPlatform builds a platform the way the daemon does, from
// config options, talking to a fake Feishu API. Dispatched messages are sent
// to the returned channel.
func newRoutingTestPlatform(t *testing.T, name string, opts map[string]any) (*interactivePlatform, *routeRecorder, chan *core.Message) {
	t.Helper()
	rec := &routeRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(rec.serveHTTP))
	t.Cleanup(srv.Close)

	all := map[string]any{"app_id": "cli_routing", "app_secret": "secret"}
	for k, v := range opts {
		all[k] = v
	}
	pf, err := newPlatform(name, srv.URL, all)
	if err != nil {
		t.Fatalf("newPlatform(%s) error = %v", name, err)
	}
	ip, ok := pf.(*interactivePlatform)
	if !ok {
		t.Fatalf("platform type = %T, want *interactivePlatform", pf)
	}
	ip.botOpenID = "ou_bot"
	got := make(chan *core.Message, 4)
	ip.handler = func(_ core.Platform, msg *core.Message) { got <- msg }
	return ip, rec, got
}

func waitRoutedMessage(t *testing.T, got <-chan *core.Message) *core.Message {
	t.Helper()
	select {
	case msg := <-got:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("no message dispatched")
		return nil
	}
}

func routingOpts(threadIsolation, shareSession bool) map[string]any {
	return map[string]any{"thread_isolation": threadIsolation, "share_session_in_channel": shareSession}
}

func routingOptsName(threadIsolation, shareSession bool) string {
	return fmt.Sprintf("thread_isolation=%v/share_session=%v", threadIsolation, shareSession)
}

// messageShape is where a message sits in a chat, as Feishu reports it.
type messageShape struct {
	rootID, parentID, threadID string
}

var (
	// shapeTop is a plain message in the chat.
	shapeTop = messageShape{}
	// shapeQuote quotes an earlier message without opening a topic.
	shapeQuote = messageShape{rootID: "om_root", parentID: "om_parent"}
	// shapeThread is a reply inside a topic.
	shapeThread = messageShape{rootID: "om_root", parentID: "om_parent", threadID: "omt_thread"}
	// shapeThreadNoParent is a reply inside a topic that names only the root.
	shapeThreadNoParent = messageShape{rootID: "om_root", threadID: "omt_thread"}
	// shapeTopicPost is a new post in a topic group.
	shapeTopicPost = messageShape{threadID: "omt_topic"}
)

func routingMessageEvent(chatType, chatID, messageID string, shape messageShape) *larkim.P2MessageReceiveV1 {
	senderType, msgType := "user", "text"
	content := `{"text":"@_user_1 hi"}`
	createTime := strconv.FormatInt(time.Now().UnixMilli(), 10)
	msg := &larkim.EventMessage{
		ChatId:      stringPtr(chatID),
		ChatType:    stringPtr(chatType),
		MessageType: &msgType,
		Content:     &content,
		CreateTime:  &createTime,
		Mentions: []*larkim.MentionEvent{{
			Key:  stringPtr("@_user_1"),
			Id:   &larkim.UserId{OpenId: stringPtr("ou_bot")},
			Name: stringPtr("Bot"),
		}},
	}
	if messageID != "" {
		msg.MessageId = stringPtr(messageID)
	}
	if shape.rootID != "" {
		msg.RootId = stringPtr(shape.rootID)
	}
	if shape.parentID != "" {
		msg.ParentId = stringPtr(shape.parentID)
	}
	if shape.threadID != "" {
		msg.ThreadId = stringPtr(shape.threadID)
	}
	return &larkim.P2MessageReceiveV1{Event: &larkim.P2MessageReceiveV1Data{
		Sender: &larkim.EventSender{
			SenderId:   &larkim.UserId{OpenId: stringPtr("ou_user")},
			SenderType: &senderType,
		},
		Message: msg,
	}}
}

func assertCalls(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Fatalf("%s: calls = %q, want %q", what, got, want)
	}
}

func assertReplyCtx(t *testing.T, got any, want replyContext) {
	t.Helper()
	rc, ok := got.(replyContext)
	if !ok {
		t.Fatalf("ReplyCtx type = %T, want replyContext", got)
	}
	if rc.messageID != want.messageID || rc.chatID != want.chatID || rc.sessionKey != want.sessionKey || rc.bootstrapThread != want.bootstrapThread {
		t.Fatalf("ReplyCtx = {messageID:%q chatID:%q sessionKey:%q bootstrapThread:%v}, want {messageID:%q chatID:%q sessionKey:%q bootstrapThread:%v}",
			rc.messageID, rc.chatID, rc.sessionKey, rc.bootstrapThread,
			want.messageID, want.chatID, want.sessionKey, want.bootstrapThread)
	}
}

// TestSessionRouting_IncomingMessage covers im.message.receive_v1: the
// session a message joins, the reply context handed to core, the messages
// fetched to quote to the agent, and where p.Reply then answers.
//
// Session keys below omit the "{platform}:" prefix; every case runs for both
// the feishu and the lark platform.
func TestSessionRouting_IncomingMessage(t *testing.T) {
	type want struct {
		key           string // session key without the platform prefix
		bootstrap     bool
		fetch         []string
		channel       string
		legacyChannel string
		reply         string
	}
	type tc struct {
		chatType, chatID string
		shape            string
		isolation, share bool
		want             want
	}
	shapes := map[string]messageShape{
		"top": shapeTop, "quote": shapeQuote, "thread": shapeThread,
		"thread-no-parent": shapeThreadNoParent, "topic-post": shapeTopicPost,
	}
	// Without thread isolation, and in p2p chats whatever the setting, a
	// message joins its sender's session (or the chat's when the session is
	// shared), quotes its parent, and the reply quotes it in the main chat.
	var cases []tc
	for _, chat := range []struct{ chatType, chatID string }{{"p2p", "oc_p2p"}, {"group", "oc_group"}} {
		for _, isolation := range []bool{false, true} {
			if isolation && chat.chatType == "group" {
				continue
			}
			for _, share := range []bool{false, true} {
				key := chat.chatID + ":ou_user"
				if share {
					key = chat.chatID
				}
				for _, shape := range []string{"top", "quote", "thread", "thread-no-parent", "topic-post"} {
					var fetch []string
					if p := shapes[shape].parentID; p != "" {
						fetch = []string{"fetch " + p}
					}
					cases = append(cases, tc{chat.chatType, chat.chatID, shape, isolation, share, want{
						key: key, fetch: fetch, channel: chat.chatID, reply: "reply om_msg",
					}})
				}
			}
		}
	}
	// With thread isolation, a group message joins the session of its topic
	// (its root, or itself when it has none) whatever share_session says. The
	// first message seen in a topic quotes its parent, or the root when it
	// has no parent, and every reply goes inside the topic.
	for _, share := range []bool{false, true} {
		topic := func(root string, fetch ...string) want {
			return want{
				key: "oc_group:root:" + root, bootstrap: true, fetch: fetch,
				channel: "oc_group:topic:" + root, legacyChannel: "oc_group",
				reply: "reply om_msg in_thread",
			}
		}
		cases = append(cases,
			tc{"group", "oc_group", "top", true, share, topic("om_msg")},
			tc{"group", "oc_group", "quote", true, share, topic("om_root", "fetch om_parent")},
			tc{"group", "oc_group", "thread", true, share, topic("om_root", "fetch om_parent")},
			tc{"group", "oc_group", "thread-no-parent", true, share, topic("om_root", "fetch om_root")},
			tc{"group", "oc_group", "topic-post", true, share, topic("om_msg")},
		)
	}

	for _, name := range []string{"feishu", "lark"} {
		for _, c := range cases {
			t.Run(fmt.Sprintf("%s/%s/%s/%s", name, c.chatType, routingOptsName(c.isolation, c.share), c.shape), func(t *testing.T) {
				p, rec, got := newRoutingTestPlatform(t, name, routingOpts(c.isolation, c.share))
				if err := p.onMessage(context.Background(), routingMessageEvent(c.chatType, c.chatID, "om_msg", shapes[c.shape])); err != nil {
					t.Fatalf("onMessage() error = %v", err)
				}
				msg := waitRoutedMessage(t, got)
				key := name + ":" + c.want.key
				if msg.SessionKey != key {
					t.Fatalf("SessionKey = %q, want %q", msg.SessionKey, key)
				}
				assertReplyCtx(t, msg.ReplyCtx, replyContext{messageID: "om_msg", chatID: c.chatID, sessionKey: key, bootstrapThread: c.want.bootstrap})
				if msg.ChannelKey != c.want.channel || msg.LegacyChannelKey != c.want.legacyChannel {
					t.Fatalf("ChannelKey, LegacyChannelKey = %q, %q, want %q, %q", msg.ChannelKey, msg.LegacyChannelKey, c.want.channel, c.want.legacyChannel)
				}
				assertCalls(t, "dispatch", rec.take(), c.want.fetch)
				if err := p.Reply(context.Background(), msg.ReplyCtx, "ok"); err != nil {
					t.Fatalf("Reply() error = %v", err)
				}
				assertCalls(t, "Reply", rec.take(), []string{c.want.reply})
			})
		}
	}
}

// A group message without a message ID cannot root a topic: it falls back
// to the user or chat session, and the reply becomes a new message.
func TestSessionRouting_IncomingMessageWithoutMessageID(t *testing.T) {
	for _, share := range []bool{false, true} {
		t.Run(routingOptsName(true, share), func(t *testing.T) {
			p, rec, got := newRoutingTestPlatform(t, "feishu", routingOpts(true, share))
			if err := p.onMessage(context.Background(), routingMessageEvent("group", "oc_group", "", shapeTop)); err != nil {
				t.Fatalf("onMessage() error = %v", err)
			}
			msg := waitRoutedMessage(t, got)
			key := "feishu:oc_group:ou_user"
			if share {
				key = "feishu:oc_group"
			}
			if msg.SessionKey != key {
				t.Fatalf("SessionKey = %q, want %q", msg.SessionKey, key)
			}
			assertReplyCtx(t, msg.ReplyCtx, replyContext{chatID: "oc_group", sessionKey: key})
			assertCalls(t, "dispatch", rec.take(), nil)
			if err := p.Reply(context.Background(), msg.ReplyCtx, "ok"); err != nil {
				t.Fatalf("Reply() error = %v", err)
			}
			assertCalls(t, "Reply", rec.take(), []string{"create chat_id:oc_group"})
		})
	}
}

// Only the first message the bot accepts in a topic quotes its parent; later
// ones rely on the topic session already holding the context. Without thread
// isolation every reply quotes its parent.
func TestSessionRouting_LaterMessagesInTopic(t *testing.T) {
	for _, isolation := range []bool{false, true} {
		t.Run(routingOptsName(isolation, false), func(t *testing.T) {
			p, rec, got := newRoutingTestPlatform(t, "feishu", routingOpts(isolation, false))
			for i, messageID := range []string{"om_first", "om_second"} {
				if err := p.onMessage(context.Background(), routingMessageEvent("group", "oc_group", messageID, shapeThread)); err != nil {
					t.Fatalf("onMessage(%s) error = %v", messageID, err)
				}
				msg := waitRoutedMessage(t, got)
				want := replyContext{messageID: messageID, chatID: "oc_group", sessionKey: "feishu:oc_group:ou_user"}
				fetch := []string{"fetch om_parent"}
				if isolation {
					want.sessionKey = "feishu:oc_group:root:om_root"
					want.bootstrapThread = i == 0
					if i > 0 {
						fetch = nil
					}
				}
				assertReplyCtx(t, msg.ReplyCtx, want)
				assertCalls(t, "dispatch "+messageID, rec.take(), fetch)
			}
		})
	}
}

// A message from a user outside allow_from is answered with a refusal that
// goes where a normal reply would.
func TestSessionRouting_UnauthorizedReply(t *testing.T) {
	for _, isolation := range []bool{false, true} {
		t.Run(routingOptsName(isolation, false), func(t *testing.T) {
			opts := routingOpts(isolation, false)
			opts["allow_from"] = "ou_someone_else"
			p, rec, got := newRoutingTestPlatform(t, "feishu", opts)
			if err := p.onMessage(context.Background(), routingMessageEvent("group", "oc_group", "om_msg", shapeThread)); err != nil {
				t.Fatalf("onMessage() error = %v", err)
			}
			select {
			case msg := <-got:
				t.Fatalf("unauthorized message dispatched: %+v", msg)
			default:
			}
			want := "reply om_msg"
			if isolation {
				want += " in_thread"
			}
			assertCalls(t, "refusal", rec.take(), []string{want})
		})
	}
}

// TestSessionRouting_Outbound covers every way core sends a message for a
// reply context: whether it replies to the trigger message, whether the reply
// stays inside the topic, and where a new message goes otherwise.
func TestSessionRouting_Outbound(t *testing.T) {
	type tc struct {
		isolation      bool
		replyToTrigger bool
		rc             replyContext
		reply          string // Reply, Send, SendWithStatusFooter, SendImage, SendPreviewStart, ReplyCard
		sendCard       string // SendCard
		recall         string // NotifyMessageRecall
	}
	userKey := replyContext{messageID: "om_msg", chatID: "oc_chat", sessionKey: "feishu:oc_chat:ou_user"}
	chatKey := replyContext{messageID: "om_msg", chatID: "oc_chat", sessionKey: "feishu:oc_chat"}
	rootKey := replyContext{messageID: "om_msg", chatID: "oc_chat", sessionKey: "feishu:oc_chat:root:om_root"}
	rootNoTrigger := replyContext{chatID: "oc_chat", sessionKey: "feishu:oc_chat:root:om_root"}
	toUser := replyContext{chatID: "ou_user", sessionKey: "feishu:ou_user:ou_user"}
	otherPlatformRoot := replyContext{messageID: "om_msg", chatID: "oc_chat", sessionKey: "lark:oc_chat:root:om_root"}

	const (
		reply    = "reply om_msg"
		inThread = "reply om_msg in_thread"
		toChat   = "create chat_id:oc_chat"
		toOpenID = "create open_id:ou_user"
	)
	cases := map[string]tc{
		// thread_isolation off: reply to the trigger in the main chat.
		"off/user":                {false, true, userKey, reply, toChat, toChat},
		"off/chat":                {false, true, chatKey, reply, toChat, toChat},
		"off/root":                {false, true, rootKey, reply, toChat, toChat},
		"off/root-no-trigger":     {false, true, rootNoTrigger, toChat, toChat, toChat},
		"off/open-id":             {false, true, toUser, toOpenID, toOpenID, toOpenID},
		"off/other-platform-root": {false, true, otherPlatformRoot, reply, toChat, toChat},
		// thread_isolation on: topic sessions reply inside the topic.
		"on/user":                {true, true, userKey, reply, toChat, toChat},
		"on/chat":                {true, true, chatKey, reply, toChat, toChat},
		"on/root":                {true, true, rootKey, inThread, inThread, toChat},
		"on/root-no-trigger":     {true, true, rootNoTrigger, toChat, toChat, toChat},
		"on/open-id":             {true, true, toUser, toOpenID, toOpenID, toOpenID},
		"on/other-platform-root": {true, true, otherPlatformRoot, inThread, inThread, toChat},
		// reply_to_trigger = false: always a new message.
		"off-noreply/user":                {false, false, userKey, toChat, toChat, toChat},
		"off-noreply/chat":                {false, false, chatKey, toChat, toChat, toChat},
		"off-noreply/root":                {false, false, rootKey, toChat, toChat, toChat},
		"off-noreply/root-no-trigger":     {false, false, rootNoTrigger, toChat, toChat, toChat},
		"off-noreply/open-id":             {false, false, toUser, toOpenID, toOpenID, toOpenID},
		"off-noreply/other-platform-root": {false, false, otherPlatformRoot, toChat, toChat, toChat},
		"on-noreply/user":                 {true, false, userKey, toChat, toChat, toChat},
		"on-noreply/chat":                 {true, false, chatKey, toChat, toChat, toChat},
		"on-noreply/root":                 {true, false, rootKey, toChat, toChat, toChat},
		"on-noreply/root-no-trigger":      {true, false, rootNoTrigger, toChat, toChat, toChat},
		"on-noreply/open-id":              {true, false, toUser, toOpenID, toOpenID, toOpenID},
		"on-noreply/other-platform-root":  {true, false, otherPlatformRoot, toChat, toChat, toChat},
	}

	ctx := context.Background()
	replyFamily := map[string]func(p *interactivePlatform, rc replyContext) error{
		"Reply": func(p *interactivePlatform, rc replyContext) error { return p.Reply(ctx, rc, "body") },
		"Send":  func(p *interactivePlatform, rc replyContext) error { return p.Send(ctx, rc, "body") },
		"SendWithStatusFooter": func(p *interactivePlatform, rc replyContext) error {
			return p.SendWithStatusFooter(ctx, rc, "body", "footer")
		},
		"SendImage": func(p *interactivePlatform, rc replyContext) error {
			return p.SendImage(ctx, rc, core.ImageAttachment{MimeType: "image/png", Data: []byte("png")})
		},
		"SendPreviewStart": func(p *interactivePlatform, rc replyContext) error {
			_, err := p.SendPreviewStart(ctx, rc, "preview")
			return err
		},
		"ReplyCard": func(p *interactivePlatform, rc replyContext) error {
			return p.ReplyCard(ctx, rc, core.NewCard().Markdown("card").Build())
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p, rec, _ := newRoutingTestPlatform(t, "feishu", map[string]any{
				"thread_isolation": c.isolation, "reply_to_trigger": c.replyToTrigger,
			})
			for method, send := range replyFamily {
				if err := send(p, c.rc); err != nil {
					t.Fatalf("%s() error = %v", method, err)
				}
				assertCalls(t, method, rec.take(), []string{c.reply})
			}
			if err := p.SendCard(ctx, c.rc, core.NewCard().Markdown("card").Build()); err != nil {
				t.Fatalf("SendCard() error = %v", err)
			}
			assertCalls(t, "SendCard", rec.take(), []string{c.sendCard})
			if err := p.NotifyMessageRecall(ctx, c.rc, "cancelled"); err != nil {
				t.Fatalf("NotifyMessageRecall() error = %v", err)
			}
			assertCalls(t, "NotifyMessageRecall", rec.take(), []string{c.recall})
		})
	}
}

// TestSessionRouting_CardAction covers card button callbacks. Cards carry
// the session key they were rendered for; a card without one falls back to
// the user or chat session (never a topic), and the reply goes to the card.
func TestSessionRouting_CardAction(t *testing.T) {
	type want struct {
		key, chatID, channel, legacyChannel, reply string
	}
	type tc struct {
		valueKey string // session_key carried by the card, "" for none
		chatID   string // open_chat_id of the callback, "" for none
		want     func(isolation, share bool) want
	}
	cases := map[string]tc{
		"no-key": {"", "oc_group", func(_, share bool) want {
			key := "feishu:oc_group:ou_user"
			if share {
				key = "feishu:oc_group"
			}
			return want{key, "oc_group", "oc_group", "", "reply om_card"}
		}},
		"no-key-no-chat": {"", "", func(_, share bool) want {
			key := "feishu:ou_user:ou_user"
			if share {
				key = "feishu:ou_user"
			}
			return want{key, "ou_user", "ou_user", "", "reply om_card"}
		}},
		"user-key": {"feishu:oc_group:ou_owner", "oc_group", func(_, _ bool) want {
			return want{"feishu:oc_group:ou_owner", "oc_group", "oc_group", "", "reply om_card"}
		}},
		"root-key": {"feishu:oc_group:root:om_root", "oc_group", func(isolation, _ bool) want {
			if isolation {
				return want{"feishu:oc_group:root:om_root", "oc_group", "oc_group:topic:om_root", "oc_group", "reply om_card in_thread"}
			}
			return want{"feishu:oc_group:root:om_root", "oc_group", "oc_group", "", "reply om_card"}
		}},
		"root-key-no-chat": {"feishu:oc_group:root:om_root", "", func(isolation, _ bool) want {
			reply := "reply om_card"
			if isolation {
				reply += " in_thread"
			}
			return want{"feishu:oc_group:root:om_root", "ou_user", "ou_user", "", reply}
		}},
	}
	for name, c := range cases {
		for _, isolation := range []bool{false, true} {
			for _, share := range []bool{false, true} {
				for _, action := range []string{"cmd:/help", "perm:allow", "askq:yes"} {
					t.Run(fmt.Sprintf("%s/%s/%s", name, routingOptsName(isolation, share), action), func(t *testing.T) {
						p, rec, got := newRoutingTestPlatform(t, "feishu", routingOpts(isolation, share))
						value := map[string]any{"action": action}
						if c.valueKey != "" {
							value["session_key"] = c.valueKey
						}
						event := &callback.CardActionTriggerEvent{Event: &callback.CardActionTriggerRequest{
							Operator: &callback.Operator{OpenID: "ou_user"},
							Action:   &callback.CallBackAction{Value: value},
							Context:  &callback.Context{OpenChatID: c.chatID, OpenMessageID: "om_card"},
						}}
						if _, err := p.onCardAction(event); err != nil {
							t.Fatalf("onCardAction() error = %v", err)
						}
						msg := waitRoutedMessage(t, got)
						w := c.want(isolation, share)
						if msg.SessionKey != w.key {
							t.Fatalf("SessionKey = %q, want %q", msg.SessionKey, w.key)
						}
						assertReplyCtx(t, msg.ReplyCtx, replyContext{messageID: "om_card", chatID: w.chatID, sessionKey: w.key})
						if msg.ChannelKey != w.channel || msg.LegacyChannelKey != w.legacyChannel {
							t.Fatalf("ChannelKey, LegacyChannelKey = %q, %q, want %q, %q", msg.ChannelKey, msg.LegacyChannelKey, w.channel, w.legacyChannel)
						}
						if err := p.Reply(context.Background(), msg.ReplyCtx, "ok"); err != nil {
							t.Fatalf("Reply() error = %v", err)
						}
						assertCalls(t, "Reply", rec.take(), []string{w.reply})
					})
				}
			}
		}
	}
}

// Card navigation (nav:/act:) hands core the same session key as a button
// that dispatches a command.
func TestSessionRouting_CardNavigation(t *testing.T) {
	for _, valueKey := range []string{"", "feishu:oc_group:root:om_root"} {
		for _, isolation := range []bool{false, true} {
			for _, share := range []bool{false, true} {
				t.Run(fmt.Sprintf("key=%q/%s", valueKey, routingOptsName(isolation, share)), func(t *testing.T) {
					p, _, _ := newRoutingTestPlatform(t, "feishu", routingOpts(isolation, share))
					want := valueKey
					if want == "" {
						want = "feishu:oc_group:ou_user"
						if share {
							want = "feishu:oc_group"
						}
					}
					// The handler runs on its own goroutine; onCardAction
					// waits for the card it returns before answering.
					var gotMsg *core.Message
					p.SetCardNavigationContextHandler(func(_ string, msg *core.Message) *core.Card {
						gotMsg = msg
						return core.NewCard().Markdown("page").Build()
					})
					value := map[string]any{"action": "nav:/help"}
					if valueKey != "" {
						value["session_key"] = valueKey
					}
					if _, err := p.onCardAction(&callback.CardActionTriggerEvent{Event: &callback.CardActionTriggerRequest{
						Operator: &callback.Operator{OpenID: "ou_user"},
						Action:   &callback.CallBackAction{Value: value},
						Context:  &callback.Context{OpenChatID: "oc_group", OpenMessageID: "om_card"},
					}}); err != nil {
						t.Fatalf("onCardAction() error = %v", err)
					}
					if gotMsg == nil {
						t.Fatal("card navigation handler not called")
					}
					if gotMsg.SessionKey != want {
						t.Fatalf("SessionKey = %q, want %q", gotMsg.SessionKey, want)
					}
					assertReplyCtx(t, gotMsg.ReplyCtx, replyContext{messageID: "om_card", chatID: "oc_group", sessionKey: want})
					p.cardActionMsgMu.Lock()
					tracked := p.cardActionMsgIDs[want]
					p.cardActionMsgMu.Unlock()
					if tracked != "om_card" {
						t.Fatalf("card tracked for refresh under %q = %q, want om_card", want, tracked)
					}
				})
			}
		}
	}
}

func clickBotMenu(t *testing.T, p *interactivePlatform, got <-chan *core.Message) *core.Message {
	t.Helper()
	var event larkapplication.P2BotMenuV6
	if err := json.Unmarshal([]byte(`{"event":{"event_key":"help","operator":{"operator_id":{"open_id":"ou_user"}}}}`), &event); err != nil {
		t.Fatal(err)
	}
	if err := p.onBotMenu(&event); err != nil {
		t.Fatalf("onBotMenu() error = %v", err)
	}
	return waitRoutedMessage(t, got)
}

func botChatEnteredEvent(chatID, userID string) *larkim.P2ChatAccessEventBotP2pChatEnteredV1 {
	return &larkim.P2ChatAccessEventBotP2pChatEnteredV1{Event: &larkim.P2ChatAccessEventBotP2pChatEnteredV1Data{
		ChatId:     stringPtr(chatID),
		OperatorId: &larkim.UserId{OpenId: stringPtr(userID)},
	}}
}

// writeInDM delivers a message from ou_user in its chat with the bot and
// returns what was dispatched.
func writeInDM(t *testing.T, p *interactivePlatform, rec *routeRecorder, got <-chan *core.Message, messageID string) *core.Message {
	t.Helper()
	if err := p.onMessage(context.Background(), routingMessageEvent("p2p", "oc_p2p", messageID, shapeTop)); err != nil {
		t.Fatalf("onMessage() error = %v", err)
	}
	msg := waitRoutedMessage(t, got)
	rec.take()
	return msg
}

// Bot menu clicks carry only the operator. The menu is shown in the user's
// chat with the bot, so once the bot has seen the user open the chat or
// write in it, a click joins the session the user's messages there join and
// answers in the chat. Before that it uses the user's own session, keyed by
// open_id, and answers the user.
func TestSessionRouting_BotMenu(t *testing.T) {
	for _, name := range []string{"feishu", "lark"} {
		for _, isolation := range []bool{false, true} {
			for _, share := range []bool{false, true} {
				for _, learned := range []string{"unknown", "opened", "wrote"} {
					t.Run(fmt.Sprintf("%s/%s/%s", name, routingOptsName(isolation, share), learned), func(t *testing.T) {
						p, rec, got := newRoutingTestPlatform(t, name, routingOpts(isolation, share))
						var dm *core.Message
						switch learned {
						case "opened":
							p.onBotChatEntered(botChatEnteredEvent("oc_p2p", "ou_user"))
						case "wrote":
							dm = writeInDM(t, p, rec, got, "om_before")
						}

						msg := clickBotMenu(t, p, got)
						key, chatID, reply := name+":ou_user:ou_user", "ou_user", "create open_id:ou_user"
						if learned != "unknown" {
							key, chatID, reply = name+":oc_p2p:ou_user", "oc_p2p", "create chat_id:oc_p2p"
							if share {
								key = name + ":oc_p2p"
							}
						}
						if msg.SessionKey != key {
							t.Fatalf("SessionKey = %q, want %q", msg.SessionKey, key)
						}
						assertReplyCtx(t, msg.ReplyCtx, replyContext{chatID: chatID, sessionKey: key})
						if msg.ChannelKey != "" || msg.LegacyChannelKey != "" {
							t.Fatalf("ChannelKey, LegacyChannelKey = %q, %q, want both empty", msg.ChannelKey, msg.LegacyChannelKey)
						}
						if err := p.Reply(context.Background(), msg.ReplyCtx, "ok"); err != nil {
							t.Fatalf("Reply() error = %v", err)
						}
						assertCalls(t, "Reply", rec.take(), []string{reply})

						if learned == "unknown" {
							return
						}
						if dm == nil {
							dm = writeInDM(t, p, rec, got, "om_after")
						}
						if dm.SessionKey != msg.SessionKey {
							t.Fatalf("message in the chat joins %q, menu click %q", dm.SessionKey, msg.SessionKey)
						}
					})
				}
			}
		}
	}
}

// The bot learns a user's chat only from the user's own one-to-one chat, and
// only for users allowed to use the bot.
func TestSessionRouting_BotMenuIgnoresOtherChats(t *testing.T) {
	cases := map[string]func(t *testing.T, p *interactivePlatform, rec *routeRecorder, got chan *core.Message){
		"group message": func(t *testing.T, p *interactivePlatform, _ *routeRecorder, got chan *core.Message) {
			if err := p.onMessage(context.Background(), routingMessageEvent("group", "oc_group", "om_group", shapeTop)); err != nil {
				t.Fatalf("onMessage() error = %v", err)
			}
			waitRoutedMessage(t, got)
		},
		"chat opened by another user": func(t *testing.T, p *interactivePlatform, _ *routeRecorder, _ chan *core.Message) {
			p.onBotChatEntered(botChatEnteredEvent("oc_other", "ou_other"))
		},
		"chat event without user": func(t *testing.T, p *interactivePlatform, _ *routeRecorder, _ chan *core.Message) {
			p.onBotChatEntered(&larkim.P2ChatAccessEventBotP2pChatEnteredV1{Event: &larkim.P2ChatAccessEventBotP2pChatEnteredV1Data{
				ChatId: stringPtr("oc_p2p"),
			}})
		},
	}
	for name, learn := range cases {
		t.Run(name, func(t *testing.T) {
			p, rec, got := newRoutingTestPlatform(t, "feishu", routingOpts(false, false))
			learn(t, p, rec, got)
			if msg := clickBotMenu(t, p, got); msg.SessionKey != "feishu:ou_user:ou_user" {
				t.Fatalf("SessionKey = %q, want feishu:ou_user:ou_user", msg.SessionKey)
			}
		})
	}

	t.Run("user outside allow_from", func(t *testing.T) {
		opts := routingOpts(false, false)
		opts["allow_from"] = "ou_user"
		p, _, got := newRoutingTestPlatform(t, "feishu", opts)
		p.onBotChatEntered(botChatEnteredEvent("oc_stranger", "ou_stranger"))
		if chatID := p.dmChats.chatOf("ou_stranger"); chatID != "" {
			t.Fatalf("chat of a user outside allow_from = %q, want none", chatID)
		}
		p.onBotChatEntered(botChatEnteredEvent("oc_p2p", "ou_user"))
		if msg := clickBotMenu(t, p, got); msg.SessionKey != "feishu:oc_p2p:ou_user" {
			t.Fatalf("SessionKey = %q, want feishu:oc_p2p:ou_user", msg.SessionKey)
		}
	})
}

// A user's chat is kept in the data dir: after a restart, a menu click joins
// the chat's session before the user opens or writes in it again.
func TestSessionRouting_BotMenuAfterRestart(t *testing.T) {
	opts := routingOpts(false, false)
	opts["cc_data_dir"] = t.TempDir()
	opts["cc_project"] = "proj"

	before, rec, got := newRoutingTestPlatform(t, "feishu", opts)
	writeInDM(t, before, rec, got, "om_before")

	after, _, got := newRoutingTestPlatform(t, "feishu", opts)
	if msg := clickBotMenu(t, after, got); msg.SessionKey != "feishu:oc_p2p:ou_user" {
		t.Fatalf("SessionKey after restart = %q, want feishu:oc_p2p:ou_user", msg.SessionKey)
	}
}

// A recall event has no session; its reply context names the chat and the
// recalled message.
func TestSessionRouting_Recall(t *testing.T) {
	for _, isolation := range []bool{false, true} {
		t.Run(routingOptsName(isolation, false), func(t *testing.T) {
			p, _, got := newRoutingTestPlatform(t, "feishu", routingOpts(isolation, false))
			if err := p.onMessageRecalled(context.Background(), &larkim.P2MessageRecalledV1{Event: &larkim.P2MessageRecalledV1Data{
				MessageId: stringPtr("om_gone"), ChatId: stringPtr("oc_group"),
			}}); err != nil {
				t.Fatalf("onMessageRecalled() error = %v", err)
			}
			msg := waitRoutedMessage(t, got)
			if msg.SessionKey != "" || !msg.Recalled {
				t.Fatalf("recall message = %+v, want no session key and Recalled", msg)
			}
			assertReplyCtx(t, msg.ReplyCtx, replyContext{messageID: "om_gone", chatID: "oc_group"})
		})
	}
}

// TestSessionRouting_ReconstructReplyCtx covers messages core sends without
// an incoming event (cron, timers, heartbeats, restart and resume notices,
// relay, `send`, webhooks): the reply context rebuilt from a stored session
// key, and where p.Send then delivers.
func TestSessionRouting_ReconstructReplyCtx(t *testing.T) {
	type tc struct {
		platform, key     string
		chatID, messageID string
		sendOff, sendOn   string // Send with thread_isolation off / on
	}
	cases := []tc{
		{"feishu", "feishu:oc_chat:ou_user", "oc_chat", "", "create chat_id:oc_chat", "create chat_id:oc_chat"},
		{"feishu", "feishu:oc_chat", "oc_chat", "", "create chat_id:oc_chat", "create chat_id:oc_chat"},
		{"feishu", "feishu:oc_chat:root:om_root", "oc_chat", "om_root", "reply om_root", "reply om_root in_thread"},
		// No version wrote topic keys ending in thread:{id}; such a key is
		// not a topic key.
		{"feishu", "feishu:oc_chat:thread:omt_old", "oc_chat", "", "create chat_id:oc_chat", "create chat_id:oc_chat"},
		{"feishu", "feishu:oc_chat:root:", "oc_chat", "", "create chat_id:oc_chat", "create chat_id:oc_chat"},
		{"feishu", "feishu:oc_chat:relay", "oc_chat", "", "create chat_id:oc_chat", "create chat_id:oc_chat"},
		{"feishu", "feishu:ou_user:ou_user", "ou_user", "", "create open_id:ou_user", "create open_id:ou_user"},
		{"lark", "lark:oc_chat:ou_user", "oc_chat", "", "create chat_id:oc_chat", "create chat_id:oc_chat"},
		{"lark", "lark:oc_chat:root:om_root", "oc_chat", "om_root", "reply om_root", "reply om_root in_thread"},
	}
	for _, c := range cases {
		for _, isolation := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%s/thread_isolation=%v", c.platform, c.key, isolation), func(t *testing.T) {
				p, rec, _ := newRoutingTestPlatform(t, c.platform, routingOpts(isolation, false))
				rctx, err := p.ReconstructReplyCtx(c.key)
				if err != nil {
					t.Fatalf("ReconstructReplyCtx() error = %v", err)
				}
				assertReplyCtx(t, rctx, replyContext{messageID: c.messageID, chatID: c.chatID, sessionKey: c.key})
				if err := p.Send(context.Background(), rctx, "scheduled"); err != nil {
					t.Fatalf("Send() error = %v", err)
				}
				want := c.sendOff
				if isolation {
					want = c.sendOn
				}
				assertCalls(t, "Send", rec.take(), []string{want})
			})
		}
	}

	for _, c := range []struct{ platform, key string }{
		{"feishu", "lark:oc_chat:ou_user"},
		{"lark", "feishu:oc_chat:root:om_root"},
		{"feishu", "feishu"},
		{"feishu", ""},
	} {
		t.Run(fmt.Sprintf("%s/reject/%q", c.platform, c.key), func(t *testing.T) {
			p, _, _ := newRoutingTestPlatform(t, c.platform, nil)
			if rctx, err := p.ReconstructReplyCtx(c.key); err == nil {
				t.Fatalf("ReconstructReplyCtx(%q) = %+v, want error", c.key, rctx)
			}
		})
	}
}

// The relay visibility echo follows a topic on lark as on feishu: a lark
// platform's topic keys start with "lark:". Keys of the other platform and
// non-topic keys fall back to core's default.
func TestSessionRouting_RelayGroupVisibilityKeyOnLark(t *testing.T) {
	p, _, _ := newRoutingTestPlatform(t, "lark", routingOpts(true, false))
	if got, ok := p.RelayGroupVisibilityKey("lark:oc_chat:root:om_root"); got != "lark:oc_chat:root:om_root" || !ok {
		t.Fatalf("RelayGroupVisibilityKey(lark root) = (%q, %v), want the same key", got, ok)
	}
	for _, key := range []string{"lark:oc_chat:ou_user", "feishu:oc_chat:root:om_root"} {
		if got, ok := p.RelayGroupVisibilityKey(key); got != "" || ok {
			t.Fatalf("RelayGroupVisibilityKey(%q) = (%q, %v), want (\"\", false)", key, got, ok)
		}
	}
}

// Group history buffered for the next agent turn is scoped to the topic
// only for messages that are inside one; a top-level mention does not move
// the main chat's history into its new topic session.
func TestSessionRouting_GroupHistoryScope(t *testing.T) {
	cases := []struct {
		chatType  string
		isolation bool
		shape     messageShape
		want      string
	}{
		{"group", false, shapeTop, "chat:oc_chat"},
		{"group", false, shapeThread, "chat:oc_chat"},
		{"group", true, shapeTop, "chat:oc_chat"},
		{"group", true, shapeTopicPost, "chat:oc_chat"},
		{"group", true, shapeThread, "thread:oc_chat:om_root"},
		{"group", true, shapeQuote, "thread:oc_chat:om_root"},
		{"p2p", true, shapeThread, "chat:oc_chat"},
	}
	for _, c := range cases {
		p := &Platform{platformName: "feishu", threadIsolation: c.isolation}
		msg := routingMessageEvent(c.chatType, "oc_chat", "om_msg", c.shape).Event.Message
		if got := p.groupHistoryScope(msg, "oc_chat"); got != c.want {
			t.Errorf("groupHistoryScope(%s, thread_isolation=%v, %+v) = %q, want %q", c.chatType, c.isolation, c.shape, got, c.want)
		}
	}
}
