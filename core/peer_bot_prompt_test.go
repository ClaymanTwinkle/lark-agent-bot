package core

import (
	"strings"
	"testing"
)

type peerBotStubPlatform struct {
	stubPlatformEngine
	names      []string
	formatting string
}

func (p *peerBotStubPlatform) PeerBotNames() []string         { return p.names }
func (p *peerBotStubPlatform) FormattingInstructions() string { return p.formatting }

func TestPlatformPrompt_TellsAgentWhichBotsItCanHandWorkTo(t *testing.T) {
	p := &peerBotStubPlatform{stubPlatformEngine: stubPlatformEngine{n: "feishu"}, names: []string{"Codex", "Gemini"}}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)

	prompt := e.platformPrompt(p)
	for _, want := range []string{"Codex, Gemini", `lark-connect send --message "@Codex `, "do not hand it on"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}

	e.i18n = NewI18n(LangChinese)
	if zh := e.platformPrompt(p); !strings.Contains(zh, "不要再转派") {
		t.Errorf("Chinese prompt not localized:\n%s", zh)
	}
}

func TestPlatformPrompt_KeepsFormattingAndOmitsEmptyPeerList(t *testing.T) {
	p := &peerBotStubPlatform{stubPlatformEngine: stubPlatformEngine{n: "feishu"}, formatting: "Use plain text."}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)

	if got := e.platformPrompt(p); got != "Use plain text." {
		t.Fatalf("platformPrompt() = %q, want only the formatting instructions", got)
	}

	p.names = []string{"Codex"}
	got := e.platformPrompt(p)
	if !strings.HasPrefix(got, "Use plain text.\n\n") || !strings.Contains(got, "@Codex") {
		t.Fatalf("platformPrompt() = %q, want formatting followed by the peer-bot section", got)
	}
}
