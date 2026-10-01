package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const typingTestProject = "typing-project"

// typingTestServer fakes the reaction API. Creates return reaction "r-<message>";
// deletes answer with deleteBody and are recorded as "<message>/<reaction>".
type typingTestServer struct {
	mu         sync.Mutex
	deleteBody string
	deletes    []string
	deleted    chan string
}

func (s *typingTestServer) serve(t *testing.T) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		// /open-apis/im/v1/messages/<message>/reactions[/<reaction>]
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/open-apis/im/v1/messages/"), "/")
		var body string
		switch {
		case r.Method == http.MethodPost && len(parts) == 2:
			body = fmt.Sprintf(`{"code":0,"data":{"reaction_id":"r-%s"}}`, parts[0])
		case r.Method == http.MethodDelete && len(parts) == 3:
			s.mu.Lock()
			s.deletes = append(s.deletes, parts[0]+"/"+parts[2])
			body = s.deleteBody
			s.mu.Unlock()
			defer func() { s.deleted <- parts[0] + "/" + parts[2] }()
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			return
		}
		if _, err := fmt.Fprint(w, body); err != nil {
			t.Errorf("write fixture response: %v", err)
		}
	}
}

func (s *typingTestServer) deleteRequests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]string(nil), s.deletes...)
	sort.Strings(out)
	return out
}

func newTypingTestPlatform(t *testing.T, dataDir, deleteBody string) (*Platform, *typingTestServer) {
	t.Helper()
	srv := &typingTestServer{deleteBody: deleteBody, deleted: make(chan string, 20)}
	p := receiptTestPlatform(t, map[string]any{"cc_data_dir": dataDir, "cc_project": typingTestProject}, srv.serve(t))
	p.typingDeleteBackoff = []time.Duration{0, time.Millisecond, time.Millisecond}
	return p, srv
}

func typingTestLedgerPath(t *testing.T, dataDir string) string {
	return typingReactionLedgerPath(dataDir, "feishu", typingTestProject, "receipt-"+t.Name())
}

func readTypingLedger(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var entries []typingReaction
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(entries))
	for _, r := range entries {
		keys = append(keys, r.key())
	}
	sort.Strings(keys)
	return keys
}

func writeTypingLedger(t *testing.T, path string, entries ...typingReaction) {
	t.Helper()
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestTypingReaction_RecordedUntilRemoved(t *testing.T) {
	dir := t.TempDir()
	p, srv := newTypingTestPlatform(t, dir, `{"code":0}`)
	path := typingTestLedgerPath(t, dir)

	stop := p.StartTyping(context.Background(), replyContext{messageID: "om_1", chatID: "oc_main"})
	if got := readTypingLedger(t, path); !equalStrings(got, []string{"om_1/r-om_1"}) {
		t.Fatalf("ledger while processing = %v", got)
	}
	stop()
	select {
	case got := <-srv.deleted:
		if got != "om_1/r-om_1" {
			t.Fatalf("deleted %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("typing reaction not deleted")
	}
	deadline := time.Now().Add(2 * time.Second)
	for readTypingLedger(t, path) != nil {
		if time.Now().After(deadline) {
			t.Fatalf("ledger still holds removed reaction: %v", readTypingLedger(t, path))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTypingReaction_FailedDeleteKeptForNextStart(t *testing.T) {
	dir := t.TempDir()
	p, srv := newTypingTestPlatform(t, dir, `{"code":231015,"msg":"repeated request is processing"}`)
	path := typingTestLedgerPath(t, dir)

	p.StartTyping(context.Background(), replyContext{messageID: "om_1", chatID: "oc_main"})
	r := typingReaction{MessageID: "om_1", ReactionID: "r-om_1"}
	if err := p.removeTypingReaction(context.Background(), r); err == nil {
		t.Fatal("removeTypingReaction succeeded against a failing API")
	}
	if got := len(srv.deleteRequests()); got != len(p.typingDeleteBackoff) {
		t.Fatalf("delete attempts = %d, want %d", got, len(p.typingDeleteBackoff))
	}
	if got := readTypingLedger(t, path); !equalStrings(got, []string{"om_1/r-om_1"}) {
		t.Fatalf("ledger after failed delete = %v", got)
	}
	// The next process start picks the entry up.
	if leftover := openTypingReactionLedger(path).takeLeftover(); len(leftover) != 1 || leftover[0].key() != "om_1/r-om_1" {
		t.Fatalf("leftover for next start = %+v", leftover)
	}
}

func TestTypingReaction_GoneReactionDroppedWithoutRetry(t *testing.T) {
	dir := t.TempDir()
	p, srv := newTypingTestPlatform(t, dir, `{"code":231003,"msg":"The message is not found, maybe not exist or deleted."}`)
	path := typingTestLedgerPath(t, dir)

	p.StartTyping(context.Background(), replyContext{messageID: "om_1", chatID: "oc_main"})
	if err := p.removeTypingReaction(context.Background(), typingReaction{MessageID: "om_1", ReactionID: "r-om_1"}); err != nil {
		t.Fatalf("recalled message treated as failure: %v", err)
	}
	if got := len(srv.deleteRequests()); got != 1 {
		t.Fatalf("delete attempts = %d, want 1", got)
	}
	if got := readTypingLedger(t, path); got != nil {
		t.Fatalf("ledger kept unreachable reaction: %v", got)
	}
}

func TestTypingReaction_StartupSweepRemovesLeftoversOnly(t *testing.T) {
	dir := t.TempDir()
	path := typingTestLedgerPath(t, dir)
	hourAgo := time.Now().Add(-time.Hour)
	writeTypingLedger(t, path,
		typingReaction{MessageID: "om_a", ReactionID: "ra", AddedAt: hourAgo},
		typingReaction{MessageID: "om_b", ReactionID: "rb", AddedAt: hourAgo},
	)
	p, srv := newTypingTestPlatform(t, dir, `{"code":0}`)

	// A turn of this process that is still running must keep its reaction.
	p.StartTyping(context.Background(), replyContext{messageID: "om_live", chatID: "oc_main"})
	p.sweepTypingReactions(context.Background())

	if got := srv.deleteRequests(); !equalStrings(got, []string{"om_a/ra", "om_b/rb"}) {
		t.Fatalf("sweep deleted %v", got)
	}
	if got := readTypingLedger(t, path); !equalStrings(got, []string{"om_live/r-om_live"}) {
		t.Fatalf("ledger after sweep = %v", got)
	}
	p.sweepTypingReactions(context.Background())
	if got := len(srv.deleteRequests()); got != 2 {
		t.Fatalf("second sweep sent requests: %d deletes total", got)
	}
}

func TestTypingReaction_SweepGivesUpOnOldEntries(t *testing.T) {
	dir := t.TempDir()
	path := typingTestLedgerPath(t, dir)
	writeTypingLedger(t, path,
		typingReaction{MessageID: "om_old", ReactionID: "r_old", AddedAt: time.Now().Add(-typingReactionMaxAge - time.Hour)},
		typingReaction{MessageID: "om_new", ReactionID: "r_new", AddedAt: time.Now().Add(-time.Hour)},
	)
	p, _ := newTypingTestPlatform(t, dir, `{"code":231015,"msg":"repeated request is processing"}`)

	p.sweepTypingReactions(context.Background())

	if got := readTypingLedger(t, path); !equalStrings(got, []string{"om_new/r_new"}) {
		t.Fatalf("ledger after failing sweep = %v", got)
	}
}

func TestTypingReactionLedger_DisabledWithoutDataDir(t *testing.T) {
	if path := typingReactionLedgerPath("", "feishu", typingTestProject, "cli_x"); path != "" {
		t.Fatalf("path without data dir = %q", path)
	}
	l := openTypingReactionLedger("")
	if l != nil {
		t.Fatal("ledger opened without a path")
	}
	// A nil ledger must accept every call.
	l.add(typingReaction{MessageID: "om", ReactionID: "r"})
	l.remove(typingReaction{MessageID: "om", ReactionID: "r"})
	if l.takeLeftover() != nil {
		t.Fatal("nil ledger returned leftovers")
	}
}

func TestTypingReactionLedgerPath_SanitizesNames(t *testing.T) {
	path := typingReactionLedgerPath("data", "feishu", `a/b:c`, "receipt-Test/sub")
	name := filepath.Base(path)
	if strings.ContainsAny(name, `/\:`) || name != "feishu_typing_reactions_a_b_c_receipt-Test_sub.json" {
		t.Fatalf("ledger file name = %q", name)
	}
}
