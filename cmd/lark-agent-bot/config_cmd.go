package main

import (
	"flag"
	"fmt"
	"os"

	larkagentbot "github.com/ClaymanTwinkle/lark-agent-bot"
	"github.com/ClaymanTwinkle/lark-agent-bot/config"
)

func runConfig(args []string) {
	if len(args) == 0 {
		printConfigUsage()
		os.Exit(1)
	}
	if args[0] == "help" || helpRequested(args) {
		fmt.Print(configUsage)
		return
	}
	switch args[0] {
	case "example":
		fmt.Print(larkagentbot.ConfigExampleTOML)
	case "format", "fmt":
		runConfigFormat(args[1:])
	case "path":
		fmt.Println(resolveConfigPath(""))
	default:
		fmt.Fprintf(os.Stderr, "Unknown config subcommand: %s\n", args[0])
		printConfigUsage()
		os.Exit(1)
	}
}

// runConfigExample is the deprecated `config-example` command.
func runConfigExample(args []string) {
	if helpRequested(args) {
		fmt.Println(`Usage: lark-agent-bot config-example

Deprecated: use 'lark-agent-bot config example', which prints the same
complete annotated config.toml example.`)
		return
	}
	fmt.Print(larkagentbot.ConfigExampleTOML)
}

func runConfigFormat(args []string) {
	fs := flag.NewFlagSet("config format", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config file (default: auto-detect)")
	_ = fs.Parse(args)

	path := resolveConfigPath(*configPath)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Config file not found: %s\n", path)
		os.Exit(1)
	}

	if err := config.FormatConfigFile(path); err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Formatted %s\n", path)
}

const configUsage = `Usage: lark-agent-bot config <subcommand>

Subcommands:
  example    Print a complete annotated config.toml example
  format     Format the config file (alias: fmt)
  path       Print the resolved config file path

Flags for 'format':
  --config <path>   Path to config file (default: auto-detect)

Examples:
  lark-agent-bot config example              Print example config
  lark-agent-bot config example > config.toml  Save example config
  lark-agent-bot config format               Format default config file
  lark-agent-bot config fmt --config /path/to/config.toml
`

// printConfigUsage prints the usage after a mistake.
func printConfigUsage() {
	fmt.Fprint(os.Stderr, configUsage)
}
