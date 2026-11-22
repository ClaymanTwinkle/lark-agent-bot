package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
	"github.com/gorilla/websocket"
)

func TestModelsMetadataProcess(t *testing.T) {
	if os.Getenv("CC_MODELS_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	page := 0
	for scanner.Scan() {
		var req struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			os.Exit(2)
		}
		switch req.Method {
		case "initialize":
			fmt.Printf("{\"id\":%d,\"result\":{}}\n", req.ID)
		case "initialized":
		case "model/list":
			if os.Getenv("CC_MODELS_FAIL") == "1" {
				fmt.Printf("{\"id\":%d,\"error\":{\"code\":-32601,\"message\":\"unsupported\"}}\n", req.ID)
				continue
			}
			if os.Getenv("CC_MODELS_HANG") == "1" {
				time.Sleep(time.Minute)
			}
			if page == 0 {
				if req.Params["cursor"] != nil || req.Params["includeHidden"] != false {
					os.Exit(3)
				}
				fmt.Printf("{\"id\":%d,\"result\":{\"data\":[{\"id\":\"picker-id\",\"model\":\"gpt-6.2-sol\",\"description\":\"Fresh model\"},{\"model\":\"secret\",\"hidden\":true}],\"nextCursor\":\"page2\"}}\n", req.ID)
			} else {
				if req.Params["cursor"] != "page2" {
					os.Exit(4)
				}
				fmt.Printf("{\"id\":%d,\"result\":{\"data\":[{\"model\":\"gpt-6.2-sol\"},{\"model\":\"gpt-6.2-astra\"}],\"nextCursor\":null}}\n", req.ID)
			}
			page++
		default:
			os.Exit(5) // Discovery must never start a thread or turn.
		}
	}
	os.Exit(0)
}

func modelTestAgent(t *testing.T) *Agent {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return &Agent{cmd: bin, cliExtraArgs: []string{"-test.run=^TestModelsMetadataProcess$", "--"}, activeIdx: -1,
		workDir: t.TempDir(), codexHome: t.TempDir(), configEnv: []string{"CC_MODELS_HELPER=1"}}
}

func TestAvailableModels_AppServerOverridesStaleCatalog(t *testing.T) {
	a := modelTestAgent(t)
	t.Setenv("CODEX_HOME", a.codexHome)
	if err := os.WriteFile(filepath.Join(a.codexHome, "config.toml"), []byte("model_catalog_json = \"catalog.json\""), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.codexHome, "catalog.json"), []byte(`{"models":[{"slug":"gpt-5.4","supported_in_api":true}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got := a.AvailableModels(ctx)
	if len(got) != 2 || got[0].Name != "gpt-6.2-sol" || got[1].Name != "gpt-6.2-astra" || got[0].Desc != "Fresh model" {
		t.Fatalf("stale catalog masked live paginated models: %+v", got)
	}
}

func TestAvailableModels_AppServerFailureFallsBackToProvider(t *testing.T) {
	for _, hang := range []bool{false, true} {
		t.Run(fmt.Sprint("hang=", hang), func(t *testing.T) {
			a := modelTestAgent(t)
			a.providers = []core.ProviderConfig{{Name: "custom", Models: []core.ModelOption{{Name: "provider-model"}}}}
			a.activeIdx = 0
			flag := "CC_MODELS_FAIL=1"
			if hang {
				flag = "CC_MODELS_HANG=1"
			}
			a.configEnv = append(a.configEnv, flag)
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			start := time.Now()
			got := a.AvailableModels(ctx)
			if len(got) != 1 || got[0].Name != "provider-model" {
				t.Fatalf("fallback = %+v", got)
			}
			if time.Since(start) > 2*time.Second {
				t.Fatal("metadata process did not stop on cancellation")
			}
		})
	}
}

func TestAvailableModels_UsesConfiguredHomeAfterDiscoveryFailure(t *testing.T) {
	a := modelTestAgent(t)
	a.configEnv = append(a.configEnv, "CC_MODELS_FAIL=1")
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "")
	if err := os.WriteFile(filepath.Join(a.codexHome, "models_cache.json"), []byte(`{"models":[{"slug":"cached-model","supported_in_api":true}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	got := a.AvailableModels(context.Background())
	if len(got) != 1 || got[0].Name != "cached-model" {
		t.Fatalf("wrong home fallback: %+v", got)
	}
}

func TestAvailableModels_SharedAppServerMetadataOnly(t *testing.T) {
	done := make(chan []string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var methods []string
		defer func() { done <- methods }()
		for {
			var req sharedRPCRequest
			if conn.ReadJSON(&req) != nil {
				return
			}
			methods = append(methods, req.Method)
			if req.ID == nil {
				continue
			}
			var result any = map[string]any{}
			if req.Method == "model/list" {
				result = map[string]any{"data": []any{map[string]any{"model": "gpt-6.2-astra"}}}
			}
			if err := conn.WriteJSON(map[string]any{"id": req.ID, "result": result}); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	a := &Agent{appServerURL: "ws" + strings.TrimPrefix(srv.URL, "http"), activeIdx: -1, cmd: "/nonexistent/codex"}
	got := a.AvailableModels(context.Background())
	if len(got) != 1 || got[0].Name != "gpt-6.2-astra" {
		t.Fatalf("shared models: %+v", got)
	}
	select {
	case methods := <-done:
		if !reflect.DeepEqual(methods, []string{"initialize", "initialized", "model/list"}) {
			t.Fatalf("unexpected RPC: %v", methods)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("metadata connection leaked")
	}
}

func TestAvailableModels_APIOverridesStaleCatalog(t *testing.T) {
	a := modelTestAgent(t)
	a.configEnv = append(a.configEnv, "CC_MODELS_FAIL=1")
	if err := os.WriteFile(filepath.Join(a.codexHome, "config.toml"), []byte("model_catalog_json = \"catalog.json\""), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.codexHome, "catalog.json"), []byte(`{"models":[{"slug":"gpt-5.4","supported_in_api":true}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("wrong API request")
		}
		fmt.Fprint(w, `{"data":[{"id":"gpt-6.2-sol"}]}`)
	}))
	defer srv.Close()
	a.providers = []core.ProviderConfig{{Name: "custom", APIKey: "test-key", BaseURL: srv.URL}}
	a.activeIdx = 0
	got := a.AvailableModels(context.Background())
	if len(got) != 1 || got[0].Name != "gpt-6.2-sol" {
		t.Fatalf("stale local catalog masked API models: %+v", got)
	}
}
