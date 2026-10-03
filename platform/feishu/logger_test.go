package feishu

import (
	"bytes"
	"context"
	"log"
	"log/slog"
	"strings"
	"testing"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
)

// setDefaultSlog makes l the default slog logger until the test ends.
// slog.SetDefault also points the log package at l's handler, and setting
// the original logger back does not undo that, so it is restored separately.
func setDefaultSlog(t *testing.T, l *slog.Logger) {
	t.Helper()
	prev, prevOut, prevFlags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(l)
	t.Cleanup(func() {
		slog.SetDefault(prev)
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
}

type recordingLarkLogger struct {
	debugCalls int
	debugArgs  [][]interface{}
}

func (l *recordingLarkLogger) Debug(_ context.Context, args ...interface{}) {
	l.debugCalls++
	cp := make([]interface{}, len(args))
	copy(cp, args)
	l.debugArgs = append(l.debugArgs, cp)
}

func (l *recordingLarkLogger) Info(context.Context, ...interface{})  {}
func (l *recordingLarkLogger) Warn(context.Context, ...interface{})  {}
func (l *recordingLarkLogger) Error(context.Context, ...interface{}) {}

var _ larkcore.Logger = (*recordingLarkLogger)(nil)

func TestSanitizingLogger_DropsHeartbeatDebugLogs(t *testing.T) {
	inner := &recordingLarkLogger{}
	logger := &sanitizingLogger{inner: inner}

	logger.Debug(context.Background(), "ping success", "conn_id=123")
	logger.Debug(context.Background(), "receive pong", "conn_id=123")

	if inner.debugCalls != 0 {
		t.Fatalf("debug calls = %d, want 0 for heartbeat noise", inner.debugCalls)
	}
}

func TestSanitizingLogger_KeepOtherDebugAndMaskSecrets(t *testing.T) {
	inner := &recordingLarkLogger{}
	logger := &sanitizingLogger{inner: inner}

	logger.Debug(context.Background(), "websocket message", "https://x.test/ws?token=abc&conn_id=123")

	if inner.debugCalls != 1 {
		t.Fatalf("debug calls = %d, want 1", inner.debugCalls)
	}
	if len(inner.debugArgs) != 1 || len(inner.debugArgs[0]) < 2 {
		t.Fatalf("debug args = %#v, want one call with args", inner.debugArgs)
	}

	urlArg, ok := inner.debugArgs[0][1].(string)
	if !ok {
		t.Fatalf("second arg type = %T, want string", inner.debugArgs[0][1])
	}
	if strings.Contains(urlArg, "token=abc") || strings.Contains(urlArg, "conn_id=123") {
		t.Fatalf("url arg not masked: %q", urlArg)
	}
}

func TestSlogLarkLogger_WritesMaskedSDKLinesToSlog(t *testing.T) {
	var buf bytes.Buffer
	setDefaultSlog(t, slog.New(slog.NewTextHandler(&buf, nil)))

	logger := &sanitizingLogger{inner: slogLarkLogger{}}
	logger.Info(context.Background(), "connected to wss://msg-frontier.feishu.cn/ws/v2?ticket=abc&aid=1")
	logger.Error(context.Background(), "connect failed, err: ", "timeout")

	out := buf.String()
	for _, want := range []string{
		`level=INFO msg="feishu sdk" message="connected to wss://msg-frontier.feishu.cn/ws/v2?ticket=***&aid=1"`,
		`level=ERROR msg="feishu sdk" message="connect failed, err: timeout"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("slog output missing %q:\n%s", want, out)
		}
	}
}
