package core

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func newConfigProjectEngine(t *testing.T) (*Engine, *[]ProjectSettingsUpdate) {
	t.Helper()
	e := NewEngine("test", &stubAgent{}, nil, "", LangEnglish)
	e.SetAdminFrom("boss")
	e.SetReplyFooterEnabled(true)
	var saved []ProjectSettingsUpdate
	e.SetProjectSettingsSaver(func(u ProjectSettingsUpdate) error {
		saved = append(saved, u)
		return nil
	})
	return e, &saved
}

func configClicker(userID string) *Message {
	return &Message{SessionKey: "test:chat1:" + userID, Platform: "test", UserID: userID}
}

func cardHasAction(card *Card, action string) bool {
	_, ok := findCardAction(card, action)
	return ok
}

func cardSelectKeys(card *Card) []string {
	var keys []string
	for _, el := range card.Elements {
		if sel, ok := el.(CardSelect); ok {
			keys = append(keys, strings.Fields(strings.TrimPrefix(sel.InitValue, "act:/config "))[0])
		}
	}
	return keys
}

func TestConfigCard_ProjectPageIsForAdmins(t *testing.T) {
	e, _ := newConfigProjectEngine(t)
	refused := e.i18n.Tf(MsgAdminRequired, "/config")

	for name, card := range map[string]*Card{
		"non-admin":       e.handleCardNavWithContext("nav:/config project", configClicker("member")),
		"unknown clicker": e.handleCardNav("nav:/config project", "test:chat1:member"),
	} {
		if !strings.Contains(card.RenderText(), refused) || len(cardSelectKeys(card)) != 0 {
			t.Fatalf("%s opened the project page: %q", name, card.RenderText())
		}
	}

	display := e.handleCardNavWithContext("nav:/config", configClicker("member"))
	if cardHasAction(display, "nav:/config project") {
		t.Fatal("non-admins must not see the project page button")
	}

	display = e.handleCardNavWithContext("nav:/config", configClicker("boss"))
	if !cardHasAction(display, "nav:/config project") || !cardHasAction(display, "nav:/config display") {
		t.Fatal("admins must get the page buttons")
	}
	for _, key := range cardSelectKeys(display) {
		if slices.Contains(configProjectKeys, key) {
			t.Fatalf("display page shows project setting %q", key)
		}
	}

	project := e.handleCardNavWithContext("nav:/config project", configClicker("boss"))
	if got := cardSelectKeys(project); !slices.Equal(got, configProjectKeys) {
		t.Fatalf("project page items = %v, want %v", got, configProjectKeys)
	}
}

func TestConfigCard_ProjectSettingSavesThenApplies(t *testing.T) {
	e, saved := newConfigProjectEngine(t)

	card := e.handleCardNavWithContext("act:/config reply_footer false", configClicker("boss"))
	if e.replyFooterEnabled {
		t.Fatal("reply_footer was not applied")
	}
	if len(*saved) != 1 || (*saved)[0].ReplyFooter == nil || *(*saved)[0].ReplyFooter {
		t.Fatalf("saved = %+v, want reply_footer=false written to the config", *saved)
	}
	if got := configCardSelect(t, card, "reply_footer").InitValue; got != "act:/config reply_footer false" {
		t.Fatalf("the card must redraw the project page with the new value, got %q", got)
	}

	e.SetProjectSettingsSaver(func(ProjectSettingsUpdate) error { return errors.New("config has unknown keys") })
	card = e.handleCardNavWithContext("act:/config inject_sender true", configClicker("boss"))
	if e.injectSender {
		t.Fatal("a setting whose save failed must not be applied")
	}
	if !strings.Contains(card.RenderText(), "config has unknown keys") {
		t.Fatalf("the card must show the save error: %q", card.RenderText())
	}
}

func TestConfigCard_ProjectSettingRefusedForNonAdmins(t *testing.T) {
	e, saved := newConfigProjectEngine(t)

	e.handleCardNavWithContext("act:/config reply_footer false", configClicker("member"))
	e.handleCardNav("act:/config reply_footer false", "test:chat1:member")
	if !e.replyFooterEnabled || len(*saved) != 0 {
		t.Fatalf("a non-admin changed a project setting: footer=%t saved=%+v", e.replyFooterEnabled, *saved)
	}

	// Display settings stay open to everyone, as before.
	e.handleCardNavWithContext("act:/config mode compact", configClicker("member"))
	if e.display.Mode != "compact" {
		t.Fatalf("display mode = %q, want compact", e.display.Mode)
	}
}

func TestConfigCard_AgentTypeWaitsForRestart(t *testing.T) {
	const next = "config-card-next-agent"
	RegisterAgent(next, func(map[string]any) (Agent, error) { return &stubAgent{}, nil })
	e, saved := newConfigProjectEngine(t)
	drainRestart := func() (RestartRequest, bool) {
		select {
		case req := <-RestartCh:
			return req, true
		default:
			return RestartRequest{}, false
		}
	}
	drainRestart()
	t.Cleanup(func() { drainRestart() })

	card := e.handleCardNavWithContext("act:/config agent_type "+next, configClicker("boss"))
	if len(*saved) != 1 || (*saved)[0].AgentType == nil || *(*saved)[0].AgentType != next {
		t.Fatalf("saved = %+v, want agent_type %q", *saved, next)
	}
	if !strings.Contains(card.RenderText(), e.i18n.Tf(MsgConfigRestartRequired, next)) || !cardHasAction(card, "act:/config restart") {
		t.Fatalf("the card must say a restart is needed and offer one: %q", card.RenderText())
	}
	if got := configCardSelect(t, card, "agent_type").InitValue; got != "act:/config agent_type "+next {
		t.Fatalf("agent_type dropdown = %q, want the saved type", got)
	}

	card = e.handleCardNavWithContext("act:/config agent_type no-such-agent", configClicker("boss"))
	if len(*saved) != 1 || !strings.Contains(card.RenderText(), "unknown agent type") {
		t.Fatalf("an unknown agent type must be refused: saved=%+v card=%q", *saved, card.RenderText())
	}

	e.handleCardNavWithContext("act:/config restart", configClicker("member"))
	if _, ok := drainRestart(); ok {
		t.Fatal("a non-admin restarted the bot")
	}
	card = e.handleCardNavWithContext("act:/config restart", configClicker("boss"))
	req, ok := drainRestart()
	if !ok || req.SessionKey != "test:chat1:boss" {
		t.Fatalf("restart request = %+v, %t; want one for the admin's chat", req, ok)
	}
	if !strings.Contains(card.RenderText(), e.i18n.T(MsgRestarting)) {
		t.Fatalf("the card must confirm the restart: %q", card.RenderText())
	}
}

func TestConfigCard_NoAdminExplainsSetup(t *testing.T) {
	e := NewEngine("test", &stubAgent{}, nil, "", LangEnglish)
	hint := e.i18n.T(MsgConfigNoAdminHint)
	if card := e.handleCardNavWithContext("nav:/config", configClicker("anyone")); !strings.Contains(card.RenderText(), hint) {
		t.Fatal("without admin_from the display page must explain how to set the first admin")
	}
	e.SetAdminFrom("boss")
	if card := e.handleCardNavWithContext("nav:/config", configClicker("anyone")); strings.Contains(card.RenderText(), hint) {
		t.Fatal("the setup hint must go away once an admin is set")
	}
}

func TestConfigCommand_ProjectSettingsNeedAdmin(t *testing.T) {
	e, saved := newConfigProjectEngine(t)
	p := &stubPlatformEngine{n: "plain"}
	send := func(userID, raw string) string {
		p.clearSent()
		e.handleCommand(p, &Message{SessionKey: "plain:chat1:" + userID, Platform: "plain", UserID: userID, ReplyCtx: "ctx"}, raw)
		return strings.Join(p.getSent(), "\n")
	}
	refused := e.i18n.Tf(MsgAdminRequired, "/config")

	for _, raw := range []string{"/config reply_footer false", "/config set inject_sender true", "/config project"} {
		if got := send("member", raw); got != refused {
			t.Fatalf("%s from a non-admin: got %q, want %q", raw, got, refused)
		}
	}
	if !e.replyFooterEnabled || e.injectSender || len(*saved) != 0 {
		t.Fatal("a refused command changed a project setting")
	}
	if got := send("member", "/config get reply_footer"); !strings.Contains(got, "`reply_footer` = `true`") {
		t.Fatalf("reading a project setting stays open: %q", got)
	}
	if got := send("member", "/config"); strings.Contains(got, "reply_footer") || strings.Contains(got, "/config project") {
		t.Fatalf("non-admins must not see project settings in the listing: %q", got)
	}

	if got := send("boss", "/config reply_footer false"); !strings.Contains(got, "`reply_footer` → `false`") {
		t.Fatalf("admin change: %q", got)
	}
	if e.replyFooterEnabled || len(*saved) != 1 {
		t.Fatal("the admin's change was not applied and saved")
	}
	if got := send("boss", "/config"); !strings.Contains(got, "/config project") {
		t.Fatalf("admins must be told where project settings are: %q", got)
	}
	if got := send("boss", "/config project"); !strings.Contains(got, "`reply_footer` = ") || strings.Contains(got, "`tool_messages` = ") {
		t.Fatalf("/config project must list the project settings only: %q", got)
	}
}

func TestIsConfigAdminInvocation(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"display"}, false},
		{[]string{"mode", "quiet"}, false},
		{[]string{"get", "reply_footer"}, false},
		{[]string{"reload"}, false},
		{[]string{"project"}, true},
		{[]string{"PROJECT"}, true},
		{[]string{"restart"}, true},
		{[]string{"reply_footer"}, true},
		{[]string{"set", "agent_type", "codex"}, true},
		{[]string{"Inject_Sender", "true"}, true},
	} {
		if got := isConfigAdminInvocation(tc.args); got != tc.want {
			t.Errorf("isConfigAdminInvocation(%q) = %t, want %t", tc.args, got, tc.want)
		}
	}
}
