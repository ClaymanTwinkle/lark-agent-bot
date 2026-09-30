package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// relayPeerGrace is how much longer the calling process waits for a peer than
// the relay timeout it asks the peer to enforce, so a partial response the peer
// returns at its deadline still makes it back before the caller gives up.
const relayPeerGrace = 10 * time.Second

// relayPeerJoinTimeout bounds the binding mirror call made during /bind.
const relayPeerJoinTimeout = 5 * time.Second

// RelayPeerRegistry records which local lark-agent-bot process serves each
// project, so relay can reach projects that run in another process on the same
// machine. Each project is one JSON file in dir pointing at that process's API
// socket.
type RelayPeerRegistry struct {
	dir string
}

type relayPeerEntry struct {
	Project   string    `json:"project"`
	Socket    string    `json:"socket"`
	PID       int       `json:"pid"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NewRelayPeerRegistry returns a registry stored in dir.
func NewRelayPeerRegistry(dir string) *RelayPeerRegistry {
	return &RelayPeerRegistry{dir: dir}
}

// DefaultRelayPeersDir returns the per-user directory shared by every
// lark-agent-bot process on this machine, or "" when the home directory is
// unknown.
func DefaultRelayPeersDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".lark-agent-bot", "relay-peers")
}

// Register records that project is served through socket.
func (r *RelayPeerRegistry) Register(project, socket string) error {
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return fmt.Errorf("relay peers: create dir: %w", err)
	}
	if prev, ok := r.read(project); ok && prev.Socket != socket {
		slog.Warn("relay peers: project was registered by another process, taking it over",
			"project", project, "previous_socket", prev.Socket, "socket", socket)
	}
	data, err := json.MarshalIndent(relayPeerEntry{
		Project:   project,
		Socket:    socket,
		PID:       os.Getpid(),
		UpdatedAt: time.Now(),
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("relay peers: marshal %s: %w", project, err)
	}
	if err := AtomicWriteFile(r.entryPath(project), data, 0o644); err != nil {
		return fmt.Errorf("relay peers: write %s: %w", project, err)
	}
	return nil
}

// Unregister removes the entry for project only if it still points at socket,
// so a process that shuts down never deletes an entry another process took
// over.
func (r *RelayPeerRegistry) Unregister(project, socket string) {
	entry, ok := r.read(project)
	if !ok || entry.Socket != socket {
		return
	}
	if err := os.Remove(r.entryPath(project)); err != nil && !os.IsNotExist(err) {
		slog.Warn("relay peers: remove entry failed", "project", project, "error", err)
	}
}

// Lookup returns the socket serving project. Entries whose socket file is gone
// (the process exited) are treated as missing.
func (r *RelayPeerRegistry) Lookup(project string) (string, bool) {
	entry, ok := r.read(project)
	if !ok || entry.Socket == "" {
		return "", false
	}
	if _, err := os.Stat(entry.Socket); err != nil {
		return "", false
	}
	return entry.Socket, true
}

// Projects lists every registered project name, sorted.
func (r *RelayPeerRegistry) Projects() []string {
	files, err := os.ReadDir(r.dir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("relay peers: list dir failed", "dir", r.dir, "error", err)
		}
		return nil
	}
	var names []string
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(r.dir, f.Name()))
		if err != nil {
			continue
		}
		var entry relayPeerEntry
		if json.Unmarshal(data, &entry) != nil || entry.Project == "" {
			continue
		}
		names = append(names, entry.Project)
	}
	sort.Strings(names)
	return names
}

func (r *RelayPeerRegistry) read(project string) (relayPeerEntry, bool) {
	path := r.entryPath(project)
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("relay peers: read entry failed", "path", path, "error", err)
		}
		return relayPeerEntry{}, false
	}
	var entry relayPeerEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		slog.Warn("relay peers: invalid entry", "path", path, "error", err)
		return relayPeerEntry{}, false
	}
	// Different project names can map to the same file name; the stored name
	// is authoritative.
	if entry.Project != project {
		return relayPeerEntry{}, false
	}
	return entry, true
}

// entryPath maps a project name to a file name that is valid on every OS.
func (r *RelayPeerRegistry) entryPath(project string) string {
	var b strings.Builder
	for _, c := range project {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
			b.WriteRune(c)
		default:
			b.WriteByte('_')
		}
	}
	return filepath.Join(r.dir, b.String()+".json")
}

// RelayPeerRequest is a relay request forwarded to the process that runs the
// target project (POST /relay/handle).
type RelayPeerRequest struct {
	From            string `json:"from"`
	FromName        string `json:"from_name"`
	To              string `json:"to"`
	ToName          string `json:"to_name"`
	SessionKey      string `json:"session_key"`
	Message         string `json:"message"`
	GroupSessionKey string `json:"group_session_key"`
	Visibility      string `json:"visibility"`
	// TimeoutSecs is enforced by the peer so it can still return a partial
	// response at the deadline; 0 = no limit.
	TimeoutSecs int `json:"timeout_secs"`
	Depth       int `json:"depth"`
}

// RelayJoinRequest asks a peer process to add projects to a chat binding
// (POST /relay/join).
type RelayJoinRequest struct {
	Platform string   `json:"platform"`
	ChatID   string   `json:"chat_id"`
	Projects []string `json:"projects"`
}

// postRelayPeer POSTs payload as JSON to path on the API socket of another
// lark-agent-bot process and decodes the JSON reply into out (if non-nil).
func postRelayPeer(ctx context.Context, socket, path string, payload, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}
	defer transport.CloseIdleConnections()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix"+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return fmt.Errorf("reach peer process: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read peer reply: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", strings.TrimSpace(string(data)))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode peer reply: %w", err)
		}
	}
	return nil
}
