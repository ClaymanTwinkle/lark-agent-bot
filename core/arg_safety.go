package core

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// providerNamePattern is what a provider name may contain. Agents write the
// name into their own config files (Codex: a [model_providers.<name>] TOML
// table header), so it must not carry newlines, brackets or quotes.
var providerNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// ValidateProviderName reports whether name is usable as a provider name:
// 1-64 characters from A-Z, a-z, 0-9, '_', '.' and '-'.
func ValidateProviderName(name string) error {
	if !providerNamePattern.MatchString(name) {
		return fmt.Errorf("invalid provider name %q: use 1-64 letters, digits, '_', '.' or '-'", name)
	}
	return nil
}

// modelNameForbidden are characters a model name never needs but cmd.exe
// interprets when the agent CLI is a batch-file shim (see CheckBatchArgs).
const modelNameForbidden = `&|<>^"%!`

// ValidateModelName rejects a model name that contains whitespace, control
// characters or any of & | < > ^ " % !. Model names are passed to the agent
// CLI as an argument (--model <name>), and names typed in chat that do not
// match a listed model are passed through verbatim. Characters used by real
// model IDs ("claude-opus-4-5[1m]", "openrouter/x", "us.anthropic.x:0") stay
// allowed. An empty name is left to the caller.
func ValidateModelName(name string) error {
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune(modelNameForbidden, r) {
			return fmt.Errorf("invalid model name %q: it must not contain whitespace, control characters or any of %s", name, modelNameForbidden)
		}
	}
	return nil
}

// batchMetachars are the characters cmd.exe acts on in a batch file's
// arguments: command chaining and redirection (& | < >), its escape
// character (^) and variable expansion (% and, with delayed expansion, !).
// A double quote is not listed: lark-agent-bot itself passes -c key="value"
// overrides, and with every character above refused a quote can only change
// how cmd.exe groups whitespace, not make it run anything.
const batchMetachars = `&|<>^%!`

// CheckBatchArgs refuses to start a batch file (.cmd or .bat, such as the
// shim npm installs for an agent CLI on Windows) with arguments cmd.exe
// would interpret. Windows runs batch files through cmd.exe, and os/exec
// quotes arguments for the C runtime, not for cmd.exe, so one of these
// characters in an argument could run another command. binPath must be the
// resolved executable (exec.Cmd.Path), not the bare name from config; any
// other executable is not checked.
func CheckBatchArgs(binPath string, args []string) error {
	ext := strings.ToLower(filepath.Ext(binPath))
	if ext != ".cmd" && ext != ".bat" {
		return nil
	}
	for i, arg := range args {
		for _, r := range arg {
			if strings.ContainsRune(batchMetachars, r) || (unicode.IsControl(r) && r != '\t') {
				return fmt.Errorf("refusing to run batch file %s: argument %d contains %q, which cmd.exe would interpret; "+
					"remove it or point the agent's cmd at a native executable", filepath.Base(binPath), i+1, r)
			}
		}
	}
	return nil
}
