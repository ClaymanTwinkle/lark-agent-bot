package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// worktreeDirName is the directory inside a repository's main worktree that
// holds the worktrees /workspace worktree creates.
const worktreeDirName = ".worktrees"

// gitWorktreeTimeout bounds one /workspace worktree command, including the
// checkout of a new worktree.
const gitWorktreeTimeout = 2 * time.Minute

// gitWorktree is one entry of `git worktree list --porcelain`.
type gitWorktree struct {
	Path   string
	Head   string
	Branch string // short branch name; "" when detached
	Bare   bool
}

// runGit runs git in dir and returns its trimmed stdout. On failure the error
// carries git's own message.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], detail)
	}
	return strings.TrimSpace(string(out)), nil
}

// listGitWorktrees returns the worktrees of the repository containing dir;
// the main worktree comes first.
func listGitWorktrees(ctx context.Context, dir string) ([]gitWorktree, error) {
	out, err := runGit(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var trees []gitWorktree
	for _, block := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n\n") {
		var t gitWorktree
		for _, line := range strings.Split(block, "\n") {
			key, value, _ := strings.Cut(line, " ")
			switch key {
			case "worktree":
				t.Path = normalizeWorkspacePath(filepath.FromSlash(value))
			case "HEAD":
				t.Head = value
			case "branch":
				t.Branch = strings.TrimPrefix(value, "refs/heads/")
			case "bare":
				t.Bare = true
			}
		}
		if t.Path != "" {
			trees = append(trees, t)
		}
	}
	if len(trees) == 0 {
		return nil, errors.New("git worktree list: no worktrees")
	}
	return trees, nil
}

// sameWorkspacePath reports whether two normalized paths name the same
// directory (case-insensitively on Windows).
func sameWorkspacePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// worktreePath returns where the worktree called name lives.
func worktreePath(root, name string) string {
	return normalizeWorkspacePath(filepath.Join(root, worktreeDirName, filepath.FromSlash(name)))
}

// validWorktreeName reports whether name can serve as both the branch and
// the directory of a worktree.
func validWorktreeName(ctx context.Context, root, name string) bool {
	if name == "" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "@") || strings.Contains(name, `\`) {
		return false
	}
	_, err := runGit(ctx, root, "check-ref-format", "--branch", name)
	return err == nil
}

// excludeWorktreeDir keeps the worktree directory out of the main worktree's
// status through the repository's local info/exclude, so a `git add -A` there
// never picks up the nested worktrees.
func excludeWorktreeDir(ctx context.Context, root string) error {
	if _, err := runGit(ctx, root, "check-ignore", "-q", worktreeDirName+"/"); err == nil {
		return nil
	}
	common, err := runGit(ctx, root, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}
	common = filepath.FromSlash(common)
	if !filepath.IsAbs(common) {
		common = filepath.Join(root, common)
	}
	path := filepath.Join(common, "info", "exclude")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	line := "/" + worktreeDirName + "/\n"
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		line = "\n" + line
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := f.WriteString(line); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	return f.Close()
}

// handleWorktreeCommand implements /workspace worktree (alias wt). It manages
// git worktrees of the repository bound to the chat, kept under
// <main worktree>/.worktrees/<name>, and routes the chat (a topic under
// thread isolation) to one, so several agents can work on one project in
// parallel:
//
//	/workspace worktree             list the worktrees
//	/workspace worktree <name>      create or reuse <name> and route the chat to it
//	/workspace worktree rm <name>   remove <name>; its branch is kept
func (e *Engine) handleWorktreeCommand(p Platform, msg *Message, channelKey string, channelName func() string, args []string) {
	b, _, usable := e.lookupEffectiveWorkspaceBinding(channelKey)
	if !usable {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsNoBinding))
		return
	}
	current := normalizeWorkspacePath(b.Workspace)

	ctx, cancel := context.WithTimeout(e.ctx, gitWorktreeTimeout)
	defer cancel()
	trees, err := listGitWorktrees(ctx, current)
	if err == nil && trees[0].Bare {
		err = errors.New("bare repository")
	}
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsWorktreeNotRepo, current, err))
		return
	}

	switch {
	case len(args) == 0 || (len(args) == 1 && args[0] == "list"):
		e.replyWorktreeList(p, msg, trees, current)
	case args[0] == "rm" || args[0] == "remove":
		if len(args) != 2 {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsWorktreeUsage))
			return
		}
		e.removeWorktree(ctx, p, msg, trees, args[1])
	case len(args) == 1:
		e.openWorktree(ctx, p, msg, channelKey, channelName, trees, args[0])
	default:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsWorktreeUsage))
	}
}

func (e *Engine) replyWorktreeList(p Platform, msg *Message, trees []gitWorktree, current string) {
	root := trees[0].Path
	var sb strings.Builder
	sb.WriteString(e.i18n.Tf(MsgWsWorktreeListTitle, root))
	for i, t := range trees {
		marker := "•"
		if sameWorkspacePath(t.Path, current) {
			marker = "▶"
		}
		name := t.Path
		if i == 0 {
			name = e.i18n.T(MsgWsWorktreeMainLabel)
		} else if rel, err := filepath.Rel(filepath.Join(root, worktreeDirName), t.Path); err == nil && !strings.HasPrefix(rel, "..") {
			name = filepath.ToSlash(rel)
		}
		branch := t.Branch
		if branch == "" && len(t.Head) >= 7 {
			branch = t.Head[:7]
		}
		sb.WriteString(fmt.Sprintf("%s `%s` · %s\n", marker, name, branch))
	}
	sb.WriteString("\n" + e.i18n.T(MsgWsWorktreeUsage))
	e.reply(p, msg.ReplyCtx, sb.String())
}

// openWorktree routes the chat to the worktree called name, creating it (and
// its branch, from the main worktree's HEAD) when it does not exist yet.
func (e *Engine) openWorktree(ctx context.Context, p Platform, msg *Message, channelKey string, channelName func() string, trees []gitWorktree, name string) {
	root := trees[0].Path
	if !validWorktreeName(ctx, root, name) {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsWorktreeInvalidName, name))
		return
	}
	path := worktreePath(root, name)
	projectKey := "project:" + e.name
	for _, t := range trees[1:] {
		if sameWorkspacePath(t.Path, path) {
			e.workspaceBindings.Bind(projectKey, channelKey, channelName(), t.Path)
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsWorktreeSwitched, name, t.Path))
			return
		}
	}
	if _, err := os.Stat(path); err == nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsWorktreePathTaken, path))
		return
	}

	if err := excludeWorktreeDir(ctx, root); err != nil {
		slog.Warn("worktree: could not exclude the worktree directory from git status", "repo", root, "error", err)
	}
	var reply string
	if _, err := runGit(ctx, root, "show-ref", "--verify", "--quiet", "refs/heads/"+name); err == nil {
		if _, err := runGit(ctx, root, "worktree", "add", path, name); err != nil {
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsWorktreeFailed, err))
			return
		}
		path = normalizeWorkspacePath(path)
		reply = e.i18n.Tf(MsgWsWorktreeCreatedExisting, name, path)
	} else {
		base, _ := runGit(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
		if _, err := runGit(ctx, root, "worktree", "add", "-b", name, path); err != nil {
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsWorktreeFailed, err))
			return
		}
		path = normalizeWorkspacePath(path)
		reply = e.i18n.Tf(MsgWsWorktreeCreated, name, base, path)
	}
	e.workspaceBindings.Bind(projectKey, channelKey, channelName(), path)
	slog.Info("worktree: chat routed to worktree", "channel_key", channelKey, "worktree", path)
	e.reply(p, msg.ReplyCtx, reply)
}

// removeWorktree removes the worktree called name once nothing runs in it,
// and routes the chats bound to it back to the main worktree.
func (e *Engine) removeWorktree(ctx context.Context, p Platform, msg *Message, trees []gitWorktree, name string) {
	root := trees[0].Path
	path := worktreePath(root, name)
	var tree *gitWorktree
	for i := range trees[1:] {
		if sameWorkspacePath(trees[i+1].Path, path) {
			tree = &trees[i+1]
			break
		}
	}
	if tree == nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsWorktreeNotFound, name))
		return
	}
	if e.worktreeHasActiveTurn(tree.Path) {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsWorktreeBusy, name))
		return
	}
	if status, err := runGit(ctx, tree.Path, "status", "--porcelain"); err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsWorktreeFailed, err))
		return
	} else if status != "" {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsWorktreeDirty, name))
		return
	}

	// The agent processes working there hold the directory open (Windows
	// refuses to delete it); they are idle, so close them first.
	e.closeWorkspaceSessions(tree.Path)
	if _, err := runGit(ctx, root, "worktree", "remove", tree.Path); err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsWorktreeFailed, err))
		return
	}

	projectKey := "project:" + e.name
	for key, b := range e.workspaceBindings.ListByProject(projectKey) {
		if sameWorkspacePath(normalizeWorkspacePath(b.Workspace), tree.Path) {
			e.workspaceBindings.Bind(projectKey, key, b.ChannelName, root)
		}
	}
	slog.Info("worktree: removed", "worktree", tree.Path, "branch", tree.Branch)
	e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsWorktreeRemoved, name, tree.Branch, root))
}

func (e *Engine) worktreeHasActiveTurn(path string) bool {
	e.interactiveMu.Lock()
	pool := e.workspacePool
	e.interactiveMu.Unlock()
	if pool == nil {
		return false
	}
	for dir, ws := range pool.All() {
		if sameWorkspacePath(normalizeWorkspacePath(dir), path) && ws.HasActiveTurn() {
			return true
		}
	}
	return false
}

// closeWorkspaceSessions closes the live agent sessions working in dir.
func (e *Engine) closeWorkspaceSessions(dir string) {
	e.interactiveMu.Lock()
	var keys []string
	for key, state := range e.interactiveStates {
		if state == nil {
			continue
		}
		state.mu.Lock()
		stateDir := state.workspaceDir
		state.mu.Unlock()
		// Workspace states are keyed "<workspace>:<sessionKey>".
		keyInDir := len(key) > len(dir) && key[len(dir)] == ':' && sameWorkspacePath(key[:len(dir)], dir)
		if keyInDir || (stateDir != "" && sameWorkspacePath(normalizeWorkspacePath(stateDir), dir)) {
			keys = append(keys, key)
		}
	}
	e.interactiveMu.Unlock()
	for _, key := range keys {
		e.cleanupInteractiveState(key)
	}
}
