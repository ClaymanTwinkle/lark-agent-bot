package codex

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// withModelRPC opens a metadata-only connection; no conversation or turn is created.
func (a *Agent) withModelRPC(ctx context.Context, visit func(func(string, any, any) error) error) error {
	a.mu.RLock()
	endpoint := normalizeAppServerURL(a.appServerURL)
	home := a.codexHome
	a.mu.RUnlock()
	if endpoint != "auto" && endpoint != "stdio://" {
		sharedCtx, cancel := context.WithCancel(ctx)
		s := &appServerSession{ctx: sharedCtx, cancel: cancel, url: endpoint, codexHome: home, events: make(chan core.Event, 128)}
		s.alive.Store(true)
		defer func() { _ = s.Close() }()
		if err := s.connectShared(); err != nil {
			return err
		}
		if err := s.initialize(); err != nil {
			return err
		}
		return visit(s.request)
	}
	return a.withStdioModelRPC(ctx, visit)
}

func (a *Agent) withStdioModelRPC(ctx context.Context, visit func(func(string, any, any) error) error) error {
	a.mu.RLock()
	bin, workDir, codexHome := a.cmd, a.workDir, a.codexHome
	args := append([]string(nil), a.cliExtraArgs...)
	env := append([]string(nil), a.configEnv...)
	env = append(env, a.providerEnvLocked()...)
	env = append(env, a.sessionEnv...)
	a.mu.RUnlock()
	bin, fromDesktop, err := resolveCodexBin(bin)
	if err != nil {
		return err
	}
	if fromDesktop {
		env = withBinDirOnPath(env, bin)
	}
	if codexHome != "" {
		env = append(env, "CODEX_HOME="+codexHome)
	}
	absDir, err := filepath.Abs(workDir)
	if err != nil {
		return fmt.Errorf("codex: models work directory: %w", err)
	}
	cmd := exec.CommandContext(ctx, bin, append(args, "app-server")...)
	cmd.Dir = absDir
	cmd.Env = core.MergeEnv(os.Environ(), env)
	prepareCmdForKill(cmd)
	cmd.Cancel = func() error { return forceKillCmd(cmd) }
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("codex: models stdin: %w", err)
	}
	defer func() { _ = stdin.Close() }()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("codex: models stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("codex: models app-server: %w", err)
	}
	defer func() {
		if err := forceKillCmd(cmd); err != nil {
			slog.Warn("codex: stop models app-server", "error", err)
		}
		_ = cmd.Wait() // The metadata-only process is deliberately terminated.
	}()
	reader := bufio.NewReader(stdout)
	if err := rpcRequestOverIO(stdin, reader, 1, "initialize", map[string]any{
		"clientInfo": map[string]any{"name": "lark-agent-bot-models", "version": "1"},
	}, nil); err != nil {
		return fmt.Errorf("codex: models initialize: %w", err)
	}
	if err := rpcNotifyOverIO(stdin, "initialized", map[string]any{}); err != nil {
		return fmt.Errorf("codex: models initialized: %w", err)
	}

	id := int64(1)
	return visit(func(method string, params any, out any) error {
		id++
		return rpcRequestOverIO(stdin, reader, id, method, params, out)
	})
}

func (a *Agent) fetchModelsFromAppServer(ctx context.Context) ([]core.ModelOption, error) {
	var models []core.ModelOption
	a.mu.RLock()
	provider, _, _, _ := a.activeProviderCodexConfig()
	a.mu.RUnlock()
	err := a.withModelRPC(ctx, func(request func(string, any, any) error) error {
		var cursor *string
		seenModels := make(map[string]bool)
		seenCursors := make(map[string]bool)
		for {
			var response struct {
				Data []struct {
					Model       string `json:"model"`
					Description string `json:"description"`
					Hidden      bool   `json:"hidden"`
				} `json:"data"`
				NextCursor *string `json:"nextCursor"`
			}
			params := map[string]any{"cursor": cursor, "limit": 100, "includeHidden": false}
			// Do not accidentally show the default account's catalog for a custom provider.
			if provider != "" {
				params["modelProvider"] = provider
			}
			if err := request("model/list", params, &response); err != nil {
				return fmt.Errorf("codex: model/list: %w", err)
			}
			for _, m := range response.Data {
				if m.Model == "" || m.Hidden || seenModels[m.Model] {
					continue
				}
				seenModels[m.Model] = true
				models = append(models, core.ModelOption{Name: m.Model, Desc: m.Description})
			}
			if response.NextCursor == nil || *response.NextCursor == "" {
				return nil
			}
			if seenCursors[*response.NextCursor] {
				return fmt.Errorf("codex: model/list repeated pagination cursor")
			}
			seenCursors[*response.NextCursor] = true
			cursor = response.NextCursor
		}
	})
	if err != nil {
		return nil, err
	}
	return models, nil
}
