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
	larkapplication "github.com/larksuite/oapi-sdk-go/v3/service/application/v6"
)

// Menu events contain an operator open_id, but no message_id or chat_id.
// Exercise the event-to-reply path so a valid command cannot silently lose its reply.
func TestOnBotMenu_RepliesToOperatorOpenID(t *testing.T) {
	for _, command := range []string{"help", "status", "upgrade"} {
		for _, mode := range []string{"card", "text", "preview"} {
			t.Run(command+"/"+mode, func(t *testing.T) {
				const userID = "ou_menu_user"
				requests := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/open-apis/auth/v3/tenant_access_token/internal":
						fmt.Fprint(w, `{"code":0,"tenant_access_token":"menu-token","expire":7200}`)
					case "/open-apis/im/v1/messages":
						requests++
						var body struct {
							ReceiveID string `json:"receive_id"`
						}
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						if r.URL.Query().Get("receive_id_type") != "open_id" || body.ReceiveID != userID {
							t.Errorf("menu reply recipient = %s/%s, want open_id/%s", r.URL.Query().Get("receive_id_type"), body.ReceiveID, userID)
							fmt.Fprint(w, `{"code":230001,"msg":"invalid receive_id"}`)
							return
						}
						fmt.Fprint(w, `{"code":0,"data":{"message_id":"om_menu_reply","chat_id":"oc_private"}}`)
					default:
						t.Errorf("unexpected request: %s", r.URL.Path)
						w.WriteHeader(http.StatusNotFound)
					}
				}))
				defer srv.Close()

				p := &Platform{
					platformName: "feishu", allowFrom: userID, useInteractiveCard: true,
					client: lark.NewClient("menu-test", "secret", lark.WithOpenBaseUrl(srv.URL), lark.WithHttpClient(srv.Client())),
				}
				p.userNameCache.Store(userID, "Menu user")
				dispatched := false
				p.handler = func(_ core.Platform, msg *core.Message) {
					dispatched = true
					if msg.Content != "/"+command || msg.UserID != userID {
						t.Fatalf("unexpected dispatched command: %+v", msg)
					}
					var err error
					switch mode {
					case "card":
						err = (&interactivePlatform{p}).ReplyCard(context.Background(), msg.ReplyCtx, &core.Card{Header: &core.CardHeader{Title: command}})
					case "text":
						err = p.Reply(context.Background(), msg.ReplyCtx, command)
					case "preview":
						_, err = p.SendPreviewStart(context.Background(), msg.ReplyCtx, command)
					}
					if err != nil {
						t.Errorf("menu reply failed: %v", err)
					}
				}
				var event larkapplication.P2BotMenuV6
				if err := json.Unmarshal([]byte(fmt.Sprintf(`{"event":{"event_key":%q,"operator":{"operator_id":{"open_id":%q}}}}`, command, userID)), &event); err != nil {
					t.Fatal(err)
				}
				if err := p.onBotMenu(&event); err != nil {
					t.Fatal(err)
				}
				if !dispatched || requests != 1 {
					t.Fatalf("dispatched=%v, reply requests=%d; want true, 1", dispatched, requests)
				}
			})
		}
	}
}
