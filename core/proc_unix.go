//go:build unix

package core

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// setProcessGroup makes cmd the leader of its own process group and, when
// cmd's context is done, stops the whole group instead of only cmd itself
// (see stopProcessTree). Without it a timed-out `sh -c` dies but whatever it
// started keeps running: a cron job reported "timed out" while its Python
// child ran on for hours holding the job's lock (#70).
func setProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error { return stopProcessTree(cmd) }
}

// stopProcessTree sends SIGTERM to cmd's process group so scripts can clean
// up, then SIGKILLs whatever is still in the group after shellKillGrace. It
// returns without waiting for the escalation. Safe to call more than once.
// A cmd that is not a group leader (setProcessGroup not applied) gets a plain
// Kill, the old behaviour.
func stopProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pgid := cmd.Process.Pid
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		if !errors.Is(err, syscall.ESRCH) {
			return err
		}
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		return nil
	}
	go killProcessGroupAfter(pgid, shellKillGrace)
	return nil
}

// killProcessGroupAfter SIGKILLs the group if it still has members after
// grace. It polls so that it stops as soon as the group is gone: the kernel
// does not reuse a PID while a process group with that ID exists, so a group
// we just saw alive is still ours.
func killProcessGroupAfter(pgid int, grace time.Duration) {
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !processGroupAlive(pgid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	if processGroupAlive(pgid) {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}

func processGroupAlive(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
