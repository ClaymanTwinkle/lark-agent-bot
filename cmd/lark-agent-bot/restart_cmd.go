package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

var errRestartUsage = errors.New("show restart usage")

type restartOptions struct {
	project    string
	all        bool
	now        bool
	sessionKey string
	dataDir    string
}

// restartTarget is one lark-agent-bot process to restart, reached through
// its API socket.
type restartTarget struct {
	project    string // "" lets the process pick
	socket     string
	sessionKey string // chat that gets the success notice; only for this bot
}

func (t restartTarget) label() string {
	if t.project != "" {
		return t.project
	}
	return t.socket
}

// runRestart asks running lark-agent-bot processes to restart. Unless --now
// is given, each restart waits for the work in progress in that process,
// including the agent turn that runs this command, to finish first.
func runRestart(args []string) {
	opts, err := parseRestartArgs(args)
	if errors.Is(err, errRestartUsage) {
		printRestartUsage()
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
		printRestartUsage()
		os.Exit(1)
	}

	var peers []core.RelayPeer
	if dir := core.DefaultRelayPeersDir(); dir != "" {
		peers = core.NewRelayPeerRegistry(dir).LivePeers()
	}
	targets, err := restartTargets(opts, strings.TrimSpace(os.Getenv("CC_PROJECT")), resolveSocketPath(opts.dataDir), peers)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	failed := false
	for _, t := range targets {
		msg, err := requestRestart(t, opts.now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", t.label(), err)
			failed = true
			continue
		}
		fmt.Printf("%s: %s\n", t.label(), msg)
	}
	if failed {
		os.Exit(1)
	}
}

func parseRestartArgs(args []string) (restartOptions, error) {
	opts := restartOptions{sessionKey: sessionKeyFromEnv()}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, hasValue := strings.Cut(arg, "=")
		switch name {
		case "-h", "--help":
			return opts, errRestartUsage
		case "--now":
			opts.now = true
			continue
		case "--all":
			opts.all = true
			continue
		case "--project", "--session-key", "--data-dir":
		default:
			return opts, fmt.Errorf("unknown argument: %s", arg)
		}
		if !hasValue {
			if i+1 >= len(args) {
				return opts, fmt.Errorf("missing value for %s", name)
			}
			i++
			value = args[i]
		}
		switch name {
		case "--project":
			opts.project = value
		case "--session-key":
			opts.sessionKey = value
		case "--data-dir":
			opts.dataDir = value
		}
	}
	if opts.all && opts.project != "" {
		return opts, errors.New("--all and --project cannot be combined")
	}
	return opts, nil
}

// restartTargets picks the processes to restart: this bot (ownProject,
// served at ownSocket), the process serving --project, or with --all every
// live process in the peer registry, this bot last.
func restartTargets(opts restartOptions, ownProject, ownSocket string, peers []core.RelayPeer) ([]restartTarget, error) {
	self := restartTarget{project: ownProject, socket: ownSocket, sessionKey: opts.sessionKey}
	switch {
	case opts.all:
		var targets []restartTarget
		seen := map[string]bool{ownSocket: true}
		for _, p := range peers {
			if seen[p.Socket] {
				continue
			}
			seen[p.Socket] = true
			targets = append(targets, restartTarget{project: p.Project, socket: p.Socket})
		}
		if socketExists(ownSocket) {
			targets = append(targets, self)
		}
		if len(targets) == 0 {
			return nil, errors.New("no running lark-agent-bot found on this machine")
		}
		return targets, nil
	case opts.project != "" && opts.project != ownProject:
		for _, p := range peers {
			if p.Project == opts.project {
				return []restartTarget{{project: p.Project, socket: p.Socket}}, nil
			}
		}
		return nil, fmt.Errorf("project %q is not running on this machine (not in the relay peer registry)", opts.project)
	default:
		if !socketExists(ownSocket) {
			return nil, fmt.Errorf("lark-agent-bot is not running (socket not found: %s)", ownSocket)
		}
		return []restartTarget{self}, nil
	}
}

func socketExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// requestRestart posts the restart to one process and describes when it
// will happen.
func requestRestart(t restartTarget, now bool) (string, error) {
	payload, err := json.Marshal(core.RestartAPIRequest{Project: t.project, SessionKey: t.sessionKey, Now: now})
	if err != nil {
		return "", fmt.Errorf("encode request: %w", err)
	}
	resp, err := apiPost(t.socket, "/restart", payload)
	if err != nil {
		return "", fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	text := strings.TrimSpace(string(body))

	switch {
	case resp.StatusCode == http.StatusNotFound && strings.Contains(text, "page not found"):
		return "", errors.New("this bot runs a version without the restart API; send /restart in its chat instead")
	case resp.StatusCode != http.StatusOK:
		return "", errors.New(text)
	}
	var out core.RestartAPIResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("decode reply: %w", err)
	}
	if out.Busy > 0 && out.MaxWaitSecs > 0 {
		return fmt.Sprintf("restarts once the %d task(s) in progress finish (at most %d min); a notice follows in the chat",
			out.Busy, int((time.Duration(out.MaxWaitSecs)*time.Second).Round(time.Minute)/time.Minute)), nil
	}
	return "restarting now", nil
}

func printRestartUsage() {
	fmt.Println(`Usage: lark-agent-bot restart [--now] [--all | --project NAME]

Restarts lark-agent-bot, e.g. after an update or a rebuild. The restart waits
until the work in progress (including the agent turn running this command)
has finished, at most upgrade_restart_wait_mins, then posts a "restart
successful" notice to the chat in CC_SESSION.

Options:
  --now               Restart at once, cutting off the work in progress
  --all               Also restart the other lark-agent-bot processes on this
                      machine (bots sharing one program file must all restart
                      to run a new version)
  --project NAME      Restart the bot serving project NAME on this machine
  --session-key KEY   Chat for the notice (default: $CC_SESSION)
  --data-dir DIR      This bot's data directory (default: $CC_DATA_DIR)

To stop or restart a bot from outside, use "lark-agent-bot daemon restart".`)
}
