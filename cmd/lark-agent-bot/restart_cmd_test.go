package main

import (
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

func TestParseRestartArgs(t *testing.T) {
	t.Setenv("CC_SESSION", "feishu:oc_chat:ou_user")
	opts, err := parseRestartArgs(nil)
	if err != nil || opts.sessionKey != "feishu:oc_chat:ou_user" || opts.now || opts.all {
		t.Fatalf("defaults = %+v, %v", opts, err)
	}
	opts, err = parseRestartArgs([]string{"--now", "--project", "codex-bot", "--data-dir=D:/data", "--session-key", "k"})
	if err != nil || !opts.now || opts.project != "codex-bot" || opts.dataDir != "D:/data" || opts.sessionKey != "k" {
		t.Fatalf("parsed = %+v, %v", opts, err)
	}
	for _, args := range [][]string{{"--all", "--project", "x"}, {"--project"}, {"--force"}} {
		if _, err := parseRestartArgs(args); err == nil {
			t.Errorf("parseRestartArgs(%q) should fail", args)
		}
	}
	if _, err := parseRestartArgs([]string{"--help"}); !errors.Is(err, errRestartUsage) {
		t.Errorf("--help: %v", err)
	}
}

func TestRestartTargets(t *testing.T) {
	ownSocket := filepath.Join(t.TempDir(), "api.sock")
	if err := os.WriteFile(ownSocket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	peers := []core.RelayPeer{
		{Project: "claude-bot", Socket: ownSocket},
		{Project: "codex-bot", Socket: "codex.sock"},
		{Project: "codex-helper", Socket: "codex.sock"},
		{Project: "docs-bot", Socket: "docs.sock"},
	}
	opts := restartOptions{sessionKey: "feishu:oc_chat:ou_user"}

	got, err := restartTargets(opts, "claude-bot", ownSocket, peers)
	if err != nil || len(got) != 1 || got[0].socket != ownSocket || got[0].sessionKey != opts.sessionKey {
		t.Fatalf("own bot: %+v, %v", got, err)
	}

	opts.project = "codex-bot"
	got, err = restartTargets(opts, "claude-bot", ownSocket, peers)
	if err != nil || len(got) != 1 || got[0].socket != "codex.sock" || got[0].sessionKey != "" {
		t.Fatalf("--project codex-bot: %+v, %v (the peer must not get this bot's chat)", got, err)
	}
	opts.project = "ghost"
	if _, err := restartTargets(opts, "claude-bot", ownSocket, peers); err == nil {
		t.Fatal("--project for a bot that is not running should fail")
	}

	opts.project, opts.all = "", true
	got, err = restartTargets(opts, "claude-bot", ownSocket, peers)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, tg := range got {
		order = append(order, tg.label())
	}
	// One request per process, this bot last.
	if want := "codex-bot,docs-bot,claude-bot"; strings.Join(order, ",") != want {
		t.Fatalf("--all order = %v, want %s", order, want)
	}

	if _, err := restartTargets(restartOptions{}, "claude-bot", filepath.Join(t.TempDir(), "missing.sock"), nil); err == nil {
		t.Fatal("a bot that is not running should fail")
	}
}

func drainRestartCh(t *testing.T) core.RestartRequest {
	t.Helper()
	select {
	case req := <-core.RestartCh:
		return req
	case <-time.After(time.Second):
		t.Fatal("no restart request reached the process")
		return core.RestartRequest{}
	}
}

func TestRequestRestart_ThroughTheAPISocket(t *testing.T) {
	// Short path: unix socket paths are limited to ~100 bytes.
	dataDir, err := os.MkdirTemp("", "rst")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dataDir) })
	api, err := core.NewAPIServer(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	engine := core.NewEngine("claude-bot", nil, nil, "", core.LangEnglish)
	engine.SetUpgradeRestartWait(time.Hour, func() int { return 1 })
	api.RegisterEngine("claude-bot", engine)
	api.Start()
	t.Cleanup(api.Stop)

	msg, err := requestRestart(restartTarget{project: "claude-bot", socket: api.SocketPath(), sessionKey: "s1"}, false)
	if err != nil {
		t.Fatalf("requestRestart: %v", err)
	}
	if !strings.Contains(msg, "1 task(s) in progress") || !strings.Contains(msg, "60 min") {
		t.Fatalf("message = %q, want the wait described", msg)
	}
	if req := drainRestartCh(t); !req.WaitIdle || req.SessionKey != "s1" {
		t.Fatalf("restart request = %+v", req)
	}
}

func TestRequestRestart_OlderBotWithoutTheRoute(t *testing.T) {
	dir, err := os.MkdirTemp("", "rst")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "api.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.NewServeMux()}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	_, err = requestRestart(restartTarget{project: "codex-bot", socket: socket}, false)
	if err == nil || !strings.Contains(err.Error(), "send /restart in its chat") {
		t.Fatalf("error = %v, want a hint for bots without the restart API", err)
	}
}
