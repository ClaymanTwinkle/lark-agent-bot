package core

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPeerVersionMismatches(t *testing.T) {
	peers := []RelayPeer{
		{Project: "same", Version: "v1.2.0", Commit: "abc1234"},
		{Project: "older", Version: "v1.1.0", Commit: "abc1234"},
		{Project: "unversioned"},
		{Project: "other-commit", Version: "v1.2.0", Commit: "def5678"},
		{Project: "no-commit", Version: "v1.2.0"},
	}
	names := func(ps []RelayPeer) []string {
		var out []string
		for _, p := range ps {
			out = append(out, p.Project)
		}
		return out
	}

	got := names(peerVersionMismatches("v1.2.0", "abc1234", peers))
	if want := []string{"older", "unversioned", "other-commit"}; !reflect.DeepEqual(got, want) {
		t.Errorf("with commit: %v, want %v", got, want)
	}
	// /upgrade knows only the new version, not its commit.
	got = names(peerVersionMismatches("v1.2.0", "", peers))
	if want := []string{"older", "unversioned"}; !reflect.DeepEqual(got, want) {
		t.Errorf("without commit: %v, want %v", got, want)
	}
	got = names(peerVersionMismatches("v1.2.0", "none", peers))
	if want := []string{"older", "unversioned"}; !reflect.DeepEqual(got, want) {
		t.Errorf("unknown commit: %v, want %v", got, want)
	}
}

func setBuildForTest(t *testing.T, version, commit string) {
	t.Helper()
	prevVersion, prevCommit := CurrentVersion, CurrentCommit
	t.Cleanup(func() { CurrentVersion, CurrentCommit = prevVersion, prevCommit })
	CurrentVersion, CurrentCommit = version, commit
}

func TestPeerVersionWarning_NamesOtherLiveProcessesOnAnotherBuild(t *testing.T) {
	peersDir := t.TempDir()

	setBuildForTest(t, "v1.0.0", "aaa1111")
	codex := NewEngine("codex-bot", &stubAgent{}, []Platform{newRelayFeishuPlatform()}, "", LangEnglish)
	startRelayTestProcess(t, peersDir, map[string]*Engine{"codex-bot": codex})

	CurrentVersion, CurrentCommit = "v1.1.0", "bbb2222"
	twin := NewEngine("twin-bot", &stubAgent{}, []Platform{newRelayFeishuPlatform()}, "", LangEnglish)
	startRelayTestProcess(t, peersDir, map[string]*Engine{"twin-bot": twin})
	claude := NewEngine("claude-bot", &stubAgent{}, []Platform{newRelayFeishuPlatform()}, "", LangEnglish)
	self := startRelayTestProcess(t, peersDir, map[string]*Engine{"claude-bot": claude})
	claude.SetRelayManager(self.rm)

	// A process that crashed leaves its entry behind but does not answer.
	if err := NewRelayPeerRegistry(peersDir).Register("ghost-bot", filepath.Join(t.TempDir(), "gone.sock")); err != nil {
		t.Fatal(err)
	}

	warn := claude.peerVersionWarning("v1.1.0", "bbb2222")
	if !strings.Contains(warn, "codex-bot (v1.0.0, aaa1111)") || !strings.Contains(warn, "v1.1.0, bbb2222") {
		t.Fatalf("warning = %q, want codex-bot's older build named", warn)
	}
	for _, unwanted := range []string{"twin-bot", "claude-bot", "ghost-bot"} {
		if strings.Contains(warn, unwanted) {
			t.Errorf("warning names %s: %q", unwanted, warn)
		}
	}

	if warn := codex.peerVersionWarning("v1.0.0", "aaa1111"); warn != "" {
		t.Errorf("an engine without a relay manager warned: %q", warn)
	}
}

func TestDispatchRestartNotify_WarnsAboutBotsOnAnotherBuild(t *testing.T) {
	peersDir := t.TempDir()
	setBuildForTest(t, "v1.0.0", "aaa1111")
	codex := NewEngine("codex-bot", &stubAgent{}, []Platform{newRelayFeishuPlatform()}, "", LangEnglish)
	startRelayTestProcess(t, peersDir, map[string]*Engine{"codex-bot": codex})

	CurrentVersion, CurrentCommit = "v1.1.0", "bbb2222"
	plat := &restartNotifyStub{name: "feishu"}
	claude := NewEngine("claude-bot", &stubAgent{}, []Platform{plat}, "", LangEnglish)
	self := startRelayTestProcess(t, peersDir, map[string]*Engine{"claude-bot": claude})
	claude.SetRelayManager(self.rm)
	plat.markReady(t, claude)

	if err := claude.dispatchRestartNotify(&RestartRequest{Platform: "feishu", SessionKey: "feishu:chat:user"}); err != nil {
		t.Fatalf("dispatchRestartNotify: %v", err)
	}
	sent := plat.sentTexts()
	if len(sent) != 1 || !strings.Contains(sent[0], "(v1.1.0)") || !strings.Contains(sent[0], "codex-bot (v1.0.0, aaa1111)") {
		t.Fatalf("restart notice = %q, want the version and the warning about codex-bot", sent)
	}
}
