//go:build windows

package core

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// setProcessGroup starts cmd in a new process group and, when cmd's context
// is done, kills cmd together with its descendants (see stopProcessTree)
// instead of only cmd itself (#70).
func setProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
	cmd.Cancel = func() error { return stopProcessTree(cmd) }
}

// stopProcessTree kills cmd's descendant tree with taskkill /T /F. Windows has
// no SIGTERM a console program reliably honours, so there is no grace period.
// Falls back to Process.Kill when taskkill is unavailable.
func stopProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	output, err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).CombinedOutput()
	if err == nil || taskkillNotRunning(output) {
		return nil
	}
	if killErr := cmd.Process.Kill(); killErr == nil || errors.Is(killErr, os.ErrProcessDone) {
		return nil
	} else {
		return fmt.Errorf("taskkill failed: %w: %s; process kill fallback failed: %w", err, strings.TrimSpace(string(output)), killErr)
	}
}

func taskkillNotRunning(output []byte) bool {
	lower := bytes.ToLower(output)
	return bytes.Contains(lower, []byte("there is no running instance")) ||
		bytes.Contains(lower, []byte("not found"))
}
