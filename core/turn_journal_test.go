package core

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// exitingAgentSession is an agent session whose events channel the test
// closes directly to simulate the agent process dying. Close is a no-op so
// the engine's cleanup does not close the channel a second time.
type exitingAgentSession struct {
	stubAgentSession
	events chan Event
}

func newExitingAgentSession() *exitingAgentSession {
	return &exitingAgentSession{events: make(chan Event, 8)}
}

func (s *exitingAgentSession) Events() <-chan Event { return s.events }
func (s *exitingAgentSession) Alive() bool          { return false }

// keyRecordingStub records the session keys passed to ReconstructReplyCtx.
type keyRecordingStub struct {
	restartNotifyStub
	keyMu sync.Mutex
	keys  []string
}

func (p *keyRecordingStub) ReconstructReplyCtx(sessionKey string) (any, error) {
	p.keyMu.Lock()
	p.keys = append(p.keys, sessionKey)
	p.keyMu.Unlock()
	return "rctx-" + sessionKey, nil
}

func TestTurnJournal_LeftoverReportedOnceThenCleared(t *testing.T) {
	path := turnJournalPath(t.TempDir(), "claude-bot")
	j, leftover := openTurnJournal(path)
	if len(leftover) != 0 {
		t.Fatalf("fresh journal leftover = %v, want none", leftover)
	}
	started := time.Date(2026, 10, 1, 9, 35, 0, 0, time.Local)
	j.begin(`D:\work:feishu:oc_1:ou_1`, inflightTurn{
		Platform: "feishu", SessionKey: "feishu:oc_1:ou_1", MessageID: "om_1",
		Preview: "fix the bug", StartedAt: started,
	})

	_, leftover = openTurnJournal(path)
	if len(leftover) != 1 {
		t.Fatalf("leftover = %v, want 1 turn", leftover)
	}
	got := leftover[0]
	if got.SessionKey != "feishu:oc_1:ou_1" || got.MessageID != "om_1" || got.Preview != "fix the bug" || !got.StartedAt.Equal(started) {
		t.Fatalf("leftover turn = %+v", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("journal file still on disk after reading leftovers: %v", err)
	}
	if _, again := openTurnJournal(path); len(again) != 0 {
		t.Fatalf("leftover reported twice: %v", again)
	}
}

func TestTurnJournal_EndRemovesTurn(t *testing.T) {
	path := turnJournalPath(t.TempDir(), "p")
	j, _ := openTurnJournal(path)
	j.begin("k1", inflightTurn{Platform: "feishu", SessionKey: "feishu:a:b", MessageID: "om_1"})
	j.begin("k2", inflightTurn{Platform: "feishu", SessionKey: "feishu:c:d", MessageID: "om_2"})
	j.end("k1")

	_, leftover := openTurnJournal(path)
	if len(leftover) != 1 || leftover[0].MessageID != "om_2" {
		t.Fatalf("leftover = %+v, want only om_2", leftover)
	}

	j2, _ := openTurnJournal(path)
	j2.begin("k", inflightTurn{MessageID: "om_3"})
	j2.end("k")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("journal file kept after last turn ended: %v", err)
	}
}

func TestTurnJournalPath_SanitizesProjectName(t *testing.T) {
	got := turnJournalPath("data", `a/b:c`)
	if !strings.HasSuffix(got, "inflight_turns_a_b_c.json") {
		t.Fatalf("path = %q", got)
	}
}

func TestBeginTurnJournal_SyntheticTurnClearsUserTurn(t *testing.T) {
	e := newTestEngine()
	dir := t.TempDir()
	e.SetDataDir(dir)
	e.beginTurnJournal("k", "test", "test:u1", "om_1", "hello")
	e.beginTurnJournal("k", "test", "test:u1", "", "cron prompt")

	if _, leftover := openTurnJournal(turnJournalPath(dir, "test")); len(leftover) != 0 {
		t.Fatalf("synthetic turn left a journal entry: %+v", leftover)
	}
}

func TestTurnPreview_FlattensAndTruncates(t *testing.T) {
	if got := turnPreview("  line one\n\n line\ttwo "); got != "line one line two" {
		t.Fatalf("preview = %q", got)
	}
	long := strings.Repeat("长", inflightPreviewMaxRunes+5)
	if got := turnPreview(long); got != strings.Repeat("长", inflightPreviewMaxRunes)+"..." {
		t.Fatalf("preview = %q", got)
	}
}

func TestNotifyInterruptedTurns_SendsNoticeAfterPlatformReady(t *testing.T) {
	dir := t.TempDir()
	prev, _ := openTurnJournal(turnJournalPath(dir, "claude-bot"))
	prev.begin(`D:\work:feishu:oc_1:ou_1`, inflightTurn{
		Platform: "feishu", SessionKey: "feishu:oc_1:ou_1", MessageID: "om_1",
		Preview: "deploy the bot", StartedAt: time.Date(2026, 10, 1, 9, 35, 0, 0, time.Local),
	})

	plat := &keyRecordingStub{restartNotifyStub: restartNotifyStub{name: "feishu"}}
	e := NewEngine("claude-bot", &stubAgent{}, []Platform{plat}, "", LangEnglish)
	e.SetDataDir(dir)
	e.NotifyInterruptedTurns()

	if got := plat.sentTexts(); len(got) != 0 {
		t.Fatalf("notice sent before platform ready: %v", got)
	}
	// Not plat.markReady: that promoted method would register the embedded
	// stub and bypass the key recording.
	e.onPlatformReady(plat)

	got := plat.waitForSent(t, 1, 3*time.Second)
	if len(got) != 1 {
		t.Fatalf("sent = %v, want 1 notice", got)
	}
	if !strings.Contains(got[0], "10-01 09:35") || !strings.Contains(got[0], "\n> deploy the bot") {
		t.Fatalf("notice = %q, want start time and quoted preview", got[0])
	}
	plat.keyMu.Lock()
	keys := append([]string(nil), plat.keys...)
	plat.keyMu.Unlock()
	if len(keys) != 1 || keys[0] != "feishu:oc_1:ou_1" {
		t.Fatalf("reconstructed keys = %v, want the platform session key", keys)
	}

	// A second call must not resend.
	e.NotifyInterruptedTurns()
	time.Sleep(100 * time.Millisecond)
	if got := plat.sentTexts(); len(got) != 1 {
		t.Fatalf("notice resent: %v", got)
	}
}

func newExitTestEngine(t *testing.T) (*Engine, *stubPlatformEngine, *exitingAgentSession, *interactiveState, string) {
	t.Helper()
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.SetDataDir(t.TempDir())
	sessionKey := "test:user1"
	agentSession := newExitingAgentSession()
	state := &interactiveState{
		agentSession: agentSession,
		platform:     p,
		replyCtx:     "ctx-1",
	}
	e.interactiveStates[sessionKey] = state
	e.beginTurnJournal(sessionKey, "test", sessionKey, "m1", "do the thing")
	return e, p, agentSession, state, sessionKey
}

func runExitTestTurn(e *Engine, state *interactiveState, sessionKey string) {
	session := e.sessions.GetOrCreateActive(sessionKey)
	e.processInteractiveEvents(state, session, e.sessions, sessionKey, "m1", time.Now(), nil, nil, "ctx-1", 0)
}

func journalLeftover(t *testing.T, e *Engine) []inflightTurn {
	t.Helper()
	_, leftover := openTurnJournal(turnJournalPath(e.dataDir, e.name))
	return leftover
}

func TestProcessInteractiveEvents_AgentExitWithoutTextNotifiesUser(t *testing.T) {
	e, p, agentSession, state, sessionKey := newExitTestEngine(t)
	close(agentSession.events)
	runExitTestTurn(e, state, sessionKey)

	got := p.getSent()
	if len(got) != 1 || got[0] != e.i18n.T(MsgAgentExitedMidTurn) {
		t.Fatalf("sent = %#v, want only the agent-exited notice", got)
	}
	if leftover := journalLeftover(t, e); len(leftover) != 0 {
		t.Fatalf("handled crash left a journal entry: %+v", leftover)
	}
}

func TestProcessInteractiveEvents_AgentExitAfterPartialTextMarksIncomplete(t *testing.T) {
	e, p, agentSession, state, sessionKey := newExitTestEngine(t)
	agentSession.events <- Event{Type: EventText, Content: "half an answer"}
	close(agentSession.events)
	runExitTestTurn(e, state, sessionKey)

	got := p.getSent()
	if len(got) != 2 || got[0] != "half an answer" || got[1] != e.i18n.T(MsgAgentExitedMidTurn) {
		t.Fatalf("sent = %#v, want partial text then the agent-exited notice", got)
	}
}

func TestProcessInteractiveEvents_AgentExitAfterSilentReplyStaysSilent(t *testing.T) {
	e, p, agentSession, state, sessionKey := newExitTestEngine(t)
	agentSession.events <- Event{Type: EventText, Content: "NO_REPLY"}
	close(agentSession.events)
	runExitTestTurn(e, state, sessionKey)

	if got := p.getSent(); len(got) != 0 {
		t.Fatalf("sent = %#v, want nothing for a silent turn", got)
	}
}

func TestProcessInteractiveEvents_StoppedSessionCloseIsNotACrash(t *testing.T) {
	e, p, agentSession, state, sessionKey := newExitTestEngine(t)
	state.markStopped()
	close(agentSession.events)
	runExitTestTurn(e, state, sessionKey)

	for _, s := range p.getSent() {
		if s == e.i18n.T(MsgAgentExitedMidTurn) {
			t.Fatalf("agent-exited notice sent for a requested stop: %#v", p.getSent())
		}
	}
}

func TestProcessInteractiveEvents_ShutdownKeepsTurnJournaled(t *testing.T) {
	e, p, agentSession, state, sessionKey := newExitTestEngine(t)
	agentSession.events <- Event{Type: EventText, Content: "half an answer"}
	e.cancel()
	close(agentSession.events)
	runExitTestTurn(e, state, sessionKey)

	if got := p.getSent(); len(got) != 0 {
		t.Fatalf("sent = %#v, want nothing during shutdown", got)
	}
	leftover := journalLeftover(t, e)
	if len(leftover) != 1 || leftover[0].MessageID != "m1" || leftover[0].Preview != "do the thing" {
		t.Fatalf("leftover = %+v, want the interrupted turn", leftover)
	}
}

func TestProcessInteractiveEvents_CompletedTurnClearsJournal(t *testing.T) {
	e, _, agentSession, state, sessionKey := newExitTestEngine(t)
	agentSession.events <- Event{Type: EventResult, Content: "done", Done: true}
	runExitTestTurn(e, state, sessionKey)

	if leftover := journalLeftover(t, e); len(leftover) != 0 {
		t.Fatalf("completed turn left a journal entry: %+v", leftover)
	}
}
