package feishu

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
)

func TestCheckWebhookTimestamp(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	sec := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).Unix(), 10) }
	ms := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).UnixMilli(), 10) }

	tests := []struct {
		name    string
		signed  bool
		ts      string
		wantErr bool
	}{
		{"unsigned request is not checked", false, sec(-time.Hour), false},
		{"signed without timestamp header", true, "", false},
		{"current", true, sec(0), false},
		{"within window (past)", true, sec(-4 * time.Minute), false},
		{"within window (future)", true, sec(4 * time.Minute), false},
		{"stale", true, sec(-6 * time.Minute), true},
		{"too far in the future", true, sec(6 * time.Minute), true},
		{"milliseconds current", true, ms(0), false},
		{"milliseconds stale", true, ms(-time.Hour), true},
		{"not a number", true, "yesterday", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			if tt.signed {
				h.Set(larkevent.EventSignature, "sig")
			}
			if tt.ts != "" {
				h.Set(larkevent.EventRequestTimestamp, tt.ts)
			}
			err := checkWebhookTimestamp(h, now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("checkWebhookTimestamp() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// newWebhookTestPlatform returns a Platform whose SDK dispatcher has no
// encrypt key or verification token, so a url_verification body is answered
// with its challenge once it gets past webhookHandler's own checks.
func newWebhookTestPlatform() *Platform {
	return &Platform{
		platformName: "feishu",
		eventHandler: dispatcher.NewEventDispatcher("", ""),
	}
}

const challengeBody = `{"type":"url_verification","challenge":"c-123","token":""}`

func postWebhook(p *Platform, body []byte, header http.Header) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/feishu/webhook", bytes.NewReader(body))
	for k, v := range header {
		r.Header[k] = v
	}
	w := httptest.NewRecorder()
	p.webhookHandler(w, r)
	return w
}

func TestWebhookHandler_ChallengeStillAnswered(t *testing.T) {
	p := newWebhookTestPlatform()

	// Unsigned challenge.
	w := postWebhook(p, []byte(challengeBody), nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "c-123") {
		t.Fatalf("unsigned challenge: status %d body %q", w.Code, w.Body.String())
	}

	// Signed request with a current timestamp passes the replay check.
	h := http.Header{}
	h.Set(larkevent.EventSignature, "sig")
	h.Set(larkevent.EventRequestTimestamp, strconv.FormatInt(time.Now().Unix(), 10))
	h.Set(larkevent.EventRequestNonce, "n")
	w = postWebhook(p, []byte(challengeBody), h)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "c-123") {
		t.Fatalf("signed fresh request: status %d body %q", w.Code, w.Body.String())
	}
}

func TestWebhookHandler_RejectsStaleSignedRequest(t *testing.T) {
	p := newWebhookTestPlatform()
	h := http.Header{}
	h.Set(larkevent.EventSignature, "sig")
	h.Set(larkevent.EventRequestNonce, "n")
	h.Set(larkevent.EventRequestTimestamp, strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10))
	w := postWebhook(p, []byte(challengeBody), h)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("stale signed request: expected 401, got %d (%q)", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "c-123") {
		t.Fatal("stale request reached the SDK dispatcher")
	}
}

func TestWebhookHandler_BodyTooLarge(t *testing.T) {
	p := newWebhookTestPlatform()
	big := []byte(`{"type":"url_verification","challenge":"` + strings.Repeat("a", int(webhookBodyLimit)) + `"}`)

	w := postWebhook(p, big, nil)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("with Content-Length: expected 413, got %d", w.Code)
	}

	r := httptest.NewRequest(http.MethodPost, "/feishu/webhook", bytes.NewReader(big))
	r.ContentLength = -1
	rec := httptest.NewRecorder()
	p.webhookHandler(rec, r)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("chunked: expected 413, got %d", rec.Code)
	}
}
