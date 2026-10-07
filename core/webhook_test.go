package core

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebhookServer_AuthBearer(t *testing.T) {
	ws := NewWebhookServer(0, "my-secret", "/hook")
	r := httptest.NewRequest(http.MethodPost, "/hook", nil)
	r.Header.Set("Authorization", "Bearer my-secret")
	if !ws.authenticate(r) {
		t.Error("expected auth to succeed with correct Bearer token")
	}
	r.Header.Set("Authorization", "Bearer wrong")
	if ws.authenticate(r) {
		t.Error("expected auth to fail with wrong Bearer token")
	}
}

func TestWebhookServer_AuthHeader(t *testing.T) {
	ws := NewWebhookServer(0, "tok123", "/hook")
	r := httptest.NewRequest(http.MethodPost, "/hook", nil)
	r.Header.Set("X-Webhook-Token", "tok123")
	if !ws.authenticate(r) {
		t.Error("expected auth to succeed with X-Webhook-Token")
	}
}

func TestWebhookServer_AuthQuery(t *testing.T) {
	ws := NewWebhookServer(0, "qsecret", "/hook")
	r := httptest.NewRequest(http.MethodPost, "/hook?token=qsecret", nil)
	if !ws.authenticate(r) {
		t.Error("expected auth to succeed with query token")
	}
}

func TestWebhookServer_NoTokenRequired(t *testing.T) {
	ws := NewWebhookServer(0, "", "/hook")
	r := httptest.NewRequest(http.MethodPost, "/hook", nil)
	if !ws.authenticate(r) {
		t.Error("expected auth to pass when no token configured")
	}
}

func TestWebhookServer_HandleHook_MethodNotAllowed(t *testing.T) {
	ws := NewWebhookServer(0, "", "/hook")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/hook", nil)
	ws.handleHook(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// localHookRequest builds a POST /hook request that comes from and is
// addressed to this machine, with a JSON body.
func localHookRequest(t *testing.T, body any) *http.Request {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://localhost:9111/hook", bytes.NewReader(data))
	r.RemoteAddr = "127.0.0.1:50000"
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestWebhookServer_HandleHook_Unauthorized(t *testing.T) {
	ws := NewWebhookServer(0, "secret", "/hook")
	w := httptest.NewRecorder()
	r := localHookRequest(t, WebhookRequest{SessionKey: "tg:1:1", Prompt: "hi"})
	ws.handleHook(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestWebhookServer_HandleHook_Validation(t *testing.T) {
	ws := NewWebhookServer(0, "", "/hook")

	tests := []struct {
		name string
		body WebhookRequest
		code int
	}{
		{"missing session_key", WebhookRequest{Prompt: "hi"}, http.StatusBadRequest},
		{"missing prompt and exec", WebhookRequest{SessionKey: "tg:1:1"}, http.StatusBadRequest},
		{"both prompt and exec", WebhookRequest{SessionKey: "tg:1:1", Prompt: "hi", Exec: "ls"}, http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := localHookRequest(t, tt.body)
			ws.handleHook(w, r)
			if w.Code != tt.code {
				t.Errorf("expected %d, got %d: %s", tt.code, w.Code, w.Body.String())
			}
		})
	}
}

// Without a token the webhook must not be reachable from other machines.
func TestWebhookServer_ListenAddr(t *testing.T) {
	if got := NewWebhookServer(9111, "", "/hook").listenAddr(); got != "127.0.0.1:9111" {
		t.Errorf("tokenless webhook listenAddr = %q, want 127.0.0.1:9111", got)
	}
	if got := NewWebhookServer(9111, "secret", "/hook").listenAddr(); got != ":9111" {
		t.Errorf("token webhook listenAddr = %q, want :9111", got)
	}
}

// A tokenless webhook refuses requests from another machine, and requests
// addressed to a non-local host name (DNS rebinding from a browser page).
func TestWebhookServer_HandleHook_TokenlessRejectsNonLocal(t *testing.T) {
	ws := NewWebhookServer(0, "", "/hook")
	body := WebhookRequest{Prompt: "hi"} // would be 400 if it got past the check

	tests := []struct {
		name   string
		remote string
		host   string
		code   int
	}{
		{"remote client", "192.0.2.10:40000", "localhost:9111", http.StatusForbidden},
		{"rebound host name", "127.0.0.1:40000", "attacker.example:9111", http.StatusForbidden},
		{"localhost", "127.0.0.1:40000", "localhost:9111", http.StatusBadRequest},
		{"ipv4 loopback", "127.0.0.1:40000", "127.0.0.1:9111", http.StatusBadRequest},
		{"ipv6 loopback", "[::1]:40000", "[::1]:9111", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := localHookRequest(t, body)
			r.RemoteAddr = tt.remote
			r.Host = tt.host
			w := httptest.NewRecorder()
			ws.handleHook(w, r)
			if w.Code != tt.code {
				t.Errorf("expected %d, got %d: %s", tt.code, w.Code, w.Body.String())
			}
		})
	}

	// With a token the host check does not apply; the token is the guard.
	withToken := NewWebhookServer(0, "secret", "/hook")
	r := localHookRequest(t, body)
	r.RemoteAddr = "192.0.2.10:40000"
	r.Host = "bot.example:9111"
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	withToken.handleHook(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("token webhook from remote host: expected 400 (validation), got %d: %s", w.Code, w.Body.String())
	}
}

// POST bodies must be declared as JSON so a cross-site form or text/plain
// request cannot reach the handler.
func TestWebhookServer_HandleHook_RequiresJSONContentType(t *testing.T) {
	ws := NewWebhookServer(0, "", "/hook")
	tests := []struct {
		contentType string
		code        int
	}{
		{"", http.StatusUnsupportedMediaType},
		{"text/plain", http.StatusUnsupportedMediaType},
		{"application/x-www-form-urlencoded", http.StatusUnsupportedMediaType},
		{"multipart/form-data; boundary=x", http.StatusUnsupportedMediaType},
		{"application/json", http.StatusBadRequest},
		{"application/json; charset=utf-8", http.StatusBadRequest},
		{"Application/JSON", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			r := localHookRequest(t, WebhookRequest{Prompt: "hi"})
			if tt.contentType == "" {
				r.Header.Del("Content-Type")
			} else {
				r.Header.Set("Content-Type", tt.contentType)
			}
			w := httptest.NewRecorder()
			ws.handleHook(w, r)
			if w.Code != tt.code {
				t.Errorf("expected %d, got %d: %s", tt.code, w.Code, w.Body.String())
			}
		})
	}
}

func TestWebhookServer_HandleHook_BodyTooLarge(t *testing.T) {
	ws := NewWebhookServer(0, "", "/hook")
	big := WebhookRequest{SessionKey: "tg:1:1", Prompt: strings.Repeat("a", int(jsonBodyLimit))}

	w := httptest.NewRecorder()
	ws.handleHook(w, localHookRequest(t, big))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("with Content-Length: expected 413, got %d", w.Code)
	}

	// Without Content-Length the limit applies while decoding.
	r := localHookRequest(t, big)
	r.ContentLength = -1
	w = httptest.NewRecorder()
	ws.handleHook(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("chunked body: expected 413, got %d", w.Code)
	}
}

func TestWebhookServer_DefaultValues(t *testing.T) {
	ws := NewWebhookServer(0, "", "")
	if ws.port != 9111 {
		t.Errorf("expected default port 9111, got %d", ws.port)
	}
	if ws.path != "/hook" {
		t.Errorf("expected default path /hook, got %s", ws.path)
	}
}
