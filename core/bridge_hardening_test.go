package core

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestBridge_ListenAddr(t *testing.T) {
	if got := NewBridgeServerInsecure(9810, "", "/bridge/ws", nil).listenAddr(); got != "127.0.0.1:9810" {
		t.Errorf("tokenless bridge listenAddr = %q, want 127.0.0.1:9810", got)
	}
	// insecure=true with a token keeps the token and the normal binding.
	if got := NewBridgeServerInsecure(9810, "tok", "/bridge/ws", nil).listenAddr(); got != ":9810" {
		t.Errorf("insecure bridge with token listenAddr = %q, want :9810", got)
	}
	if got := NewBridgeServer(9810, "tok", "/bridge/ws", nil).listenAddr(); got != ":9810" {
		t.Errorf("token bridge listenAddr = %q, want :9810", got)
	}
}

// A tokenless bridge refuses WebSocket requests from other machines, and
// requests addressed to a non-local host name.
func TestBridge_TokenlessWSRejectsNonLocal(t *testing.T) {
	bs := NewBridgeServerInsecure(0, "", "/bridge/ws", nil)
	tests := []struct {
		name   string
		remote string
		host   string
	}{
		{"remote client", "192.0.2.10:40000", "127.0.0.1:9810"},
		{"rebound host name", "127.0.0.1:40000", "attacker.example:9810"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/bridge/ws", nil)
			r.RemoteAddr = tt.remote
			r.Host = tt.host
			w := httptest.NewRecorder()
			bs.handleWS(w, r)
			if w.Code != http.StatusForbidden {
				t.Errorf("expected 403, got %d", w.Code)
			}
		})
	}

	// A bridge with a token keeps accepting remote clients (the token guards it).
	withToken := NewBridgeServer(0, "tok", "/bridge/ws", nil)
	r := httptest.NewRequest(http.MethodGet, "/bridge/ws", nil)
	r.RemoteAddr = "192.0.2.10:40000"
	r.Host = "bot.example:9810"
	w := httptest.NewRecorder()
	withToken.handleWS(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("token bridge without credentials: expected 401, got %d", w.Code)
	}
}

// The WebSocket is also mounted on the management port, which listens on
// every interface; a tokenless bridge must stay local-only there too.
func TestBridge_TokenlessOnManagementPortIsLocalOnly(t *testing.T) {
	mgmt := NewManagementServer(0, "mgmt-tok", nil)
	mgmt.SetBridgeServer(NewBridgeServerInsecure(0, "", "/bridge/ws", nil))
	handler := mgmt.buildHandler(http.NewServeMux())

	r := httptest.NewRequest(http.MethodGet, "/bridge/ws", nil)
	r.RemoteAddr = "192.0.2.10:40000"
	r.Host = "bot.example:9820"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("remote request to tokenless bridge on management port: expected 403, got %d", w.Code)
	}
}

func TestBridge_CheckOriginEnforcedWithoutToken(t *testing.T) {
	bs := NewBridgeServerInsecure(0, "", "/bridge/ws", nil)
	check := func(origin string) bool {
		r := httptest.NewRequest(http.MethodGet, "/bridge/ws", nil)
		r.Host = "127.0.0.1:9810"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return bs.checkOrigin(r)
	}
	if !check("") {
		t.Error("non-browser client (no Origin) should pass")
	}
	if !check("http://127.0.0.1:9810") {
		t.Error("same-host origin should pass")
	}
	if check("http://evil.example") {
		t.Error("cross-site origin must be rejected in insecure mode")
	}
}

func TestBridge_WildcardOriginIgnoredWithoutToken(t *testing.T) {
	tokenless := NewBridgeServerInsecure(0, "", "/bridge/ws", []string{"*", "http://localhost:9821"})
	withToken := NewBridgeServer(0, "tok", "/bridge/ws", []string{"*"})

	req := func(origin string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/bridge/ws", nil)
		r.Host = "127.0.0.1:9810"
		r.Header.Set("Origin", origin)
		return r
	}
	if tokenless.checkOrigin(req("http://evil.example")) {
		t.Error("tokenless bridge must not honor cors_origins \"*\"")
	}
	if !tokenless.checkOrigin(req("http://localhost:9821")) {
		t.Error("explicitly listed origin should pass")
	}
	if !withToken.checkOrigin(req("http://evil.example")) {
		t.Error("bridge with token keeps honoring \"*\"")
	}

	// CORS response headers follow the same list.
	w := httptest.NewRecorder()
	tokenless.setCORS(w, req("http://evil.example"))
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("tokenless bridge sent Access-Control-Allow-Origin %q for an unlisted origin", got)
	}
}

func TestBridge_WSRejectsCrossSiteOriginWithoutToken(t *testing.T) {
	_, wsURL := startTestBridge(t, "")
	headers := http.Header{}
	headers.Set("Origin", "http://evil.example")
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err == nil {
		conn.Close()
		t.Fatal("expected handshake to fail for a cross-site origin")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		t.Fatalf("expected 403, got %d (%v)", code, err)
	}
}

func TestBridge_TokenlessRESTRejectsCrossSiteOrigin(t *testing.T) {
	_, baseURL := startTestBridgeWithREST(t, "")
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/bridge/sessions",
		strings.NewReader(`{"session_key":"test:u1:u1","name":"x"}`))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Origin", "http://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestBridge_RESTBodyTooLarge(t *testing.T) {
	_, baseURL := startTestBridgeWithREST(t, "tok")
	body := `{"session_key":"test:u1:u1","name":"` + strings.Repeat("a", int(jsonBodyLimit)) + `"}`
	for _, path := range []string{"/bridge/sessions", "/bridge/sessions/switch"} {
		req, _ := http.NewRequest(http.MethodPost, baseURL+path, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer tok")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			t.Errorf("POST %s: expected 413, got %d", path, resp.StatusCode)
		}
	}
}

// An oversized register message closes the connection instead of being
// buffered whole.
func TestBridge_RegisterReadLimit(t *testing.T) {
	_, wsURL := startTestBridge(t, "")
	conn := dialWS(t, wsURL, nil)
	big := map[string]any{
		"type":     "register",
		"platform": "test",
		"metadata": map[string]any{"pad": strings.Repeat("a", int(bridgeRegisterReadLimit))},
	}
	if err := conn.WriteJSON(big); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Fatal("expected the server to close the connection")
	}
	if !websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
		t.Logf("connection closed with: %v", err)
	}
}

// The per-message cap must still fit one maximum-size attachment after base64
// expansion.
func TestBridge_MessageReadLimitFitsMaxAttachment(t *testing.T) {
	if min := DefaultMaxAttachmentSize * 4 / 3; bridgeMessageReadLimit <= min {
		t.Fatalf("bridgeMessageReadLimit = %d, must exceed base64 size of a max attachment (%d)", bridgeMessageReadLimit, min)
	}
}
