package main

import (
	"errors"
	"flag"
	"net/http"
	"strings"
	"testing"

	"github.com/ClaymanTwinkle/lark-agent-bot/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// blockHTTP fails the test on any HTTP request made through the default
// transport, which the update commands use.
func blockHTTP(t *testing.T) {
	t.Helper()
	old := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("unexpected HTTP request to %s", r.URL)
		return nil, errors.New("blocked by test")
	})
	t.Cleanup(func() { http.DefaultTransport = old })
}

func TestParseUpdateArgs(t *testing.T) {
	for _, args := range [][]string{{"--pre"}, {"--beta"}, {"-pre"}} {
		pre, err := parseUpdateArgs("update", args)
		if err != nil || !pre {
			t.Errorf("parseUpdateArgs(%q) = %v, %v; want true, nil", args, pre, err)
		}
	}
	if pre, err := parseUpdateArgs("update", nil); err != nil || pre {
		t.Errorf("parseUpdateArgs(nil) = %v, %v; want false, nil", pre, err)
	}
	for _, args := range [][]string{{"--help"}, {"-h"}, {"--pre", "--help"}} {
		if _, err := parseUpdateArgs("update", args); !errors.Is(err, flag.ErrHelp) {
			t.Errorf("parseUpdateArgs(%q) err = %v, want flag.ErrHelp", args, err)
		}
	}
	if _, err := parseUpdateArgs("update", []string{"--bogus"}); err == nil {
		t.Error("parseUpdateArgs(--bogus) succeeded")
	}
}

// Regression: `update --help` checked for and installed an update.
func TestUpdateCommands_HelpMakesNoRequest(t *testing.T) {
	blockHTTP(t)
	out := captureStdout(t, func() { runUpdate([]string{"--help"}) })
	if !strings.Contains(out, "Usage: lark-agent-bot update") || strings.Contains(out, "Checking for updates") {
		t.Errorf("update --help printed %q", out)
	}
	out = captureStdout(t, func() { checkUpdate([]string{"-h"}) })
	if !strings.Contains(out, "Usage: lark-agent-bot check-update") {
		t.Errorf("check-update -h printed %q", out)
	}
}

// Regression: `daemon install --help` failed with "unknown flag: --help", and
// no daemon subcommand took --help.
func TestRunDaemon_HelpDoesNotRunSubcommand(t *testing.T) {
	called := map[string]bool{}
	old := daemonCommands
	daemonCommands = map[string]func([]string){}
	for name := range old {
		daemonCommands[name] = func([]string) { called[name] = true }
	}
	t.Cleanup(func() { daemonCommands = old })

	for name := range old {
		for _, help := range []string{"--help", "-h"} {
			args := []string{name, "--config", "bot.toml", help}
			out := captureStdout(t, func() { runDaemon(args) })
			if called[name] {
				t.Errorf("daemon %q ran the subcommand", args)
			}
			if !strings.Contains(out, "Install flags:") {
				t.Errorf("daemon %q printed %q, want the usage", args, out)
			}
		}
	}
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}} {
		if out := captureStdout(t, func() { runDaemon(args) }); !strings.Contains(out, "Usage: lark-agent-bot daemon") {
			t.Errorf("daemon %q printed %q", args, out)
		}
	}

	runDaemon([]string{"status", "--name", "claude"})
	if !called["status"] {
		t.Error("daemon status did not run the subcommand")
	}
}

// Regression: `feishu setup --help` printed "Usage of feishu auto" with
// descriptions in two languages.
func TestFeishuSetup_HelpPrintsUsage(t *testing.T) {
	isolateHome(t)
	for _, mode := range []string{feishuSetupModeAuto, feishuSetupModeNew, feishuSetupModeBind, "check"} {
		out := captureStdout(t, func() { runFeishuSetup([]string{"--help"}, mode) })
		if !strings.Contains(out, "Usage: lark-agent-bot feishu <command>") || strings.Contains(out, "Usage of") {
			t.Errorf("feishu %s --help printed %q", feishuSubcommandName(mode), out)
		}
		if config.ConfigPath != "" {
			t.Errorf("feishu %s --help resolved the config path %q", feishuSubcommandName(mode), config.ConfigPath)
		}
	}
}

func TestSubcommandHelp_DoesNotReachTheBot(t *testing.T) {
	// No bot's socket is there: without the help guard these would fail
	// with "lark-agent-bot is not running" and exit.
	isolateHome(t)
	t.Setenv("CC_DATA_DIR", t.TempDir())
	for name, run := range map[string]func([]string){
		"cron list":  runCronList,
		"cron del":   runCronDel,
		"cron info":  runCronInfo,
		"timer list": runTimerList,
		"timer del":  runTimerDel,
		"timer info": runTimerInfo,
	} {
		out := captureStdout(t, func() { run([]string{"--help"}) })
		if !strings.Contains(out, "Usage: lark-agent-bot "+strings.Fields(name)[0]) {
			t.Errorf("%s --help printed %q", name, out)
		}
	}
}

func TestTopLevelHelpCommand(t *testing.T) {
	if topLevelCommandHandlers["help"] == nil {
		t.Fatal("no help command")
	}
	out := captureStderr(t, func() { runTopLevelCommand([]string{"help"}) })
	if !strings.Contains(out, "doctor") || !strings.Contains(out, "--help") {
		t.Errorf("help printed %q", out)
	}
}
