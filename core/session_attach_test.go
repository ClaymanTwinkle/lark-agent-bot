package core

import (
	"context"
	"fmt"
	"testing"
)

type unavailableResumeAgent struct {
	stubAgent
	started []string
}

func (a *unavailableResumeAgent) StartSession(_ context.Context, id string) (AgentSession, error) {
	a.started = append(a.started, id)
	return nil, fmt.Errorf("%w: active writer is elsewhere", ErrSessionResumeUnavailable)
}

func TestResumeUnavailableDoesNotReplaceSelectedConversation(t *testing.T) {
	a := &unavailableResumeAgent{}
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", a, []Platform{p}, "", LangEnglish)
	defer e.Stop()
	s := e.sessions.SwitchToAgentSession("test:alice", "live-thread", a.Name(), "terminal work")
	e.getOrCreateInteractiveStateWith("test:alice", p, "ctx", s, e.sessions, nil, "")
	if len(a.started) != 1 || a.started[0] != "live-thread" {
		t.Fatalf("resume started a different conversation: %v", a.started)
	}
	if got := s.GetAgentSessionID(); got != "live-thread" {
		t.Fatalf("selected thread lost: %q", got)
	}
}
