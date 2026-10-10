package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SetProjectWorkspaceMode switches a project between single- and
// multi-workspace mode in the config file, keeping comments and formatting.
//
//   - multi: mode = "multi-workspace" and base_dir = dir are set, and the
//     agent's work_dir is removed, which multi-workspace mode refuses.
//   - single: mode is removed and the agent's work_dir = dir is set. base_dir
//     is kept so switching back needs no new input.
//
// dir must be an absolute path; "~" is expanded. The edited file must pass
// the same checks as loading it, or nothing is written.
func SetProjectWorkspaceMode(projectName string, multi bool, dir string) error {
	configMu.Lock()
	defer configMu.Unlock()
	if ConfigPath == "" {
		return fmt.Errorf("config path not set")
	}
	dir = strings.TrimSpace(dir)
	if home, err := os.UserHomeDir(); err == nil {
		dir = expandLeadingHome(dir, home)
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("workspace mode: %q is not an absolute path", dir)
	}

	data, err := os.ReadFile(ConfigPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	cfg, _, err := parseConfigData(data)
	if err != nil {
		return err
	}
	projectIdx := -1
	for i := range cfg.Projects {
		if cfg.Projects[i].Name == projectName {
			projectIdx = i
			break
		}
	}
	lines, hadTrailing := splitConfigLines(string(data))
	if projectIdx < 0 || projectIdx >= len(buildRawProjectSpans(lines)) {
		return fmt.Errorf("project %q not found in config", projectName)
	}

	if multi {
		lines = upsertProjectKey(lines, projectIdx, "mode", quoteTomlString("multi-workspace"))
		lines = upsertProjectKey(lines, projectIdx, "base_dir", quoteTomlString(dir))
		if span := buildRawProjectSpans(lines)[projectIdx]; span.agentOptionsStart >= 0 {
			lines = removeKeyInRange(lines, span.agentOptionsStart+1, span.agentOptionsEnd, "work_dir")
		}
	} else {
		span := buildRawProjectSpans(lines)[projectIdx]
		lines = removeKeyInRange(lines, span.start+1, projectKeysEnd(lines, span), "mode")
		lines = ensureProjectAgentOptions(lines, projectIdx)
		span = buildRawProjectSpans(lines)[projectIdx]
		lines = upsertKeyInRange(lines, span.agentOptionsStart+1, span.agentOptionsEnd, "work_dir", quoteTomlString(dir))
	}

	content := joinConfigLines(lines, hadTrailing)
	edited, _, err := parseConfigData([]byte(content))
	if err != nil {
		return fmt.Errorf("workspace mode: edited config does not parse: %w", err)
	}
	if err := edited.validatePermissive(); err != nil {
		return fmt.Errorf("workspace mode: %w", err)
	}
	return writeRawConfig(content)
}
