package core

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRelayPeerRegistry_RegisterLookupUnregister(t *testing.T) {
	reg := NewRelayPeerRegistry(t.TempDir())
	socket := filepath.Join(t.TempDir(), "api.sock")
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := reg.Register("codex-bot", socket); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if got, ok := reg.Lookup("codex-bot"); !ok || got != socket {
		t.Fatalf("Lookup() = %q, %v; want %q, true", got, ok, socket)
	}
	if got := reg.Projects(); !reflect.DeepEqual(got, []string{"codex-bot"}) {
		t.Fatalf("Projects() = %#v", got)
	}

	reg.Unregister("codex-bot", socket)
	if _, ok := reg.Lookup("codex-bot"); ok {
		t.Fatal("Lookup() found the project after Unregister")
	}
}

func TestRelayPeerRegistry_UnregisterKeepsEntryTakenOverByAnotherProcess(t *testing.T) {
	reg := NewRelayPeerRegistry(t.TempDir())
	oldSocket := filepath.Join(t.TempDir(), "old.sock")
	newSocket := filepath.Join(t.TempDir(), "new.sock")
	if err := os.WriteFile(newSocket, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := reg.Register("bot", oldSocket); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register("bot", newSocket); err != nil {
		t.Fatal(err)
	}
	reg.Unregister("bot", oldSocket)

	if got, ok := reg.Lookup("bot"); !ok || got != newSocket {
		t.Fatalf("Lookup() = %q, %v; want the entry of the process that took over", got, ok)
	}
}

func TestRelayPeerRegistry_LookupIgnoresExitedProcess(t *testing.T) {
	reg := NewRelayPeerRegistry(t.TempDir())
	if err := reg.Register("bot", filepath.Join(t.TempDir(), "gone.sock")); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Lookup("bot"); ok {
		t.Fatal("Lookup() returned an entry whose socket no longer exists")
	}
}

func TestRelayPeerRegistry_NamesSharingAFileAreNotConfused(t *testing.T) {
	reg := NewRelayPeerRegistry(t.TempDir())
	socket := filepath.Join(t.TempDir(), "api.sock")
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register("team/bot", socket); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Lookup("team:bot"); ok {
		t.Fatal("Lookup() matched a different project that maps to the same file")
	}
	if _, ok := reg.Lookup("team/bot"); !ok {
		t.Fatal("Lookup() lost the registered project")
	}
}

type relayTestProcess struct {
	rm  *RelayManager
	api *APIServer
}

// startRelayTestProcess stands up what one lark-connect process provides for
// relay: an API server on a real unix socket, a relay manager, and its entries
// in the shared peer registry.
func startRelayTestProcess(t *testing.T, peersDir string, engines map[string]*Engine) *relayTestProcess {
	t.Helper()
	// Short path: unix socket paths are limited to ~100 bytes.
	dataDir, err := os.MkdirTemp("", "rly")
	if err != nil {
		t.Fatal(err)
	}
	api, err := NewAPIServer(dataDir)
	if err != nil {
		_ = os.RemoveAll(dataDir)
		t.Fatalf("NewAPIServer() error = %v", err)
	}
	rm := NewRelayManager("")
	api.SetRelayManager(rm)
	for name, e := range engines {
		api.RegisterEngine(name, e)
	}
	rm.EnablePeers(NewRelayPeerRegistry(peersDir), api.SocketPath())
	api.Start()
	t.Cleanup(func() {
		rm.ClosePeers()
		api.Stop()
		_ = os.RemoveAll(dataDir)
	})
	return &relayTestProcess{rm: rm, api: api}
}

func newRelayFeishuPlatform() *relayVisibilityPlatform {
	return &relayVisibilityPlatform{stubPlatformEngine: stubPlatformEngine{n: "feishu"}}
}

func TestRelay_CrossProcessSendRunsTargetInPeerProcess(t *testing.T) {
	peersDir := t.TempDir()
	sourcePlatform := newRelayFeishuPlatform()
	targetPlatform := newRelayFeishuPlatform()
	sourceEngine := NewEngine("source", &stubAgent{}, []Platform{sourcePlatform}, "", LangEnglish)
	targetAgent := &sessionEnvRecordingAgent{session: newResultAgentSession("target done")}
	targetEngine := NewEngine("target", targetAgent, []Platform{targetPlatform}, "", LangEnglish)

	src := startRelayTestProcess(t, peersDir, map[string]*Engine{"source": sourceEngine})
	tgt := startRelayTestProcess(t, peersDir, map[string]*Engine{"target": targetEngine})
	tgt.rm.AddToBind("feishu", "chat-1", "other")

	if !src.rm.HasTarget("target") {
		t.Fatal("source process cannot see the target running in the peer process")
	}
	src.rm.LinkProjects("feishu", "chat-1", "source", "target")
	peerBinding := tgt.rm.GetBinding("chat-1")
	for _, project := range []string{"source", "target", "other"} {
		if peerBinding == nil || peerBinding.Bots[project] == "" {
			t.Fatalf("peer binding = %#v, want %q mirrored without dropping existing bots", peerBinding, project)
		}
	}

	resp, err := src.rm.Send(context.Background(), RelayRequest{
		From:       "source",
		To:         "target",
		SessionKey: "feishu:chat-1:user-1",
		Message:    "please review",
		Depth:      1,
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if resp.Response != "target done" {
		t.Fatalf("response = %q, want %q", resp.Response, "target done")
	}
	if got := sourcePlatform.getSent(); len(got) != 1 || got[0] != "@target please review" {
		t.Fatalf("source bot posted %#v, want the request addressed to the target", got)
	}
	if got := targetPlatform.getSent(); len(got) != 1 || got[0] != "@source target done" {
		t.Fatalf("target bot posted %#v, want the reply addressed to the source", got)
	}
	if got := targetAgent.EnvValue("CC_SESSION"); got != "feishu:chat-1:user-1" {
		t.Fatalf("target CC_SESSION = %q, want the caller's session key", got)
	}
	if got := targetAgent.EnvValue("CC_RELAY_DEPTH"); got != "2" {
		t.Fatalf("target CC_RELAY_DEPTH = %q, want 2 after a second hop", got)
	}
}

func TestRelay_CrossProcessTimeoutReturnsPartialResponse(t *testing.T) {
	peersDir := t.TempDir()
	sourceEngine := NewEngine("source", &stubAgent{}, []Platform{newRelayFeishuPlatform()}, "", LangEnglish)
	targetSession := newControllableSession("target-session")
	targetEngine := NewEngine("target", &controllableAgent{nextSession: targetSession}, []Platform{newRelayFeishuPlatform()}, "", LangEnglish)

	src := startRelayTestProcess(t, peersDir, map[string]*Engine{"source": sourceEngine})
	startRelayTestProcess(t, peersDir, map[string]*Engine{"target": targetEngine})
	src.rm.SetTimeout(time.Second)
	src.rm.SetVisibility(RelayVisibilityNone)
	src.rm.LinkProjects("feishu", "chat-1", "source", "target")

	type relayResult struct {
		resp *RelayResponse
		err  error
	}
	done := make(chan relayResult, 1)
	go func() {
		resp, err := src.rm.Send(context.Background(), RelayRequest{
			From: "source", To: "target", SessionKey: "feishu:chat-1:user-1", Message: "long task",
		})
		done <- relayResult{resp: resp, err: err}
	}()

	targetSession.events <- Event{Type: EventText, Content: "partial answer"}
	time.Sleep(1500 * time.Millisecond)
	// One event to wake the timed-out relay loop, one for the background drain.
	targetSession.events <- Event{Type: EventThinking, Content: "still working"}
	targetSession.events <- Event{Type: EventResult, Content: "done", Done: true}

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Send() error = %v, want the partial response", got.err)
		}
		if got.resp.Response != "partial answer" {
			t.Fatalf("response = %q, want %q", got.resp.Response, "partial answer")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Send() did not return")
	}
}

func TestRelayManager_RejectsTooDeepRelayChain(t *testing.T) {
	rm := NewRelayManager("")
	rm.Bind("feishu", "chat-1", map[string]string{"source": "source", "target": "target"})

	_, err := rm.Send(context.Background(), RelayRequest{
		From: "source", To: "target", SessionKey: "feishu:chat-1:user-1", Message: "again", Depth: maxRelayDepth,
	})
	if err == nil || !strings.Contains(err.Error(), "already been relayed") {
		t.Fatalf("Send() error = %v, want the relay chain limit", err)
	}
}

func TestRelayManager_ExitedPeerIsNotATarget(t *testing.T) {
	reg := NewRelayPeerRegistry(t.TempDir())
	if err := reg.Register("ghost", filepath.Join(t.TempDir(), "gone.sock")); err != nil {
		t.Fatal(err)
	}
	rm := NewRelayManager("")
	rm.EnablePeers(reg, filepath.Join(t.TempDir(), "self.sock"))
	rm.Bind("feishu", "chat-1", map[string]string{"source": "source", "ghost": "ghost"})

	if rm.HasTarget("ghost") {
		t.Fatal("HasTarget() = true for a peer whose process exited")
	}
	_, err := rm.Send(context.Background(), RelayRequest{
		From: "source", To: "ghost", SessionKey: "feishu:chat-1:user-1", Message: "hi",
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Send() error = %v, want target not found", err)
	}
}

func TestRelayManager_TargetsListsBoundAndReachableProjects(t *testing.T) {
	peersDir := t.TempDir()
	newEngine := func(name string) *Engine { return NewEngine(name, &stubAgent{}, nil, "", LangEnglish) }
	src := startRelayTestProcess(t, peersDir, map[string]*Engine{"source": newEngine("source"), "helper": newEngine("helper")})
	startRelayTestProcess(t, peersDir, map[string]*Engine{"target": newEngine("target")})
	src.rm.LinkProjects("feishu", "chat-1", "source", "target")

	bound, available, err := src.rm.Targets("feishu:chat-1:user-1", "source")
	if err != nil {
		t.Fatalf("Targets() error = %v", err)
	}
	if !reflect.DeepEqual(bound, []string{"target"}) {
		t.Fatalf("bound = %#v, want [target]", bound)
	}
	if !reflect.DeepEqual(available, []string{"helper", "target"}) {
		t.Fatalf("available = %#v, want [helper target]", available)
	}
}

func TestCmdBind_AcceptsProjectRunningInPeerProcess(t *testing.T) {
	peersDir := t.TempDir()
	tgt := startRelayTestProcess(t, peersDir, map[string]*Engine{
		"codex-bot": NewEngine("codex-bot", &stubAgent{}, nil, "", LangEnglish),
	})

	p := &stubPlatformEngine{n: "feishu"}
	e := NewEngine("claude-bot", &stubAgent{}, []Platform{p}, "", LangEnglish)
	rm := NewRelayManager("")
	rm.RegisterEngine("claude-bot", e)
	rm.EnablePeers(NewRelayPeerRegistry(peersDir), filepath.Join(t.TempDir(), "self.sock"))
	t.Cleanup(rm.ClosePeers)
	e.SetRelayManager(rm)

	e.cmdBind(p, &Message{SessionKey: "feishu:chat-1:user-1", ReplyCtx: "ctx"}, []string{"codex-bot"})

	if got := strings.Join(p.getSent(), "\n"); !strings.Contains(got, "Bind successful") {
		t.Fatalf("reply = %q, want bind success", got)
	}
	for name, binding := range map[string]*RelayBinding{"local": rm.GetBinding("chat-1"), "peer": tgt.rm.GetBinding("chat-1")} {
		if binding == nil || binding.Bots["claude-bot"] == "" || binding.Bots["codex-bot"] == "" {
			t.Fatalf("%s binding = %#v, want both bots", name, binding)
		}
	}
}
