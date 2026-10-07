package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Tests for the chat-side authorization boundary: which commands, card
// actions and replies need admin_from, and the input checks on values that
// reach the agent process.

func TestIsPrivilegedCommandInvocation_HostLevelSubcommands(t *testing.T) {
	cases := []struct {
		cmd, args string
		want      bool
	}{
		{"alias", "", false},
		{"alias", "list", false},
		{"alias", "add hi /shell", true},
		{"alias", "a hi /shell", true},
		{"alias", "del hi", true},
		{"alias", "delete hi", true},
		{"alias", "remove hi", true},
		{"allow", "", false},
		{"allow", "Bash", true},
		{"provider", "", false},
		{"provider", "list", false},
		{"provider", "current", false},
		{"provider", "add relay sk-x", true},
		{"provider", "remove relay", true},
		{"provider", "switch relay", true},
		{"provider", "clear", true},
		{"provider", "relay", true}, // positional switch
		{"memory", "", false},
		{"memory", "add note", false},
		{"memory", "show", false},
		{"memory", "global", true},
		{"memory", "global add x", true},
		{"workspace", "", false},
		{"workspace", "bind repo", false},
		{"workspace", "available 2", false},
		{"workspace", "route /tmp", true},
		{"workspace", "init https://example.com/a/b.git", true},
		{"workspace", "shared", false},
		{"workspace", "shared bind repo", false},
		{"workspace", "shared list", false},
		{"workspace", "shared route /tmp", true},
		{"workspace", "shared init https://example.com/a/b.git", true},
	}
	for _, c := range cases {
		if got := isPrivilegedCommandInvocation(c.cmd, strings.Fields(c.args)); got != c.want {
			t.Errorf("/%s %s: privileged = %v, want %v", c.cmd, c.args, got, c.want)
		}
	}
}

func TestHandleCommand_HostLevelSubcommandsNeedAdmin(t *testing.T) {
	for _, cmd := range []string{
		"/alias add hi /shell",
		"/allow Bash",
		"/provider add relay sk-x https://example.com",
		"/provider switch relay",
		"/provider relay",
		"/memory global add x",
	} {
		p := &stubPlatformEngine{n: "test"}
		e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
		e.SetAdminFrom("boss")
		msg := &Message{SessionKey: "test:c:u", UserID: "u", Platform: "test", ReplyCtx: "ctx"}
		if !e.handleCommand(p, msg, cmd) {
			t.Fatalf("%s: must be intercepted", cmd)
		}
		if sent := p.getSent(); len(sent) == 0 || !strings.Contains(sent[0], "requires admin") {
			t.Fatalf("%s: want admin-required reply, got %#v", cmd, sent)
		}
	}
}

func TestHandleCommand_ReadOnlySiblingsStayOpen(t *testing.T) {
	for _, cmd := range []string{"/alias", "/alias list", "/allow", "/provider", "/provider list", "/memory"} {
		p := &stubPlatformEngine{n: "test"}
		e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
		e.SetAdminFrom("boss")
		msg := &Message{SessionKey: "test:c:u", UserID: "u", Platform: "test", ReplyCtx: "ctx"}
		e.handleCommand(p, msg, cmd)
		for _, s := range p.getSent() {
			if strings.Contains(s, "requires admin") {
				t.Fatalf("%s must stay open to non-admins, got %q", cmd, s)
			}
		}
	}
}

func TestAliasAdd_NonAdminCannotHijackWords(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.SetAdminFrom("boss")
	msg := &Message{SessionKey: "test:c:u", UserID: "u", Platform: "test", ReplyCtx: "ctx"}
	e.handleCommand(p, msg, "/alias add status /shell id")
	e.aliasMu.RLock()
	n := len(e.aliases)
	e.aliasMu.RUnlock()
	if n != 0 {
		t.Fatalf("non-admin added an alias: %d aliases", n)
	}
}

func TestEffectiveDisabledCmds_RoleAddsToProjectList(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.SetDisabledCommands([]string{"restart"})
	urm := NewUserRoleManager()
	urm.Configure("member", []RoleInput{
		{Name: "admin", UserIDs: []string{"boss"}},
		{Name: "member", UserIDs: []string{"*"}, DisabledCommands: []string{"cron"}},
	})
	e.SetUserRoles(urm)

	boss, member := e.effectiveDisabledCmds("boss"), e.effectiveDisabledCmds("someone")
	if !boss["restart"] || boss["cron"] {
		t.Fatalf("admin role: got %v, want only the project's restart", boss)
	}
	if !member["restart"] || !member["cron"] {
		t.Fatalf("member role: got %v, want restart and cron", member)
	}
	if e.disabledCmds["cron"] {
		t.Fatal("merging must not modify the project-level list")
	}

	msg := &Message{SessionKey: "test:c:boss", UserID: "boss", Platform: "test", ReplyCtx: "ctx"}
	e.handleCommand(p, msg, "/restart")
	if sent := p.getSent(); len(sent) == 0 || !strings.Contains(sent[0], "disabled") {
		t.Fatalf("a role with no disabled_commands must not re-enable /restart, got %#v", sent)
	}
}

func TestCardNavWithContext_RoleCannotReenableWorkspace(t *testing.T) {
	e := NewEngine("test", &stubAgent{}, nil, "", LangEnglish)
	e.SetMultiWorkspace(t.TempDir(), filepath.Join(t.TempDir(), "bindings.json"))
	e.SetDisabledCommands([]string{"workspace"})
	urm := NewUserRoleManager()
	urm.Configure("", []RoleInput{{Name: "admin", UserIDs: []string{"boss"}}})
	e.SetUserRoles(urm)
	card := e.handleCardNavWithContext("nav:/workspace bind", &Message{SessionKey: "test:c:boss", UserID: "boss", Platform: "test"})
	if card == nil || !strings.Contains(card.RenderText(), "disabled") {
		t.Fatalf("want disabled card, got %#v", card)
	}
}

func TestCheckAllowFrom_WarnsOnWildcard(t *testing.T) {
	buf, restore := captureSlog(t)
	defer restore()
	CheckAllowFrom("test", "*")
	if !strings.Contains(buf.String(), "allow_from") {
		t.Fatalf("allow_from = \"*\" must log a warning, got %q", buf.String())
	}
	buf.Reset()
	CheckAllowFrom("test", "ou_1,ou_2")
	if buf.Len() != 0 {
		t.Fatalf("an explicit list must not warn, got %q", buf.String())
	}
}

// privilegedModeAgent has one mode that drops all approvals, reachable by an
// alias the engine only sees through NormalizeMode.
type privilegedModeAgent struct{ stubModelModeAgent }

func (a *privilegedModeAgent) PermissionModes() []PermissionModeInfo {
	return []PermissionModeInfo{
		{Key: "default", Name: "Default"},
		{Key: "bypassPermissions", Name: "YOLO", Privileged: true},
	}
}

func (a *privilegedModeAgent) NormalizeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "yolo", "bypasspermissions":
		return "bypassPermissions"
	}
	return "default"
}

func TestModeRequiresAdmin(t *testing.T) {
	a := &privilegedModeAgent{}
	for target, want := range map[string]bool{
		"default":           false,
		"bypassPermissions": true,
		"bypasspermissions": true,
		"yolo":              true, // alias resolved through NormalizeMode
	} {
		if got := modeRequiresAdmin(a, target); got != want {
			t.Errorf("modeRequiresAdmin(%q) = %v, want %v", target, got, want)
		}
	}
	// Without a normalizer, a target that is not a listed key fails closed.
	plain := &stubModelModeAgent{}
	if modeRequiresAdmin(plain, "default") {
		t.Error("listed non-privileged mode must not need admin")
	}
	if !modeRequiresAdmin(plain, "something-else") {
		t.Error("unlisted mode must need admin")
	}
}

func TestCmdMode_PrivilegedModeNeedsAdmin(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	agent := &privilegedModeAgent{}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	e.SetAdminFrom("boss")

	for _, target := range []string{"yolo", "bypassPermissions"} {
		p.clearSent()
		e.handleCommand(p, &Message{SessionKey: "test:c:u", UserID: "u", Platform: "test", ReplyCtx: "ctx"}, "/mode "+target)
		if sent := p.getSent(); len(sent) == 0 || !strings.Contains(sent[0], "requires admin") {
			t.Fatalf("/mode %s from non-admin: got %#v", target, sent)
		}
		if agent.GetMode() != "default" {
			t.Fatalf("/mode %s from non-admin changed the mode to %q", target, agent.GetMode())
		}
	}

	e.handleCommand(p, &Message{SessionKey: "test:c:u", UserID: "boss", Platform: "test", ReplyCtx: "ctx"}, "/mode yolo")
	if agent.GetMode() != "yolo" {
		t.Fatalf("admin /mode yolo: mode = %q", agent.GetMode())
	}
}

func TestModeCard_PrivilegedModeChecksClicker(t *testing.T) {
	agent := &privilegedModeAgent{}
	e := NewEngine("test", agent, nil, filepath.Join(t.TempDir(), "sessions.json"), LangEnglish)
	e.SetAdminFrom("boss")
	key := "test:group:user"

	for name, card := range map[string]*Card{
		"no clicker": e.handleCardNav("act:/mode yolo", key),
		"non-admin":  e.handleCardNavWithContext("act:/mode yolo", &Message{SessionKey: key, UserID: "user", Platform: "test"}),
	} {
		if card == nil || !strings.Contains(card.RenderText(), "requires admin") {
			t.Fatalf("%s: want admin-required card, got %#v", name, card)
		}
		if agent.GetMode() != "default" {
			t.Fatalf("%s: mode changed to %q", name, agent.GetMode())
		}
	}

	// A non-privileged mode stays open to everyone.
	if card := e.handleCardNav("act:/mode default", key); card == nil || strings.Contains(card.RenderText(), "requires admin") {
		t.Fatalf("act:/mode default must stay open, got %#v", card)
	}

	e.handleCardNavWithContext("act:/mode yolo", &Message{SessionKey: key, UserID: "boss", Platform: "test"})
	if agent.GetMode() != "yolo" {
		t.Fatalf("admin click: mode = %q", agent.GetMode())
	}
}

func TestProviderCard_ActionsCheckClicker(t *testing.T) {
	agent := &stubModelModeAgent{providers: []ProviderConfig{{Name: "a"}, {Name: "b"}}, active: "a"}
	e := NewEngine("test", agent, nil, filepath.Join(t.TempDir(), "sessions.json"), LangEnglish)
	e.SetAdminFrom("boss")
	key := "test:group:user"
	user := &Message{SessionKey: key, UserID: "user", Platform: "test"}

	if card := e.handleCardNavWithContext("nav:/provider", user); card == nil || strings.Contains(card.RenderText(), "requires admin") {
		t.Fatalf("viewing providers must stay open, got %#v", card)
	}
	for _, action := range []string{"act:/provider b", "act:/provider clear", "act:/provider/add-other", "act:/provider/add somepreset", "act:/provider/link g"} {
		for name, card := range map[string]*Card{
			"no clicker": e.handleCardNav(action, key),
			"non-admin":  e.handleCardNavWithContext(action, user),
		} {
			if card == nil || !strings.Contains(card.RenderText(), "requires admin") {
				t.Fatalf("%s %s: want admin-required card, got %#v", name, action, card)
			}
		}
	}
	if agent.active != "a" {
		t.Fatalf("active provider changed to %q", agent.active)
	}
	if e.getPendingProviderAdd(key) != nil {
		t.Fatal("non-admin click started a provider add")
	}

	e.handleCardNavWithContext("act:/provider b", &Message{SessionKey: key, UserID: "boss", Platform: "test"})
	if agent.active != "b" {
		t.Fatalf("admin click: active = %q, want b", agent.active)
	}
}

func TestPendingProviderAdd_OnlyAdminCompletesIt(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	agent := &stubProviderAgent{}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	e.SetAdminFrom("boss")
	key := "test:group:user"
	e.setPendingProviderAdd(key, &pendingProviderAddState{phase: "other"})

	if e.handlePendingProviderAdd(p, &Message{SessionKey: key, UserID: "mallory", ReplyCtx: "ctx"}, "evil sk-x https://evil.example", "") {
		t.Fatal("a non-admin message must not complete the add")
	}
	if len(agent.providers) != 0 || e.getPendingProviderAdd(key) == nil {
		t.Fatalf("non-admin changed the flow: providers=%v", agent.providers)
	}

	if !e.handlePendingProviderAdd(p, &Message{SessionKey: key, UserID: "boss", ReplyCtx: "ctx"}, "bad]name sk-x", "") {
		t.Fatal("admin input must be consumed")
	}
	if len(agent.providers) != 0 {
		t.Fatalf("invalid provider name was added: %v", agent.providers)
	}
	if sent := p.getSent(); len(sent) == 0 || !strings.Contains(sent[len(sent)-1], "invalid provider name") {
		t.Fatalf("want invalid-name reply, got %#v", sent)
	}
}

func TestCmdProviderAdd_RejectsInvalidNames(t *testing.T) {
	for _, args := range [][]string{
		{"bad]name", "sk-x"},
		{"bad\nname", "sk-x"},
		{`{"name":"x]\n[evil","api_key":"sk-x"}`},
		{`{"name":"has space","api_key":"sk-x"}`},
	} {
		p := &stubPlatformEngine{n: "test"}
		agent := &stubProviderAgent{}
		e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
		msg := &Message{SessionKey: "test:c:boss", UserID: "boss", ReplyCtx: "ctx"}
		e.cmdProviderAdd(p, msg, agent, args)
		if len(agent.providers) != 0 {
			t.Fatalf("%q: provider added: %v", args, agent.providers)
		}
		if sent := p.getSent(); len(sent) == 0 || !strings.Contains(sent[0], "invalid provider name") {
			t.Fatalf("%q: want invalid-name reply, got %#v", args, sent)
		}
	}

	p := &stubPlatformEngine{n: "test"}
	agent := &stubProviderAgent{}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	e.cmdProviderAdd(p, &Message{SessionKey: "test:c:boss", UserID: "boss", ReplyCtx: "ctx"}, agent, []string{"my-relay_2.0", "sk-x"})
	if len(agent.providers) != 1 || agent.providers[0].Name != "my-relay_2.0" {
		t.Fatalf("valid provider not added: %v", agent.providers)
	}
}

func TestValidateProviderName(t *testing.T) {
	for _, ok := range []string{"relay", "my-relay_2.0", "A", strings.Repeat("a", 64)} {
		if err := ValidateProviderName(ok); err != nil {
			t.Errorf("ValidateProviderName(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "a b", "a\nb", "a]b", "a[b", `a"b`, "a=b", "中文", strings.Repeat("a", 65)} {
		if ValidateProviderName(bad) == nil {
			t.Errorf("ValidateProviderName(%q) = nil, want error", bad)
		}
	}
}

func TestValidateModelName(t *testing.T) {
	for _, ok := range []string{"", "gpt-4.1", "claude-opus-4-5[1m]", "openrouter/x", "us.anthropic.claude:0", "model@2025"} {
		if err := ValidateModelName(ok); err != nil {
			t.Errorf("ValidateModelName(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"a&b", "a|b", "a<b", "a>b", "a^b", `a"b`, "a%PATH%", "a!b", "a b", "a\tb", "a\nb", "a\x00b"} {
		if ValidateModelName(bad) == nil {
			t.Errorf("ValidateModelName(%q) = nil, want error", bad)
		}
	}
}

func TestModelSwitch_RejectsShellMetacharacters(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	agent := &stubModelModeAgent{}
	e := NewEngine("test", agent, []Platform{p}, filepath.Join(t.TempDir(), "sessions.json"), LangEnglish)
	msg := &Message{SessionKey: "test:c:u", UserID: "u", Platform: "test", ReplyCtx: "ctx"}

	e.handleCommand(p, msg, "/model gpt&calc")
	if agent.model != "" {
		t.Fatalf("text /model accepted %q", agent.model)
	}
	if sent := p.getSent(); len(sent) == 0 || !strings.Contains(sent[0], "invalid model name") {
		t.Fatalf("want invalid-model reply, got %#v", sent)
	}

	card := e.handleCardNav("act:/model gpt|calc", "test:c:u")
	if agent.model != "" {
		t.Fatalf("model card accepted %q", agent.model)
	}
	if card == nil {
		t.Fatal("model card action must answer with a card")
	}

	e.handleCommand(p, msg, "/model us.anthropic.claude:0")
	if agent.model != "us.anthropic.claude:0" {
		t.Fatalf("valid model rejected: model = %q", agent.model)
	}
}

func TestCheckBatchArgs(t *testing.T) {
	for _, bin := range []string{`C:\npm\claude.cmd`, `C:\npm\CODEX.BAT`, "/usr/local/bin/x.cmd"} {
		for _, arg := range []string{"x&calc", "a|b", "a<b", "a>b", "a^b", "%PATH%", "!x!", "a\nb", "a\rb", "a\x00b"} {
			if CheckBatchArgs(bin, []string{"--model", arg}) == nil {
				t.Errorf("CheckBatchArgs(%q, %q) = nil, want error", bin, arg)
			}
		}
		// What lark-agent-bot itself passes must keep working.
		if err := CheckBatchArgs(bin, []string{"-c", `model="gpt-5"`, "--plugin-dir", `C:\Program Files (x86)\p`, "--allowedTools", "Bash(git status:*)", "a\tb"}); err != nil {
			t.Errorf("CheckBatchArgs(%q) on ordinary args = %v", bin, err)
		}
	}
	for _, bin := range []string{`C:\bin\claude.exe`, "/usr/bin/claude", "claude", "codex.cmd.exe"} {
		if err := CheckBatchArgs(bin, []string{"x&calc"}); err != nil {
			t.Errorf("CheckBatchArgs(%q) must only check batch files, got %v", bin, err)
		}
	}
}

func TestWorkspaceDirUnderBase(t *testing.T) {
	base := t.TempDir()
	for _, ok := range []string{"repo", "my project", "..repo", "a/b"} {
		if _, err := workspaceDirUnderBase(base, ok); err != nil {
			t.Errorf("workspaceDirUnderBase(%q) = %v, want ok", ok, err)
		}
	}
	for _, bad := range []string{"", ".", "..", "../x", "a/../../x", filepath.Join(base, "repo"), filepath.Dir(base)} {
		if _, err := workspaceDirUnderBase(base, bad); err == nil {
			t.Errorf("workspaceDirUnderBase(%q) = nil error, want refusal", bad)
		}
	}
}

func TestWorkspaceBind_StaysInsideBaseDir(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "base")
	for _, d := range []string{filepath.Join(base, "repo"), filepath.Join(root, "secret")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.SetMultiWorkspace(base, filepath.Join(t.TempDir(), "bindings.json"))
	channelKey := workspaceChannelKey("test", "ch1")
	msg := &Message{SessionKey: "test:ch1:user1", UserID: "user1", Platform: "test", ReplyCtx: "ctx"}

	for _, cmd := range []string{"/workspace bind ../secret", "/workspace bind " + filepath.Join(root, "secret"), "/workspace shared bind ../secret"} {
		e.handleCommand(p, msg, cmd)
		if b := e.workspaceBindings.Lookup("project:test", channelKey); b != nil {
			t.Fatalf("%s bound %q outside base_dir", cmd, b.Workspace)
		}
		if b := e.workspaceBindings.Lookup(sharedWorkspaceBindingsKey, channelKey); b != nil {
			t.Fatalf("%s bound shared %q outside base_dir", cmd, b.Workspace)
		}
	}

	e.handleCommand(p, msg, "/workspace bind repo")
	if b := e.workspaceBindings.Lookup("project:test", channelKey); b == nil || b.Workspace != normalizeWorkspacePath(filepath.Join(base, "repo")) {
		t.Fatalf("bind inside base_dir: got %+v", b)
	}
}

func TestWorkspaceInit_RepoNameCannotLeaveBaseDir(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.SetMultiWorkspace(t.TempDir(), filepath.Join(t.TempDir(), "bindings.json"))
	e.SetAdminFrom("user1")
	msg := &Message{SessionKey: "test:ch1:user1", UserID: "user1", Platform: "test", ReplyCtx: "ctx"}
	for _, url := range []string{"https://example.com/..", "https://example.com/org/"} {
		e.handleCommand(p, msg, "/workspace init "+url)
		if b := e.workspaceBindings.Lookup("project:test", workspaceChannelKey("test", "ch1")); b != nil {
			t.Fatalf("init %s bound %q", url, b.Workspace)
		}
	}
}

func TestWorkspaceInitFlow_CloneNeedsAdmin(t *testing.T) {
	e := newTestEngineWithMultiWorkspace(t, t.TempDir())
	e.SetAdminFrom("boss")
	p := &mockWorkspacePlatform{}
	channelKey := workspaceChannelKey(p.Name(), "C1")
	e.initFlowsMu.Lock()
	e.initFlows[channelKey] = &workspaceInitFlow{state: "awaiting_url", channelName: "chan"}
	e.initFlowsMu.Unlock()

	msg := &Message{SessionKey: "mock:C1:user1", UserID: "user1", Content: "https://example.com/org/repo.git"}
	if !e.handleWorkspaceInitFlow(p, msg, "chan") {
		t.Fatal("the URL must be consumed")
	}
	e.initFlowsMu.Lock()
	flow := e.initFlows[channelKey]
	e.initFlowsMu.Unlock()
	if flow == nil || flow.state != "awaiting_url" || flow.repoURL != "" {
		t.Fatalf("non-admin URL advanced the clone flow: %+v", flow)
	}
}

func TestGitClone_RefusesOptionLikeURL(t *testing.T) {
	err := gitClone("--upload-pack=touch /tmp/x", filepath.Join(t.TempDir(), "x"))
	if err == nil || !strings.Contains(err.Error(), "invalid repository URL") {
		t.Fatalf("gitClone with an option-like URL: err = %v", err)
	}
}

// --- permission responder ---

func newPendingPermissionEngine(t *testing.T, requester string) (*Engine, *stubPlatformEngine, *recordingAgentSession, *pendingPermission, string) {
	t.Helper()
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	key := "test:group"
	session := &recordingAgentSession{}
	pending := &pendingPermission{RequestID: "req-1", ToolName: "Bash", RequesterID: requester, Resolved: make(chan struct{})}
	e.interactiveMu.Lock()
	e.interactiveStates[key] = &interactiveState{agentSession: session, pending: pending}
	e.interactiveMu.Unlock()
	return e, p, session, pending, key
}

func TestPendingPermission_OtherUserCannotAnswer(t *testing.T) {
	e, p, session, pending, key := newPendingPermissionEngine(t, "alice")
	e.SetAdminFrom("boss")

	for _, answer := range []string{"allow", "allow all", "deny"} {
		for _, viaButton := range []bool{false, true} {
			msg := &Message{SessionKey: key, UserID: "mallory", Platform: "test", ReplyCtx: "ctx", IsPermissionResponse: viaButton}
			if !e.handlePendingPermission(p, msg, answer, "") {
				t.Fatalf("%q from another user must be consumed, not forwarded to the agent", answer)
			}
		}
	}
	if session.calls != 0 {
		t.Fatalf("another user's answer reached the agent (%d calls)", session.calls)
	}
	state, still := e.lookupPending(key)
	if still != pending || state.approveAll {
		t.Fatal("another user's answer resolved the request or enabled approve-all")
	}
	if sent := p.getSent(); len(sent) == 0 || sent[0] != e.i18n.T(MsgPermissionNotRequester) {
		t.Fatalf("want not-requester reply, got %#v", sent)
	}

	if !e.handlePendingPermission(p, &Message{SessionKey: key, UserID: "alice", ReplyCtx: "ctx"}, "allow", "") {
		t.Fatal("requester's answer must be handled")
	}
	if session.calls != 1 || session.lastResult.Behavior != "allow" {
		t.Fatalf("requester's allow: calls=%d result=%+v", session.calls, session.lastResult)
	}
}

func TestPendingPermission_AdminCanAnswerForOthers(t *testing.T) {
	e, p, session, _, key := newPendingPermissionEngine(t, "alice")
	e.SetAdminFrom("boss")
	if !e.handlePendingPermission(p, &Message{SessionKey: key, UserID: "BOSS", ReplyCtx: "ctx", IsPermissionResponse: true}, "deny", "") {
		t.Fatal("admin answer must be handled")
	}
	if session.calls != 1 || session.lastResult.Behavior != "deny" {
		t.Fatalf("admin deny: calls=%d result=%+v", session.calls, session.lastResult)
	}
}

func TestPendingPermission_UnknownRequesterKeepsOldBehavior(t *testing.T) {
	for _, requester := range []string{"", "cron", "timer", "heartbeat"} {
		e, p, session, _, key := newPendingPermissionEngine(t, requester)
		if !e.handlePendingPermission(p, &Message{SessionKey: key, UserID: "anyone", ReplyCtx: "ctx"}, "allow", "") {
			t.Fatalf("requester %q: answer must be handled", requester)
		}
		if session.calls != 1 {
			t.Fatalf("requester %q: anyone may answer when the requester is unknown", requester)
		}
	}
}

// The requester is the sender of the message whose turn raised the request.
func TestPendingPermission_RecordsTurnSender(t *testing.T) {
	env := newCUJEnv(t)
	env.agent.setNextSessionEvents([]Event{
		{Type: EventPermissionRequest, RequestID: "req-1", ToolName: "Bash", ToolInput: "make deploy"},
		{Type: EventResult, Content: "done", Done: true},
	}, 0)
	key := env.userSends("alice", "deploy please")

	var pending *pendingPermission
	env.waitFor("permission prompt", 2*time.Second, func() bool {
		_, pending = env.engine.lookupPending(key)
		return pending != nil
	})
	if pending.RequesterID != "alice" {
		t.Fatalf("RequesterID = %q, want alice", pending.RequesterID)
	}
	env.engine.handlePendingPermission(plat(env.plat), &Message{SessionKey: key, UserID: "mallory", ReplyCtx: "ctx"}, "allow", "")
	if _, still := env.engine.lookupPending(key); still != pending {
		t.Fatal("another user resolved the prompt")
	}
	env.engine.handlePendingPermission(plat(env.plat), &Message{SessionKey: key, UserID: "alice", ReplyCtx: "ctx"}, "allow", "")
	select {
	case <-pending.Resolved:
	case <-time.After(2 * time.Second):
		t.Fatal("requester's allow did not resolve the prompt")
	}
}
