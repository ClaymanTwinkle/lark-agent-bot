package codex

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// Opt-in local account smoke test. Any unexpected permission request is denied.
func TestIntegration_AppServerResumeAndGo(t *testing.T) {
	workDir, threadID := os.Getenv("LARK_CODEX_SMOKE_DIR"), os.Getenv("LARK_CODEX_SMOKE_THREAD")
	if workDir == "" || threadID == "" {
		t.Skip("local app-server smoke test not requested")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	s, err := newAppServerSession(ctx, "codex", "stdio://", workDir, "", "", "read-only", threadID, "", "", nil, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.CurrentSessionID() != threadID {
		t.Fatal("resume changed the thread")
	}
	if err := s.Send("这是机器人环境检查，只运行 go version，然后报告版本。不要修改文件，不要提交或推送，不要继续之前的任务。", "smoke-go", nil, nil); err != nil {
		t.Fatal(err)
	}
	var reply strings.Builder
	for {
		select {
		case event, ok := <-s.Events():
			if !ok {
				t.Fatal("session closed before result")
			}
			switch event.Type {
			case core.EventText:
				reply.WriteString(event.Content)
			case core.EventPermissionRequest:
				_ = s.RespondPermission(event.RequestID, core.PermissionResult{Behavior: "deny"})
				t.Fatal("unexpected approval needed for go version")
			case core.EventError:
				t.Fatal(event.Error)
			case core.EventResult:
				reply.WriteString(event.Content)
				if !strings.Contains(reply.String(), "go1.") {
					t.Fatalf("Go version missing: %s", reply.String())
				}
				t.Log(reply.String())
				return
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

// TestIntegration_CodexProviderFlow verifies the full provider config flow:
// 1. ensureCodexProviderConfig writes correct config.toml
// 2. ensureCodexAuth writes correct auth.json
// 3. Codex CLI can authenticate and respond using the written config
//
// Requires: SHENGSUANYUN_API_KEY env var and `codex` CLI in PATH.
// Skip with: go test ./agent/codex/ -run TestIntegration -v
func TestIntegration_CodexProviderFlow(t *testing.T) {
	apiKey := os.Getenv("SHENGSUANYUN_API_KEY")
	if apiKey == "" {
		t.Skip("SHENGSUANYUN_API_KEY not set, skipping integration test")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex CLI not in PATH, skipping integration test")
	}

	home := filepath.Join(t.TempDir(), ".codex")

	// Step 1: write config.toml via our function
	err := ensureCodexProviderConfig(home, "shengsuanyun-codex",
		"https://router.shengsuanyun.com/api/v1", "responses", nil)
	if err != nil {
		t.Fatalf("ensureCodexProviderConfig: %v", err)
	}

	// Verify config.toml content
	cfgData, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	cfgContent := string(cfgData)
	t.Logf("Generated config.toml:\n%s", cfgContent)

	for _, want := range []string{
		`[model_providers.shengsuanyun-codex]`,
		`base_url = "https://router.shengsuanyun.com/api/v1"`,
		`wire_api = "responses"`,
		`env_key = "OPENAI_API_KEY"`,
	} {
		if !strings.Contains(cfgContent, want) {
			t.Errorf("config.toml missing: %s", want)
		}
	}

	// Step 2: write auth.json via our function
	err = ensureCodexAuth(home, apiKey)
	if err != nil {
		t.Fatalf("ensureCodexAuth: %v", err)
	}

	// Verify auth.json content
	authData, err := os.ReadFile(filepath.Join(home, "auth.json"))
	if err != nil {
		t.Fatalf("read auth.json: %v", err)
	}
	var authMap map[string]any
	if err := json.Unmarshal(authData, &authMap); err != nil {
		t.Fatalf("parse auth.json: %v", err)
	}
	if authMap["auth_mode"] != "apikey" {
		t.Errorf("auth_mode = %v, want apikey", authMap["auth_mode"])
	}
	if authMap["OPENAI_API_KEY"] != apiKey {
		t.Errorf("OPENAI_API_KEY not set correctly in auth.json")
	}

	// Step 3: run codex exec with the generated config
	workDir := filepath.Join(t.TempDir(), "repo")
	os.MkdirAll(workDir, 0o755)
	gitInit := exec.Command("git", "init", "-q")
	gitInit.Dir = workDir
	if err := gitInit.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}

	cmd := exec.Command("codex", "exec",
		"--model", "openai/gpt-5.3-codex",
		"-c", `model_provider="shengsuanyun-codex"`,
		"-c", `openai_base_url="https://router.shengsuanyun.com/api/v1"`,
		"--sandbox", "workspace-write",
		"-c", `approval_policy="never"`,
	)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"CODEX_HOME="+home,
		"OPENAI_API_KEY="+apiKey,
	)
	cmd.Stdin = strings.NewReader("reply with exactly 'integration-ok' and nothing else")

	output, err := cmd.CombinedOutput()
	t.Logf("Codex output:\n%s", string(output))

	if err != nil {
		t.Fatalf("codex exec failed: %v\noutput: %s", err, output)
	}
	if !strings.Contains(string(output), "integration-ok") {
		t.Errorf("expected 'integration-ok' in output, got:\n%s", output)
	}
}
