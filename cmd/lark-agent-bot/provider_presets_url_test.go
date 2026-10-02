package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfiguredPresetsURL(t *testing.T) {
	dir := t.TempDir()
	if got := configuredPresetsURL(filepath.Join(dir, "missing.toml")); got != "" {
		t.Fatalf("missing config: got %q, want empty", got)
	}

	path := filepath.Join(dir, "config.toml")
	cfg := `provider_presets_url = "https://example.com/presets.json"

[[projects]]
name = "p"
[projects.agent]
type = "claudecode"
`
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := configuredPresetsURL(path), "https://example.com/presets.json"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
