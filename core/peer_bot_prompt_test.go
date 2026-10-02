package core

import (
	"strings"
	"testing"
)

type peerBotStubPlatform struct {
	stubPlatformEngine
	names []string
}

func (p *peerBotStubPlatform) PeerBotNames() []string { return p.names }

func TestPlatformPrompt_TellsAgentWhichBotsItCanHandWorkTo(t *testing.T) {
	p := &peerBotStubPlatform{stubPlatformEngine: stubPlatformEngine{n: "feishu"}, names: []string{"Codex", "Gemini"}}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)

	prompt := e.platformPrompt(p)
	for _, want := range []string{"Codex, Gemini", `lark-agent-bot send --message "@Codex `, "do not hand it on"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}

	e.i18n = NewI18n(LangChinese)
	if zh := e.platformPrompt(p); !strings.Contains(zh, "不要再转派") {
		t.Errorf("Chinese prompt not localized:\n%s", zh)
	}
}

func TestPlatformPrompt_EmptyWithoutPeerBots(t *testing.T) {
	p := &peerBotStubPlatform{stubPlatformEngine: stubPlatformEngine{n: "feishu"}}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)

	if got := e.platformPrompt(p); got != "" {
		t.Fatalf("platformPrompt() = %q, want empty without peer bots", got)
	}

	p.names = []string{"Codex"}
	if got := e.platformPrompt(p); !strings.Contains(got, "@Codex") {
		t.Fatalf("platformPrompt() = %q, want the peer-bot section", got)
	}
}
