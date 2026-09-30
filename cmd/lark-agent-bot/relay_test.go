package main

import (
	"path/filepath"
	"testing"

	"github.com/ClaymanTwinkle/lark-agent-bot/config"
	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

func TestRelayDepthFromEnv(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want int
	}{
		{"", 0},
		{"2", 2},
		{" 1 ", 1},
		{"-1", 0},
		{"abc", 0},
	} {
		t.Setenv("CC_RELAY_DEPTH", tc.env)
		if got := relayDepthFromEnv(); got != tc.want {
			t.Errorf("CC_RELAY_DEPTH=%q: relayDepthFromEnv() = %d, want %d", tc.env, got, tc.want)
		}
	}
}

func TestResolveRelayPeersDir(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "peers")
	cfg := &config.Config{}
	cfg.Relay.PeersDir = custom
	if got := resolveRelayPeersDir(cfg); got != custom {
		t.Fatalf("resolveRelayPeersDir() = %q, want configured %q", got, custom)
	}

	cfg.Relay.PeersDir = ""
	if got := resolveRelayPeersDir(cfg); got != core.DefaultRelayPeersDir() {
		t.Fatalf("resolveRelayPeersDir() = %q, want default %q", got, core.DefaultRelayPeersDir())
	}
}
