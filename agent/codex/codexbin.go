package codex

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// resolveCodexBin returns the codex executable to spawn for the configured
// cmd, and whether it came from the Codex desktop app rather than PATH.
//
// It runs before every spawn, not once at startup: the Codex desktop app
// installs its CLI under a per-version directory and deletes the old one on
// update, so a path resolved at startup goes stale while lark-agent-bot keeps
// running. A bare cmd is looked up in PATH first; when the default "codex" is
// not there, the newest CLI bundled with the desktop app is used.
func resolveCodexBin(cmd string) (bin string, fromDesktop bool, err error) {
	if cmd == "" {
		cmd = "codex"
	}
	if path, err := exec.LookPath(cmd); err == nil {
		return path, false, nil
	}
	if cmd == "codex" {
		if path := newestDesktopCodex(desktopCodexBinRoot()); path != "" {
			return path, true, nil
		}
	}
	return "", false, fmt.Errorf("codex: %q CLI not found in PATH or the Codex desktop app, install with: npm install -g @openai/codex", cmd)
}

// desktopCodexBinRoot is where the Codex desktop app keeps its CLI, one
// directory per version: %LOCALAPPDATA%\OpenAI\Codex\bin\<hash>\codex.exe.
// Only the Windows layout is known; elsewhere it returns "".
func desktopCodexBinRoot() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return ""
	}
	return filepath.Join(localAppData, "OpenAI", "Codex", "bin")
}

// newestDesktopCodex returns the most recently written codex executable in
// root's subdirectories, or "" when there is none.
func newestDesktopCodex(root string) string {
	if root == "" {
		return ""
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	exeName := "codex"
	if runtime.GOOS == "windows" {
		exeName = "codex.exe"
	}
	var best string
	var bestInfo os.FileInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), exeName)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if bestInfo == nil || info.ModTime().After(bestInfo.ModTime()) ||
			(info.ModTime().Equal(bestInfo.ModTime()) && path > best) {
			best, bestInfo = path, info
		}
	}
	return best
}

// withBinDirOnPath adds bin's directory to the front of PATH in env, the way
// PATH looked when the desktop CLI was found through it, so anything the CLI
// looks up next to itself by name still resolves. The effective PATH is the
// last PATH entry in env (lark-agent-bot injects one per session), falling back
// to the process environment.
func withBinDirOnPath(env []string, bin string) []string {
	current := os.Getenv("PATH")
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && strings.EqualFold(k, "PATH") {
			current = v
			break
		}
	}
	dir := filepath.Dir(bin)
	if current != "" {
		dir += string(os.PathListSeparator) + current
	}
	return append(append([]string(nil), env...), "PATH="+dir)
}
