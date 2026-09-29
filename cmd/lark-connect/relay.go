package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

func runRelay(args []string) {
	if len(args) == 0 {
		printRelayUsage()
		return
	}
	switch args[0] {
	case "send":
		runRelaySend(args[1:])
	case "list", "ls":
		runRelayList(args[1:])
	case "--help", "-h", "help":
		printRelayUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown relay subcommand: %s\n", args[0])
		printRelayUsage()
		os.Exit(1)
	}
}

func runRelaySend(args []string) {
	var from, to, sessionKey, message, dataDir string

	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--from", "-f":
			if i+1 < len(args) {
				i++
				from = args[i]
			}
		case "--to", "-t":
			if i+1 < len(args) {
				i++
				to = args[i]
			}
		case "--session-key", "--session", "-s":
			if i+1 < len(args) {
				i++
				sessionKey = args[i]
			}
		case "--message", "-m":
			if i+1 < len(args) {
				i++
				message = args[i]
			}
		case "--data-dir":
			if i+1 < len(args) {
				i++
				dataDir = args[i]
			}
		case "--help", "-h":
			printRelaySendUsage()
			return
		default:
			positional = append(positional, args[i])
		}
	}

	if from == "" {
		from = os.Getenv("CC_PROJECT")
	}
	if sessionKey == "" {
		sessionKey = sessionKeyFromEnv()
	}
	if message == "" && len(positional) > 0 {
		if to == "" && len(positional) >= 2 {
			to = positional[0]
			message = strings.Join(positional[1:], " ")
		} else {
			message = strings.Join(positional, " ")
		}
	}

	if to == "" || message == "" {
		fmt.Fprintln(os.Stderr, "Error: target project (--to) and message are required")
		printRelaySendUsage()
		os.Exit(1)
	}
	if sessionKey == "" {
		fmt.Fprintln(os.Stderr, "Error: session key is required (set CC_SESSION or use --session-key)")
		os.Exit(1)
	}

	sockPath := resolveSocketPath(dataDir)
	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: lark-connect is not running (socket not found: %s)\n", sockPath)
		os.Exit(1)
	}

	payload, _ := json.Marshal(map[string]any{
		"from":        from,
		"to":          to,
		"session_key": sessionKey,
		"message":     message,
		"depth":       relayDepthFromEnv(),
	})

	resp, err := apiPost(sockPath, "/relay/send", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error: %s\n", strings.TrimSpace(string(body)))
		os.Exit(1)
	}

	var result struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Fprintf(os.Stderr, "Error: decode response: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(result.Response)
}

// relayDepthFromEnv returns how many relay hops led to the current agent
// session (set by lark-connect as CC_RELAY_DEPTH), 0 outside a relay.
func relayDepthFromEnv() int {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("CC_RELAY_DEPTH")))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func runRelayList(args []string) {
	var from, sessionKey, dataDir string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--from", "-f":
			if i+1 < len(args) {
				i++
				from = args[i]
			}
		case "--session-key", "--session", "-s":
			if i+1 < len(args) {
				i++
				sessionKey = args[i]
			}
		case "--data-dir":
			if i+1 < len(args) {
				i++
				dataDir = args[i]
			}
		case "--help", "-h":
			printRelayListUsage()
			return
		}
	}
	if from == "" {
		from = os.Getenv("CC_PROJECT")
	}
	if sessionKey == "" {
		sessionKey = sessionKeyFromEnv()
	}
	if sessionKey == "" {
		fmt.Fprintln(os.Stderr, "Error: session key is required (set CC_SESSION or use --session-key)")
		os.Exit(1)
	}

	sockPath := resolveSocketPath(dataDir)
	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: lark-connect is not running (socket not found: %s)\n", sockPath)
		os.Exit(1)
	}

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
	}
	query := url.Values{"session_key": {sessionKey}, "from": {from}}
	resp, err := client.Get("http://unix/relay/targets?" + query.Encode())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error: %s\n", strings.TrimSpace(string(body)))
		os.Exit(1)
	}
	var result struct {
		Bound     []string `json:"bound"`
		Available []string `json:"available"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Fprintf(os.Stderr, "Error: decode response: %v\n", err)
		os.Exit(1)
	}

	if len(result.Bound) > 0 {
		fmt.Println("Bots you can relay to in this chat:")
		for _, name := range result.Bound {
			fmt.Println("  " + name)
		}
		return
	}
	fmt.Println("No bots are bound in this chat, so relay send will fail here.")
	if len(result.Available) > 0 {
		fmt.Printf("Running bots that can be bound: %s\n", strings.Join(result.Available, ", "))
		fmt.Println("Ask the user to send \"/bind <name>\" to you in this group chat first.")
	}
}

func printRelayUsage() {
	fmt.Println(`Usage: lark-connect relay <command> [options]

Commands:
  send      Send a message to another bot via relay
  list      List the bots you can relay to in this chat

Run 'lark-connect relay <command> --help' for details.`)
}

func printRelayListUsage() {
	fmt.Println(`Usage: lark-connect relay list [options]

List the bots bound with this bot in the current chat (valid --to values for
relay send), including bots served by other lark-connect processes.

Options:
  -f, --from <project>       Source project (auto-detected from CC_PROJECT env)
  -s, --session-key <key>    Session key (auto-detected from CC_SESSION env)
      --data-dir <path>      Data directory (default: ~/.lark-connect)
  -h, --help                 Show this help`)
}

func printRelaySendUsage() {
	fmt.Println(`Usage: lark-connect relay send [options] [<target_project> <message>]

Send a message to another bot and wait for the response.

Options:
  -f, --from <project>       Source project (auto-detected from CC_PROJECT env)
  -t, --to <project>         Target bot project name
  -s, --session-key <key>    Session key (auto-detected from CC_SESSION env)
  -m, --message <text>       Message to send
      --data-dir <path>      Data directory (default: ~/.lark-connect)
  -h, --help                 Show this help

Examples:
  lark-connect relay send --to claude-bot "What's the weather today?"
  lark-connect relay send claude-bot What is the weather today`)
}
