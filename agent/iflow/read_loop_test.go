package iflow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
	"github.com/ClaymanTwinkle/lark-agent-bot/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.RunCLI()
	os.Exit(m.Run())
}

// Exercise timeout -> child cancellation -> result -> ready for another turn
// on every OS. Only the terminal boundary is replaced with an ordinary pipe.
func TestIFlowReadLoop_ToolTimeoutClearsBusyBeforeResult(t *testing.T) {
	workDir := t.TempDir()
	s, err := newIFlowSession(context.Background(), "unused", nil, workDir, "", "default", "", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	turn := &iflowTurn{
		cancel: cancel, startedAt: time.Now(), mode: "default", pendingTimeout: 80 * time.Millisecond,
		transcriptPath: filepath.Join(workDir, "session-test.jsonl"), processDone: make(chan struct{}),
		pendingToolIDs: make(map[string]struct{}), pendingTools: make(map[string]iflowToolUse),
		seenToolIDs: make(map[string]struct{}), doneToolIDs: make(map[string]struct{}),
	}
	transcript := `{"sessionId":"session-test","type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"run:1","name":"run_shell_command","input":{"command":"ls"}}]}}
{"sessionId":"session-test","type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hello"}]}}
`
	if err := os.WriteFile(turn.transcriptPath, []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, testutil.NewCLI(t, testutil.CLI{WaitEOF: true}))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	cmd.Stdout, cmd.Stderr = writer, writer
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	s.turnActive.Store(true)
	s.wg.Add(1)
	go s.readLoop(turn, cmd, reader)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-s.Events():
			if event.Type == core.EventError {
				t.Fatal(event.Error)
			}
			if event.Type != core.EventResult {
				continue
			}
			if !strings.Contains(event.Content, "timed out") || !strings.Contains(event.Content, "hello") {
				t.Fatalf("unexpected result: %q", event.Content)
			}
			if s.turnActive.Load() {
				t.Fatal("result delivered while session is still busy")
			}
			return
		case <-deadline:
			t.Fatal("timeout waiting for result after pending tool cancellation")
		}
	}
}
