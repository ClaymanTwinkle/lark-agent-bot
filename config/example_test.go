package config

import (
	"os"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The example config's promise: every commented example works when it is
// uncommented where it stands. TOML has no way to end a table, so a key
// written below a table header belongs to that table, and a misplaced key is
// dropped without an error (#12).

var (
	exampleKeyLine    = regexp.MustCompile(`^# [A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*\s*=\s*\S`)
	exampleHeaderLine = regexp.MustCompile(`^# \[\[?[A-Za-z0-9_.-]+\]\]?\s*(#.*)?$`)
)

func readExampleLines(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile("../config.example.toml")
	if err != nil {
		t.Fatalf("read config.example.toml: %v", err)
	}
	lines := strings.Split(string(data), "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

func isExampleTOMLLine(line string) bool {
	return exampleKeyLine.MatchString(line) || exampleHeaderLine.MatchString(line)
}

// uncommentExample returns the example with the given lines uncommented.
func uncommentExample(lines []string, which []int) string {
	out := append([]string(nil), lines...)
	for _, i := range which {
		out[i] = strings.TrimPrefix(out[i], "# ")
	}
	return strings.Join(out, "\n")
}

func decodeExample(t *testing.T, content string) (*Config, []string, error) {
	t.Helper()
	cfg := &Config{}
	unknown, err := decodeConfig([]byte(content), cfg)
	return cfg, unknown, err
}

// validateExample validates cfg, except for run_as_user, which only Linux and
// macOS accept.
func validateExample(cfg *Config) error {
	err := cfg.validate()
	if err != nil && runtime.GOOS == "windows" && strings.Contains(err.Error(), "run_as_user is only supported") {
		return nil
	}
	return err
}

func TestConfigExampleLoadsAsShipped(t *testing.T) {
	cfg, unknown, err := decodeExample(t, strings.Join(readExampleLines(t), "\n"))
	if err != nil {
		t.Fatalf("parse config.example.toml: %v", err)
	}
	if len(unknown) > 0 {
		t.Fatalf("config.example.toml has unknown keys: %v", unknown)
	}
	if err := validateExample(cfg); err != nil {
		t.Fatalf("config.example.toml does not validate: %v", err)
	}
}

// TestConfigExampleCommentedKeysWorkInPlace uncomments each commented key on
// its own, together with every commented table header between it and the
// active table header above it: the key must still land in its own table
// when the example sections above it are enabled too.
func TestConfigExampleCommentedKeysWorkInPlace(t *testing.T) {
	lines := readExampleLines(t)
	for i, line := range lines {
		if !exampleKeyLine.MatchString(line) {
			continue
		}
		which := []int{i}
		for j := i - 1; j >= 0 && !strings.HasPrefix(lines[j], "["); j-- {
			if exampleHeaderLine.MatchString(lines[j]) {
				which = append(which, j)
			}
		}
		_, unknown, err := decodeExample(t, uncommentExample(lines, which))
		if err != nil {
			t.Errorf("line %d %q: uncommented in place, the config does not parse: %v", i+1, line, err)
			continue
		}
		if len(unknown) > 0 {
			t.Errorf("line %d %q: uncommented in place, it lands in the wrong table and is ignored: %v", i+1, line, unknown)
		}
	}
}

// TestConfigExampleCommentedBlocksWorkInPlace uncomments every commented
// setting of one comment block at once, the way a user enables a whole
// example section, and checks the result still validates.
func TestConfigExampleCommentedBlocksWorkInPlace(t *testing.T) {
	lines := readExampleLines(t)
	for start := 0; start < len(lines); start++ {
		if !strings.HasPrefix(lines[start], "#") {
			continue
		}
		end := start
		var which []int
		for end < len(lines) && strings.HasPrefix(lines[end], "#") {
			if isExampleTOMLLine(lines[end]) {
				which = append(which, end)
			}
			end++
		}
		if len(which) > 0 {
			cfg, unknown, err := decodeExample(t, uncommentExample(lines, which))
			switch {
			case err != nil:
				t.Errorf("lines %d-%d uncommented: the config does not parse: %v", start+1, end, err)
			case len(unknown) > 0:
				t.Errorf("lines %d-%d uncommented: keys land in the wrong table and are ignored: %v", start+1, end, unknown)
			default:
				if err := validateExample(cfg); err != nil {
					t.Errorf("lines %d-%d uncommented: the config does not validate: %v", start+1, end, err)
				}
			}
		}
		start = end
	}
}

// TestConfigExampleKeysLandInTheirField checks keys users reach for, each
// uncommented on its own, take effect where the comments say.
func TestConfigExampleKeysLandInTheirField(t *testing.T) {
	cases := []struct {
		prefix string
		check  func(*Config) bool
	}{
		{"# language = \"en\"", func(c *Config) bool { return c.Language != "" }},
		{"# provider_presets_url = ", func(c *Config) bool { return c.ProviderPresetsURL != "" }},
		{"# idle_timeout_mins = ", func(c *Config) bool { return c.IdleTimeoutMins != nil }},
		{"# stall_notice_mins = ", func(c *Config) bool { return c.StallNoticeMins != nil }},
		{"# stall_tool_notice_mins = ", func(c *Config) bool { return c.StallToolNoticeMins != nil }},
		{"# retry_notice_attempts = ", func(c *Config) bool { return c.RetryNoticeAttempts != nil }},
		{"# upgrade_restart_wait_mins = ", func(c *Config) bool { return c.UpgradeRestartWaitMins != nil }},
		{"# max_turn_time_mins = ", func(c *Config) bool { return c.MaxTurnTimeMins != nil }},
		{"# workspace_idle_timeout_mins = ", func(c *Config) bool { return c.WorkspaceIdleTimeoutMins != nil }},
		{"# banned_words = ", func(c *Config) bool { return len(c.BannedWords) > 0 }},
		{"# reset_on_idle_mins = ", func(c *Config) bool { return len(c.Projects) == 1 && c.Projects[0].ResetOnIdleMins != nil }},
		{"# agent_session_idle_timeout_mins = ", func(c *Config) bool {
			return len(c.Projects) == 1 && c.Projects[0].AgentSessionIdleTimeoutMins != nil
		}},
		{"# provider_refs = ", func(c *Config) bool { return len(c.Projects) == 1 && len(c.Projects[0].Agent.ProviderRefs) > 0 }},
		{"# cmd = ", func(c *Config) bool { return len(c.Projects) == 1 && c.Projects[0].Agent.Options["cmd"] != nil }},
		{"# router_url = ", func(c *Config) bool {
			return len(c.Projects) == 1 && c.Projects[0].Agent.Options["router_url"] != nil
		}},
		{"# require_mention = ", func(c *Config) bool {
			return len(c.Projects) == 1 && len(c.Projects[0].Platforms) == 1 &&
				c.Projects[0].Platforms[0].Options["require_mention"] != nil
		}},
	}
	lines := readExampleLines(t)
	for _, tc := range cases {
		t.Run(strings.TrimSpace(strings.TrimPrefix(tc.prefix, "#")), func(t *testing.T) {
			found := -1
			for i, line := range lines {
				if strings.HasPrefix(line, tc.prefix) {
					if found >= 0 {
						t.Fatalf("lines %d and %d both start with %q", found+1, i+1, tc.prefix)
					}
					found = i
				}
			}
			if found < 0 {
				t.Fatalf("config.example.toml has no line starting with %q", tc.prefix)
			}
			cfg, unknown, err := decodeExample(t, uncommentExample(lines, []int{found}))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(unknown) > 0 {
				t.Fatalf("unknown keys: %v", unknown)
			}
			if !tc.check(cfg) {
				t.Fatalf("line %d %q, uncommented, does not set its field", found+1, lines[found])
			}
			if err := validateExample(cfg); err != nil {
				t.Fatalf("validate: %v", err)
			}
		})
	}
}
