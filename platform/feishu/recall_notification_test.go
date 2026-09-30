package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
	lark "github.com/larksuite/oapi-sdk-go/v3"
)

func TestRecallNotification_SendsNewMessageWithoutQuotingDeletedTrigger(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			fmt.Fprint(w, `{"code":0,"tenant_access_token":"recall-token","expire":7200}`)
		case "/open-apis/im/v1/messages":
			requests++
			var body struct {
				ReceiveID string `json:"receive_id"`
				Content   string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.ReceiveID != "oc_original_chat" || r.URL.Query().Get("receive_id_type") != "chat_id" {
				t.Errorf("wrong cancellation receipt recipient: %+v", body)
			}
			if body.Content == "" {
				t.Error("empty cancellation receipt")
			}
			fmt.Fprint(w, `{"code":0,"data":{"message_id":"om_receipt"}}`)
		default:
			t.Errorf("must not reply to the recalled message: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	p := &Platform{platformName: "feishu", threadIsolation: true,
		client: lark.NewClient("recall-test", "secret", lark.WithOpenBaseUrl(srv.URL), lark.WithHttpClient(srv.Client()))}
	var notifier core.MessageRecallNotifier = &interactivePlatform{p}
	err := notifier.NotifyMessageRecall(context.Background(), replyContext{
		chatID: "oc_original_chat", messageID: "om_deleted", sessionKey: "feishu:oc_original_chat:root:om_deleted",
	}, "Task cancelled")
	if err != nil || requests != 1 {
		t.Fatalf("recall notification: requests=%d, error=%v", requests, err)
	}
}
