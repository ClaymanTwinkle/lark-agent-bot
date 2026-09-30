package main

import (
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ClaymanTwinkle/lark-agent-bot/config"
)

func TestSetupRegistrationURL_RoundTripTemplateAndPreset(t *testing.T) {
	addons, err := loadSetupTemplate("")
	if err != nil {
		t.Fatal(err)
	}
	if len(addons.Scopes.Tenant) != 35 || len(addons.Scopes.User) != 1 {
		t.Fatal("shared permissions changed; review the template contract")
	}
	if !slices.Contains(addons.Scopes.Tenant, "im:message.reactions:write_only") {
		t.Fatal("missing processing reaction permission")
	}
	if !slices.Contains(addons.Events.Items.Tenant, "application.bot.menu_v6") {
		t.Fatal("missing menu event")
	}
	raw, err := setupRegistrationURL("https://accounts.feishu.cn/confirm?device=keep-me&createOnly=false", registrationFlowOptions{
		Addons: addons, Name: "Claude & Codex 测试", Description: "remote + local", Avatar: "https://example.com/a.png?x=1&y=2",
	})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	q := u.Query()
	if q.Get("device") != "keep-me" || q.Get("createOnly") != "true" || q.Get("name") != "Claude & Codex 测试" || q.Get("avatar") != "https://example.com/a.png?x=1&y=2" {
		t.Fatalf("bad URL: %v", q)
	}
	compressed, err := base64.RawURLEncoding.DecodeString(q.Get("addons"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := gzip.NewReader(strings.NewReader(string(compressed)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	var got setupAddons
	if err := json.NewDecoder(r).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&got, addons) {
		t.Fatal("template did not survive URL encoding")
	}
}

func TestLoadSetupTemplate_RejectsBrokenOverrides(t *testing.T) {
	for _, data := range []string{`{}`, `null`, `{"unknown":true}`, string(defaultFeishuSetupTemplate) + `{}`, strings.Replace(string(defaultFeishuSetupTemplate), "im:resource", "", 1)} {
		file := filepath.Join(t.TempDir(), "template.json")
		if err := os.WriteFile(file, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadSetupTemplate(file); err == nil {
			t.Fatalf("accepted invalid template: %.30s", data)
		}
	}
	file := filepath.Join(t.TempDir(), "template.json")
	data := strings.Replace(string(defaultFeishuSetupTemplate), `"offline_access"`, `"calendar:calendar:read"`, 1)
	if err := os.WriteFile(file, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := loadSetupTemplate(file)
	if err != nil || got.Scopes.User[0] != "calendar:calendar:read" {
		t.Fatalf("valid override: %v", err)
	}
}

func TestSetupCheck_VerifiesGrantedIdentityAndSubscriptions(t *testing.T) {
	addons, _ := loadSetupTemplate("")
	for _, scenario := range []string{"complete", "pending", "wrong-identity", "missing-event", "omitted-subscriptions", "owner-is-bot", "denied"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
					var body map[string]string
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body["app_id"] != "cli_test" || body["app_secret"] != "test-secret" {
						t.Error("incorrect token request")
					}
					_, _ = fmt.Fprint(w, `{"code":0,"tenant_access_token":"test-token"}`)
					return
				}
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("missing token")
				}
				switch r.URL.Path {
				case "/open-apis/bot/v3/info":
					_, _ = fmt.Fprint(w, `{"code":0,"bot":{"open_id":"ou_bot"}}`)
				case "/open-apis/application/v6/scopes":
					var scopes []map[string]any
					for _, group := range []struct {
						id    string
						names []string
					}{{"tenant", addons.Scopes.Tenant}, {"user", addons.Scopes.User}} {
						for _, name := range group.names {
							status, identity := 1, group.id
							if name == "im:resource" && scenario == "pending" {
								status = 0
							}
							if name == "im:resource" && scenario == "wrong-identity" {
								identity = "user"
							}
							scopes = append(scopes, map[string]any{"scope_name": name, "scope_type": identity, "grant_status": status})
						}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"scopes": scopes}})
				case "/open-apis/application/v6/applications/cli_test":
					if r.URL.Query().Get("user_id_type") != "open_id" {
						t.Error("owner identity must be app-scoped")
					}
					if scenario == "denied" {
						_, _ = fmt.Fprint(w, `{"code":99991672,"msg":"test-secret"}`)
						return
					}
					owner := "ou_owner"
					if scenario == "owner-is-bot" {
						owner = "ou_bot"
					}
					app := map[string]any{"owner": map[string]string{"owner_id": owner}}
					if scenario != "omitted-subscriptions" {
						events := addons.Events.Items.Tenant
						if scenario == "missing-event" {
							events = nil
						}
						app["event"] = map[string]any{"subscribed_events": events}
						app["callback"] = map[string]any{"subscribed_callbacks": addons.Callbacks.Items}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"app": app}})
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			api := setupAPI{base: server.URL, client: server.Client()}
			check, err := api.check("cli_test", "test-secret", addons)
			if scenario == "denied" {
				if err == nil || strings.Contains(err.Error(), "test-secret") || !strings.Contains(err.Error(), "99991672") {
					t.Fatalf("unsafe/incorrect error: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantMissing := scenario == "pending" || scenario == "wrong-identity" || scenario == "missing-event"
			if (len(check.Missing) > 0) != wantMissing {
				t.Fatalf("missing: %v", check.Missing)
			}
			if check.SubscriptionsVerified != (scenario != "omitted-subscriptions") {
				t.Fatal("incorrect verification status")
			}
			if (check.OwnerOpenID == "") != (scenario == "owner-is-bot") {
				t.Fatalf("owner: %s", check.OwnerOpenID)
			}
		})
	}
}

func TestRegistrationFlow_UsesTemplateAndDoesNotLogCredentials(t *testing.T) {
	var actions []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		action := r.Form.Get("action")
		actions = append(actions, action)
		switch action {
		case "init":
			_, _ = fmt.Fprint(w, `{"supported_auth_methods":["client_secret"]}`)
		case "begin":
			_, _ = fmt.Fprint(w, `{"device_code":"private-device","verification_uri_complete":"https://accounts.feishu.cn/confirm?ticket=test","expire_in":30}`)
		case "poll":
			if r.Form.Get("device_code") != "private-device" {
				t.Error("wrong device code")
			}
			_, _ = fmt.Fprint(w, `{"client_id":"cli_test","client_secret":"private-secret","user_info":{"open_id":"ou_registration","tenant_brand":"feishu"}}`)
		default:
			t.Error("unexpected action")
		}
	}))
	defer server.Close()
	oldOut, oldErr := os.Stdout, os.Stderr
	file, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = file, file
	t.Cleanup(func() { os.Stdout, os.Stderr = oldOut, oldErr; _ = file.Close() })
	result, err := runRegistrationFlowWithClient(registrationFlowOptions{TimeoutSeconds: 1, Name: "test bot"}, &registrationClient{baseURL: server.URL, http: server.Client(), debug: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.AppID != "cli_test" || result.AppSecret != "private-secret" {
		t.Fatal("credentials not returned")
	}
	if !reflect.DeepEqual(actions, []string{"init", "begin", "poll"}) {
		t.Fatalf("actions: %v", actions)
	}
	_, _ = file.Seek(0, 0)
	output, _ := io.ReadAll(file)
	if strings.Contains(string(output), "private-secret") || strings.Contains(string(output), "private-device") {
		t.Fatal("registration debug leaked secrets")
	}
	if !strings.Contains(string(output), "addons=") || !strings.Contains(string(output), "createOnly=true") {
		t.Fatal("registration omitted template URL")
	}
}

func TestPreflightNewSetup_PreventsReplacingExistingBot(t *testing.T) {
	old := config.ConfigPath
	t.Cleanup(func() { config.ConfigPath = old })
	config.ConfigPath = filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(config.ConfigPath, []byte("[[projects]]\nname='existing'\n[[projects.platforms]]\ntype='feishu'\n[projects.platforms.options]\napp_id='cli_old'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := preflightNewSetup("existing", 0); err == nil {
		t.Fatal("allowed replacement")
	}
	if err := preflightNewSetup("new-project", 0); err != nil {
		t.Fatal(err)
	}
	if err := preflightNewSetup("new-project", 2); err == nil {
		t.Fatal("accepted impossible index")
	}
}

func TestPreflightNewSetup_CreatesFreshConfig(t *testing.T) {
	old := config.ConfigPath
	t.Cleanup(func() { config.ConfigPath = old })
	config.ConfigPath = filepath.Join(t.TempDir(), "fresh.toml")
	if err := preflightNewSetup("new", 0); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(config.ConfigPath)
	if err != nil || len(data) != 0 {
		t.Fatalf("fresh config: %v", err)
	}
}
