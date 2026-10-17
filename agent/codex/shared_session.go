package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
	"github.com/gorilla/websocket"
)

var errSharedThreadUnavailable = errors.New("thread is not loaded in the shared daemon")

// AttachSession only connects to existing sessions. Auto discovery never loads
// an idle transcript into a different backend or starts a daemon as a side effect.
func (a *Agent) AttachSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	a.mu.RLock()
	backend, endpoint := normalizeBackend(a.backend), normalizeAppServerURL(a.appServerURL)
	workDir, home, mode := a.workDir, a.codexHome, a.mode
	a.mu.RUnlock()
	if backend != "app_server" || endpoint == "stdio://" || sessionID == "" || sessionID == core.ContinueSession {
		return nil, nil
	}
	s, err := newAppServerSession(ctx, "", endpoint, workDir, "", "", mode, sessionID, "", "", nil, home, "", "", "")
	if errors.Is(err, errSharedThreadUnavailable) {
		return nil, nil
	}
	if err != nil {
		// The engine must not silently create a new conversation when attaching
		// fails: the selected thread may still be running in the terminal.
		return nil, fmt.Errorf("%w: codex attach: %v", core.ErrSessionResumeUnavailable, err)
	}
	return s, nil
}

func daemonSocketPath(home string) string {
	return filepath.Join(resolveCodexHomeDir(home), "app-server-control", "app-server-control.sock")
}

func (s *appServerSession) connectShared() error {
	endpoint := s.url
	auto := endpoint == "auto"
	if auto {
		path := daemonSocketPath(s.codexHome)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return errSharedThreadUnavailable
		} else if err != nil {
			return fmt.Errorf("codex daemon socket: %w", err)
		}
		endpoint = "unix://" + path
	}
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second, Proxy: http.ProxyFromEnvironment}
	if strings.HasPrefix(endpoint, "unix://") {
		path := strings.TrimPrefix(endpoint, "unix://")
		if path == "" {
			path = daemonSocketPath(s.codexHome)
		}
		dialer.Proxy = nil
		dialer.NetDialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		}
		endpoint = "ws://localhost/"
	} else if !strings.HasPrefix(endpoint, "ws://") && !strings.HasPrefix(endpoint, "wss://") {
		return fmt.Errorf("codex app-server: unsupported transport (use auto, stdio://, unix://, ws:// or wss://)")
	}
	conn, resp, err := dialer.DialContext(s.ctx, endpoint, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		// A stale local socket means no shared backend is available. Explicit
		// endpoints never fall back to starting an unrelated process.
		if auto && s.ctx.Err() == nil {
			return fmt.Errorf("%w: %v", errSharedThreadUnavailable, err)
		}
		return fmt.Errorf("codex app-server connect: %w", err)
	}
	s.ws = conn
	s.shared = true
	s.wg.Add(2)
	go s.readWebSocket(conn)
	go func() {
		defer s.wg.Done()
		<-s.ctx.Done()
		_ = conn.Close()
	}()
	return nil
}

func (s *appServerSession) readWebSocket(conn *websocket.Conn) {
	defer s.wg.Done()
	defer s.cancel()
	defer s.closeEvents()
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			s.alive.Store(false)
			if s.ctx.Err() == nil {
				s.emitError(fmt.Errorf("codex app-server connection closed: %w", err))
			}
			s.rejectPending(err)
			s.rejectPendingApprovals(err)
			return
		}
		s.handleLine(data)
	}
}

func (s *appServerSession) requireLoadedThread(id string) error {
	var cursor *string
	for {
		var resp struct {
			Data       []string `json:"data"`
			NextCursor *string  `json:"nextCursor"`
		}
		if err := s.request("thread/loaded/list", map[string]any{"cursor": cursor, "limit": 100}, &resp); err != nil {
			return fmt.Errorf("codex loaded threads: %w", err)
		}
		for _, loaded := range resp.Data {
			if loaded == id {
				return nil
			}
		}
		if resp.NextCursor == nil || (cursor != nil && *cursor == *resp.NextCursor) {
			return errSharedThreadUnavailable
		}
		cursor = resp.NextCursor
	}
}

// refreshSharedTurn reads only the latest turn, including paginated threads.
// Never restore a stale snapshot over notifications received during the RPC.
func (s *appServerSession) refreshSharedTurn() error {
	s.stateMu.Lock()
	revision := s.turnRevision
	s.stateMu.Unlock()
	var metadata struct {
		Thread struct {
			Status struct {
				Type string `json:"type"`
			} `json:"status"`
		} `json:"thread"`
	}
	if err := s.request("thread/read", map[string]any{"threadId": s.CurrentSessionID(), "includeTurns": false}, &metadata); err != nil {
		return fmt.Errorf("codex thread status: %w", err)
	}
	var resp struct {
		Data []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	// A freshly created thread has no persisted turns; turns/list rejects it
	// until the first user message. Metadata reads work before materialization.
	if metadata.Thread.Status.Type == "active" {
		if err := s.request("thread/turns/list", map[string]any{
			"threadId": s.CurrentSessionID(), "limit": 1, "sortDirection": "desc", "itemsView": "summary",
		}, &resp); err != nil {
			return fmt.Errorf("codex active turn: %w", err)
		}
	}
	active := ""
	if len(resp.Data) > 0 && resp.Data[0].Status == "inProgress" {
		active = resp.Data[0].ID
	}
	s.stateMu.Lock()
	changed := s.turnRevision == revision && s.currentTurn != active
	if s.turnRevision == revision {
		s.currentTurn = active
	}
	s.stateMu.Unlock()
	if changed && active != "" {
		s.emit(core.Event{Type: core.EventTurnStarted, SessionID: s.CurrentSessionID()})
	}
	return nil
}

// Notifications for subagents and other threads may share the connection.
func (s *appServerSession) acceptsThread(params json.RawMessage) bool {
	var scope struct {
		ThreadID string `json:"threadId"`
	}
	if json.Unmarshal(params, &scope) != nil {
		return false
	}
	id := s.CurrentSessionID()
	return id == "" || scope.ThreadID == "" || scope.ThreadID == id
}

// A different client can answer an approval. Closing this channel wakes the
// engine without sending a conflicting decision back to the shared server.
func (s *appServerSession) registerApproval(id string) (chan core.PermissionResult, chan struct{}) {
	s.approvalsMu.Lock()
	defer s.approvalsMu.Unlock()
	if s.pendingApprovals == nil {
		s.pendingApprovals = make(map[string]chan core.PermissionResult)
	}
	if s.approvalResolved == nil {
		s.approvalResolved = make(map[string]chan struct{})
	}
	ch, resolved := make(chan core.PermissionResult, 1), make(chan struct{})
	s.pendingApprovals[id], s.approvalResolved[id] = ch, resolved
	return ch, resolved
}

func (s *appServerSession) resolveApproval(id string) {
	s.approvalsMu.Lock()
	defer s.approvalsMu.Unlock()
	delete(s.pendingApprovals, id)
	if ch := s.approvalResolved[id]; ch != nil {
		delete(s.approvalResolved, id)
		close(ch)
	}
}
