//go:build !unix && !windows

package core

import (
	"errors"
	"os"
	"os/exec"
)

// setProcessGroup is a no-op where there are no process groups to use; a
// cancelled command falls back to exec's default Kill of cmd itself.
func setProcessGroup(cmd *exec.Cmd) {}

// stopProcessTree kills only cmd itself on platforms without process groups.
func stopProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}
