package core

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsLoopbackHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"localhost:9111", true},
		{"LOCALHOST:9111", true},
		{"localhost.:9111", true},
		{"app.localhost:9111", true},
		{"127.0.0.1", true},
		{"127.0.0.1:9111", true},
		{"127.1.2.3:9111", true},
		{"[::1]:9111", true},
		{"::1", true},
		{"", false},
		{"example.com:9111", false},
		{"localhost.example.com:9111", false},
		{"192.168.1.5:9111", false},
		{"0.0.0.0:9111", false},
		{"[::ffff:127.0.0.1]:9111", true},
	}
	for _, tt := range tests {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Host = tt.host
		if got := isLoopbackHost(r); got != tt.want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestIsLoopbackRemote(t *testing.T) {
	tests := []struct {
		remote string
		want   bool
	}{
		{"127.0.0.1:5000", true},
		{"[::1]:5000", true},
		{"[::ffff:127.0.0.1]:5000", true},
		{"192.0.2.1:5000", false},
		{"[2001:db8::1]:5000", false},
		{"", false},
		{"garbage", false},
	}
	for _, tt := range tests {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = tt.remote
		if got := isLoopbackRemote(r); got != tt.want {
			t.Errorf("isLoopbackRemote(%q) = %v, want %v", tt.remote, got, tt.want)
		}
	}
}

func TestIsJSONContentType(t *testing.T) {
	tests := []struct {
		ct   string
		want bool
	}{
		{"application/json", true},
		{"application/json; charset=utf-8", true},
		{"APPLICATION/JSON", true},
		{"", false},
		{"text/plain", false},
		{"text/plain; application/json", false},
		{"application/x-www-form-urlencoded", false},
		{"application/jsonx", false},
		{"application/json; charset", true}, // malformed parameter, JSON media type
		{"text/plain; charset", false},
	}
	for _, tt := range tests {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		if tt.ct != "" {
			r.Header.Set("Content-Type", tt.ct)
		}
		if got := isJSONContentType(r); got != tt.want {
			t.Errorf("isJSONContentType(%q) = %v, want %v", tt.ct, got, tt.want)
		}
	}
}

func TestCapRequestBody(t *testing.T) {
	// Declared length over the limit is refused up front.
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(make([]byte, 11)))
	if !capRequestBody(httptest.NewRecorder(), r, 10) {
		t.Fatal("expected Content-Length over limit to be reported")
	}

	// Unknown length: reading past the limit fails with a MaxBytesError.
	r = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", 11)))
	r.ContentLength = -1
	if capRequestBody(httptest.NewRecorder(), r, 10) {
		t.Fatal("unknown length should not be refused up front")
	}
	_, err := io.ReadAll(r.Body)
	if !isBodyTooLarge(err) {
		t.Fatalf("expected MaxBytesError, got %v", err)
	}

	// Within the limit reads through.
	r = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("ok"))
	if capRequestBody(httptest.NewRecorder(), r, 10) {
		t.Fatal("small body refused")
	}
	if data, err := io.ReadAll(r.Body); err != nil || string(data) != "ok" {
		t.Fatalf("read = %q, %v", data, err)
	}
}
