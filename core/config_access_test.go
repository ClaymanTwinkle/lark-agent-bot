package core

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestChangeMemberList(t *testing.T) {
	const last = MsgConfigCannotRemoveLastAllowed
	for _, tc := range []struct {
		name    string
		list    string
		add     bool
		ids     []string
		want    string
		wantErr MsgKey
	}{
		{"add", "boss", true, []string{"ou_a"}, "boss,ou_a", ""},
		{"add keeps existing entries once", "boss, ou_a", true, []string{"OU_A", "ou_b"}, "boss,ou_a,ou_b", ""},
		{"remove", "boss,ou_a,ou_b", false, []string{"OU_A"}, "boss,ou_b", ""},
		{"remove self", "boss,ou_a", false, []string{"Boss"}, "", MsgConfigCannotRemoveSelf},
		{"remove last", "ou_a", false, []string{"ou_a"}, "", last},
		{"remove missing", "boss,ou_a", false, []string{"ou_z"}, "", MsgConfigNotListed},
		{"everyone", "*", true, []string{"ou_a"}, "", MsgConfigListUnrestricted},
		{"empty", " ", false, []string{"ou_a"}, "", MsgConfigListUnrestricted},
		{"comma in id", "boss", true, []string{"ou_a,ou_b"}, "", MsgConfigInvalidUserID},
		{"wildcard id", "boss", true, []string{"*"}, "", MsgConfigInvalidUserID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := changeMemberList(tc.list, tc.add, tc.ids, "boss", last)
			var listErr *memberListError
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error %v", err)
			case tc.wantErr != "" && (!errors.As(err, &listErr) || listErr.key != tc.wantErr):
				t.Fatalf("error = %v, want %s", err, tc.wantErr)
			case got != tc.want:
				t.Fatalf("list = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestConfigMemberTargets(t *testing.T) {
	msg := &Message{Mentions: []Mention{{ID: "ou_ann", Name: "Ann"}, {ID: "ou_annlee", Name: "Ann Lee"}}}
	got, err := configMemberTargets(msg, []string{"@Ann", "Lee", "ou_raw", "@Ann"})
	if err != nil || !slices.Equal(got, []string{"ou_annlee", "ou_ann", "ou_raw"}) {
		t.Fatalf("targets = %v, %v; want the mentioned IDs and the raw ID", got, err)
	}
	var listErr *memberListError
	if _, err := configMemberTargets(&Message{}, []string{"@Nobody"}); !errors.As(err, &listErr) || listErr.key != MsgConfigUnknownMention {
		t.Fatalf("an unresolved @name must be refused, got %v", err)
	}
	if _, err := configMemberTargets(msg, nil); !errors.Is(err, errConfigAccessUsage) {
		t.Fatalf("no target must show the usage, got %v", err)
	}
}

// newConfigAccessEngine has admin "boss" and one platform whose allow_from
// can change at runtime.
func newConfigAccessEngine(t *testing.T, allowFrom string) (*Engine, *allowFromPlatform, *[]ProjectSettingsUpdate) {
	t.Helper()
	p := &allowFromPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}, allowFrom: allowFrom}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.SetAdminFrom("boss")
	var saved []ProjectSettingsUpdate
	e.SetProjectSettingsSaver(func(u ProjectSettingsUpdate) error {
		saved = append(saved, u)
		return nil
	})
	return e, p, &saved
}

func sendConfigCommand(e *Engine, p *allowFromPlatform, userID, raw string, mentions ...Mention) string {
	p.clearSent()
	e.handleCommand(p, &Message{SessionKey: "test:group:" + userID, Platform: "test", UserID: userID, ReplyCtx: "ctx", Mentions: mentions}, raw)
	return strings.Join(p.getSent(), "\n")
}

func TestConfigAccess_AdminFrom(t *testing.T) {
	e, p, saved := newConfigAccessEngine(t, "")

	if got := sendConfigCommand(e, p, "member", "/config admin add member"); got != e.i18n.Tf(MsgAdminRequired, "/config") {
		t.Fatalf("a non-admin added an admin: %q", got)
	}
	got := sendConfigCommand(e, p, "boss", "/config admin add @Ann Lee", Mention{ID: "ou_ann", Name: "Ann Lee"})
	if !e.isAdmin("ou_ann") || !strings.Contains(got, "`admin_from` → `boss,ou_ann`") {
		t.Fatalf("adding by @mention: reply %q", got)
	}
	if len(*saved) != 1 || (*saved)[0].AdminFrom == nil || *(*saved)[0].AdminFrom != "boss,ou_ann" {
		t.Fatalf("saved = %+v, want admin_from written to the config", *saved)
	}
	if got := sendConfigCommand(e, p, "boss", "/config admin remove boss"); got != e.i18n.Tf(MsgConfigCannotRemoveSelf, "boss") {
		t.Fatalf("an admin removed themselves: %q", got)
	}
	sendConfigCommand(e, p, "ou_ann", "/config admin remove boss")
	if e.isAdmin("boss") || !e.isAdmin("ou_ann") {
		t.Fatal("another admin must be able to remove boss")
	}
}

func TestConfigAccess_AllowFrom(t *testing.T) {
	e, p, saved := newConfigAccessEngine(t, "boss,ou_a")

	sendConfigCommand(e, p, "boss", "/config allow add ou_b")
	sendConfigCommand(e, p, "boss", "/config allow remove ou_a")
	if p.allowFrom != "boss,ou_b" {
		t.Fatalf("allow_from = %q, want it applied without a restart", p.allowFrom)
	}
	if len(*saved) != 2 || (*saved)[1].PlatformAllowFrom["test"] != "boss,ou_b" {
		t.Fatalf("saved = %+v, want the platform's allow_from written to the config", *saved)
	}
	if got := sendConfigCommand(e, p, "boss", "/config allow remove boss"); got != e.i18n.Tf(MsgConfigCannotRemoveSelf, "boss") {
		t.Fatalf("an admin removed themselves from allow_from: %q", got)
	}

	p.allowFrom = ""
	if got := sendConfigCommand(e, p, "boss", "/config allow add ou_c"); got != e.i18n.Tf(MsgConfigListUnrestricted, `""`) || p.allowFrom != "" {
		t.Fatalf("adding to an unrestricted allow_from would lock everyone else out: %q", got)
	}

	plain := NewEngine("test", &stubAgent{}, []Platform{&stubPlatformEngine{n: "test"}}, "", LangEnglish)
	plain.SetAdminFrom("boss")
	if got := plain.configAccessChange(&Message{UserID: "boss", Platform: "test"}, []string{"allow", "add", "ou_c"}); got != plain.i18n.T(MsgConfigAllowUnsupported) {
		t.Fatalf("a platform without runtime allow_from: %q", got)
	}
}

func TestConfigAccess_DisabledCommands(t *testing.T) {
	e, p, saved := newConfigAccessEngine(t, "")

	sendConfigCommand(e, p, "boss", "/config disable /list")
	if !e.effectiveDisabledCmds("anyone")["list"] {
		t.Fatal("/list was not disabled")
	}
	for _, name := range []string{"config", "*"} {
		if got := sendConfigCommand(e, p, "boss", "/config disable "+name); got != e.i18n.T(MsgConfigCannotDisableConfig) {
			t.Fatalf("disabling %q must be refused: %q", name, got)
		}
	}
	sendConfigCommand(e, p, "boss", "/config enable list")
	if e.effectiveDisabledCmds("anyone")["list"] {
		t.Fatal("/list was not enabled again")
	}
	if last := (*saved)[len(*saved)-1].DisabledCommands; last == nil || len(last) != 0 {
		t.Fatalf("enabling the last command must save an empty list, got %#v", last)
	}
	if got := sendConfigCommand(e, p, "boss", "/config enable list"); got != e.i18n.Tf(MsgConfigNotDisabled, "list") {
		t.Fatalf("enabling a command that is not disabled: %q", got)
	}
}

func TestConfigAccess_CardPage(t *testing.T) {
	e, p, _ := newConfigAccessEngine(t, "boss,ou_a")
	e.SetAdminFrom("boss,ou_ann")
	e.SetDisabledCommands([]string{"list"})
	boss := &Message{SessionKey: "test:group:boss", Platform: "test", UserID: "boss"}

	card := e.handleCardNavWithContext("nav:/config access", boss)
	for _, action := range []string{"act:/config admin remove ou_ann", "act:/config allow remove ou_a", "act:/config enable list"} {
		if !cardHasAction(card, action) {
			t.Fatalf("access page lacks %q: %q", action, card.RenderText())
		}
	}
	if cardHasAction(card, "act:/config admin remove boss") || cardHasAction(card, "act:/config allow remove boss") {
		t.Fatal("the viewer must not get a button to remove themselves")
	}
	var disable CardSelect
	for _, el := range card.Elements {
		if sel, ok := el.(CardSelect); ok {
			disable = sel
		}
	}
	for _, opt := range disable.Options {
		if opt.Value == "act:/config disable config" || opt.Value == "act:/config disable list" {
			t.Fatalf("disable dropdown offers %q", opt.Value)
		}
	}

	card = e.handleCardNavWithContext("act:/config allow remove ou_a", boss)
	if p.allowFrom != "boss" || !strings.Contains(card.RenderText(), "`allow_from` → `boss`") {
		t.Fatalf("remove button: allow_from %q, card %q", p.allowFrom, card.RenderText())
	}

	for name, card := range map[string]*Card{
		"non-admin":       e.handleCardNavWithContext("act:/config admin remove boss", &Message{SessionKey: "test:group:ou_x", Platform: "test", UserID: "ou_x"}),
		"unknown clicker": e.handleCardNav("act:/config admin remove boss", "test:group:ou_x"),
	} {
		if !e.isAdmin("boss") || !strings.Contains(card.RenderText(), e.i18n.Tf(MsgAdminRequired, "/config")) {
			t.Fatalf("%s changed admin_from: %q", name, card.RenderText())
		}
	}
}
