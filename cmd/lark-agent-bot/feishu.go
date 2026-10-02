package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/config"
	"github.com/ClaymanTwinkle/lark-agent-bot/core"
	qrterminal "github.com/mdp/qrterminal/v3"
	"rsc.io/qr"
)

const (
	feishuSetupModeAuto = "auto"
	feishuSetupModeNew  = "new"
	feishuSetupModeBind = "bind"

	accountsFeishuBaseURL = "https://accounts.feishu.cn"
	accountsLarkBaseURL   = "https://accounts.larksuite.com"
	openFeishuBaseURL     = "https://open.feishu.cn"
	openLarkBaseURL       = "https://open.larksuite.com"
)

type registrationInitResponse struct {
	SupportedAuthMethods []string `json:"supported_auth_methods"`
	Error                string   `json:"error"`
	ErrorDescription     string   `json:"error_description"`
}

type registrationBeginResponse struct {
	DeviceCode              string `json:"device_code"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	Interval                int    `json:"interval"`
	ExpireIn                int    `json:"expire_in"`
	Error                   string `json:"error"`
	ErrorDescription        string `json:"error_description"`
}

type registrationPollUserInfo struct {
	OpenID      string `json:"open_id"`
	TenantBrand string `json:"tenant_brand"`
}

type registrationPollResponse struct {
	ClientID         string                   `json:"client_id"`
	ClientSecret     string                   `json:"client_secret"`
	UserInfo         registrationPollUserInfo `json:"user_info"`
	Error            string                   `json:"error"`
	ErrorDescription string                   `json:"error_description"`
}

type tenantTokenResponse struct {
	Code              int    `json:"code"`
	Msg               string `json:"msg"`
	TenantAccessToken string `json:"tenant_access_token"`
}

type registrationClient struct {
	baseURL string
	http    *http.Client
	debug   bool
}

type registrationFlowOptions struct {
	TimeoutSeconds int
	QRImagePath    string
	Debug          bool
	Addons         *setupAddons
	Name           string
	Description    string
	Avatar         string
}

type registrationFlowResult struct {
	AppID       string
	AppSecret   string
	OwnerOpenID string
	Platform    string // feishu or lark
}

func runFeishu(args []string) {
	if len(args) == 0 {
		printFeishuUsage()
		return
	}

	switch args[0] {
	case "setup":
		runFeishuSetup(args[1:], feishuSetupModeAuto)
	case "new", "create":
		runFeishuSetup(args[1:], feishuSetupModeNew)
	case "bind", "link":
		runFeishuSetup(args[1:], feishuSetupModeBind)
	case "check":
		runFeishuSetup(args[1:], "check")
	case "help", "--help", "-h", "-help":
		printFeishuUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown feishu subcommand: %s\n\n", args[0])
		printFeishuUsage()
		os.Exit(1)
	}
}

// feishuSubcommandName is the subcommand the user types for a setup mode.
func feishuSubcommandName(mode string) string {
	if mode == feishuSetupModeAuto {
		return "setup"
	}
	return mode
}

func runFeishuSetup(args []string, requestedMode string) {
	// Flag descriptions match feishuUsage, which -h / --help prints.
	fs := flag.NewFlagSet("feishu "+feishuSubcommandName(requestedMode), flag.ContinueOnError)
	configFile := fs.String("config", "", "Path to config file")
	project := fs.String("project", "", "Target project (auto-created if missing)")
	platformIndex := fs.Int("platform-index", 0, "1-based Feishu/Lark platform index in the project (default: first)")
	platformType := fs.String("platform-type", "", "Force platform type: feishu or lark")
	app := fs.String("app", "", "Existing credentials as app_id:app_secret")
	appID := fs.String("app-id", "", "Existing app_id")
	appSecret := fs.String("app-secret", "", "Existing app_secret")
	timeout := fs.Int("timeout", 600, "QR onboarding timeout in seconds")
	qrImage := fs.String("qr-image", "", "Save QR code as PNG image file")
	setAllowFromEmpty := fs.Bool("set-allow-from-empty", false, "Merge owner open_id into allow_from when available")
	debug := fs.Bool("debug", false, "Print onboarding debug logs")
	templatePath := fs.String("template", "", "Custom registration addons JSON (default: built-in shared template)")
	name := fs.String("name", "", "App name (default: project name)")
	description := fs.String("description", "", "App description")
	avatar := fs.String("avatar", "", "App avatar HTTPS URL")
	agentType := fs.String("agent", "", "Agent type for a new project")
	workDirFlag := fs.String("work-dir", "", "Working directory for a new project")
	model := fs.String("model", "", "Model for a new project")
	mode := fs.String("mode", "", "Agent permission mode for a new project")
	display := fs.String("display", "quiet", "Display for a new project: quiet, compact, full")
	err := parseCommandFlags(fs, args)
	if code, done := flagParseExit(err, feishuUsage); done {
		exitWith(code)
		return
	}

	initConfigPath(*configFile)

	effectiveMode, resolvedAppID, resolvedAppSecret, err := resolveFeishuSetupInputs(
		requestedMode,
		*app,
		*appID,
		*appSecret,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	targetProject, err := resolveTargetProject(*project)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	normalizedType, err := normalizeFeishuPlatformType(*platformType)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	template, err := loadSetupTemplate(*templatePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if requestedMode == "check" {
		if err := checkConfiguredSetup(targetProject, *platformIndex, template); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	// Both modes can create a project or fill in the starter one.
	if !slices.Contains([]string{"quiet", "compact", "full"}, *display) {
		fmt.Fprintln(os.Stderr, "display must be quiet, compact or full")
		os.Exit(1)
	}
	if *agentType != "" && !slices.Contains(core.ListRegisteredAgents(), *agentType) {
		fmt.Fprintf(os.Stderr, "unknown agent %q\n", *agentType)
		os.Exit(1)
	}
	if effectiveMode == feishuSetupModeNew {
		if err := preflightNewSetup(targetProject, *platformIndex); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	finalPlatformType := normalizedType
	var ownerOpenID string

	switch effectiveMode {
	case feishuSetupModeBind:
		detectedType, err := validateAppCredentials(resolvedAppID, resolvedAppSecret, normalizedType)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: app_id/app_secret validation failed: %v\n", err)
			os.Exit(1)
		}
		if finalPlatformType == "" {
			finalPlatformType = detectedType
		}
		fmt.Printf("Credentials verified for app_id %s.\n", resolvedAppID)

	case feishuSetupModeNew:
		if *name == "" {
			*name = targetProject
		}
		fmt.Println(setupText(core.MsgSetupPrepared, len(template.Scopes.Tenant), len(template.Scopes.User)))
		result, err := runRegistrationFlow(registrationFlowOptions{
			TimeoutSeconds: *timeout,
			QRImagePath:    *qrImage,
			Debug:          *debug,
			Addons:         template,
			Name:           *name,
			Description:    *description,
			Avatar:         *avatar,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: onboarding failed: %v\n", err)
			fmt.Fprintln(os.Stderr, "Tip: you can bind an existing bot with `lark-agent-bot feishu bind --app app_id:app_secret`")
			os.Exit(1)
		}
		resolvedAppID = result.AppID
		resolvedAppSecret = result.AppSecret
		ownerOpenID = result.OwnerOpenID
		if finalPlatformType == "" {
			finalPlatformType = result.Platform
		}

	default:
		fmt.Fprintf(os.Stderr, "Error: unsupported mode %q\n", effectiveMode)
		os.Exit(1)
	}

	provisionType := finalPlatformType
	if provisionType == "" {
		provisionType = "feishu"
	}
	workDir, _ := os.Getwd()
	if *workDirFlag != "" {
		workDir = *workDirFlag
	}
	if err := ensureSetupConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	provisionResult, err := config.EnsureProjectWithFeishuPlatform(config.EnsureProjectWithFeishuOptions{
		ProjectName:       targetProject,
		PlatformType:      provisionType,
		WorkDir:           workDir,
		AgentType:         *agentType,
		FallbackAgentType: defaultSetupAgent(exec.LookPath),
		Model:             *model,
		Mode:              *mode,
		DisplayMode:       *display,
		TakeOverStarter:   true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: prepare project failed: %v\n", err)
		os.Exit(1)
	}
	switch {
	case provisionResult.Created:
		fmt.Println(setupText(core.MsgSetupProjectCreated, targetProject, provisionResult.AgentType))
	case provisionResult.FromStarter:
		fmt.Println(setupText(core.MsgSetupStarterFilled, targetProject, provisionResult.AgentType))
	case provisionResult.AddedPlatform:
		fmt.Println(setupText(core.MsgSetupPlatformAdded, targetProject))
	}
	if provisionResult.FilledWorkDir {
		fmt.Println(setupText(core.MsgSetupWorkDirFilled, targetProject, workDir))
	}

	saveResult, err := config.SaveFeishuPlatformCredentials(config.FeishuCredentialUpdateOptions{
		ProjectName:   targetProject,
		PlatformIndex: *platformIndex,
		PlatformType:  finalPlatformType,
		AppID:         resolvedAppID,
		AppSecret:     resolvedAppSecret,
		// New-app owner IDs are resolved in the new app's own identity namespace
		// after saving credentials, not trusted from registration's user_info.
		OwnerOpenID:       "",
		SetAllowFromEmpty: false,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: update config failed: %v\n", err)
		os.Exit(1)
	}
	if effectiveMode == feishuSetupModeNew {
		check, checkErr := inspectSetup(resolvedAppID, resolvedAppSecret, saveResult.PlatformType, template)
		if check != nil && check.OwnerOpenID != "" {
			ownerOpenID = check.OwnerOpenID
			saveResult, err = config.SaveFeishuPlatformCredentials(config.FeishuCredentialUpdateOptions{
				ProjectName: targetProject, PlatformIndex: *platformIndex,
				AppID: resolvedAppID, AppSecret: resolvedAppSecret,
				OwnerOpenID: ownerOpenID, SetAllowFromEmpty: *setAllowFromEmpty || provisionResult.Created || provisionResult.FromStarter,
				SetAdminFromEmpty: true,
			})
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
		if err := reportSetupCheck(check, checkErr); err != nil {
			fmt.Fprintln(os.Stderr, setupText(core.MsgSetupIncomplete, err))
			os.Exit(1)
		}
	}

	fmt.Printf("✅ Feishu/Lark bot configured for project %q\n", saveResult.ProjectName)
	fmt.Printf("   Platform: %s (projects[%d].platforms[%d])\n", saveResult.PlatformType, saveResult.ProjectIndex, saveResult.PlatformAbsIndex)
	fmt.Printf("   App ID:   %s\n", resolvedAppID)
	if saveResult.AllowFrom != "" {
		fmt.Printf("   allow_from: %s\n", saveResult.AllowFrom)
	}
	fmt.Println()

	if ownerOpenID != "" && effectiveMode != feishuSetupModeNew {
		printAllowFromGuidance(resolvedAppID, resolvedAppSecret, ownerOpenID, saveResult)
	}

	printBotMenuGuidance(saveResult.PlatformType)

	fmt.Println(setupText(core.MsgSetupMenuNotice))
}

// setupAgentCLIs lists, in order of preference, the agents a first project
// created by setup defaults to when their CLI is installed.
var setupAgentCLIs = []struct{ agent, bin string }{
	{"claudecode", "claude"},
	{"codex", "codex"},
}

// defaultSetupAgent picks the agent for a first project created without
// --agent: the first installed CLI from setupAgentCLIs, else Claude Code.
func defaultSetupAgent(lookPath func(string) (string, error)) string {
	registered := core.ListRegisteredAgents()
	for _, c := range setupAgentCLIs {
		if !slices.Contains(registered, c.agent) {
			continue
		}
		if _, err := lookPath(c.bin); err == nil {
			return c.agent
		}
	}
	return "claudecode"
}

func printAllowFromGuidance(appID, appSecret, ownerOpenID string, result *config.FeishuCredentialUpdateResult) {
	botOpenID := fetchBotOpenIDForSetup(appID, appSecret, result.PlatformType)

	if botOpenID != "" && ownerOpenID == botOpenID {
		fmt.Println("⚠️  注册返回的 open_id 是机器人自身的 ID，不是你的用户 ID。")
		fmt.Println("   飞书 open_id 是应用级别的标识符，注册流程返回的 ID 无法直接用于 allow_from。")
		fmt.Println()
	}

	if result.AllowFrom == "" {
		fmt.Println("💡 allow_from 未设置（所有用户均可使用）。如需限制访问，请：")
		fmt.Println("   1. 向机器人发送 /whoami 获取你的 User ID")
		fmt.Println("   2. 在 config.toml 中设置: allow_from = \"<你的User ID>\"")
		fmt.Println("   同样，admin_from 也可用 /whoami 获取的 ID 来设置。")
		fmt.Println()
	}
}

func fetchBotOpenIDForSetup(appID, appSecret, platformType string) string {
	base := openFeishuBaseURL
	if platformType == "lark" {
		base = openLarkBaseURL
	}
	body, _ := json.Marshal(map[string]string{
		"app_id":     appID,
		"app_secret": appSecret,
	})
	req, err := http.NewRequest(http.MethodPost, base+"/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(body))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var tokenResp tenantTokenResponse
	if err := json.Unmarshal(data, &tokenResp); err != nil || tokenResp.TenantAccessToken == "" {
		return ""
	}

	botReq, err := http.NewRequest(http.MethodGet, base+"/open-apis/bot/v3/info", nil)
	if err != nil {
		return ""
	}
	botReq.Header.Set("Authorization", "Bearer "+tokenResp.TenantAccessToken)

	botResp, err := client.Do(botReq)
	if err != nil {
		return ""
	}
	defer botResp.Body.Close()
	botData, _ := io.ReadAll(io.LimitReader(botResp.Body, 1<<20))

	var result struct {
		Code int `json:"code"`
		Bot  struct {
			OpenID string `json:"open_id"`
		} `json:"bot"`
	}
	if err := json.Unmarshal(botData, &result); err != nil || result.Code != 0 {
		return ""
	}
	return result.Bot.OpenID
}

func printBotMenuGuidance(platformType string) {
	base := "https://open.feishu.cn"
	if platformType == "lark" {
		base = "https://open.larksuite.com"
	}
	fmt.Println(setupText(core.MsgSetupMenuGuidance, base+"/app"))
}

func printFeishuUsage() {
	fmt.Print(feishuUsage)
}

const feishuUsage = `Usage: lark-agent-bot feishu <command> [options]

Commands:
  setup   Unified entry: no credentials => NEW flow; with --app/--app-id => BIND flow
  new     Force NEW flow (QR onboarding). Rejects --app/--app-id.
  bind    Force BIND flow (requires app_id/app_secret).
  check   Check an existing project's bot and template permissions (read-only).

Options:
  --config <path>             Path to config file
  --project <name>            Target project (auto-created if missing)
  --platform-index <n>        1-based Feishu/Lark platform index in the project (default: first)
  --platform-type <type>      Force platform type: feishu or lark
  --app <id:secret>           Existing credentials (recommended for bind/setup)
  --app-id <id>               Existing app_id
  --app-secret <secret>       Existing app_secret
  --timeout <seconds>         QR onboarding timeout (default: 600)
  --qr-image <path>           Save QR code as PNG image file (e.g. --qr-image qr.png)
  --set-allow-from-empty      Merge owner open_id into allow_from when available (default: false)
  --debug                     Print onboarding debug logs
  --template <path>           Custom registration addons JSON (default: built-in shared template)
  --name <name>               App name (default: project name)
  --description <text>        App description
  --avatar <https-url>        App avatar
  --agent <type>              Agent type for a new project
  --work-dir <path>           Working directory for a new project
  --model <name>              Model for a new project
  --mode <mode>               Agent permission mode for a new project
  --display <mode>            Display for a new project: quiet (default), compact, full
  -h, --help                  Show this help

Examples:
  # Recommended: one command for both flows
  lark-agent-bot feishu setup --project my-project
  lark-agent-bot feishu setup --project my-project --app cli_xxx:sec_xxx

  # Equivalent to "setup --app ..."
  lark-agent-bot feishu bind --project my-project --app cli_xxx:sec_xxx

  # Use only when you must force QR onboarding
  lark-agent-bot feishu new --project my-project --platform-type lark
`

func resolveFeishuSetupInputs(mode, app, appID, appSecret string) (effectiveMode, resolvedAppID, resolvedAppSecret string, err error) {
	app = strings.TrimSpace(app)
	appID = strings.TrimSpace(appID)
	appSecret = strings.TrimSpace(appSecret)

	if app != "" && (appID != "" || appSecret != "") {
		return "", "", "", fmt.Errorf("use either --app or --app-id/--app-secret, not both")
	}

	if app != "" {
		appID, appSecret, err = parseAppPair(app)
		if err != nil {
			return "", "", "", err
		}
	}

	if appID != "" || appSecret != "" {
		if appID == "" || appSecret == "" {
			return "", "", "", fmt.Errorf("both --app-id and --app-secret are required")
		}
	}

	effectiveMode = mode
	if mode == feishuSetupModeAuto {
		if appID != "" && appSecret != "" {
			effectiveMode = feishuSetupModeBind
		} else {
			effectiveMode = feishuSetupModeNew
		}
	}
	if mode == feishuSetupModeBind && (appID == "" || appSecret == "") {
		return "", "", "", fmt.Errorf("bind mode requires credentials: use --app id:secret or --app-id/--app-secret")
	}
	if mode == feishuSetupModeNew && (appID != "" || appSecret != "") {
		return "", "", "", fmt.Errorf("new mode does not accept credentials; use `lark-agent-bot feishu bind`")
	}

	return effectiveMode, appID, appSecret, nil
}

func parseAppPair(raw string) (appID, appSecret string, err error) {
	idx := strings.Index(raw, ":")
	if idx <= 0 || idx >= len(raw)-1 {
		return "", "", fmt.Errorf("--app format must be app_id:app_secret")
	}
	appID = strings.TrimSpace(raw[:idx])
	appSecret = strings.TrimSpace(raw[idx+1:])
	if appID == "" || appSecret == "" {
		return "", "", fmt.Errorf("--app format must be app_id:app_secret")
	}
	return appID, appSecret, nil
}

func resolveTargetProject(project string) (string, error) {
	project = strings.TrimSpace(project)
	if project != "" {
		return project, nil
	}
	projects, err := config.ListProjects()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	switch len(projects) {
	case 0:
		// First setup on this machine: use the starter config's project name.
		return "my-project", nil
	case 1:
		return projects[0], nil
	default:
		sort.Strings(projects)
		return "", fmt.Errorf("multiple projects found, please specify --project (%s)", strings.Join(projects, ", "))
	}
}

func normalizeFeishuPlatformType(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "", nil
	}
	if raw != "feishu" && raw != "lark" {
		return "", fmt.Errorf("invalid --platform-type %q, want feishu or lark", raw)
	}
	return raw, nil
}

func validateAppCredentials(appID, appSecret, platformType string) (string, error) {
	appID = strings.TrimSpace(appID)
	appSecret = strings.TrimSpace(appSecret)
	if appID == "" || appSecret == "" {
		return "", errors.New("app_id/app_secret are required")
	}

	candidates := []string{"feishu", "lark"}
	if platformType == "feishu" || platformType == "lark" {
		candidates = []string{platformType}
	}

	var lastErr error
	for _, candidate := range candidates {
		base := openFeishuBaseURL
		if candidate == "lark" {
			base = openLarkBaseURL
		}
		ok, err := validateAppCredentialsAgainstBase(base, appID, appSecret)
		if err != nil {
			lastErr = err
			continue
		}
		if ok {
			return candidate, nil
		}
		lastErr = fmt.Errorf("remote returned non-zero code")
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("unknown validation error")
	}
	return "", lastErr
}

func validateAppCredentialsAgainstBase(baseURL, appID, appSecret string) (bool, error) {
	body, _ := json.Marshal(map[string]string{
		"app_id":     appID,
		"app_secret": appSecret,
	})
	req, err := http.NewRequest(http.MethodPost, baseURL+"/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false, err
	}

	var parsed tenantTokenResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return false, fmt.Errorf("decode response: %w", err)
	}
	if parsed.Code == 0 && parsed.TenantAccessToken != "" {
		return true, nil
	}
	if parsed.Msg != "" {
		return false, fmt.Errorf("code=%d msg=%s", parsed.Code, parsed.Msg)
	}
	return false, nil
}

func runRegistrationFlow(opts registrationFlowOptions) (*registrationFlowResult, error) {
	return runRegistrationFlowWithClient(opts, &registrationClient{
		baseURL: accountsFeishuBaseURL,
		http:    &http.Client{Timeout: 15 * time.Second},
		debug:   opts.Debug,
	})
}

func runRegistrationFlowWithClient(opts registrationFlowOptions, client *registrationClient) (*registrationFlowResult, error) {
	if opts.Addons == nil {
		var err error
		opts.Addons, err = loadSetupTemplate("")
		if err != nil {
			return nil, err
		}
	}
	if opts.TimeoutSeconds <= 0 {
		opts.TimeoutSeconds = 600
	}
	var initRes registrationInitResponse
	if err := client.registrationCall("init", nil, &initRes); err != nil {
		return nil, fmt.Errorf("init failed: %w", err)
	}
	if initRes.Error != "" {
		return nil, fmt.Errorf("%s: %s", initRes.Error, initRes.ErrorDescription)
	}
	if len(initRes.SupportedAuthMethods) > 0 && !containsString(initRes.SupportedAuthMethods, "client_secret") {
		return nil, fmt.Errorf("current environment does not support client_secret auth")
	}

	var beginRes registrationBeginResponse
	beginParams := map[string]string{
		"archetype":         "PersonalAgent",
		"auth_method":       "client_secret",
		"request_user_info": "open_id",
	}
	if err := client.registrationCall("begin", beginParams, &beginRes); err != nil {
		return nil, fmt.Errorf("begin failed: %w", err)
	}
	if beginRes.Error != "" {
		return nil, fmt.Errorf("%s: %s", beginRes.Error, beginRes.ErrorDescription)
	}
	if beginRes.DeviceCode == "" || beginRes.VerificationURIComplete == "" {
		return nil, fmt.Errorf("incomplete onboarding response")
	}
	registrationURL, err := setupRegistrationURL(beginRes.VerificationURIComplete, opts)
	if err != nil {
		return nil, err
	}

	fmt.Println(setupText(core.MsgSetupScanQR))
	fmt.Printf("URL: %s\n\n", registrationURL)
	tryPrintTerminalQRCode(registrationURL)
	if opts.QRImagePath != "" {
		if err := saveQRCodeImage(registrationURL, opts.QRImagePath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to save QR image: %v\n", err)
		} else {
			fmt.Printf("QR code saved to: %s\n\n", opts.QRImagePath)
		}
	}

	interval := beginRes.Interval
	if interval <= 0 {
		interval = 5
	}
	expireIn := beginRes.ExpireIn
	if expireIn <= 0 {
		expireIn = opts.TimeoutSeconds
	}

	timeoutAt := time.Now().Add(time.Duration(expireIn) * time.Second)
	if limitByFlag := time.Now().Add(time.Duration(opts.TimeoutSeconds) * time.Second); limitByFlag.Before(timeoutAt) {
		timeoutAt = limitByFlag
	}

	platformType := "feishu"
	for time.Now().Before(timeoutAt) {
		var pollRes registrationPollResponse
		if err := client.registrationCall("poll", map[string]string{"device_code": beginRes.DeviceCode}, &pollRes); err != nil {
			return nil, fmt.Errorf("poll failed: %w", err)
		}

		tenantBrand := strings.ToLower(strings.TrimSpace(pollRes.UserInfo.TenantBrand))
		if tenantBrand == "lark" {
			platformType = "lark"
			if client.baseURL != accountsLarkBaseURL {
				client.baseURL = accountsLarkBaseURL
				if opts.Debug {
					fmt.Fprintln(os.Stderr, "[debug] tenant brand detected as lark, switched onboarding domain")
				}
				continue
			}
		}

		if pollRes.ClientID != "" && pollRes.ClientSecret != "" {
			return &registrationFlowResult{
				AppID:       pollRes.ClientID,
				AppSecret:   pollRes.ClientSecret,
				OwnerOpenID: pollRes.UserInfo.OpenID,
				Platform:    platformType,
			}, nil
		}

		switch pollRes.Error {
		case "", "authorization_pending":
		case "slow_down":
			interval += 5
		case "access_denied":
			return nil, fmt.Errorf("authorization denied by user")
		case "expired_token":
			return nil, fmt.Errorf("onboarding session expired")
		default:
			if pollRes.Error != "" {
				return nil, fmt.Errorf("%s: %s", pollRes.Error, pollRes.ErrorDescription)
			}
		}

		time.Sleep(time.Duration(interval) * time.Second)
	}

	return nil, fmt.Errorf("timed out waiting for QR onboarding result")
}

func (c *registrationClient) registrationCall(action string, params map[string]string, out any) error {
	form := url.Values{}
	form.Set("action", action)
	for k, v := range params {
		form.Set(k, v)
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/oauth/v1/app/registration", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if c.debug {
		fmt.Fprintf(os.Stderr, "[debug] registration action=%s status=%d\n", action, resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func containsString(values []string, expected string) bool {
	for _, item := range values {
		if strings.EqualFold(strings.TrimSpace(item), expected) {
			return true
		}
	}
	return false
}

func tryPrintTerminalQRCode(content string) {
	if content == "" {
		return
	}
	qrterminal.GenerateWithConfig(content, qrterminal.Config{
		Level:      qrterminal.M,
		Writer:     os.Stdout,
		HalfBlocks: false,
		BlackChar:  "██",
		WhiteChar:  "  ",
		QuietZone:  4,
	})
	if _, err := fmt.Fprintln(os.Stdout); err != nil {
		return
	}
}

func saveQRCodeImage(content, path string) error {
	code, err := qr.Encode(content, qr.M)
	if err != nil {
		return fmt.Errorf("encode QR: %w", err)
	}
	code.Scale = 8
	return os.WriteFile(path, code.PNG(), 0644)
}
