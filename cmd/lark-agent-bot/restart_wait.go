package main

import (
	"log/slog"
	"os"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// restartIdlePollInterval is how often a restart that waits for idle checks
// for work in progress.
const restartIdlePollInterval = 5 * time.Second

// workInProgress counts the sessions with work in progress across engines.
func workInProgress(engines []*core.Engine) int {
	n := 0
	for _, e := range engines {
		n += e.WorkInProgress()
	}
	return n
}

// awaitIdleForRestart holds a restart that waits for idle (the one after
// /upgrade) until no session has work in progress, since the restart stops
// every agent process. It stops waiting after req.MaxWait (no limit when 0).
// A request that does not wait (/restart, the management API) restarts at
// once; a later waiting request keeps waiting and takes over the
// post-restart notice. A signal shuts down without restarting, as it does
// without the wait: the result is then nil.
func awaitIdleForRestart(req core.RestartRequest, busy func() int, restartCh <-chan core.RestartRequest, sigCh <-chan os.Signal, poll time.Duration) *core.RestartRequest {
	n := busy()
	if n == 0 {
		return &req
	}
	slog.Info("restart: waiting for tasks in progress to finish", "busy", n, "max_wait", req.MaxWait)
	start := time.Now()
	var deadline <-chan time.Time
	if req.MaxWait > 0 {
		timer := time.NewTimer(req.MaxWait)
		defer timer.Stop()
		deadline = timer.C
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if busy() == 0 {
				slog.Info("restart: tasks finished, restarting", "waited", time.Since(start).Round(time.Second))
				return &req
			}
		case <-deadline:
			slog.Warn("restart: tasks still in progress after max wait, restarting anyway", "busy", busy(), "max_wait", req.MaxWait)
			return &req
		case next := <-restartCh:
			if !next.WaitIdle {
				slog.Info("restart: immediate restart requested while waiting", "session", next.SessionKey, "platform", next.Platform)
				return &next
			}
			req.SessionKey, req.Platform = next.SessionKey, next.Platform
		case <-sigCh:
			slog.Info("restart: signal received while waiting; shutting down without restart")
			return nil
		}
	}
}
