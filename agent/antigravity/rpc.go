package antigravity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
)

type rpcOutcome struct {
	result json.RawMessage
	err    *rpcErrPayload
}

type rpcErrPayload struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcErrPayload) Error() string {
	if e == nil {
		return "json-rpc error"
	}
	return fmt.Sprintf("json-rpc %d: %s", e.Code, e.Message)
}

type rpcNotifyHandler func(method string, params json.RawMessage)
type rpcRequestHandler func(method string, id json.RawMessage, params json.RawMessage)

// transport implements newline-delimited JSON-RPC 2.0 over a pair of streams,
// the framing `agy acp` uses on stdio. Notifications and server requests are
// dispatched synchronously from readLoop, in the order they arrive, so every
// session/update for a turn is handled before that turn's session/prompt
// response is delivered to its caller.
type transport struct {
	in  *bufio.Reader
	out io.Writer
	mu  sync.Mutex
	enc *json.Encoder

	nextID atomic.Int64

	pendingMu sync.Mutex
	pending   map[string]chan rpcOutcome
	closedErr error // set once the read side is gone; new calls fail fast

	onNotif rpcNotifyHandler
	onReq   rpcRequestHandler
}

func newTransport(in io.Reader, out io.Writer, onNotif rpcNotifyHandler, onReq rpcRequestHandler) *transport {
	return &transport{
		in:      bufio.NewReader(in),
		out:     out,
		enc:     json.NewEncoder(out),
		pending: make(map[string]chan rpcOutcome),
		onNotif: onNotif,
		onReq:   onReq,
	}
}

// readLoop reads until the stream ends, so the peer's output is always
// drained (a peer blocked writing to a full pipe could never exit).
func (t *transport) readLoop() {
	for {
		line, err := t.readLine()
		if err != nil {
			if err != io.EOF {
				slog.Debug("antigravity: read error", "error", err)
			}
			t.cancelAll(fmt.Errorf("antigravity: read closed: %w", err))
			return
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		t.dispatchLine(line)
	}
}

func (t *transport) readLine() ([]byte, error) {
	line, err := t.in.ReadBytes('\n')
	if err != nil {
		return line, err
	}
	return bytes.TrimSuffix(line, []byte("\r")), nil
}

func (t *transport) dispatchLine(line []byte) {
	var env struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
		Result  json.RawMessage `json:"result"`
		Error   *rpcErrPayload  `json:"error"`
	}
	if err := json.Unmarshal(line, &env); err != nil {
		slog.Debug("antigravity: skip non-json line", "line", string(line))
		return
	}
	if env.Method != "" {
		if isJSONRPCIDNullOrAbsent(env.ID) {
			if t.onNotif != nil {
				t.onNotif(env.Method, env.Params)
			}
			return
		}
		if t.onReq != nil {
			t.onReq(env.Method, env.ID, env.Params)
		}
		return
	}
	if !isJSONRPCIDNullOrAbsent(env.ID) {
		t.completePending(env.ID, env.Result, env.Error)
	}
}

func isJSONRPCIDNullOrAbsent(id json.RawMessage) bool {
	if len(id) == 0 {
		return true
	}
	return bytes.Equal(bytes.TrimSpace(id), []byte("null"))
}

func jsonIDKey(id json.RawMessage) string {
	id = bytes.TrimSpace(id)
	var n json.Number
	if json.Unmarshal(id, &n) == nil {
		return string(n)
	}
	var s string
	if json.Unmarshal(id, &s) == nil {
		return s
	}
	return string(id)
}

func (t *transport) completePending(id json.RawMessage, result json.RawMessage, rpcErr *rpcErrPayload) {
	key := jsonIDKey(id)
	t.pendingMu.Lock()
	ch, ok := t.pending[key]
	delete(t.pending, key)
	t.pendingMu.Unlock()
	if !ok {
		slog.Debug("antigravity: unmatched rpc response", "id", key)
		return
	}
	select {
	case ch <- rpcOutcome{result: result, err: rpcErr}:
	default:
	}
}

// closed reports whether the peer's output has ended (it exited or closed stdout).
func (t *transport) closed() bool {
	t.pendingMu.Lock()
	defer t.pendingMu.Unlock()
	return t.closedErr != nil
}

func (t *transport) cancelAll(err error) {
	t.pendingMu.Lock()
	defer t.pendingMu.Unlock()
	if t.closedErr == nil {
		t.closedErr = err
	}
	msg := err.Error()
	for k, ch := range t.pending {
		select {
		case ch <- rpcOutcome{err: &rpcErrPayload{Code: -32000, Message: msg}}:
		default:
		}
		delete(t.pending, k)
	}
}

func (t *transport) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := t.nextID.Add(1)
	key := fmt.Sprintf("%d", id)
	ch := make(chan rpcOutcome, 1)
	t.pendingMu.Lock()
	if t.closedErr != nil {
		err := t.closedErr
		t.pendingMu.Unlock()
		return nil, fmt.Errorf("antigravity: %s: %w", method, err)
	}
	t.pending[key] = ch
	t.pendingMu.Unlock()

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	if err := t.writeJSON(req); err != nil {
		t.pendingMu.Lock()
		delete(t.pending, key)
		t.pendingMu.Unlock()
		return nil, fmt.Errorf("antigravity: write %s: %w", method, err)
	}
	select {
	case <-ctx.Done():
		t.pendingMu.Lock()
		delete(t.pending, key)
		t.pendingMu.Unlock()
		return nil, ctx.Err()
	case out := <-ch:
		if out.err != nil {
			return nil, out.err
		}
		return out.result, nil
	}
}

func (t *transport) sendNotification(method string, params any) error {
	return t.writeJSON(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (t *transport) writeJSON(v any) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.enc.Encode(v)
}

type rpcResponseMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErrPayload  `json:"error,omitempty"`
}

func (t *transport) respondSuccess(id json.RawMessage, result any) error {
	return t.writeJSON(rpcResponseMsg{JSONRPC: "2.0", ID: id, Result: result})
}

func (t *transport) respondError(id json.RawMessage, code int, message string) error {
	return t.writeJSON(rpcResponseMsg{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcErrPayload{Code: code, Message: message},
	})
}
