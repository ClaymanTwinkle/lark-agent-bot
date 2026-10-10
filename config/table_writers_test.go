package config

import (
	"os"
	"slices"
	"strings"
	"testing"
)

const tableWritersFixture = `# top comment
language = "en"

# shared providers
[[providers]]
name = "relay" # main relay
api_key = "sk-relay"
agent_types = ["claudecode"]

[[providers.models]]
model = "fast-1" # cheapest

[providers.env]
RELAY_REGION = "eu"

[[providers]]
name = "backup"
api_key = "sk-backup"

# custom commands
[[commands]]
name = "review"
prompt = "review {{args}}" # used daily

# aliases
[[aliases]]
name = "帮助"
command = "/help" # chinese help

# projects
[[projects]]
name = "alpha"

# alpha agent
[projects.agent]
type = "claudecode"
provider_refs = ["relay", "backup"] # shared ones

[projects.agent.options]
work_dir = "/srv/alpha" # alpha checkout

# alpha's own provider
[[projects.agent.providers]]
name = "own"
api_key = "sk-own" # rotated monthly

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "cli_a" # alpha app

[[projects]]
name = "beta"

[projects.agent]
type = "codex"
provider_refs = ["relay"]

[[projects.platforms]]
type = "feishu" # beta bot

# trailing comment
`

// tableWritersComments are all the comments in tableWritersFixture.
var tableWritersComments = []string{
	"# top comment", "# shared providers", "# main relay", "# cheapest", "# custom commands", "# used daily",
	"# aliases", "# chinese help", "# projects", "# alpha agent", "# shared ones", "# alpha checkout",
	"# alpha's own provider", "# rotated monthly", "# alpha app", "# beta bot", "# trailing comment",
}

func TestTableWritersKeepComments(t *testing.T) {
	for _, tc := range []struct {
		name  string
		save  func() error
		lost  []string // comments inside the removed or replaced tables
		check func(t *testing.T, cfg *Config)
	}{
		{
			name: "AddCommand",
			save: func() error { return AddCommand(CommandConfig{Name: "deploy", Exec: "make deploy"}) },
			check: func(t *testing.T, cfg *Config) {
				if len(cfg.Commands) != 2 || cfg.Commands[1].Name != "deploy" || cfg.Commands[1].Exec != "make deploy" {
					t.Fatalf("commands = %+v", cfg.Commands)
				}
			},
		},
		{
			name: "RemoveCommand",
			save: func() error { return RemoveCommand("review") },
			lost: []string{"# used daily"},
			check: func(t *testing.T, cfg *Config) {
				if len(cfg.Commands) != 0 {
					t.Fatalf("commands = %+v", cfg.Commands)
				}
			},
		},
		{
			name: "AddAlias",
			save: func() error { return AddAlias(AliasConfig{Name: "新建", Command: "/new"}) },
			check: func(t *testing.T, cfg *Config) {
				if len(cfg.Aliases) != 2 || cfg.Aliases[1] != (AliasConfig{Name: "新建", Command: "/new"}) {
					t.Fatalf("aliases = %+v", cfg.Aliases)
				}
			},
		},
		{
			name: "AddAlias retargets",
			save: func() error { return AddAlias(AliasConfig{Name: "帮助", Command: "/list"}) },
			check: func(t *testing.T, cfg *Config) {
				if len(cfg.Aliases) != 1 || cfg.Aliases[0].Command != "/list" {
					t.Fatalf("aliases = %+v", cfg.Aliases)
				}
			},
		},
		{
			name: "RemoveAlias",
			save: func() error { return RemoveAlias("帮助") },
			lost: []string{"# chinese help"},
			check: func(t *testing.T, cfg *Config) {
				if len(cfg.Aliases) != 0 {
					t.Fatalf("aliases = %+v", cfg.Aliases)
				}
			},
		},
		{
			name: "AddProviderToConfig after existing ones",
			save: func() error {
				return AddProviderToConfig("alpha", ProviderConfig{Name: "second", APIKey: "sk-2"})
			},
			check: func(t *testing.T, cfg *Config) {
				if ps := cfg.Projects[0].Agent.Providers; len(ps) != 2 || ps[1].Name != "second" {
					t.Fatalf("alpha providers = %+v", ps)
				}
			},
		},
		{
			name: "AddProviderToConfig first one",
			save: func() error {
				return AddProviderToConfig("beta", ProviderConfig{Name: "extra", APIKey: "sk-x",
					Models: []ProviderModelConfig{{Model: "m1", Alias: "one"}}, Env: map[string]string{"A": "b"}})
			},
			check: func(t *testing.T, cfg *Config) {
				ps := cfg.Projects[1].Agent.Providers
				if len(ps) != 1 || ps[0].Name != "extra" || len(ps[0].Models) != 1 || ps[0].Env["A"] != "b" {
					t.Fatalf("beta providers = %+v", ps)
				}
				if len(cfg.Projects[1].Platforms) != 1 || len(cfg.Projects[0].Agent.Providers) != 1 {
					t.Fatalf("other tables changed: %+v", cfg.Projects)
				}
			},
		},
		{
			name: "RemoveProviderFromConfig inline",
			save: func() error { return RemoveProviderFromConfig("alpha", "own") },
			lost: []string{"# rotated monthly"},
			check: func(t *testing.T, cfg *Config) {
				if a := cfg.Projects[0].Agent; len(a.Providers) != 0 || len(a.ProviderRefs) != 2 {
					t.Fatalf("alpha agent = %+v", a)
				}
			},
		},
		{
			name: "RemoveProviderFromConfig ref",
			save: func() error { return RemoveProviderFromConfig("alpha", "backup") },
			check: func(t *testing.T, cfg *Config) {
				if a := cfg.Projects[0].Agent; len(a.Providers) != 1 || !slices.Equal(a.ProviderRefs, []string{"relay"}) {
					t.Fatalf("alpha agent = %+v", a)
				}
			},
		},
		{
			name: "AddGlobalProvider",
			save: func() error {
				return AddGlobalProvider(ProviderConfig{Name: "third", APIKey: "sk-3", Env: map[string]string{}, AgentTypes: []string{}})
			},
			check: func(t *testing.T, cfg *Config) {
				if len(cfg.Providers) != 3 || cfg.Providers[2].Name != "third" || cfg.Providers[2].APIKey != "sk-3" {
					t.Fatalf("providers = %+v", cfg.Providers)
				}
			},
		},
		{
			name: "UpdateGlobalProvider",
			save: func() error {
				return UpdateGlobalProvider("relay", ProviderConfig{APIKey: "sk-new", AgentTypes: []string{},
					Models: []ProviderModelConfig{{Model: "fast-2"}}})
			},
			lost: []string{"# cheapest"},
			check: func(t *testing.T, cfg *Config) {
				p := cfg.Providers[0]
				if p.Name != "relay" || p.APIKey != "sk-new" || len(p.AgentTypes) != 0 || len(p.Env) != 0 ||
					len(p.Models) != 1 || p.Models[0].Model != "fast-2" {
					t.Fatalf("relay = %+v", p)
				}
				if len(cfg.Providers) != 2 || cfg.Providers[1].APIKey != "sk-backup" {
					t.Fatalf("providers = %+v", cfg.Providers)
				}
			},
		},
		{
			name: "RemoveGlobalProvider",
			save: func() error { return RemoveGlobalProvider("relay") },
			lost: []string{"# main relay", "# cheapest"},
			check: func(t *testing.T, cfg *Config) {
				if len(cfg.Providers) != 1 || cfg.Providers[0].Name != "backup" {
					t.Fatalf("providers = %+v", cfg.Providers)
				}
				if refs := cfg.Projects[0].Agent.ProviderRefs; !slices.Equal(refs, []string{"backup"}) {
					t.Fatalf("alpha refs = %v", refs)
				}
				if refs := cfg.Projects[1].Agent.ProviderRefs; refs != nil {
					t.Fatalf("beta refs = %v, want the key removed", refs)
				}
			},
		},
		{
			name: "RemoveProject",
			save: func() error { return RemoveProject("alpha") },
			lost: []string{"# alpha agent", "# shared ones", "# alpha checkout", "# alpha's own provider", "# rotated monthly", "# alpha app"},
			check: func(t *testing.T, cfg *Config) {
				if len(cfg.Projects) != 1 || cfg.Projects[0].Name != "beta" {
					t.Fatalf("projects = %+v", cfg.Projects)
				}
			},
		},
		{
			name: "AddPlatformToProject",
			save: func() error {
				return AddPlatformToProject("alpha", PlatformConfig{Type: "lark", Options: map[string]any{"app_id": "cli_l"}}, "", "")
			},
			check: func(t *testing.T, cfg *Config) {
				ps := cfg.Projects[0].Platforms
				if len(ps) != 2 || ps[1].Type != "lark" || stringMapValue(ps[1].Options, "app_id") != "cli_l" {
					t.Fatalf("alpha platforms = %+v", ps)
				}
			},
		},
		{
			name: "AddPlatformToProject new project",
			save: func() error {
				return AddPlatformToProject("gamma", PlatformConfig{Type: "feishu"}, "/srv/gamma", "codex")
			},
			check: func(t *testing.T, cfg *Config) {
				p := cfg.Projects[2]
				if p.Name != "gamma" || p.Agent.Type != "codex" || stringMapValue(p.Agent.Options, "work_dir") != "/srv/gamma" ||
					len(p.Platforms) != 1 || p.Platforms[0].Type != "feishu" {
					t.Fatalf("gamma = %+v", p)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configPath := writeConfigFixture(t, tableWritersFixture)
			patchConfigPath(t, configPath)
			logs := captureSlog(t)

			if err := tc.save(); err != nil {
				t.Fatalf("save: %v", err)
			}
			raw, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(logs.String(), "rewriting the whole file") {
				t.Fatalf("fell back to rewriting the whole file:\n%s", logs)
			}
			for _, comment := range tableWritersComments {
				if kept := strings.Contains(string(raw), comment); kept == slices.Contains(tc.lost, comment) {
					t.Errorf("comment %q kept = %v:\n%s", comment, kept, raw)
				}
			}
			if strings.Contains(string(raw), `= ""`) {
				t.Errorf("an empty key was written:\n%s", raw)
			}
			if _, err := Load(configPath); err != nil {
				t.Fatalf("Load: %v\n%s", err, raw)
			}
			tc.check(t, readConfigFixture(t, configPath))
		})
	}
}

// The tables after the edited provider must stay as they were, apart from
// the blank lines formatTOML adds on every write.
func TestTableWritersLeaveFollowingTablesAlone(t *testing.T) {
	formatted := formatTOML(tableWritersFixture)
	following := formatted[strings.Index(formatted, "# custom commands"):strings.Index(formatted, "# alpha agent")]
	for _, save := range []func() error{
		func() error { return RemoveGlobalProvider("backup") },
		func() error {
			return UpdateGlobalProvider("backup", ProviderConfig{APIKey: "sk-b2", Env: map[string]string{"K": "v"},
				Models: []ProviderModelConfig{{Model: "m"}}})
		},
	} {
		configPath := writeConfigFixture(t, tableWritersFixture)
		patchConfigPath(t, configPath)
		if err := save(); err != nil {
			t.Fatalf("save: %v", err)
		}
		raw, _ := os.ReadFile(configPath)
		if !strings.Contains(string(raw), "\n\n"+following) {
			t.Fatalf("the tables after [[providers]] changed:\n%s", raw)
		}
	}
}

// The first element of a top-level array goes before the first project,
// after the last table, so the comments above the project stay with it.
func TestAddCommandPlacesFirstCommandBeforeProjects(t *testing.T) {
	configPath := writeConfigFixture(t, `language = "en"

# =====
# Projects
# =====

[[projects]]
name = "alpha"

[projects.agent]
type = "claudecode"

[[projects.platforms]]
type = "feishu"
`)
	patchConfigPath(t, configPath)

	if err := AddCommand(CommandConfig{Name: "review", Prompt: "review it"}); err != nil {
		t.Fatalf("AddCommand: %v", err)
	}
	raw, _ := os.ReadFile(configPath)
	want := "language = \"en\"\n\n[[commands]]\nname = \"review\"\nprompt = \"review it\"\n\n# =====\n# Projects\n"
	if !strings.HasPrefix(string(raw), want) {
		t.Fatalf("got:\n%s\nwant it to start with:\n%s", raw, want)
	}
}

// A layout the line edits cannot handle still saves, by rewriting the file.
func TestTableWritersFallBackForInlineTables(t *testing.T) {
	configPath := writeConfigFixture(t, `[[projects]]
name = "alpha"
agent = { type = "claudecode" }

[[projects.platforms]]
type = "feishu"
`)
	patchConfigPath(t, configPath)
	logs := captureSlog(t)

	if err := AddProviderToConfig("alpha", ProviderConfig{Name: "relay", APIKey: "sk"}); err != nil {
		t.Fatalf("AddProviderToConfig: %v", err)
	}
	if !strings.Contains(logs.String(), "rewriting the whole file") {
		t.Fatalf("want the fallback warning, got logs:\n%s", logs)
	}
	if ps := readConfigFixture(t, configPath).Projects[0].Agent.Providers; len(ps) != 1 || ps[0].Name != "relay" {
		t.Fatalf("providers = %+v", ps)
	}
}

func TestTableWritersReportMissingEntries(t *testing.T) {
	configPath := writeConfigFixture(t, tableWritersFixture)
	patchConfigPath(t, configPath)

	for name, err := range map[string]error{
		"AddCommand duplicate":          AddCommand(CommandConfig{Name: "review"}),
		"RemoveCommand missing":         RemoveCommand("nope"),
		"RemoveAlias missing":           RemoveAlias("nope"),
		"AddProviderToConfig project":   AddProviderToConfig("nope", ProviderConfig{Name: "x"}),
		"AddProviderToConfig duplicate": AddProviderToConfig("alpha", ProviderConfig{Name: "own"}),
		"RemoveProviderFromConfig":      RemoveProviderFromConfig("beta", "own"),
		"AddGlobalProvider duplicate":   AddGlobalProvider(ProviderConfig{Name: "relay"}),
		"UpdateGlobalProvider missing":  UpdateGlobalProvider("nope", ProviderConfig{}),
		"RemoveGlobalProvider missing":  RemoveGlobalProvider("nope"),
		"RemoveProject missing":         RemoveProject("nope"),
	} {
		if err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if raw, _ := os.ReadFile(configPath); string(raw) != tableWritersFixture {
		t.Fatalf("a failed save changed the file:\n%s", raw)
	}
}

func TestTableBlocks(t *testing.T) {
	lines := strings.Split(`[[providers]]
name = "a"
prompt = """
[[providers]]
"""

[[providers.models]]
model = "m"
# alias = "x" (commented out, part of the block)

# introduces [log]
[log]
level = "info"

[[providers]]
name = "b"
# introduces [[aliases]]
[[aliases]]
name = "x"`, "\n")
	got := tableBlocks(lines, 0, len(lines)-1, "providers")
	want := []tableBlock{{0, 8}, {14, 15}}
	if !slices.Equal(got, want) {
		t.Fatalf("tableBlocks = %v, want %v", got, want)
	}
}

// New tables go below commented-out keys, as the starter config has them,
// so uncommenting one later does not move it into the new table.
func TestAddProviderToConfigGoesBelowCommentedOutKeys(t *testing.T) {
	configPath := writeConfigFixture(t, `[[projects]]
name = "alpha"

[projects.agent]
type = "claudecode"

[projects.agent.options]
work_dir = "/srv/alpha"
# mode = "default"

[[projects.platforms]]
type = "feishu"
`)
	patchConfigPath(t, configPath)

	if err := AddProviderToConfig("alpha", ProviderConfig{Name: "relay", APIKey: "sk"}); err != nil {
		t.Fatalf("AddProviderToConfig: %v", err)
	}
	raw, _ := os.ReadFile(configPath)
	want := "work_dir = \"/srv/alpha\"\n# mode = \"default\"\n\n[[projects.agent.providers]]\nname = \"relay\"\napi_key = \"sk\"\n\n[[projects.platforms]]"
	if !strings.Contains(string(raw), want) {
		t.Fatalf("got:\n%s\nwant it to contain:\n%s", raw, want)
	}
}
