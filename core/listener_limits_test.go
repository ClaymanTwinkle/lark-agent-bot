package core

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func oversizedJSONBody() []byte {
	return []byte(`{"pad":"` + strings.Repeat("a", int(jsonBodyLimit)) + `"}`)
}

func TestMgmt_RequestBodyTooLarge(t *testing.T) {
	_, ts, _ := testManagementServer(t, "tok")

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/settings", bytes.NewReader(oversizedJSONBody()))
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", resp.StatusCode)
	}
}

// Without Content-Length the cap applies while decoding and still answers 413.
func TestMgmt_ChunkedBodyTooLarge(t *testing.T) {
	mgmt := NewManagementServer(0, "tok", nil)
	h := mgmt.wrap(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			mgmtDecodeError(w, err)
			return
		}
		mgmtOK(w, "ok")
	})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/x", bytes.NewReader(oversizedJSONBody()))
	r.ContentLength = -1
	r.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPIServer_SmallEndpointsCapBody(t *testing.T) {
	// Non-nil schedulers so each handler reaches its body decode; the decode
	// fails before any of them is used.
	s := &APIServer{
		mux:     http.NewServeMux(),
		engines: map[string]*Engine{},
		cron:    &CronScheduler{},
		timer:   &TimerScheduler{},
		relay:   &RelayManager{},
	}
	s.registerRoutes()

	for _, path := range []string{"/cron/add", "/timer/add", "/relay/send", "/relay/bind"} {
		r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(oversizedJSONBody()))
		w := httptest.NewRecorder()
		s.mux.ServeHTTP(w, r)
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("POST %s: expected 413, got %d", path, w.Code)
		}

		// Chunked: the decoder hits the cap.
		r = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(oversizedJSONBody()))
		r.ContentLength = -1
		w = httptest.NewRecorder()
		s.mux.ServeHTTP(w, r)
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("POST %s (chunked): expected 413, got %d", path, w.Code)
		}
	}
}
