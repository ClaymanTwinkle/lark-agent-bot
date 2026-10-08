package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/config"
	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// webAssetsAvailable and openWebBrowser are replaced in tests.
var (
	webAssetsAvailable = core.WebAssetsAvailable
	openWebBrowser     = openBrowser
	runWebBot          = func(configPath string, ready func() bool) {
		runBot(rootCLIOptions{configPath: configPath}, nil, nil, ready)
	}
)

const webUsage = `Usage: lark-agent-bot web [--config <path>] [--no-browser]

Enable the web admin and open it once the server is ready. If the bot is
not running, start it in this terminal (Ctrl+C stops it). If a running bot
needs the new settings, restart that bot, then run this command again.

On a new install this creates the default config first. A project whose
work_dir is still the default config's placeholder gets the current folder.

Flags:
  --config <path>    Config file (default: ./config.toml, then
                     ~/.lark-agent-bot/config.toml)
  -n, --no-browser   Configure and print URL/token only; do not start the bot
  -h, --help         Show this help
`

type webOptions struct {
	configPath string
	noBrowser  bool
}

func parseWebArgs(args []string) (webOptions, error) {
	var opts webOptions
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	fs.StringVar(&opts.configPath, "config", "", "config file")
	fs.BoolVar(&opts.noBrowser, "no-browser", false, "configure and print URL/token without starting the bot")
	fs.BoolVar(&opts.noBrowser, "n", false, "alias of --no-browser")
	err := parseCommandFlags(fs, args)
	return opts, err
}

func runWeb(args []string) {
	exitWith(webCommand(args))
}

// webCommand runs `lark-agent-bot web` and returns its exit code.
func webCommand(args []string) int {
	opts, err := parseWebArgs(args)
	if code, done := flagParseExit(err, webUsage); done {
		return code
	}

	configPath := resolveConfigPath(opts.configPath)
	if !webAssetsAvailable() {
		fmt.Fprintln(os.Stderr, cliText(configPath, core.MsgCLIWebNotBuilt))
		return 1
	}
	// The web admin is how a new install gets set up, so create the starter
	// config the first time, like a plain `lark-agent-bot` run does.
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := bootstrapConfig(configPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating config: %v\n", err)
			return 1
		}
		fmt.Println(cliText(configPath, core.MsgCLIWebConfigCreated, configPath))
	}

	// Use LoadPermissive so `lark-agent-bot web` works even before any platforms
	// are configured (e.g. during initial setup via the Web Admin UI).
	cfg, err := config.LoadPermissive(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		return 1
	}
	config.ConfigPath = configPath

	// lark-agent-bot cannot start, and so cannot serve the web admin, while a
	// project keeps the starter's placeholder work_dir. Feishu setup fills it
	// with the current folder; do the same here.
	if err := fillStarterWorkDirs(configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error setting work_dir: %v\n", err)
		return 1
	}

	if cfg.Management.Enabled == nil || !*cfg.Management.Enabled {
		fmt.Println(cliText(configPath, core.MsgCLIWebEnabling))
	}
	result, err := config.EnableWebAdmin(core.GenerateToken(16), core.GenerateToken(16))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error enabling web admin: %v\n", err)
		return 1
	}
	port, token := result.ManagementPort, result.ManagementToken
	if !result.AlreadyEnabled {
		fmt.Println(cliText(configPath, core.MsgCLIWebEnabled, port, configPath))
	}

	baseURL := fmt.Sprintf("http://localhost:%d", port)

	if opts.noBrowser {
		fmt.Printf("URL:   %s\n", baseURL)
		fmt.Printf("Token: %s\n", token)
		fmt.Println(cliText(configPath, core.MsgCLIWebManualStart, commandArg(configPath)))
		return 0
	}
	return openOrServeWeb(configPath, baseURL, token, port)
}

func openOrServeWeb(configPath, baseURL, token string, port int) int {
	ready, err := probeWebAdmin(baseURL, token)
	if err != nil {
		fmt.Fprintln(os.Stderr, cliText(configPath, core.MsgCLIWebUnavailable, baseURL, err))
		return 1
	}
	if ready {
		openReadyWeb(configPath, baseURL, token, port)
		return 0
	}
	if runningInstancePID(configPath) != 0 {
		fmt.Fprintln(os.Stderr, cliText(configPath, core.MsgCLIWebRunningNeedsRestart, configPath))
		return 1
	}
	fmt.Println(cliText(configPath, core.MsgCLIWebStarting, configPath))
	code := 1
	runWebBot(configPath, func() bool {
		if err := waitWebAdmin(baseURL, token, 5*time.Second); err != nil {
			fmt.Fprintln(os.Stderr, cliText(configPath, core.MsgCLIWebUnavailable, baseURL, err))
			return false
		}
		openReadyWeb(configPath, baseURL, token, port)
		code = 0
		return true
	})
	return code
}

func openReadyWeb(configPath, baseURL, token string, port int) {
	loginURL := fmt.Sprintf("%s/login?token=%s",
		baseURL, url.QueryEscape(token))

	fmt.Println(cliText(configPath, core.MsgCLIWebOpening, baseURL))
	if err := openWebBrowser(loginURL); err != nil {
		fmt.Println(cliText(configPath, core.MsgCLIWebOpenFailed, loginURL, port))
	}
}

// probeWebAdmin bypasses proxies and redirects so a local management token
// cannot be forwarded elsewhere. A listening but incompatible server is an
// error, not a reason to start a second bot on the same port.
func probeWebAdmin(baseURL, token string) (bool, error) {
	transport := &http.Transport{DialContext: (&net.Dialer{Timeout: time.Second}).DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport, Timeout: time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/status", nil)
	if err != nil {
		return false, fmt.Errorf("create web status request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		// Only failure to connect means there is no service. Read timeouts
		// and other HTTP failures must not launch another instance.
		if ue, ok := err.(*url.Error); ok {
			if ne, ok := ue.Err.(*net.OpError); ok && ne.Op == "dial" {
				return false, nil
			}
		}
		return false, fmt.Errorf("request web status: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("web status: HTTP %d", resp.StatusCode)
	}
	var status struct {
		OK   bool `json:"ok"`
		Data struct {
			Bridge struct {
				Enabled bool `json:"enabled"`
			} `json:"bridge"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&status); err != nil {
		return false, fmt.Errorf("decode web status: %w", err)
	}
	if !status.OK || !status.Data.Bridge.Enabled {
		return false, fmt.Errorf("web status: management or bridge is not ready")
	}
	return true, nil
}

func waitWebAdmin(baseURL, token string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		ready, err := probeWebAdmin(baseURL, token)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("web server did not start at %s", baseURL)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// fillStarterWorkDirs points every project that still has the starter
// placeholder work_dir at the current folder, and says so.
func fillStarterWorkDirs(configPath string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current folder: %w", err)
	}
	filled, err := config.FillStarterWorkDirs(cwd)
	for _, project := range filled {
		fmt.Println(cliText(configPath, core.MsgSetupWorkDirFilled, project, cwd))
	}
	return err
}

func openBrowser(rawURL string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", rawURL).Start()
	case "linux":
		if isWSL() {
			return exec.Command("cmd.exe", "/c", "start", rawURL).Start()
		}
		// On headless Linux, xdg-open is often unavailable.
		// Check early and return a clear error so the caller can print the URL.
		if _, err := exec.LookPath("xdg-open"); err != nil {
			return fmt.Errorf("xdg-open not found (headless server?): %w", err)
		}
		return exec.Command("xdg-open", rawURL).Start()
	case "windows":
		return exec.Command("cmd", "/c", "start", rawURL).Start()
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

func isWSL() bool {
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(data)), "microsoft")
}
