//go:build unix

package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// backgroundChild is a shell script that starts a long sleep with its output
// redirected (so it does not hold the job's pipes, like a script that logs to
// a file), records the sleep's PID and waits for it.
func backgroundChild(pidFile string) string {
	return "sleep 30 >/dev/null 2>&1 & echo $! > " + pidFile + "; wait"
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("child never wrote its pid to %s", path)
	return 0
}

func processGone(pid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// cleanupPID makes sure a test that fails does not leave a 30s sleep behind.
func cleanupPID(t *testing.T, pid *int) {
	t.Cleanup(func() {
		if *pid > 0 {
			_ = syscall.Kill(*pid, syscall.SIGKILL)
		}
	})
}

func TestShellExecCommand_OwnProcessGroup(t *testing.T) {
	cmd := shellExecCommand(context.Background(), "sh", "-c", "", "true")
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("shell command does not start its own process group")
	}
	if cmd.Cancel == nil {
		t.Fatal("shell command keeps exec's default Cancel, which kills only the shell")
	}
}

// Regression test for #70 on the CombinedOutput path (webhook, hooks): the
// context timing out must take the shell's children down with it.
func TestShellExecCommand_TimeoutStopsChildren(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	cmd := shellExecCommand(ctx, "sh", "-c", "", backgroundChild(pidFile))
	var pid int
	cleanupPID(t, &pid)
	done := make(chan error, 1)
	go func() { _, err := cmd.CombinedOutput(); done <- err }()
	pid = readPID(t, pidFile)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("command did not return after its context timed out")
	}
	if !processGone(pid, 3*time.Second) {
		t.Fatalf("child %d survived the timeout", pid)
	}
}

// A process group that ignores SIGTERM is SIGKILLed after shellKillGrace.
func TestStopProcessTree_EscalatesToSIGKILL(t *testing.T) {
	old := shellKillGrace
	shellKillGrace = 300 * time.Millisecond
	t.Cleanup(func() { shellKillGrace = old })

	pidFile := filepath.Join(t.TempDir(), "pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := shellExecCommand(ctx, "sh", "-c", "", "trap '' TERM; "+backgroundChild(pidFile))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var pid int
	cleanupPID(t, &pid)
	waited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(waited) }()
	pid = readPID(t, pidFile)

	start := time.Now()
	if err := stopProcessTree(cmd); err != nil {
		t.Fatalf("stopProcessTree: %v", err)
	}
	if !processGone(pid, 5*time.Second) {
		t.Fatalf("TERM-ignoring child %d was never killed", pid)
	}
	if elapsed := time.Since(start); elapsed < shellKillGrace {
		t.Errorf("child gone after %v, before the %v grace period: SIGTERM was not ignored as intended", elapsed, shellKillGrace)
	}
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("shell was not reaped after the group was killed")
	}
}

func TestStopProcessTree_NotStarted(t *testing.T) {
	cmd := shellExecCommand(context.Background(), "sh", "-c", "", "true")
	if err := stopProcessTree(cmd); err != nil {
		t.Errorf("un-started cmd: got %v", err)
	}
	if err := stopProcessTree(nil); err != nil {
		t.Errorf("nil cmd: got %v", err)
	}
}

// Regression test for #70 on the cron and timer exec paths: stopping the job
// (here by stopping the engine, which cancels the job's context the same way
// a timeout does) must stop the children of the job's shell as well.
func TestExecShellJobs_StopKillsChildren(t *testing.T) {
	cases := []struct {
		name string
		run  func(e *Engine, p Platform, exec, dir string) error
	}{
		{"cron", func(e *Engine, p Platform, exec, dir string) error {
			return e.executeCronShell(p, nil, &CronJob{Exec: exec, WorkDir: dir})
		}},
		{"timer", func(e *Engine, p Platform, exec, dir string) error {
			return e.executeTimerShell(p, nil, &TimerJob{Exec: exec, WorkDir: dir})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			pidFile := filepath.Join(dir, "pid")
			e := newTestEngine()
			p := &stubPlatformEngine{n: "test"}
			var pid int
			cleanupPID(t, &pid)
			done := make(chan error, 1)
			go func() { done <- tc.run(e, p, backgroundChild(pidFile), dir) }()
			pid = readPID(t, pidFile)

			e.cancel()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("job did not return after being stopped")
			}
			if !processGone(pid, 3*time.Second) {
				t.Fatalf("child %d kept running after the job was stopped", pid)
			}
		})
	}
}

func TestShellJobContext(t *testing.T) {
	ctx, cancel := shellJobContext(context.Background(), 0)
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Error("timeout 0 (no limit) got a deadline")
	}
	ctx2, cancel2 := shellJobContext(context.Background(), time.Minute)
	defer cancel2()
	if d, ok := ctx2.Deadline(); !ok || time.Until(d) > time.Minute {
		t.Errorf("timeout 1m: deadline %v, %v", d, ok)
	}
}
