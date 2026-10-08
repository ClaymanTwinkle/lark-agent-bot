package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/ClaymanTwinkle/lark-agent-bot/config"
	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// webAssetsAvailable and openWebBrowser are replaced in tests.
var (
	webAssetsAvailable = core.WebAssetsAvailable
	openWebBrowser     = openBrowser
)

const webUsage = `Usage: lark-agent-bot web [--config <path>] [--no-browser]

Turn on the web admin in the config file if it is off, then open it in a
browser. The running bot serves the web admin: restart lark-agent-bot after
it is turned on.

On a new install this creates the default config first. A project whose
work_dir is still the default config's placeholder gets the current folder.

Flags:
  --config <path>    Config file (default: ./config.toml, then
                     ~/.lark-agent-bot/config.toml)
  -n, --no-browser   Print the URL and token instead of opening a browser
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
	fs.BoolVar(&opts.noBrowser, "no-browser", false, "print the URL and token instead of opening a browser")
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
	created := false
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := bootstrapConfig(configPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating config: %v\n", err)
			return 1
		}
		created = true
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

	mgmtEnabled := cfg.Management.Enabled != nil && *cfg.Management.Enabled
	port := cfg.Management.Port
	if port == 0 {
		port = 9820
	}
	token := cfg.Management.Token

	if !mgmtEnabled {
		fmt.Println(cliText(configPath, core.MsgCLIWebEnabling))

		mgmtToken := core.GenerateToken(16)
		bridgeToken := core.GenerateToken(16)
		result, err := config.EnableWebAdmin(mgmtToken, bridgeToken)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error enabling web admin: %v\n", err)
			return 1
		}
		port = result.ManagementPort
		token = result.ManagementToken
		fmt.Println(cliText(configPath, core.MsgCLIWebEnabled, port, configPath))
		if created {
			fmt.Println(cliText(configPath, core.MsgCLIWebStartBot))
		} else {
			fmt.Println(cliText(configPath, core.MsgCLIWebRestartBot))
		}
	}

	baseURL := fmt.Sprintf("http://localhost:%d", port)

	if opts.noBrowser {
		fmt.Printf("URL:   %s\n", baseURL)
		fmt.Printf("Token: %s\n", token)
		return 0
	}

	loginURL := fmt.Sprintf("%s/login?token=%s",
		baseURL, url.QueryEscape(token))

	fmt.Println(cliText(configPath, core.MsgCLIWebOpening, baseURL))
	if err := openWebBrowser(loginURL); err != nil {
		fmt.Println(cliText(configPath, core.MsgCLIWebOpenFailed, loginURL, port))
	}
	return 0
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
