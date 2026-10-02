package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/daemon"
)

func runDaemon(args []string) {
	if len(args) == 0 {
		printDaemonUsage()
		os.Exit(1)
	}

	switch args[0] {
	case "install":
		daemonInstall(args[1:])
	case "uninstall":
		daemonUninstall(args[1:])
	case "start":
		daemonStart(args[1:])
	case "stop":
		daemonStop(args[1:])
	case "restart":
		daemonRestart(args[1:])
	case "status":
		daemonStatus(args[1:])
	case "logs":
		daemonLogs(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown daemon command: %s\n\n", args[0])
		printDaemonUsage()
		os.Exit(1)
	}
}

// ── install ─────────────────────────────────────────────────

func daemonInstall(args []string) {
	cfg, force, err := parseDaemonInstallArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Settle the config file before resolving, so that daemon.Resolve's env
	// capture (including ${ENV} placeholder scanning of the config file) and
	// the installed service's --config and working directory all use the
	// config actually in use.
	if err := resolveDaemonConfigPath(&cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if cfg.Instance == "" {
		cfg.Instance = daemon.InstanceFromConfigPath(cfg.ConfigPath)
	}
	if other := instanceUsingConfig(daemon.ListMeta(), cfg.ConfigPath, cfg.Instance); other != nil && !force {
		fmt.Fprintf(os.Stderr, "%s is already installed as %s. Uninstall that one first, or use --force to install it twice.\n",
			cfg.ConfigPath, instanceLabel(other.Instance))
		os.Exit(1)
	}

	if err := daemon.Resolve(&cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	mgr := mustManager(cfg.Instance)

	st, _ := mgr.Status()
	if st != nil && st.Installed && !force {
		fmt.Fprintf(os.Stderr, "Service %s already installed. Use --force to reinstall.\n", daemon.ServiceNameFor(cfg.Instance))
		os.Exit(1)
	}

	// A bot already running with this config outside the service holds the
	// instance lock, and the service would retry until it exits.
	if pid := runningInstancePID(cfg.ConfigPath); pid > 0 {
		if force {
			fmt.Printf("Stopping lark-agent-bot (PID %d), already running with %s...\n", pid, cfg.ConfigPath)
			KillExistingInstance(cfg.ConfigPath)
		} else {
			fmt.Fprintf(os.Stderr, "Warning: lark-agent-bot (PID %d) is already running with %s outside the service.\n", pid, cfg.ConfigPath)
			fmt.Fprintln(os.Stderr, "  The service will keep retrying until it exits. Use --force to stop it first.")
		}
	}

	if err := mgr.Install(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Install failed: %v\n", err)
		os.Exit(1)
	}

	if err := daemon.SaveMeta(&daemon.Meta{
		LogFile:       cfg.LogFile,
		LogMaxSize:    cfg.LogMaxSize,
		LogMaxBackups: cfg.LogMaxBackups,
		WorkDir:       cfg.WorkDir,
		BinaryPath:    cfg.BinaryPath,
		InstalledAt:   daemon.NowISO(),
		ConfigPath:    cfg.ConfigPath,
		Instance:      cfg.Instance,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save metadata: %v\n", err)
	}

	sel := instanceSelectorFlag(cfg.Instance)
	fmt.Println("lark-agent-bot daemon installed and started.")
	fmt.Println()
	fmt.Printf("  Service:   %s\n", daemon.ServiceNameFor(cfg.Instance))
	fmt.Printf("  Platform:  %s\n", mgr.Platform())
	fmt.Printf("  Binary:    %s\n", cfg.BinaryPath)
	fmt.Printf("  Config:    %s\n", cfg.ConfigPath)
	fmt.Printf("  WorkDir:   %s\n", cfg.WorkDir)
	fmt.Printf("  Log:       %s\n", cfg.LogFile)
	fmt.Printf("  LogMax:    %d MB\n", cfg.LogMaxSize/1024/1024)
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Printf("  lark-agent-bot daemon status%s    - Check status\n", sel)
	fmt.Printf("  lark-agent-bot daemon logs -f%s   - Follow logs\n", sel)
	fmt.Printf("  lark-agent-bot daemon restart%s   - Restart\n", sel)
	fmt.Printf("  lark-agent-bot daemon stop%s      - Stop\n", sel)
	fmt.Printf("  lark-agent-bot daemon uninstall%s - Remove\n", sel)

	// Check linger for user-mode systemd
	if strings.Contains(mgr.Platform(), "user") {
		enabled, user := daemon.CheckLinger()
		if !enabled {
			fmt.Println()
			fmt.Println("⚠️  Warning: Linger is not enabled for this user.")
			fmt.Println("   lark-agent-bot will stop when your last login session ends (e.g., SSH disconnect).")
			fmt.Println("   To keep it running persistently, run:")
			fmt.Printf("     sudo loginctl enable-linger %s\n", user)
		}
	}
}

// resolveDaemonConfigPath settles the absolute config file the service runs
// with. An explicit --config must exist. Otherwise it is config.toml in the
// work dir (default: the current dir), then ~/.lark-agent-bot/config.toml,
// the order the bot itself uses.
func resolveDaemonConfigPath(cfg *daemon.Config) error {
	if cfg.ConfigPath != "" {
		abs, err := filepath.Abs(cfg.ConfigPath)
		if err != nil {
			return fmt.Errorf("config path %s: %w", cfg.ConfigPath, err)
		}
		if _, err := os.Stat(abs); err != nil {
			return fmt.Errorf("config file not found: %s", abs)
		}
		cfg.ConfigPath = abs
		return nil
	}

	workDir := cfg.WorkDir
	if workDir == "" {
		if wd, err := os.Getwd(); err == nil {
			workDir = wd
		}
	}
	if abs, err := filepath.Abs(filepath.Join(workDir, "config.toml")); err == nil {
		if _, err := os.Stat(abs); err == nil {
			cfg.ConfigPath = abs
			return nil
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		homeConfig := filepath.Join(home, ".lark-agent-bot", "config.toml")
		if _, err := os.Stat(homeConfig); err == nil {
			cfg.ConfigPath = homeConfig
			if cfg.WorkDir == "" {
				cfg.WorkDir = filepath.Dir(homeConfig)
			}
			fmt.Fprintf(os.Stderr, "Note: using config from %s\n", homeConfig)
			return nil
		}
	}
	return fmt.Errorf("config.toml not found in %s\n  Use --work-dir to specify the config directory or --config to point to the config file", workDir)
}

// instanceUsingConfig returns another installed instance that runs with
// configPath, or nil.
func instanceUsingConfig(metas []*daemon.Meta, configPath, instance string) *daemon.Meta {
	for _, m := range metas {
		if m.Instance != instance && m.ConfigPath != "" && sameFilePath(m.ConfigPath, configPath) {
			return m
		}
	}
	return nil
}

func sameFilePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if filepath.Separator == '\\' {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func parseDaemonInstallArgs(args []string) (daemon.Config, bool, error) {
	var cfg daemon.Config
	var force, workDirSet bool

	// Env-based opt-out: CC_DAEMON_NO_CAPTURE_SECRETS=1 / true / yes / on
	// triggers --no-capture-secrets without the CLI flag, for CI / container
	// scenarios where the global env is the right configuration surface.
	if isTruthyEnv(os.Getenv("CC_DAEMON_NO_CAPTURE_SECRETS")) {
		cfg.NoCaptureSecrets = true
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--force":
			force = true
		case arg == "--no-capture-secrets":
			cfg.NoCaptureSecrets = true
		case arg == "--log-file":
			value, next, err := daemonInstallFlagValue(args, i, "--log-file")
			if err != nil {
				return daemon.Config{}, false, err
			}
			cfg.LogFile = value
			i = next
		case strings.HasPrefix(arg, "--log-file="):
			cfg.LogFile = strings.TrimPrefix(arg, "--log-file=")
		case arg == "--log-max-size":
			value, next, err := daemonInstallFlagValue(args, i, "--log-max-size")
			if err != nil {
				return daemon.Config{}, false, err
			}
			mb, err := strconv.Atoi(value)
			if err != nil {
				return daemon.Config{}, false, fmt.Errorf("invalid value for --log-max-size: %s", value)
			}
			cfg.LogMaxSize = int64(mb) * 1024 * 1024
			i = next
		case strings.HasPrefix(arg, "--log-max-size="):
			value := strings.TrimPrefix(arg, "--log-max-size=")
			mb, err := strconv.Atoi(value)
			if err != nil {
				return daemon.Config{}, false, fmt.Errorf("invalid value for --log-max-size: %s", value)
			}
			cfg.LogMaxSize = int64(mb) * 1024 * 1024
		case arg == "--work-dir":
			value, next, err := daemonInstallFlagValue(args, i, "--work-dir")
			if err != nil {
				return daemon.Config{}, false, err
			}
			cfg.WorkDir = value
			workDirSet = true
			i = next
		case strings.HasPrefix(arg, "--work-dir="):
			cfg.WorkDir = strings.TrimPrefix(arg, "--work-dir=")
			workDirSet = true
		case arg == "--config" || arg == "-config":
			value, next, err := daemonInstallFlagValue(args, i, arg)
			if err != nil {
				return daemon.Config{}, false, err
			}
			cfg.ConfigPath = value
			i = next
		case strings.HasPrefix(arg, "--config="):
			cfg.ConfigPath = strings.TrimPrefix(arg, "--config=")
		case strings.HasPrefix(arg, "-config="):
			cfg.ConfigPath = strings.TrimPrefix(arg, "-config=")
		case arg == "--name" || strings.HasPrefix(arg, "--name="):
			value := strings.TrimPrefix(arg, "--name=")
			if arg == "--name" {
				v, next, err := daemonInstallFlagValue(args, i, "--name")
				if err != nil {
					return daemon.Config{}, false, err
				}
				value, i = v, next
			}
			instance, err := daemon.SanitizeInstance(value)
			if err != nil {
				return daemon.Config{}, false, err
			}
			cfg.Instance = instance
		default:
			return daemon.Config{}, false, fmt.Errorf("unknown flag: %s", arg)
		}
	}

	// The service runs where its config lives unless --work-dir says otherwise.
	if cfg.ConfigPath != "" && !workDirSet {
		cfg.WorkDir = filepath.Dir(cfg.ConfigPath)
	}
	return cfg, force, nil
}

func daemonInstallFlagValue(args []string, index int, flagName string) (string, int, error) {
	next := index + 1
	if next >= len(args) {
		return "", index, fmt.Errorf("missing value for %s", flagName)
	}
	return args[next], next, nil
}

// isTruthyEnv accepts the conventional opt-in values for boolean env vars.
// Anything else, including "0" / "false" / "" / unset, is treated as false.
func isTruthyEnv(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// ── instance selection ──────────────────────────────────────

// parseInstanceSelector takes --name N or --config PATH out of args and
// returns the instance they select ("" is the default instance) together
// with the remaining args.
func parseInstanceSelector(args []string) (string, []string, error) {
	instance := ""
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		flagName, value, hasValue := strings.Cut(arg, "=")
		switch flagName {
		case "--name", "--config", "-config":
		default:
			rest = append(rest, arg)
			continue
		}
		if !hasValue {
			v, next, err := daemonInstallFlagValue(args, i, flagName)
			if err != nil {
				return "", nil, err
			}
			value, i = v, next
		}
		if flagName == "--name" {
			name, err := daemon.SanitizeInstance(value)
			if err != nil {
				return "", nil, err
			}
			instance = name
		} else {
			instance = instanceForConfig(value)
		}
	}
	return instance, rest, nil
}

// instanceForConfig is the instance installed with configPath, or the
// instance its file name gives when none is.
func instanceForConfig(configPath string) string {
	if abs, err := filepath.Abs(configPath); err == nil {
		for _, m := range daemon.ListMeta() {
			if m.ConfigPath != "" && sameFilePath(m.ConfigPath, abs) {
				return m.Instance
			}
		}
	}
	return daemon.InstanceFromConfigPath(configPath)
}

// selectedInstance parses the instance selector of a subcommand that takes
// no other flags.
func selectedInstance(args []string) string {
	instance, rest, err := parseInstanceSelector(args)
	if err == nil && len(rest) > 0 {
		err = fmt.Errorf("unknown flag: %s", rest[0])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return instance
}

func instanceLabel(instance string) string {
	if instance == "" {
		return "the default instance"
	}
	return "instance " + instance
}

func instanceSelectorFlag(instance string) string {
	if instance == "" {
		return ""
	}
	return " --name " + instance
}

// ── uninstall ───────────────────────────────────────────────

func daemonUninstall(args []string) {
	instance := selectedInstance(args)
	mgr := mustManager(instance)

	st, _ := mgr.Status()
	if st != nil && !st.Installed {
		fmt.Printf("Service %s is not installed.\n", daemon.ServiceNameFor(instance))
		return
	}

	if err := mgr.Uninstall(); err != nil {
		fmt.Fprintf(os.Stderr, "Uninstall failed: %v\n", err)
		os.Exit(1)
	}

	daemon.RemoveMeta(instance)
	fmt.Printf("lark-agent-bot daemon %s uninstalled.\n", daemon.ServiceNameFor(instance))
}

// ── start / stop / restart ──────────────────────────────────

func daemonStart(args []string) {
	instance := selectedInstance(args)
	mgr := mustManager(instance)
	requireInstalled(mgr, instance)
	if err := mgr.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Start failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("lark-agent-bot daemon %s started.\n", daemon.ServiceNameFor(instance))
}

func daemonStop(args []string) {
	instance := selectedInstance(args)
	mgr := mustManager(instance)
	requireInstalled(mgr, instance)
	if err := mgr.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "Stop failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("lark-agent-bot daemon %s stopped.\n", daemon.ServiceNameFor(instance))
}

func daemonRestart(args []string) {
	instance, rest, err := parseInstanceSelector(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	force := false
	for _, a := range rest {
		if a != "--force" {
			fmt.Fprintf(os.Stderr, "unknown flag: %s\n", a)
			os.Exit(1)
		}
		force = true
	}

	mgr := mustManager(instance)
	requireInstalled(mgr, instance)

	if force {
		if meta, err := daemon.LoadMeta(instance); err == nil {
			KillExistingInstance(metaConfigPath(meta))
		}
	}

	if err := mgr.Restart(); err != nil {
		fmt.Fprintf(os.Stderr, "Restart failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("lark-agent-bot daemon %s restarted.\n", daemon.ServiceNameFor(instance))
}

// metaConfigPath is the config an instance runs with. Installs from before
// config_path was recorded used config.toml in their work dir.
func metaConfigPath(meta *daemon.Meta) string {
	if meta.ConfigPath != "" {
		return meta.ConfigPath
	}
	return filepath.Join(meta.WorkDir, "config.toml")
}

func requireInstalled(mgr daemon.Manager, instance string) {
	st, _ := mgr.Status()
	if st == nil || !st.Installed {
		fmt.Fprintf(os.Stderr, "Service %s is not installed. Run first:\n", daemon.ServiceNameFor(instance))
		fmt.Fprintln(os.Stderr, "  lark-agent-bot daemon install --config /path/to/config.toml")
		os.Exit(1)
	}
}

// ── status ──────────────────────────────────────────────────

func daemonStatus(args []string) {
	instances := []string{selectedInstance(args)}
	if len(args) == 0 {
		// Without a selector, report every installed instance.
		instances = nil
		for _, m := range daemon.ListMeta() {
			instances = append(instances, m.Instance)
		}
		if len(instances) == 0 {
			instances = []string{""}
		}
	}

	fmt.Println("lark-agent-bot daemon status")
	for _, instance := range instances {
		fmt.Println()
		printInstanceStatus(instance)
	}
}

func printInstanceStatus(instance string) {
	mgr := mustManager(instance)
	st, err := mgr.Status()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("  Service:   %s\n", daemon.ServiceNameFor(instance))
	if !st.Installed {
		fmt.Println("  Status:    Not installed")
		fmt.Printf("  Platform:  %s\n", st.Platform)
		fmt.Println()
		fmt.Println("  Run: lark-agent-bot daemon install --config /path/to/config.toml")
		return
	}

	meta, metaErr := daemon.LoadMeta(instance)
	botPID := 0
	if metaErr == nil {
		botPID = runningInstancePID(metaConfigPath(meta))
	}

	statusStr := "Stopped"
	switch {
	case st.Running:
		statusStr = "Running"
	case botPID > 0:
		// The bot runs but the service does not track it, e.g. after a
		// restart by a version that started the new process itself.
		statusStr = fmt.Sprintf("Stopped, but lark-agent-bot (PID %d) runs outside the service", botPID)
	}
	fmt.Printf("  Status:    %s\n", statusStr)
	fmt.Printf("  Platform:  %s\n", st.Platform)
	if st.PID > 0 {
		fmt.Printf("  PID:       %d\n", st.PID)
	} else if st.Running && botPID > 0 {
		fmt.Printf("  PID:       %d\n", botPID)
	}

	if metaErr == nil {
		fmt.Printf("  Config:    %s\n", metaConfigPath(meta))
		fmt.Printf("  Log:       %s\n", meta.LogFile)
		fmt.Printf("  WorkDir:   %s\n", meta.WorkDir)
		if t, err := time.Parse(time.RFC3339, meta.InstalledAt); err == nil {
			fmt.Printf("  Installed: %s\n", t.Format("2006-01-02 15:04:05"))
		}
	}
}

// ── logs ────────────────────────────────────────────────────

func daemonLogs(args []string) {
	instance, rest, err := parseInstanceSelector(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	follow := false
	lines := 100
	logFile := ""

	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "-f", "--follow":
			follow = true
		case "-n":
			i++
			if i < len(rest) {
				if n, err := strconv.Atoi(rest[i]); err == nil && n > 0 {
					lines = n
				}
			}
		case "--log-file":
			i++
			if i < len(rest) {
				logFile = rest[i]
			}
		}
	}

	if logFile == "" {
		if meta, err := daemon.LoadMeta(instance); err == nil {
			logFile = meta.LogFile
		} else {
			logFile = daemon.DefaultLogFileFor(instance)
		}
	}

	if _, err := os.Stat(logFile); err != nil {
		fmt.Fprintf(os.Stderr, "Log file not found: %s\n", logFile)
		os.Exit(1)
	}

	if !follow {
		printLastLines(logFile, lines)
		return
	}

	printLastLines(logFile, lines)
	followFile(logFile)
}

func printLastLines(path string, n int) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading log: %v\n", err)
		return
	}

	allLines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	start := 0
	if len(allLines) > n {
		start = len(allLines) - n
	}
	for _, line := range allLines[start:] {
		fmt.Println(line)
	}
}

func followFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	_, _ = f.Seek(0, io.SeekEnd)
	reader := bufio.NewReader(f)

	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			fmt.Print(line)
		}
		if err == io.EOF {
			time.Sleep(300 * time.Millisecond)
			reader.Reset(f)
			continue
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return
		}
	}
}

// ── helpers ─────────────────────────────────────────────────

func mustManager(instance string) daemon.Manager {
	mgr, err := daemon.NewManager(instance)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return mgr
}

func printDaemonUsage() {
	fmt.Println(`Usage: lark-agent-bot daemon <command> [flags]

Commands:
  install     Install and start as system service
  uninstall   Remove system service
  start       Start the service
  stop        Stop the service
  restart     Restart the service
  status      Show service status (every instance unless one is selected)
  logs        View log output

Instances:
  Each config file is installed as its own service, so several bots can run
  on one machine. The instance is named after the config file (claude.toml
  gives lark-agent-bot-claude); config.toml is the default instance
  (lark-agent-bot). Every command takes --name NAME or --config PATH to
  select an instance; without either it acts on the default instance.

Install flags:
  --config PATH         Config file to run with (default: config.toml in the
                        work dir, then ~/.lark-agent-bot/config.toml)
  --name NAME           Instance name (default: from the config file name)
  --log-file PATH       Log file path (default: ~/.lark-agent-bot/logs/<service>.log)
  --log-max-size N      Max log file size in MB (default: 10)
  --work-dir DIR        Working directory (default: the config file's directory)
  --force               Overwrite an existing installation, and stop a bot
                        already running with the same config
  --no-capture-secrets  Do not capture config.toml ${ENV} placeholders into
                        the service file. Also enabled by setting
                        CC_DAEMON_NO_CAPTURE_SECRETS=1 in the environment.

Restart flags:
  --force               Kill existing process before restarting

Logs flags:
  -f, --follow          Follow log output (like tail -f)
  -n N                  Number of lines to show (default: 100)
  --log-file PATH       Custom log file path

Supported platforms:
  Linux (root)     - systemd system service (/etc/systemd/system/)
  Linux (non-root) - systemd user service (~/.config/systemd/user/)
  macOS            - launchd LaunchAgent
  Windows          - Task Scheduler task (schtasks)`)
}
