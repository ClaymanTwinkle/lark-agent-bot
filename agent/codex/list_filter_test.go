package codex

import (
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

type unexpectedTranscriptReader struct {
	reads int
}

func (r *unexpectedTranscriptReader) Read([]byte) (int, error) {
	r.reads++
	return 0, errors.New("excluded transcript body was read")
}

// A menu request must not read megabytes of messages, images and tool output
// from other projects or internal subagents before discarding their sessions.
func TestParseCodexSession_MenuListSkipsExcludedTranscriptBodies(t *testing.T) {
	workDir := t.TempDir()
	for _, tc := range []struct {
		name   string
		cwd    string
		source any
	}{
		{"other_project", filepath.Join(workDir, "other"), "vscode"},
		{"subagent", workDir, map[string]any{"subagent": map[string]any{"other": "guardian"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta, err := json.Marshal(map[string]any{
				"type":    "session_meta",
				"payload": map[string]any{"id": "excluded", "cwd": tc.cwd, "source": tc.source},
			})
			if err != nil {
				t.Fatal(err)
			}
			body := &unexpectedTranscriptReader{}
			info, err := parseCodexSession(io.MultiReader(strings.NewReader(string(meta)+"\n"), body), workDir)
			if info != nil || err != nil || body.reads != 0 {
				t.Fatalf("excluded session: info=%+v error=%v body_reads=%d; want no session and no body reads", info, err, body.reads)
			}
		})
	}
}

func TestParseCodexSession_IncompleteMetadataDoesNotHideMatchingSession(t *testing.T) {
	data := `{"type":"session_meta","payload":{"cwd":"/other","source":{"subagent":{}}}}` + "\n" +
		`{"type":"session_meta","payload":{"id":"parent","cwd":"/project","source":"vscode"}}` + "\n" +
		`{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"actual prompt"}]}}` + "\n"
	info, err := parseCodexSession(strings.NewReader(data), "/project")
	if err != nil || info == nil {
		t.Fatalf("parse matching session: info=%+v error=%v", info, err)
	}
	if info.ID != "parent" || info.Summary != "actual prompt" || info.MessageCount != 1 {
		t.Fatalf("unexpected session: %+v", info)
	}
}
