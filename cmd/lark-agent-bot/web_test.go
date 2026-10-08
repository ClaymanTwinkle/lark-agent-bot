package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/config"
	"github.com/ClaymanTwinkle/lark-agent-bot/core"
	"github.com/gorilla/websocket"
)

// Regression: web used to open the browser even when the port belonged to
// another service or a bot using a different management token.
func TestWebCommand_DoesNotOpenUnusableServer(t *testing.T) {
	isolateHome(t)
	stubWeb(t, true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	path, _ := writeWebTestConfig(t)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(f, "\n[management]\nenabled = true\nport = %s\ntoken = \"test-token\"\n", u.Port())
	_ = f.Close()
	if code, _, _ := runWebCommand(t, "--config", path); code == 0 {
		t.Fatal("web reported success for an unusable server")
	}
}

const webTestConfig = `language = "en"

[[projects]]
name = "demo"

[projects.agent]
type = "claudecode"

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "cli_test"
app_secret = "secret"
`

// stubWeb makes the web admin look built in or not, and fails the test if
// the command tries to open a browser.
func stubWeb(t *testing.T, available bool) {
	t.Helper()
	oldAvailable, oldOpen := webAssetsAvailable, openWebBrowser
	oldRun := runWebBot
	webAssetsAvailable = func() bool { return available }
	openWebBrowser = func(string) error {
		t.Error("web opened a browser")
		return nil
	}
	runWebBot = func(string, func() bool) { t.Error("web unexpectedly started a bot") }
	t.Cleanup(func() { webAssetsAvailable, openWebBrowser, runWebBot = oldAvailable, oldOpen, oldRun })
}

func freeWebPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func TestWebCommand_ReusesReadyServer(t *testing.T) {
	isolateHome(t)
	stubWeb(t, true)
	port := freeWebPort(t)
	m := core.NewManagementServer(port, "existing-token", nil)
	m.SetBridgeServer(core.NewBridgeServer(9810, "bridge-token", "/bridge/ws", nil))
	m.Start()
	defer m.Stop()
	base := fmt.Sprintf("http://localhost:%d", port)
	if err := waitWebAdmin(base, "existing-token", time.Second); err != nil {
		t.Fatal(err)
	}
	path, _ := writeWebTestConfig(t)
	data := webTestConfig + fmt.Sprintf("\n[management]\nenabled = true\nport = %d\ntoken = \"existing-token\"\n[bridge]\nenabled = true\ntoken = \"bridge-token\"\n", port)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	opened := false
	openWebBrowser = func(raw string) error {
		opened = true
		if raw != base+"/login?token=existing-token" {
			t.Errorf("login URL = %q", raw)
		}
		return nil
	}
	if code, _, stderr := runWebCommand(t, "--config", path); code != 0 || !opened {
		t.Fatalf("code=%d opened=%v stderr=%s", code, opened, stderr)
	}
}

func TestWebCommand_RunningBotNeedsRestart(t *testing.T) {
	isolateHome(t)
	stubWeb(t, true)
	path, _ := writeWebTestConfig(t)
	data := webTestConfig + fmt.Sprintf("\n[management]\nenabled = true\nport = %d\ntoken = \"test-token\"\n", freeWebPort(t))
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	lock, err := AcquireInstanceLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	code, _, stderr := runWebCommand(t, "--config", path)
	if code == 0 || !strings.Contains(stderr, "Restart that bot") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestWebCommand_EnablesBridgeForExistingManagement(t *testing.T) {
	isolateHome(t)
	stubWeb(t, true)
	path, _ := writeWebTestConfig(t)
	if err := os.WriteFile(path, []byte(webTestConfig+"\n[management]\nenabled = true\ntoken = \"keep-token\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runWebCommand(t, "--config", path, "--no-browser"); code != 0 {
		t.Fatal(stderr)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Management.Token != "keep-token" || cfg.Bridge.Enabled == nil || !*cfg.Bridge.Enabled || cfg.Bridge.Token == "" {
		t.Fatal("web did not preserve management auth and enable bridge auth")
	}
}

// Exercise the actual startup, management API and shutdown in a subprocess:
// runBot owns process-wide registries, signal handlers and fatal startup exits.
func TestWebCommand_ForegroundStartsBeforeOpening(t *testing.T) {
	if os.Getenv("LARK_WEB_STARTUP_TEST") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWebCommand_ForegroundStartsBeforeOpening$", "-test.v")
		cmd.Env = append(os.Environ(), "LARK_WEB_STARTUP_TEST=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("web startup: %v\n%s", err, out)
		}
		return
	}
	isolateHome(t)
	actualRun := runWebBot
	stubWeb(t, true)
	if !core.WebAssetsAvailable() {
		core.RegisterWebAssets(fstest.MapFS{"index.html": {Data: []byte("<!doctype html><div id=\"root\"></div>")}})
	}
	core.RegisterAgent("web-startup-test", func(opts map[string]any) (core.Agent, error) {
		return &stubMainAgent{workDir: opts["work_dir"].(string)}, nil
	})
	port, bridgePort := freeWebPort(t), freeWebPort(t)
	path := filepath.Join(t.TempDir(), "web.toml")
	data := fmt.Sprintf(`language = "en"
data_dir = %q
[[projects]]
name = "web-test"
[projects.agent]
type = "web-startup-test"
[projects.agent.options]
work_dir = %q
[management]
port = %d
[bridge]
port = %d
`, filepath.ToSlash(t.TempDir()), filepath.ToSlash(t.TempDir()), port, bridgePort)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	opened, started := false, false
	openWebBrowser = func(raw string) (resultErr error) {
		defer func() {
			if resultErr != nil {
				t.Errorf("web page/bridge verification: %v", resultErr)
			}
		}()
		opened = true
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		ready, err := probeWebAdmin("http://"+u.Host, u.Query().Get("token"))
		if err != nil || !ready {
			t.Errorf("browser opened before readiness: %v", err)
		}
		client := &http.Client{Timeout: time.Second, Transport: &http.Transport{}}
		defer client.CloseIdleConnections()
		resp, err := client.Get("http://" + u.Host + "/login")
		if err != nil {
			return err
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `id="root"`) {
			t.Errorf("login page unavailable: status=%d err=%v", resp.StatusCode, err)
		}
		cfg, err := config.Load(path)
		if err != nil {
			return err
		}
		dialer := websocket.Dialer{HandshakeTimeout: time.Second}
		conn, _, err := dialer.Dial("ws://"+u.Host+"/bridge/ws?token="+url.QueryEscape(cfg.Bridge.Token), http.Header{"Origin": {"http://" + u.Host}})
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()
		if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			return err
		}
		if err := conn.WriteJSON(map[string]any{"type": "register", "platform": "web", "capabilities": []string{"text"}}); err != nil {
			return err
		}
		var ack struct {
			Type string `json:"type"`
			OK   bool   `json:"ok"`
		}
		if err := conn.ReadJSON(&ack); err != nil {
			return err
		}
		if ack.Type != "register_ack" || !ack.OK {
			t.Errorf("bridge registration failed: %+v", ack)
		}
		return nil
	}
	runWebBot = func(gotPath string, ready func() bool) {
		started = true
		if opened {
			t.Error("browser opened before startup")
		}
		if gotPath != path {
			t.Errorf("config = %q", gotPath)
		}
		actualRun(gotPath, func() bool {
			if !ready() {
				t.Error("web never became ready")
			}
			return false // Exercise graceful shutdown after checking readiness.
		})
	}
	if code := webCommand([]string{"--config", path}); code != 0 || !started || !opened {
		t.Fatalf("code=%d started=%v opened=%v", code, started, opened)
	}
	if ready, err := probeWebAdmin(fmt.Sprintf("http://localhost:%d", port), "unused"); err != nil || ready {
		t.Fatalf("web listener remained after shutdown: ready=%v err=%v", ready, err)
	}
}

func TestProbeWebAdmin_RejectsRedirectAndInvalidStatus(t *testing.T) {
	for _, body := range []string{"not JSON", `{"ok":true,"data":{}}`, `{"ok":false}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Error("missing management auth")
			}
			_, _ = fmt.Fprint(w, body)
		}))
		ready, err := probeWebAdmin(srv.URL, "test-token")
		srv.Close()
		if ready || err == nil {
			t.Errorf("accepted invalid status %q", body)
		}
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("followed redirect with management credentials")
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer source.Close()
	if ready, err := probeWebAdmin(source.URL, "test-token"); ready || err == nil {
		t.Fatal("accepted redirect")
	}
}

func writeWebTestConfig(t *testing.T) (path string, content []byte) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "custom.toml")
	content = []byte(webTestConfig)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, content
}

// runWebCommand runs webCommand and returns its exit code and output.
func runWebCommand(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	stdout = captureStdout(t, func() {
		stderr = captureStderr(t, func() { code = webCommand(args) })
	})
	return code, stdout, stderr
}

func TestParseWebArgs(t *testing.T) {
	opts, err := parseWebArgs([]string{"--config", "a.toml", "-n"})
	if err != nil || opts.configPath != "a.toml" || !opts.noBrowser {
		t.Fatalf("parseWebArgs = %+v, %v", opts, err)
	}
	opts, err = parseWebArgs([]string{"--no-browser"})
	if err != nil || !opts.noBrowser {
		t.Fatalf("parseWebArgs(--no-browser) = %+v, %v", opts, err)
	}
	if _, err := parseWebArgs([]string{"--bogus"}); err == nil {
		t.Fatal("parseWebArgs(--bogus) succeeded")
	}
}

// Regression: `web --help` turned the web admin on, rewrote the config and
// opened a browser.
func TestWebCommand_HelpDoesNotTouchConfig(t *testing.T) {
	isolateHome(t)
	stubWeb(t, true)
	path, before := writeWebTestConfig(t)

	for _, args := range [][]string{{"--help"}, {"-h"}, {"--config", path, "--no-browser", "--help"}} {
		code, stdout, stderr := runWebCommand(t, args...)
		if code != 0 {
			t.Errorf("web %q exit code = %d, stderr %q", args, code, stderr)
		}
		if !strings.Contains(stdout, "Usage: lark-agent-bot web") {
			t.Errorf("web %q printed %q, want the usage", args, stdout)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("web --help changed the config:\n%s", after)
	}
}

func TestWebCommand_UsesConfigFlag(t *testing.T) {
	home := isolateHome(t)
	stubWeb(t, true)
	path, _ := writeWebTestConfig(t)

	code, stdout, stderr := runWebCommand(t, "--config", path, "--no-browser")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr %q", code, stderr)
	}
	cfg, err := config.LoadPermissive(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Management.Enabled == nil || !*cfg.Management.Enabled {
		t.Fatalf("web did not enable the web admin in %s", path)
	}
	if !strings.Contains(stdout, "Token: "+cfg.Management.Token) {
		t.Errorf("stdout %q lacks the token", stdout)
	}
	if _, err := os.Stat(filepath.Join(home, ".lark-agent-bot", "config.toml")); !os.IsNotExist(err) {
		t.Errorf("web touched the default config: %v", err)
	}
}

func TestWebCommand_NotBuiltLeavesConfigAlone(t *testing.T) {
	isolateHome(t)
	stubWeb(t, false)
	path, before := writeWebTestConfig(t)

	code, _, stderr := runWebCommand(t, "--config", path)
	if code == 0 {
		t.Fatal("web succeeded without the web admin in the build")
	}
	if want := core.NewI18n(core.LangEnglish).T(core.MsgCLIWebNotBuilt); !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("web changed the config:\n%s", after)
	}
}

// Regression: on a new install `web` stopped at "Config file not found", and
// the config a plain `lark-agent-bot` run creates kept a placeholder work_dir
// that keeps the bot, and so the web admin, from starting.
func TestWebCommand_FirstRunCreatesStartableConfig(t *testing.T) {
	home := isolateHome(t)
	stubWeb(t, true)
	t.Chdir(t.TempDir())
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runWebCommand(t, "--no-browser")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr %q", code, stderr)
	}
	path := filepath.Join(home, ".lark-agent-bot", "config.toml")
	// config.Load is what lark-agent-bot itself starts with.
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("lark-agent-bot cannot load the config web created: %v", err)
	}
	if cfg.Management.Enabled == nil || !*cfg.Management.Enabled {
		t.Error("web did not enable the web admin in the new config")
	}
	if got := cfg.Projects[0].Agent.Options["work_dir"]; got != cwd {
		t.Errorf("work_dir = %v, want the current folder %s", got, cwd)
	}
	for _, want := range []string{path, cwd, "Token: " + cfg.Management.Token} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout %q lacks %q", stdout, want)
		}
	}
	// The starter config's comments are its only guide to the file.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# lark-agent-bot configuration") {
		t.Errorf("the starter config lost its comments:\n%s", data)
	}
}

func TestWebCommand_FillsOnlyStarterWorkDir(t *testing.T) {
	isolateHome(t)
	stubWeb(t, true)
	t.Chdir(t.TempDir())
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	ownDir := t.TempDir()
	project := func(name, workDir string) string {
		return fmt.Sprintf(`
[[projects]]
name = %q

[projects.agent]
type = "claudecode"

[projects.agent.options]
work_dir = %q

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "cli_%s"
app_secret = "secret"
`, name, workDir, name)
	}
	path := filepath.Join(t.TempDir(), "custom.toml")
	content := "language = \"en\"\n" + project("starter", config.StarterWorkDir) + project("own", ownDir)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runWebCommand(t, "--config", path, "--no-browser")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr %q", code, stderr)
	}
	cfg, err := config.LoadPermissive(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"starter": cwd, "own": ownDir}
	for _, proj := range cfg.Projects {
		if got := proj.Agent.Options["work_dir"]; got != want[proj.Name] {
			t.Errorf("project %s: work_dir = %v, want %s", proj.Name, got, want[proj.Name])
		}
	}
	if msg := core.NewI18n(core.LangEnglish).Tf(core.MsgSetupWorkDirFilled, "starter", cwd); !strings.Contains(stdout, msg) {
		t.Errorf("stdout %q lacks %q", stdout, msg)
	}
}

func TestWebCommand_NotBuiltCreatesNoConfig(t *testing.T) {
	home := isolateHome(t)
	stubWeb(t, false)
	t.Chdir(t.TempDir())

	if code, _, _ := runWebCommand(t, "--no-browser"); code == 0 {
		t.Fatal("web succeeded without the web admin in the build")
	}
	if _, err := os.Stat(filepath.Join(home, ".lark-agent-bot", "config.toml")); !os.IsNotExist(err) {
		t.Errorf("web created a config it cannot use: %v", err)
	}
}
