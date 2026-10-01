package claudecode

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// apiRetryLine builds a stream-json api_retry message in the shape Claude
// Code 2.1.x writes to stdout.
func apiRetryLine(t *testing.T, line string) map[string]any {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		t.Fatalf("bad fixture: %v", err)
	}
	return raw
}

func TestHandleSystem_APIRetryEmitsRetryEvent(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
		want core.RetryInfo
	}{
		{
			name: "overloaded",
			line: `{"type":"system","subtype":"api_retry","attempt":3,"max_retries":10,"retry_delay_ms":2150,"error_status":529,"error":"overloaded","session_id":"sid","uuid":"u1"}`,
			want: core.RetryInfo{Attempt: 3, MaxAttempts: 10, Delay: 2150 * time.Millisecond, Status: 529, Reason: core.RetryReasonOverloaded},
		},
		{
			name: "rate limit wait",
			line: `{"type":"system","subtype":"api_retry","attempt":1,"max_retries":10,"retry_delay_ms":180000,"error_status":429,"error":"rate_limit","session_id":"sid"}`,
			want: core.RetryInfo{Attempt: 1, MaxAttempts: 10, Delay: 3 * time.Minute, Status: 429, Reason: core.RetryReasonRateLimit},
		},
		{
			// Captured from Claude Code 2.1.285 run with ANTHROPIC_BASE_URL
			// pointing at a closed port and CLAUDE_CODE_MAX_RETRIES=2.
			name: "network error has no status",
			line: `{"type":"system","subtype":"api_retry","attempt":1,"max_retries":2,"retry_delay_ms":566,"error_status":null,"error":"unknown","session_id":"998aecfb-dbad-4281-bce3-60f33d3108c9","uuid":"a0758df7-7c77-4885-a2ee-480ffa6fb160"}`,
			want: core.RetryInfo{Attempt: 1, MaxAttempts: 2, Delay: 566 * time.Millisecond},
		},
		{
			name: "no response",
			line: `{"type":"system","subtype":"api_retry","attempt":1,"max_retries":10,"retry_delay_ms":500,"error_status":null,"error":"unknown","no_response":{"waited_ms":300000,"retry_wait_ms":500},"session_id":"sid"}`,
			want: core.RetryInfo{Attempt: 1, MaxAttempts: 10, Delay: 500 * time.Millisecond, NoResponse: true},
		},
		{
			name: "cloud credentials count as auth",
			line: `{"type":"system","subtype":"api_retry","attempt":1,"max_retries":10,"retry_delay_ms":500,"error":"cloud_credential_error"}`,
			want: core.RetryInfo{Attempt: 1, MaxAttempts: 10, Delay: 500 * time.Millisecond, Reason: core.RetryReasonAuth},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cs := &claudeSession{events: make(chan core.Event, 8), ctx: ctx}

			cs.handleSystem(apiRetryLine(t, tc.line))

			if len(cs.events) != 1 {
				t.Fatalf("emitted %d events, want exactly the retry event", len(cs.events))
			}
			ev := <-cs.events
			if ev.Type != core.EventRetry || ev.Retry == nil {
				t.Fatalf("event = %+v, want EventRetry with info", ev)
			}
			if *ev.Retry != tc.want {
				t.Fatalf("retry = %+v, want %+v", *ev.Retry, tc.want)
			}
		})
	}
}

func TestHandleSystem_InitStillReportsSessionID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cs := &claudeSession{events: make(chan core.Event, 8), ctx: ctx}
	cs.recoverUsageOnce.Do(func() {}) // no transcript to recover in this test

	cs.handleSystem(map[string]any{"type": "system", "subtype": "init", "session_id": "sid-1"})

	ev := <-cs.events
	if ev.Type != core.EventText || ev.SessionID != "sid-1" {
		t.Fatalf("event = %+v, want the session ID text event", ev)
	}
}
