package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	DefaultLogMaxSize    = 10 * 1024 * 1024 // 10 MB
	DefaultLogMaxBackups = 3                // active + .1 + .2 + .3
	ServiceName          = "lark-agent-bot"
)

// A service launcher that starts the bot again when it exits with
// RestartExitCode sets RestartExitCodeEnv to that code. The bot then
// restarts by exiting with it instead of starting the new process itself,
// which would leave the service tracking a process that has exited.
const (
	RestartExitCode    = 75 // EX_TEMPFAIL
	RestartExitCodeEnv = "CC_RESTART_EXIT_CODE"
)

type Config struct {
	BinaryPath    string
	WorkDir       string
	LogFile       string
	LogMaxSize    int64
	LogMaxBackups int
	EnvPATH       string            // capture user's PATH so agents are accessible
	EnvExtra      map[string]string // selected environment variables needed by the service runtime
	// NoCaptureSecrets, when true, restricts the install-time env capture
	// to proxy-related variables only and skips both the config.toml ${ENV}
	// placeholder scan and any extension discoverers registered via
	// RegisterEnvDiscoverer. Operators who'd rather inject secrets via
	// keychain / `secret-tool` / EnvironmentFile= set this to keep token
	// values out of the service manager files on disk.
	NoCaptureSecrets bool
	// ConfigPath is the absolute path of the config file the service runs
	// with. It is passed to lark-agent-bot as --config.
	ConfigPath string
	// Instance names the service when several bots run on one machine,
	// each with its own config. Empty is the default instance, which keeps
	// the historical service, metadata and log names.
	Instance string
}

type Status struct {
	Installed bool
	Running   bool
	PID       int
	Platform  string // "systemd", "launchd", "schtasks"
}

type Manager interface {
	Install(cfg Config) error
	Uninstall() error
	Start() error
	Stop() error
	Restart() error
	Status() (*Status, error)
	Platform() string
}

// NewManager returns a platform-specific daemon manager for the named
// instance; "" is the default instance.
func NewManager(instance string) (Manager, error) {
	return newPlatformManager(instance)
}

// ServiceNameFor is the service name of an instance: "lark-agent-bot" for the
// default instance, "lark-agent-bot-<instance>" otherwise.
func ServiceNameFor(instance string) string {
	if instance == "" {
		return ServiceName
	}
	return ServiceName + "-" + instance
}

const maxInstanceLen = 48

// SanitizeInstance turns a user-supplied instance name into one that is safe
// in task, unit and file names: ASCII letters, digits and "_", with every
// other run of characters replaced by a single "-".
func SanitizeInstance(raw string) (string, error) {
	var sb strings.Builder
	dash := false
	for _, r := range strings.TrimSpace(raw) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			sb.WriteRune(r)
			dash = false
		case !dash:
			sb.WriteByte('-')
			dash = true
		}
	}
	name := strings.Trim(sb.String(), "-")
	if len(name) > maxInstanceLen {
		name = strings.TrimRight(name[:maxInstanceLen], "-")
	}
	if name == "" && strings.TrimSpace(raw) != "" {
		return "", fmt.Errorf("invalid instance name %q: use letters, digits, - or _", raw)
	}
	return name, nil
}

// InstanceFromConfigPath derives an instance name from a config file name:
// "claude-bot.toml" gives "claude-bot". The usual "config.toml" gives the
// default instance, so installs that use it keep their names.
func InstanceFromConfigPath(path string) string {
	base := filepath.Base(path)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	if strings.EqualFold(name, "config") {
		return ""
	}
	instance, err := SanitizeInstance(name)
	if err != nil {
		return ""
	}
	return instance
}

func DefaultLogFile() string {
	return DefaultLogFileFor("")
}

// DefaultLogFileFor is the default log file of an instance. Instances never
// share a log file: each process rotates its own.
func DefaultLogFileFor(instance string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".lark-agent-bot", "logs", ServiceNameFor(instance)+".log")
}

func DefaultDataDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".lark-agent-bot")
}

// ── Metadata ────────────────────────────────────────────────
// Stored at ~/.lark-agent-bot/daemon.json (daemon-<instance>.json for a
// named instance) so that `logs`, `status`, etc. can locate the log file
// without parsing service definitions.

type Meta struct {
	LogFile       string `json:"log_file"`
	LogMaxSize    int64  `json:"log_max_size"`
	LogMaxBackups int    `json:"log_max_backups"`
	WorkDir       string `json:"work_dir"`
	BinaryPath    string `json:"binary_path"`
	InstalledAt   string `json:"installed_at"`
	ConfigPath    string `json:"config_path,omitempty"`
	Instance      string `json:"instance,omitempty"`
}

const (
	metaFilePrefix = "daemon"
	metaFileExt    = ".json"
)

func metaPathFor(instance string) string {
	name := metaFilePrefix + metaFileExt
	if instance != "" {
		name = metaFilePrefix + "-" + instance + metaFileExt
	}
	return filepath.Join(DefaultDataDir(), name)
}

func SaveMeta(m *Meta) error {
	path := metaPathFor(m.Instance)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// LoadMeta reads the metadata of an instance; "" is the default instance.
func LoadMeta(instance string) (*Meta, error) {
	data, err := os.ReadFile(metaPathFor(instance))
	if err != nil {
		return nil, err
	}
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	m.Instance = instance
	return &m, nil
}

func RemoveMeta(instance string) {
	_ = os.Remove(metaPathFor(instance))
}

// ListMeta returns the metadata of every installed instance, the default
// instance first. Unreadable files are skipped.
func ListMeta() []*Meta {
	var metas []*Meta
	if m, err := LoadMeta(""); err == nil {
		metas = append(metas, m)
	}
	matches, _ := filepath.Glob(filepath.Join(DefaultDataDir(), metaFilePrefix+"-*"+metaFileExt))
	sort.Strings(matches)
	for _, path := range matches {
		instance := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), metaFilePrefix+"-"), metaFileExt)
		if m, err := LoadMeta(instance); err == nil {
			metas = append(metas, m)
		}
	}
	return metas
}

func NowISO() string {
	return time.Now().Format(time.RFC3339)
}

func Resolve(cfg *Config) error {
	if cfg.BinaryPath == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("cannot detect binary path: %w", err)
		}
		real, err := filepath.EvalSymlinks(exe)
		if err == nil {
			exe = real
		}
		cfg.BinaryPath = exe
	}
	if cfg.WorkDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("cannot detect working directory: %w", err)
		}
		cfg.WorkDir = wd
	}
	// The service starts in another directory than the installer, so paths
	// given relative to where install ran must be made absolute here.
	for _, p := range []*string{&cfg.WorkDir, &cfg.LogFile} {
		if *p == "" {
			continue
		}
		abs, err := filepath.Abs(*p)
		if err != nil {
			return fmt.Errorf("resolve path %s: %w", *p, err)
		}
		*p = abs
	}
	if cfg.ConfigPath == "" {
		cfg.ConfigPath = filepath.Join(cfg.WorkDir, "config.toml")
	}
	if cfg.LogFile == "" {
		cfg.LogFile = DefaultLogFileFor(cfg.Instance)
	}
	if cfg.LogMaxSize <= 0 {
		cfg.LogMaxSize = DefaultLogMaxSize
	}
	if cfg.LogMaxBackups < 1 {
		cfg.LogMaxBackups = DefaultLogMaxBackups
	}
	if cfg.EnvPATH == "" {
		cfg.EnvPATH = os.Getenv("PATH")
	}
	if len(cfg.EnvExtra) == 0 {
		cfg.EnvExtra = captureDaemonEnv(cfg.NoCaptureSecrets)
		if !cfg.NoCaptureSecrets {
			captureConfigEnvPlaceholders(cfg.ConfigPath, cfg.EnvExtra)
		}
	}
	return nil
}

// captureDaemonEnv builds the EnvExtra map baked into the installed
// service file. Proxy-related vars are always captured. When
// noCaptureSecrets is false, every registered EnvDiscoverer is also
// invoked and its (envName -> value) pairs are merged in.
//
// Discoverer errors are logged but never fail the install — the
// daemon's job is to install the service; plugins surface their own
// per-feature warnings at runtime.
func captureDaemonEnv(noCaptureSecrets bool) map[string]string {
	env := make(map[string]string)
	proxyKeys := []string{
		"http_proxy", "https_proxy", "no_proxy",
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
		"all_proxy", "ALL_PROXY",
	}
	for _, key := range proxyKeys {
		if value := os.Getenv(key); value != "" {
			env[key] = value
		}
	}

	if noCaptureSecrets {
		return env
	}

	for i, d := range snapshotEnvDiscoverers() {
		extra, err := d()
		if err != nil {
			slog.Warn("daemon: env discoverer reported warnings",
				"index", i, "err", err)
		}
		for k, v := range extra {
			if !isValidEnvName(k) {
				slog.Warn("daemon: dropping invalid env name from discoverer",
					"index", i, "key", k)
				continue
			}
			if v == "" {
				continue
			}
			env[k] = v
		}
	}
	return env
}

var configEnvPlaceholderPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// captureConfigEnvPlaceholders scans configPath for ${ENV_NAME} placeholders
// and, for each one set in the current process environment, copies it into
// env. lark-agent-bot resolves these placeholders at startup using os.ExpandEnv;
// if the daemon's service file doesn't carry the values, the started daemon
// process will see empty strings and fail to authenticate to any platform.
//
// Errors are logged and swallowed: a broken or missing config.toml must not
// abort `daemon install`. Empty / unset env names are skipped silently.
func captureConfigEnvPlaceholders(configPath string, env map[string]string) {
	if strings.TrimSpace(configPath) == "" || env == nil {
		return
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("daemon: config env placeholder discovery failed",
				"path", configPath, "err", err)
		}
		return
	}
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		slog.Warn("daemon: config env placeholder discovery failed",
			"path", configPath, "err", err)
		return
	}
	captureConfigEnvPlaceholdersInValue(reflect.ValueOf(raw), env)
}

func captureConfigEnvPlaceholdersInValue(v reflect.Value, env map[string]string) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if !v.IsNil() {
			captureConfigEnvPlaceholdersInValue(v.Elem(), env)
		}
	case reflect.String:
		captureConfigEnvPlaceholdersInString(v.String(), env)
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			captureConfigEnvPlaceholdersInValue(v.Index(i), env)
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			captureConfigEnvPlaceholdersInValue(iter.Value(), env)
		}
	}
}

func captureConfigEnvPlaceholdersInString(s string, env map[string]string) {
	matches := configEnvPlaceholderPattern.FindAllStringSubmatch(s, -1)
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		name := match[1]
		if v, ok := os.LookupEnv(name); ok && v != "" {
			env[name] = v
		}
	}
}
