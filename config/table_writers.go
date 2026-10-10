package config

import (
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// The writers in this file add, replace or remove whole tables: commands,
// aliases, global and project providers, projects and platforms. Each one
// changes the decoded struct, edits the matching block of lines (see
// toml_lines.go), and writes the edited text only when it decodes to the
// changed struct. Otherwise it falls back to rewriting the whole file.

// editConfig saves one such change to config.toml. change applies it to cfg,
// decoded from the file, and to lines, the file's text. It returns ok false
// when the file's layout defeats the line edits (inline tables or arrays,
// dotted keys); cfg must hold the change either way. When the edited text
// does not decode to cfg, or ok is false, the whole file is rewritten from
// cfg, which drops its comments. The caller must hold configMu.
func editConfig(what string, change func(cfg *Config, lines []string) (edited []string, ok bool, err error)) error {
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
	lines, hadTrailing := splitConfigLines(string(data))
	edited, ok, err := change(cfg, lines)
	if err != nil {
		return err
	}
	if ok {
		if content, match := lineEditMatches(edited, hadTrailing, cfg); match {
			return writeRawConfig(content)
		}
	}
	slog.Warn("config: could not edit the file in place; rewriting the whole file, which drops its comments",
		"change", what)
	return saveConfig(cfg)
}

// projectBlock returns the idx-th [[projects]] block, or ok false when the
// blocks do not match the n decoded projects one to one.
func projectBlock(lines []string, n, idx int) (tableBlock, bool) {
	blocks := tableBlocks(lines, 0, len(lines)-1, "projects")
	if len(blocks) != n {
		return tableBlock{}, false
	}
	return blocks[idx], true
}

// setProviderRefsLines writes refs as provider_refs under the idx-th
// project's [projects.agent], removing the key when refs is empty.
func setProviderRefsLines(lines []string, idx int, refs []string) ([]string, bool) {
	spans := buildRawProjectSpans(lines)
	if idx >= len(spans) || spans[idx].agentStart < 0 {
		return nil, false
	}
	s := spans[idx]
	if len(refs) == 0 {
		return removeKeyInRange(lines, s.agentStart+1, s.agentEnd, "provider_refs"), true
	}
	return upsertKeyInRange(lines, s.agentStart+1, s.agentEnd, "provider_refs", tomlStringArray(refs)), true
}

// AddCommand adds a global custom command and persists to config.
func AddCommand(cmd CommandConfig) error {
	configMu.Lock()
	defer configMu.Unlock()
	return editConfig("commands", func(cfg *Config, lines []string) ([]string, bool, error) {
		for _, c := range cfg.Commands {
			if c.Name == cmd.Name {
				return nil, false, fmt.Errorf("command %q already exists", cmd.Name)
			}
		}
		n := len(cfg.Commands)
		cfg.Commands = append(cfg.Commands, cmd)
		blocks := tableBlocks(lines, 0, len(lines)-1, "commands")
		return appendTableBlock(lines, blocks, n, topLevelTableAt(lines), "commands", cmd)
	})
}

// RemoveCommand removes a global custom command and persists to config.
func RemoveCommand(name string) error {
	configMu.Lock()
	defer configMu.Unlock()
	return editConfig("commands", func(cfg *Config, lines []string) ([]string, bool, error) {
		match := func(c CommandConfig) bool { return c.Name == name }
		if !slices.ContainsFunc(cfg.Commands, match) {
			return nil, false, fmt.Errorf("command %q not found", name)
		}
		blocks := tableBlocks(lines, 0, len(lines)-1, "commands")
		edited, ok := removeTableBlocks(lines, blocks, len(cfg.Commands), func(k int) bool { return match(cfg.Commands[k]) })
		cfg.Commands = slices.DeleteFunc(cfg.Commands, match)
		return edited, ok, nil
	})
}

// AddAlias adds a global alias, or retargets an existing one, and persists
// to config.
func AddAlias(alias AliasConfig) error {
	configMu.Lock()
	defer configMu.Unlock()
	return editConfig("aliases", func(cfg *Config, lines []string) ([]string, bool, error) {
		blocks := tableBlocks(lines, 0, len(lines)-1, "aliases")
		n := len(cfg.Aliases)
		if k := slices.IndexFunc(cfg.Aliases, func(a AliasConfig) bool { return a.Name == alias.Name }); k >= 0 {
			cfg.Aliases[k] = alias
			return replaceTableBlock(lines, blocks, n, k, "aliases", alias)
		}
		cfg.Aliases = append(cfg.Aliases, alias)
		return appendTableBlock(lines, blocks, n, topLevelTableAt(lines), "aliases", alias)
	})
}

// RemoveAlias removes a global alias and persists to config.
func RemoveAlias(name string) error {
	configMu.Lock()
	defer configMu.Unlock()
	return editConfig("aliases", func(cfg *Config, lines []string) ([]string, bool, error) {
		match := func(a AliasConfig) bool { return a.Name == name }
		if !slices.ContainsFunc(cfg.Aliases, match) {
			return nil, false, fmt.Errorf("alias %q not found", name)
		}
		blocks := tableBlocks(lines, 0, len(lines)-1, "aliases")
		edited, ok := removeTableBlocks(lines, blocks, len(cfg.Aliases), func(k int) bool { return match(cfg.Aliases[k]) })
		cfg.Aliases = slices.DeleteFunc(cfg.Aliases, match)
		return edited, ok, nil
	})
}

// AddProviderToConfig adds a provider to a project's agent config and saves.
func AddProviderToConfig(projectName string, provider ProviderConfig) error {
	configMu.Lock()
	defer configMu.Unlock()
	return editConfig("project providers", func(cfg *Config, lines []string) ([]string, bool, error) {
		i := projectIndex(cfg.Projects, projectName)
		if i < 0 {
			return nil, false, fmt.Errorf("project %q not found in config", projectName)
		}
		agent := &cfg.Projects[i].Agent
		if slices.ContainsFunc(agent.Providers, func(p ProviderConfig) bool { return p.Name == provider.Name }) {
			return nil, false, fmt.Errorf("provider %q already exists in project %q", provider.Name, projectName)
		}
		n := len(agent.Providers)
		agent.Providers = append(agent.Providers, provider)

		p, ok := projectBlock(lines, len(cfg.Projects), i)
		if !ok {
			return nil, false, nil
		}
		// The project's first provider goes after its agent tables.
		at := tableEnd(lines, p.start, p.end, "projects.agent")
		if at < 0 {
			return nil, false, nil // agent written inline
		}
		blocks := tableBlocks(lines, p.start, p.end, "projects.agent.providers")
		return appendTableBlock(lines, blocks, n, at+1, "projects.agent.providers", provider)
	})
}

// RemoveProviderFromConfig removes a provider from a project's agent config and saves.
// For global providers referenced via provider_refs, it removes the reference
// instead of deleting the global definition.
func RemoveProviderFromConfig(projectName, providerName string) error {
	configMu.Lock()
	defer configMu.Unlock()
	return editConfig("project providers", func(cfg *Config, lines []string) ([]string, bool, error) {
		i := projectIndex(cfg.Projects, projectName)
		if i < 0 {
			return nil, false, fmt.Errorf("provider %q not found in project %q", providerName, projectName)
		}
		agent := &cfg.Projects[i].Agent
		inline := slices.IndexFunc(agent.Providers, func(p ProviderConfig) bool { return p.Name == providerName })
		ref := slices.Index(agent.ProviderRefs, providerName)
		if inline < 0 && ref < 0 {
			return nil, false, fmt.Errorf("provider %q not found in project %q", providerName, projectName)
		}
		n := len(agent.Providers)
		if inline >= 0 {
			agent.Providers = slices.Delete(agent.Providers, inline, inline+1)
		}
		if ref >= 0 {
			agent.ProviderRefs = slices.Delete(agent.ProviderRefs, ref, ref+1)
		}

		p, ok := projectBlock(lines, len(cfg.Projects), i)
		if ok && inline >= 0 {
			blocks := tableBlocks(lines, p.start, p.end, "projects.agent.providers")
			lines, ok = removeTableBlocks(lines, blocks, n, func(k int) bool { return k == inline })
		}
		if ok && ref >= 0 {
			lines, ok = setProviderRefsLines(lines, i, agent.ProviderRefs)
		}
		return lines, ok, nil
	})
}

// AddGlobalProvider appends a provider to the top-level [[providers]] and saves.
func AddGlobalProvider(provider ProviderConfig) error {
	configMu.Lock()
	defer configMu.Unlock()
	return editConfig("global providers", func(cfg *Config, lines []string) ([]string, bool, error) {
		for _, existing := range cfg.Providers {
			if existing.Name == provider.Name {
				return nil, false, fmt.Errorf("global provider %q already exists", provider.Name)
			}
		}
		n := len(cfg.Providers)
		cfg.Providers = append(cfg.Providers, provider)
		blocks := tableBlocks(lines, 0, len(lines)-1, "providers")
		return appendTableBlock(lines, blocks, n, topLevelTableAt(lines), "providers", provider)
	})
}

// UpdateGlobalProvider replaces an existing global provider by name.
func UpdateGlobalProvider(name string, provider ProviderConfig) error {
	configMu.Lock()
	defer configMu.Unlock()
	return editConfig("global providers", func(cfg *Config, lines []string) ([]string, bool, error) {
		k := slices.IndexFunc(cfg.Providers, func(p ProviderConfig) bool { return p.Name == name })
		if k < 0 {
			return nil, false, fmt.Errorf("global provider %q not found", name)
		}
		provider.Name = name // name is immutable in update
		cfg.Providers[k] = provider
		blocks := tableBlocks(lines, 0, len(lines)-1, "providers")
		return replaceTableBlock(lines, blocks, len(cfg.Providers), k, "providers", provider)
	})
}

// RemoveGlobalProvider removes a provider from top-level [[providers]] and
// also strips the name from every project's provider_refs, then saves.
func RemoveGlobalProvider(name string) error {
	configMu.Lock()
	defer configMu.Unlock()
	return editConfig("global providers", func(cfg *Config, lines []string) ([]string, bool, error) {
		k := slices.IndexFunc(cfg.Providers, func(p ProviderConfig) bool { return p.Name == name })
		if k < 0 {
			return nil, false, fmt.Errorf("global provider %q not found", name)
		}
		blocks := tableBlocks(lines, 0, len(lines)-1, "providers")
		lines, ok := removeTableBlocks(lines, blocks, len(cfg.Providers), func(j int) bool { return j == k })
		cfg.Providers = slices.Delete(cfg.Providers, k, k+1)
		for i := range cfg.Projects {
			agent := &cfg.Projects[i].Agent
			j := slices.Index(agent.ProviderRefs, name)
			if j < 0 {
				continue
			}
			agent.ProviderRefs = slices.Delete(agent.ProviderRefs, j, j+1)
			if ok {
				lines, ok = setProviderRefsLines(lines, i, agent.ProviderRefs)
			}
		}
		return lines, ok, nil
	})
}

// RemoveProject removes a project from the config file.
func RemoveProject(projectName string) error {
	configMu.Lock()
	defer configMu.Unlock()
	return editConfig("projects", func(cfg *Config, lines []string) ([]string, bool, error) {
		i := projectIndex(cfg.Projects, projectName)
		if i < 0 {
			return nil, false, fmt.Errorf("project %q not found", projectName)
		}
		blocks := tableBlocks(lines, 0, len(lines)-1, "projects")
		edited, ok := removeTableBlocks(lines, blocks, len(cfg.Projects), func(k int) bool { return k == i })
		cfg.Projects = slices.Delete(cfg.Projects, i, i+1)
		return edited, ok, nil
	})
}

// AddPlatformToProject appends a platform config to a project.
// If the project doesn't exist, it is created using agentType and workDir when provided,
// otherwise agent config is cloned from the first existing project when present.
func AddPlatformToProject(projectName string, platform PlatformConfig, workDir, agentType string) error {
	configMu.Lock()
	defer configMu.Unlock()
	return editConfig("projects", func(cfg *Config, lines []string) ([]string, bool, error) {
		if platform.Options == nil {
			platform.Options = map[string]any{}
		}
		projects := tableBlocks(lines, 0, len(lines)-1, "projects")
		if i := projectIndex(cfg.Projects, projectName); i >= 0 {
			n := len(cfg.Projects[i].Platforms)
			cfg.Projects[i].Platforms = append(cfg.Projects[i].Platforms, platform)
			if len(projects) != len(cfg.Projects) {
				return nil, false, nil
			}
			p := projects[i]
			blocks := tableBlocks(lines, p.start, p.end, "projects.platforms")
			return appendTableBlock(lines, blocks, n, p.end+1, "projects.platforms", platform)
		}

		agentCfg := AgentConfig{Type: "codex", Options: map[string]any{}}
		at := strings.TrimSpace(agentType)
		if at != "" {
			agentCfg.Type = at
		}
		if len(cfg.Projects) > 0 && at == "" {
			agentCfg = cloneAgentConfig(cfg.Projects[0].Agent)
		}
		wd := strings.TrimSpace(workDir)
		if wd != "" {
			if agentCfg.Options == nil {
				agentCfg.Options = map[string]any{}
			}
			agentCfg.Options["work_dir"] = wd
		}
		proj := ProjectConfig{
			Name:      projectName,
			Agent:     agentCfg,
			Platforms: []PlatformConfig{platform},
		}
		n := len(cfg.Projects)
		cfg.Projects = append(cfg.Projects, proj)
		return appendTableBlock(lines, projects, n, len(lines), "projects", proj)
	})
}
