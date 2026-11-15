package core

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

type projectStateData struct {
	WorkDirOverride         string            `json:"work_dir_override,omitempty"`
	WorkspaceDirOverrides   map[string]string `json:"workspace_dir_overrides,omitempty"`
	WorkspaceModelOverrides map[string]string `json:"workspace_model_overrides,omitempty"`
	// WorkspaceReasoningOverrides keeps /reasoning per workspace, so a
	// workspace agent recreated after an idle reap or a restart keeps it.
	WorkspaceReasoningOverrides map[string]string `json:"workspace_reasoning_overrides,omitempty"`
	// WorkspaceModeOverrides keeps /mode per workspace for the same reason.
	WorkspaceModeOverrides map[string]string `json:"workspace_mode_overrides,omitempty"`
	// WorkspaceAllowedTools keeps the tools /allow added in each workspace.
	// The recreated agent gets them on top of the project's allowed_tools.
	WorkspaceAllowedTools map[string][]string `json:"workspace_allowed_tools,omitempty"`
}

// ProjectStateStore persists lightweight runtime state for one project.
type ProjectStateStore struct {
	mu        sync.RWMutex
	storePath string
	state     projectStateData
}

func NewProjectStateStore(path string) *ProjectStateStore {
	ps := &ProjectStateStore{storePath: path}
	if path != "" {
		ps.load()
	}
	return ps
}

func (ps *ProjectStateStore) WorkDirOverride() string {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.state.WorkDirOverride
}

func (ps *ProjectStateStore) SetWorkDirOverride(dir string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.state.WorkDirOverride = dir
}

func (ps *ProjectStateStore) WorkspaceDirOverride(workspace string) string {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	if ps.state.WorkspaceDirOverrides == nil {
		return ""
	}
	return ps.state.WorkspaceDirOverrides[workspace]
}

func (ps *ProjectStateStore) SetWorkspaceDirOverride(workspace, dir string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.state.WorkspaceDirOverrides == nil {
		ps.state.WorkspaceDirOverrides = make(map[string]string)
	}
	ps.state.WorkspaceDirOverrides[workspace] = dir
}

func (ps *ProjectStateStore) ClearWorkspaceDirOverride(workspace string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.state.WorkspaceDirOverrides == nil {
		return
	}
	delete(ps.state.WorkspaceDirOverrides, workspace)
	if len(ps.state.WorkspaceDirOverrides) == 0 {
		ps.state.WorkspaceDirOverrides = nil
	}
}

func (ps *ProjectStateStore) WorkspaceModelOverride(workspace string) string {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	if ps.state.WorkspaceModelOverrides == nil {
		return ""
	}
	return ps.state.WorkspaceModelOverrides[workspace]
}

func (ps *ProjectStateStore) SetWorkspaceModelOverride(workspace, model string) {
	if model == "" {
		ps.ClearWorkspaceModelOverride(workspace)
		return
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.state.WorkspaceModelOverrides == nil {
		ps.state.WorkspaceModelOverrides = make(map[string]string)
	}
	ps.state.WorkspaceModelOverrides[workspace] = model
}

func (ps *ProjectStateStore) ClearWorkspaceModelOverride(workspace string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.state.WorkspaceModelOverrides == nil {
		return
	}
	delete(ps.state.WorkspaceModelOverrides, workspace)
	if len(ps.state.WorkspaceModelOverrides) == 0 {
		ps.state.WorkspaceModelOverrides = nil
	}
}

func (ps *ProjectStateStore) WorkspaceReasoningOverride(workspace string) string {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	if ps.state.WorkspaceReasoningOverrides == nil {
		return ""
	}
	return ps.state.WorkspaceReasoningOverrides[workspace]
}

func (ps *ProjectStateStore) SetWorkspaceReasoningOverride(workspace, effort string) {
	if effort == "" {
		ps.ClearWorkspaceReasoningOverride(workspace)
		return
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.state.WorkspaceReasoningOverrides == nil {
		ps.state.WorkspaceReasoningOverrides = make(map[string]string)
	}
	ps.state.WorkspaceReasoningOverrides[workspace] = effort
}

func (ps *ProjectStateStore) ClearWorkspaceReasoningOverride(workspace string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.state.WorkspaceReasoningOverrides == nil {
		return
	}
	delete(ps.state.WorkspaceReasoningOverrides, workspace)
	if len(ps.state.WorkspaceReasoningOverrides) == 0 {
		ps.state.WorkspaceReasoningOverrides = nil
	}
}

func (ps *ProjectStateStore) WorkspaceModeOverride(workspace string) string {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.state.WorkspaceModeOverrides[workspace]
}

func (ps *ProjectStateStore) SetWorkspaceModeOverride(workspace, mode string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if mode == "" {
		delete(ps.state.WorkspaceModeOverrides, workspace)
		if len(ps.state.WorkspaceModeOverrides) == 0 {
			ps.state.WorkspaceModeOverrides = nil
		}
		return
	}
	if ps.state.WorkspaceModeOverrides == nil {
		ps.state.WorkspaceModeOverrides = make(map[string]string)
	}
	ps.state.WorkspaceModeOverrides[workspace] = mode
}

// WorkspaceAllowedTools returns a copy of the tools /allow added in workspace.
func (ps *ProjectStateStore) WorkspaceAllowedTools(workspace string) []string {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return slices.Clone(ps.state.WorkspaceAllowedTools[workspace])
}

// AddWorkspaceAllowedTool records tool for workspace, once.
func (ps *ProjectStateStore) AddWorkspaceAllowedTool(workspace, tool string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if slices.Contains(ps.state.WorkspaceAllowedTools[workspace], tool) {
		return
	}
	if ps.state.WorkspaceAllowedTools == nil {
		ps.state.WorkspaceAllowedTools = make(map[string][]string)
	}
	ps.state.WorkspaceAllowedTools[workspace] = append(ps.state.WorkspaceAllowedTools[workspace], tool)
}

func (ps *ProjectStateStore) ClearWorkDirOverride() {
	ps.SetWorkDirOverride("")
}

func (ps *ProjectStateStore) Save() {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	ps.saveLocked()
}

func (ps *ProjectStateStore) saveLocked() {
	if ps.storePath == "" {
		return
	}

	data, err := json.MarshalIndent(ps.state, "", "  ")
	if err != nil {
		slog.Error("project_state: failed to marshal", "error", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(ps.storePath), 0o755); err != nil {
		slog.Error("project_state: failed to create dir", "path", ps.storePath, "error", err)
		return
	}
	if err := AtomicWriteFile(ps.storePath, data, 0o644); err != nil {
		slog.Error("project_state: failed to write", "path", ps.storePath, "error", err)
	}
}

func (ps *ProjectStateStore) load() {
	data, err := os.ReadFile(ps.storePath)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Error("project_state: failed to read", "path", ps.storePath, "error", err)
		}
		return
	}

	var state projectStateData
	if err := json.Unmarshal(data, &state); err != nil {
		slog.Error("project_state: failed to unmarshal", "path", ps.storePath, "error", err)
		return
	}
	ps.state = state
}
