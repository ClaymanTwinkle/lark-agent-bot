package codex

import (
	"fmt"
	"strings"

	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

type permissionSettings struct {
	approval string
	sandbox  string
	reviewer string
}

func normalizeMode(raw string) string {
	mode := strings.ToLower(strings.TrimSpace(raw))
	if mode == "" {
		return "default"
	}
	return mode
}

func validateMode(mode, backend string) error {
	mode = normalizeMode(mode)
	switch mode {
	case "default", "auto-review", "read-only", "full-access":
	default:
		return fmt.Errorf("codex: unsupported mode %q; use default, auto-review, read-only or full-access", mode)
	}
	if normalizeBackend(backend) == "exec" && (mode == "default" || mode == "auto-review") {
		return fmt.Errorf("codex: mode %q requires backend=app_server; exec supports only read-only or full-access", mode)
	}
	return nil
}

// ValidateMode lets the engine reject obsolete commands and stale card actions
// before changing the agent or tearing down its running conversation.
func (a *Agent) ValidateMode(mode string) error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return validateMode(mode, a.backend)
}

// Every preset owns all three settings. No inherited reviewer can silently turn
// user approval into automatic review, or survive switching away from auto-review.
func modeSettings(mode string) permissionSettings {
	switch normalizeMode(mode) {
	case "default":
		return permissionSettings{"on-request", "workspace-write", "user"}
	case "auto-review":
		return permissionSettings{"on-request", "workspace-write", "auto_review"}
	case "full-access":
		return permissionSettings{"never", "danger-full-access", "user"}
	default:
		return permissionSettings{"on-request", "read-only", "user"}
	}
}

func (a *Agent) PermissionModes() []core.PermissionModeInfo {
	a.mu.RLock()
	backend := normalizeBackend(a.backend)
	a.mu.RUnlock()
	var modes []core.PermissionModeInfo
	if backend == "app_server" {
		modes = append(modes,
			core.PermissionModeInfo{Key: "default", NameKey: core.MsgPermissionDefaultName, DescKey: core.MsgPermissionDefaultDesc},
			core.PermissionModeInfo{Key: "auto-review", NameKey: core.MsgPermissionAutoReviewName, DescKey: core.MsgPermissionAutoReviewDesc})
	}
	readOnlyDesc := core.MsgPermissionReadOnlyDesc
	if backend == "exec" {
		readOnlyDesc = core.MsgPermissionReadOnlyExecDesc
	}
	return append(modes,
		core.PermissionModeInfo{Key: "read-only", NameKey: core.MsgPermissionReadOnlyName, DescKey: readOnlyDesc},
		core.PermissionModeInfo{Key: "full-access", NameKey: core.MsgPermissionFullAccessName, DescKey: core.MsgPermissionFullAccessDesc, Privileged: true})
}

// NormalizeMode returns the mode key SetMode(mode) would select
// (core.ModeNormalizer).
func (a *Agent) NormalizeMode(mode string) string {
	return normalizeMode(mode)
}
