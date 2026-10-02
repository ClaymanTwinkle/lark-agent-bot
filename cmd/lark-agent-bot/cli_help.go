package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// isHelpArg reports whether a is one of the help flags the flag package
// accepts: -h, -help or --help.
func isHelpArg(a string) bool {
	return a == "-h" || a == "-help" || a == "--help"
}

// helpRequested reports whether args ask for help. Commands check it before
// they do anything, so `<command> --help` only prints the usage.
func helpRequested(args []string) bool {
	for _, a := range args {
		if isHelpArg(a) {
			return true
		}
	}
	return false
}

// parseCommandFlags parses the flags of a command that takes no positional
// arguments. It returns flag.ErrHelp for -h / --help and leaves printing the
// usage to the caller, so the usage goes to stdout when it was asked for and
// to stderr after a mistake.
func parseCommandFlags(fs *flag.FlagSet, args []string) error {
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument: %s", fs.Arg(0))
	}
	return nil
}

// flagParseExit turns a parseCommandFlags error into what the command does
// next: for help it prints usage to stdout and returns 0, for a bad argument
// it prints the error and usage to stderr and returns 2. done is false when
// err is nil and the command should go on.
func flagParseExit(err error, usage string) (code int, done bool) {
	switch {
	case err == nil:
		return 0, false
	case errors.Is(err, flag.ErrHelp):
		fmt.Print(usage)
		return 0, true
	default:
		fmt.Fprintf(os.Stderr, "Error: %v\n\n%s", err, usage)
		return 2, true
	}
}

// exitWith ends the process with code unless it is 0, so a command that
// returns normally keeps running deferred cleanup.
func exitWith(code int) {
	if code != 0 {
		os.Exit(code)
	}
}

// commandArg quotes a value for a command line the user copies, when it
// contains spaces.
func commandArg(s string) string {
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}
