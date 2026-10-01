package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// queuedTestServer fakes the reaction API, recording the emoji of each create
// and the path of each delete. createBody, when set, answers creates.
type queuedTestServer struct {
	mu         sync.Mutex
	createBody string
	creates    []string
	deleted    chan string
}

func (s *queuedTestServer) serve(t *testing.T) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		var body string
		switch r.Method {
		case http.MethodPost:
			var req struct {
				ReactionType struct {
					EmojiType string `json:"emoji_type"`
				} `json:"reaction_type"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			s.mu.Lock()
			s.creates = append(s.creates, req.ReactionType.EmojiType)
			body = s.createBody
			s.mu.Unlock()
			if body == "" {
				body = fmt.Sprintf(`{"code":0,"data":{"reaction_id":"r-%s"}}`, req.ReactionType.EmojiType)
			}
		case http.MethodDelete:
			body = `{"code":0}`
			defer func() { s.deleted <- r.URL.Path }()
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			return
		}
		if _, err := fmt.Fprint(w, body); err != nil {
			t.Errorf("write fixture response: %v", err)
		}
	}
}

func (s *queuedTestServer) createdEmojis() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.creates...)
}

func newQueuedTestPlatform(t *testing.T, dataDir string, opts map[string]any) (*Platform, *queuedTestServer) {
	t.Helper()
	srv := &queuedTestServer{deleted: make(chan string, 4)}
	opts["cc_data_dir"] = dataDir
	opts["cc_project"] = typingTestProject
	p := receiptTestPlatform(t, opts, srv.serve(t))
	p.typingDeleteBackoff = []time.Duration{0}
	return p, srv
}

func TestMarkQueued_AddsDefaultEmojiAndClearRemovesIt(t *testing.T) {
	dir := t.TempDir()
	p, srv := newQueuedTestPlatform(t, dir, map[string]any{})
	path := typingTestLedgerPath(t, dir)

	clear, ok := p.MarkQueued(context.Background(), replyContext{messageID: "om_2", chatID: "oc_main"})
	if !ok || clear == nil {
		t.Fatalf("MarkQueued ok=%v clear=%v, want a mark", ok, clear != nil)
	}
	if got := srv.createdEmojis(); !equalStrings(got, []string{defaultQueuedEmoji}) {
		t.Fatalf("created reactions = %v, want [%s]", got, defaultQueuedEmoji)
	}
	// Recorded like the processing emoji, so a crash leaves nothing behind.
	if got := readTypingLedger(t, path); !equalStrings(got, []string{"om_2/r-" + defaultQueuedEmoji}) {
		t.Fatalf("ledger while queued = %v", got)
	}

	clear()
	select {
	case got := <-srv.deleted:
		if !strings.HasSuffix(got, "/om_2/reactions/r-"+defaultQueuedEmoji) {
			t.Fatalf("deleted %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued reaction not deleted")
	}
	deadline := time.Now().Add(2 * time.Second)
	for readTypingLedger(t, path) != nil {
		if time.Now().After(deadline) {
			t.Fatalf("ledger still holds removed reaction: %v", readTypingLedger(t, path))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestMarkQueued_NotShown(t *testing.T) {
	for _, tc := range []struct {
		name       string
		opts       map[string]any
		createBody string
		wantCreate bool
	}{
		{name: "disabled", opts: map[string]any{"queued_emoji": "none"}},
		{name: "same as processing emoji", opts: map[string]any{"queued_emoji": "OnIt"}},
		{name: "add fails", opts: map[string]any{}, createBody: `{"code":231002,"msg":"no permission"}`, wantCreate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, srv := newQueuedTestPlatform(t, t.TempDir(), tc.opts)
			srv.createBody = tc.createBody

			clear, ok := p.MarkQueued(context.Background(), replyContext{messageID: "om_2", chatID: "oc_main"})
			if ok || clear != nil {
				t.Fatalf("MarkQueued ok=%v clear=%v, want no mark so the engine sends its text notice", ok, clear != nil)
			}
			if got := len(srv.createdEmojis()) > 0; got != tc.wantCreate {
				t.Fatalf("reaction requested = %v, want %v", got, tc.wantCreate)
			}
		})
	}
}

// A receipt with the same emoji already marks the message; adding and later
// deleting a queued reaction would remove the receipt.
func TestMarkQueued_ReceiptWithSameEmojiOwnsIt(t *testing.T) {
	p, srv := newQueuedTestPlatform(t, t.TempDir(), map[string]any{"queued_emoji": "Get"})

	clear, ok := p.MarkQueued(context.Background(), replyContext{
		messageID: "om_2", chatID: "oc_main", receiptMessageID: "om_2", receiptEmoji: "Get",
	})
	if !ok {
		t.Fatal("expected the receipt to count as the mark")
	}
	if clear != nil {
		t.Fatal("expected no clear func: the receipt is not removed")
	}
	if got := srv.createdEmojis(); len(got) != 0 {
		t.Fatalf("created reactions = %v, want none", got)
	}
}

func TestParseQueuedEmoji(t *testing.T) {
	for _, tc := range []struct {
		name, reaction string
		opts           map[string]any
		want           string
	}{
		{"omitted", "OnIt", map[string]any{}, defaultQueuedEmoji},
		{"empty", "OnIt", map[string]any{"queued_emoji": ""}, defaultQueuedEmoji},
		{"none", "OnIt", map[string]any{"queued_emoji": " NONE "}, ""},
		{"custom", "OnIt", map[string]any{"queued_emoji": "Alarm"}, "Alarm"},
		{"default equals processing", defaultQueuedEmoji, map[string]any{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseQueuedEmoji("feishu", tc.opts, tc.reaction); got != tc.want {
				t.Fatalf("parseQueuedEmoji = %q, want %q", got, tc.want)
			}
		})
	}
}
