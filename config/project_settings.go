package config

import (
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// ProjectSettingsUpdate carries optional field updates for SaveProjectSettings.
type ProjectSettingsUpdate struct {
	Language             *string
	AdminFrom            *string
	DisabledCommands     []string
	WorkDir              *string
	Mode                 *string
	AgentType            *string
	ShowContextIndicator *bool
	ShowWorkdirIndicator *bool
	ReplyFooter          *bool
	InjectSender         *bool
	PlatformAllowFrom    map[string]string
}

// SaveProjectSettings persists project-level settings and the global language
// to config.toml. It edits only the lines of the keys it changes, so comments
// and unknown keys survive. When the file's layout defeats the line edits
// (inline tables, dotted keys), it falls back to rewriting the whole file.
func SaveProjectSettings(projectName string, update ProjectSettingsUpdate) error {
	configMu.Lock()
	defer configMu.Unlock()
	if ConfigPath == "" {
		return fmt.Errorf("config path not set")
	}
	data, err := os.ReadFile(ConfigPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	// Decode without resolving ${ENV} placeholders: the fallback writes this
	// struct back and must not store resolved secrets.
	cfg := &Config{}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	idx := slices.IndexFunc(cfg.Projects, func(p ProjectConfig) bool { return p.Name == projectName })
	if idx < 0 {
		return fmt.Errorf("project %q not found", projectName)
	}
	agentTypeChanged, providerRemoved := applyProjectSettings(cfg, idx, update)

	lines, hadTrailing := splitConfigLines(string(data))
	if edited, ok := patchProjectSettingsLines(lines, cfg, idx, update, agentTypeChanged, providerRemoved); ok {
		content := joinConfigLines(edited, hadTrailing)
		check := &Config{}
		if err := toml.Unmarshal([]byte(content), check); err == nil && projectSettingsMatch(check, cfg, idx) {
			return writeRawConfig(content)
		}
	}
	slog.Warn("config: could not edit project settings in place; rewriting the whole file, which drops its comments",
		"project", projectName)
	return saveConfig(cfg)
}

// applyProjectSettings applies update to the idx-th project of cfg. It
// reports whether the agent type changed, and whether that removed the
// active provider (options.provider) along with incompatible provider_refs.
func applyProjectSettings(cfg *Config, idx int, update ProjectSettingsUpdate) (agentTypeChanged, providerRemoved bool) {
	if update.Language != nil {
		cfg.Language = *update.Language
	}
	proj := &cfg.Projects[idx]
	if update.AgentType != nil && *update.AgentType != proj.Agent.Type {
		agentTypeChanged = true
		newType := *update.AgentType
		proj.Agent.Type = newType
		// Filter out provider_refs incompatible with the new agent type.
		globalByName := make(map[string]ProviderConfig, len(cfg.Providers))
		for _, p := range cfg.Providers {
			globalByName[p.Name] = p
		}
		var compatible []string
		for _, ref := range proj.Agent.ProviderRefs {
			gp, ok := globalByName[ref]
			if !ok {
				continue
			}
			if len(gp.AgentTypes) > 0 && !containsString(gp.AgentTypes, newType) {
				slog.Info("removing incompatible provider ref on agent type change",
					"project", proj.Name, "provider", ref,
					"provider_agents", gp.AgentTypes, "new_agent", newType)
				continue
			}
			compatible = append(compatible, ref)
		}
		proj.Agent.ProviderRefs = compatible
		// Clear active provider if it was removed.
		if prov, ok := proj.Agent.Options["provider"].(string); ok && prov != "" && !slices.Contains(compatible, prov) {
			delete(proj.Agent.Options, "provider")
			providerRemoved = true
		}
	}
	if update.AdminFrom != nil {
		proj.AdminFrom = *update.AdminFrom
	}
	if update.DisabledCommands != nil {
		proj.DisabledCommands = update.DisabledCommands
	}
	setBool := func(dst **bool, v *bool) {
		if v != nil {
			b := *v
			*dst = &b
		}
	}
	setBool(&proj.ShowContextIndicator, update.ShowContextIndicator)
	setBool(&proj.ShowWorkdirIndicator, update.ShowWorkdirIndicator)
	setBool(&proj.ReplyFooter, update.ReplyFooter)
	setBool(&proj.InjectSender, update.InjectSender)
	setOption := func(key string, v *string) {
		if v == nil {
			return
		}
		if proj.Agent.Options == nil {
			proj.Agent.Options = map[string]any{}
		}
		if s := strings.TrimSpace(*v); s != "" {
			proj.Agent.Options[key] = s
		} else {
			delete(proj.Agent.Options, key)
		}
	}
	setOption("work_dir", update.WorkDir)
	setOption("mode", update.Mode)
	for j := range proj.Platforms {
		af, found := platformAllowFromUpdate(update.PlatformAllowFrom, proj.Platforms[j].Type)
		if !found {
			continue
		}
		if proj.Platforms[j].Options == nil {
			proj.Platforms[j].Options = map[string]any{}
		}
		proj.Platforms[j].Options["allow_from"] = strings.TrimSpace(af)
	}
	return agentTypeChanged, providerRemoved
}

// platformAllowFromUpdate returns the allow_from update for a platform type,
// matched case-insensitively.
func platformAllowFromUpdate(updates map[string]string, platformType string) (string, bool) {
	platformType = strings.TrimSpace(platformType)
	if platformType == "" {
		return "", false
	}
	for k, v := range updates {
		if strings.EqualFold(strings.TrimSpace(k), platformType) {
			return v, true
		}
	}
	return "", false
}

// patchProjectSettingsLines writes the fields update names, with the values
// cfg now holds, into the raw config lines. It reports false when the file's
// layout is not one the line edits handle.
func patchProjectSettingsLines(lines []string, cfg *Config, idx int, update ProjectSettingsUpdate, agentTypeChanged, providerRemoved bool) ([]string, bool) {
	if idx >= len(buildRawProjectSpans(lines)) {
		return nil, false
	}
	span := func() rawProjectSpan { return buildRawProjectSpans(lines)[idx] }
	proj := &cfg.Projects[idx]

	if update.Language != nil {
		lines = upsertKeyInRange(lines, 0, topLevelKeysEnd(lines), "language", quoteTomlString(cfg.Language))
	}
	if update.AdminFrom != nil {
		lines = upsertProjectKey(lines, idx, "admin_from", quoteTomlString(proj.AdminFrom))
	}
	if update.DisabledCommands != nil {
		lines = upsertProjectKey(lines, idx, "disabled_commands", tomlStringArray(proj.DisabledCommands))
	}
	for _, b := range []struct {
		key   string
		set   *bool
		value *bool
	}{
		{"show_context_indicator", update.ShowContextIndicator, proj.ShowContextIndicator},
		{"show_workdir_indicator", update.ShowWorkdirIndicator, proj.ShowWorkdirIndicator},
		{"reply_footer", update.ReplyFooter, proj.ReplyFooter},
		{"inject_sender", update.InjectSender, proj.InjectSender},
	} {
		if b.set != nil {
			lines = upsertProjectKey(lines, idx, b.key, tomlBool(*b.value))
		}
	}

	if agentTypeChanged {
		lines = ensureProjectAgentOptions(lines, idx)
		s := span()
		lines = upsertKeyInRange(lines, s.agentStart+1, s.agentEnd, "type", quoteTomlString(proj.Agent.Type))
		s = span()
		if len(proj.Agent.ProviderRefs) == 0 {
			lines = removeKeyInRange(lines, s.agentStart+1, s.agentEnd, "provider_refs")
		} else {
			lines = upsertKeyInRange(lines, s.agentStart+1, s.agentEnd, "provider_refs", tomlStringArray(proj.Agent.ProviderRefs))
		}
		if s = span(); providerRemoved {
			lines = removeKeyInRange(lines, s.agentOptionsStart+1, s.agentOptionsEnd, "provider")
		}
	}
	agentOption := func(key string) {
		value, _ := proj.Agent.Options[key].(string)
		if value == "" {
			if s := span(); s.agentOptionsStart >= 0 {
				lines = removeKeyInRange(lines, s.agentOptionsStart+1, s.agentOptionsEnd, key)
			}
			return
		}
		lines = ensureProjectAgentOptions(lines, idx)
		s := span()
		lines = upsertKeyInRange(lines, s.agentOptionsStart+1, s.agentOptionsEnd, key, quoteTomlString(value))
	}
	if update.WorkDir != nil {
		agentOption("work_dir")
	}
	if update.Mode != nil {
		agentOption("mode")
	}

	for j, pc := range proj.Platforms {
		if _, found := platformAllowFromUpdate(update.PlatformAllowFrom, pc.Type); !found {
			continue
		}
		if j >= len(span().platforms) {
			return nil, false // platforms written as an inline array
		}
		lines = ensurePlatformOptions(lines, idx, j)
		ps := span().platforms[j]
		allowFrom, _ := pc.Options["allow_from"].(string)
		lines = upsertKeyInRange(lines, ps.optionsStart+1, ps.optionsEnd, "allow_from", quoteTomlString(allowFrom))
	}
	return lines, true
}

// projectSettingsMatch reports whether edited, decoded from the line-edited
// file, holds what want holds: the fields SaveProjectSettings may change in
// the idx-th project, and every other project unchanged.
func projectSettingsMatch(edited, want *Config, idx int) bool {
	if edited.Language != want.Language || len(edited.Projects) != len(want.Projects) {
		return false
	}
	for k := range want.Projects {
		if k != idx && !reflect.DeepEqual(edited.Projects[k], want.Projects[k]) {
			return false
		}
	}
	e, w := &edited.Projects[idx], &want.Projects[idx]
	if e.Name != w.Name || e.AdminFrom != w.AdminFrom ||
		!slices.Equal(e.DisabledCommands, w.DisabledCommands) ||
		!ptrEqual(e.ShowContextIndicator, w.ShowContextIndicator) ||
		!ptrEqual(e.ShowWorkdirIndicator, w.ShowWorkdirIndicator) ||
		!ptrEqual(e.ReplyFooter, w.ReplyFooter) ||
		!ptrEqual(e.InjectSender, w.InjectSender) ||
		e.Agent.Type != w.Agent.Type ||
		!slices.Equal(e.Agent.ProviderRefs, w.Agent.ProviderRefs) ||
		len(e.Platforms) != len(w.Platforms) {
		return false
	}
	for _, key := range []string{"work_dir", "mode", "provider"} {
		if fmt.Sprint(e.Agent.Options[key]) != fmt.Sprint(w.Agent.Options[key]) {
			return false
		}
	}
	for j := range w.Platforms {
		if fmt.Sprint(e.Platforms[j].Options["allow_from"]) != fmt.Sprint(w.Platforms[j].Options["allow_from"]) {
			return false
		}
	}
	return true
}

// ptrEqual reports whether a and b are both nil or point to equal values.
func ptrEqual[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
