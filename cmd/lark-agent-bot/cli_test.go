package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ClaymanTwinkle/lark-agent-bot/config"
	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	var buf bytes.Buffer
	copied := make(chan error, 1)
	go func() {
		_, err := io.Copy(&buf, r)
		copied <- err
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	if err := <-copied; err != nil {
		t.Fatalf("copy stdout: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}
	return buf.String()
}

// isolateHome points the home directory and the config path at an empty
// temp dir, so a test never reads or writes the user's real config.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	oldPath := config.ConfigPath
	config.ConfigPath = ""
	t.Cleanup(func() { config.ConfigPath = oldPath })
	return home
}

func TestHelpRequested(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"--help"}, {"-help"}, {"--config", "x.toml", "--help"}} {
		if !helpRequested(args) {
			t.Errorf("helpRequested(%q) = false", args)
		}
	}
	for _, args := range [][]string{nil, {"--config", "x.toml"}, {"help"}, {"--helpful"}} {
		if helpRequested(args) {
			t.Errorf("helpRequested(%q) = true", args)
		}
	}
}

func TestParseCommandFlags(t *testing.T) {
	newFS := func() (*flag.FlagSet, *string) {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		return fs, fs.String("config", "", "")
	}

	fs, cfg := newFS()
	if err := parseCommandFlags(fs, []string{"--config", "a.toml"}); err != nil || *cfg != "a.toml" {
		t.Fatalf("parse --config: err=%v config=%q", err, *cfg)
	}
	for _, args := range [][]string{{"-h"}, {"--help"}, {"--config", "a.toml", "--help"}} {
		fs, _ := newFS()
		if err := parseCommandFlags(fs, args); !errors.Is(err, flag.ErrHelp) {
			t.Errorf("parse %q: err = %v, want flag.ErrHelp", args, err)
		}
	}
	fs, _ = newFS()
	if err := parseCommandFlags(fs, []string{"--bogus"}); err == nil || errors.Is(err, flag.ErrHelp) {
		t.Errorf("parse --bogus: err = %v, want an error", err)
	}
	fs, _ = newFS()
	if err := parseCommandFlags(fs, []string{"extra"}); err == nil || !strings.Contains(err.Error(), "extra") {
		t.Errorf("parse extra: err = %v, want unexpected argument", err)
	}
}

func TestFlagParseExit(t *testing.T) {
	var code int
	var done bool
	stdout := captureStdout(t, func() {
		stderr := captureStderr(t, func() { code, done = flagParseExit(flag.ErrHelp, "USAGE") })
		if stderr != "" {
			t.Errorf("help wrote %q to stderr", stderr)
		}
	})
	if !done || code != 0 || stdout != "USAGE" {
		t.Errorf("help: code=%d done=%v stdout=%q", code, done, stdout)
	}

	stdout = captureStdout(t, func() {
		stderr := captureStderr(t, func() { code, done = flagParseExit(errors.New("bad flag"), "USAGE") })
		if !strings.Contains(stderr, "bad flag") || !strings.Contains(stderr, "USAGE") {
			t.Errorf("error wrote %q to stderr", stderr)
		}
	})
	if !done || code != 2 || stdout != "" {
		t.Errorf("error: code=%d done=%v stdout=%q", code, done, stdout)
	}

	if _, done := flagParseExit(nil, "USAGE"); done {
		t.Error("nil error: done = true")
	}
}

func TestLocaleLanguage(t *testing.T) {
	tests := []struct {
		value string
		want  core.Language
		ok    bool
	}{
		{"zh_CN.UTF-8", core.LangChinese, true},
		{"zh-CN", core.LangChinese, true},
		{"zh_TW.UTF-8", core.LangTraditionalChinese, true},
		{"zh-HK", core.LangTraditionalChinese, true},
		{"zh-Hant-TW", core.LangTraditionalChinese, true},
		{"zh-Hans-HK", core.LangChinese, true},
		{"ja_JP.UTF-8", core.LangJapanese, true},
		{"es_ES.UTF-8", core.LangSpanish, true},
		{"en_US.UTF-8", core.LangEnglish, true},
		{"C.UTF-8", core.LangEnglish, true},
		{"POSIX", core.LangEnglish, true},
		{"fr_FR.UTF-8", core.LangEnglish, true},
		{"de_DE@euro", core.LangEnglish, true},
		{"", "", false},
		{".UTF-8", "", false},
	}
	for _, tt := range tests {
		got, ok := localeLanguage(tt.value)
		if got != tt.want || ok != tt.ok {
			t.Errorf("localeLanguage(%q) = %q, %v; want %q, %v", tt.value, got, ok, tt.want, tt.ok)
		}
	}
}

func TestResolveCLILanguage(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(name string) string { return vars[name] }
	}
	sys := func(lang string) func() string { return func() string { return lang } }

	tests := []struct {
		name   string
		config string
		env    map[string]string
		system string
		want   core.Language
	}{
		{"config wins", "ja", map[string]string{"LANG": "en_US.UTF-8"}, "zh-CN", core.LangJapanese},
		{"config auto falls through", "auto", map[string]string{"LANG": "es_ES.UTF-8"}, "", core.LangSpanish},
		{"LC_ALL over LANG", "", map[string]string{"LC_ALL": "en_US.UTF-8", "LANG": "zh_CN.UTF-8"}, "", core.LangEnglish},
		{"LC_MESSAGES over LANG", "", map[string]string{"LC_MESSAGES": "zh_TW.UTF-8", "LANG": "en_US.UTF-8"}, "", core.LangTraditionalChinese},
		{"Chinese LANG", "", map[string]string{"LANG": "zh_CN.UTF-8"}, "en-US", core.LangChinese},
		{"OS language without env", "", nil, "en-US", core.LangEnglish},
		{"OS Chinese", "", nil, "zh-CN", core.LangChinese},
		{"nothing known", "", nil, "", core.LangChinese},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveCLILanguage(tt.config, env(tt.env), sys(tt.system)); got != tt.want {
				t.Errorf("resolveCLILanguage = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConfigFileLanguage(t *testing.T) {
	dir := t.TempDir()
	withLang := filepath.Join(dir, "lang.toml")
	if err := os.WriteFile(withLang, []byte("language = \"es\"\n\n[[projects]]\nname = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A config that does not validate still names its language.
	broken := filepath.Join(dir, "broken.toml")
	if err := os.WriteFile(broken, []byte("language = \"ja\"\n[display]\nmode = \"nonsense\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	notTOML := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(notTOML, []byte("language = \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{withLang: "es", broken: "ja", notTOML: "", filepath.Join(dir, "missing.toml"): "", "": ""} {
		if got := configFileLanguage(path); got != want {
			t.Errorf("configFileLanguage(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestSetupText_FollowsConfigLanguage(t *testing.T) {
	isolateHome(t)
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "zh_CN.UTF-8")
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("language = \"en\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config.ConfigPath = path

	want := core.NewI18n(core.LangEnglish).T(core.MsgSetupChecked)
	if got := setupText(core.MsgSetupChecked); got != want {
		t.Errorf("setupText = %q, want the English text %q", got, want)
	}
	// Without a language in the config, LANG decides.
	if err := os.WriteFile(path, []byte("[log]\nlevel = \"info\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want = core.NewI18n(core.LangChinese).T(core.MsgSetupChecked)
	if got := setupText(core.MsgSetupChecked); got != want {
		t.Errorf("setupText = %q, want the Chinese text %q", got, want)
	}
}

func TestConfigCommand_HelpPrintsUsage(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}, {"path", "--help"}, {"example", "-h"}, {"format", "--help"}} {
		out := captureStdout(t, func() { runConfig(args) })
		if !strings.Contains(out, "Usage: lark-agent-bot config") {
			t.Errorf("config %q printed %q, want the usage", args, out)
		}
	}
	out := captureStdout(t, func() { runConfigExample([]string{"--help"}) })
	if !strings.Contains(out, "Usage: lark-agent-bot config-example") || strings.Contains(out, "[[projects]]") {
		t.Errorf("config-example --help printed %q", out)
	}
}
