package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newWorktreeTestRepo creates a git repository on branch main with one commit.
func newWorktreeTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := normalizeWorkspacePath(t.TempDir())
	testGit(t, repo, "init", "-q", "-b", "main")
	writeGitFile(t, filepath.Join(repo, "README.md"), "hello\n")
	testGit(t, repo, "add", "README.md")
	testGit(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init")
	return repo
}

func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

type worktreeTestChat struct {
	e          *Engine
	p          *stubPlatformEngine
	msg        *Message
	channelKey string
}

// newWorktreeTestChat returns an admin's chat in a multi-workspace engine,
// bound to dir.
func newWorktreeTestChat(t *testing.T, dir string) *worktreeTestChat {
	t.Helper()
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.SetMultiWorkspace(t.TempDir(), filepath.Join(t.TempDir(), "bindings.json"))
	e.SetAdminFrom("admin")
	c := &worktreeTestChat{
		e:          e,
		p:          p,
		msg:        &Message{SessionKey: "test:ch1:admin", UserID: "admin", ReplyCtx: "ctx"},
		channelKey: workspaceChannelKey("test", "ch1"),
	}
	if dir != "" {
		e.workspaceBindings.Bind("project:test", c.channelKey, "", dir)
	}
	return c
}

func (c *worktreeTestChat) run(content string) string {
	c.p.clearSent()
	c.msg.Content = content
	c.e.handleCommand(c.p, c.msg, content)
	return strings.Join(c.p.getSent(), "\n")
}

func (c *worktreeTestChat) bound() string {
	if b := c.e.workspaceBindings.Lookup("project:test", c.channelKey); b != nil {
		return b.Workspace
	}
	return ""
}

func TestWorktreeCommand_CreateListSwitchRemove(t *testing.T) {
	repo := newWorktreeTestRepo(t)
	c := newWorktreeTestChat(t, repo)
	wt := worktreePath(repo, "feat-a")

	if got := c.run("/ws wt feat-a"); !strings.Contains(got, "Created worktree `feat-a` (new branch from `main`)") {
		t.Fatalf("create reply = %q", got)
	}
	if c.bound() != wt {
		t.Fatalf("chat bound to %q, want the worktree %q", c.bound(), wt)
	}
	if got := testGit(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); got != "feat-a" {
		t.Fatalf("worktree branch = %q, want feat-a", got)
	}
	if got := testGit(t, repo, "status", "--porcelain"); got != "" {
		t.Fatalf("main worktree status = %q, want the worktree directory excluded", got)
	}

	list := c.run("/ws wt")
	if !strings.Contains(list, "▶ `feat-a` · feat-a") || !strings.Contains(list, "• `main` · main") {
		t.Fatalf("list = %q, want feat-a marked current next to main", list)
	}
	if got := c.run("/workspace worktree feat-a"); !strings.Contains(got, "Switched to worktree `feat-a`") {
		t.Fatalf("reuse reply = %q", got)
	}

	writeGitFile(t, filepath.Join(wt, "wip.txt"), "wip\n")
	if got := c.run("/ws wt rm feat-a"); !strings.Contains(got, "uncommitted changes") {
		t.Fatalf("dirty remove reply = %q", got)
	}
	if err := os.Remove(filepath.Join(wt, "wip.txt")); err != nil {
		t.Fatal(err)
	}

	// A session working there, in a turn or with background tasks left
	// running, keeps the worktree: removing it would kill that work.
	busy := &interactiveState{agentSession: newControllableSession("wt-busy"), workspaceDir: wt}
	endTurn := busy.beginTurn()
	c.e.interactiveMu.Lock()
	c.e.interactiveStates["busy"] = busy
	c.e.interactiveMu.Unlock()
	if got := c.run("/ws wt rm feat-a"); !strings.Contains(got, "still running") {
		t.Fatalf("busy remove reply = %q", got)
	}
	endTurn()
	bg := newBgTaskSession()
	bg.setTasks("survey")
	c.e.interactiveMu.Lock()
	c.e.interactiveStates["busy"] = &interactiveState{agentSession: bg, workspaceDir: wt}
	c.e.interactiveMu.Unlock()
	if got := c.run("/ws wt rm feat-a"); !strings.Contains(got, "still running") {
		t.Fatalf("remove with background tasks running reply = %q", got)
	}
	c.e.interactiveMu.Lock()
	delete(c.e.interactiveStates, "busy")
	c.e.interactiveMu.Unlock()

	live := newControllableSession("wt-session")
	liveKey := wt + ":" + c.msg.SessionKey
	c.e.interactiveMu.Lock()
	c.e.interactiveStates[liveKey] = &interactiveState{agentSession: live, workspaceDir: wt}
	c.e.interactiveMu.Unlock()

	if got := c.run("/ws wt rm feat-a"); !strings.Contains(got, "Removed worktree `feat-a`; branch `feat-a` is kept") {
		t.Fatalf("remove reply = %q", got)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree directory still there: %v", err)
	}
	if c.bound() != repo {
		t.Fatalf("chat bound to %q after removal, want the main worktree %q", c.bound(), repo)
	}
	if live.Alive() {
		t.Fatal("the idle agent session in the worktree was not closed")
	}
	testGit(t, repo, "rev-parse", "--verify", "refs/heads/feat-a")
}

func TestWorktreeCommand_ChecksOutAnExistingBranch(t *testing.T) {
	repo := newWorktreeTestRepo(t)
	testGit(t, repo, "branch", "fix/login")
	c := newWorktreeTestChat(t, repo)

	if got := c.run("/ws wt fix/login"); !strings.Contains(got, "Created a worktree for the existing branch `fix/login`") {
		t.Fatalf("reply = %q", got)
	}
	wt := worktreePath(repo, "fix/login")
	if c.bound() != wt || testGit(t, wt, "rev-parse", "--abbrev-ref", "HEAD") != "fix/login" {
		t.Fatalf("chat bound to %q, want %q on fix/login", c.bound(), wt)
	}
}

func TestWorktreeCommand_Refusals(t *testing.T) {
	repo := newWorktreeTestRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, worktreeDirName, "taken"), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, dir, user, command, want string
	}{
		{"invalid name", repo, "admin", "/ws wt bad..name", "cannot be used as a branch name"},
		{"option-like name", repo, "admin", "/ws wt -x", "cannot be used as a branch name"},
		{"directory taken", repo, "admin", "/ws wt taken", "is not a worktree of this repository"},
		{"unknown worktree", repo, "admin", "/ws wt rm nope", "No worktree named `nope`"},
		{"not an admin", repo, "someone", "/ws wt feat-b", "requires admin privilege"},
		{"no binding", "", "admin", "/ws wt feat-b", "No workspace bound to this channel."},
		{"not a repository", normalizeWorkspacePath(t.TempDir()), "admin", "/ws wt feat-b", "is not a usable git repository"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.dir != "" && tc.dir != repo {
				// Keep git from finding a repository above the temp dir.
				t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(tc.dir))
			}
			c := newWorktreeTestChat(t, tc.dir)
			c.msg.UserID = tc.user
			if got := c.run(tc.command); !strings.Contains(got, tc.want) {
				t.Fatalf("reply = %q, want %q", got, tc.want)
			}
			if c.bound() != tc.dir {
				t.Fatalf("chat bound to %q, want unchanged %q", c.bound(), tc.dir)
			}
		})
	}
	if got := testGit(t, repo, "worktree", "list"); strings.Count(got, "\n") != 0 {
		t.Fatalf("worktrees = %q, want only the main one", got)
	}
}

// Under Feishu thread isolation a topic has its own binding: routing a topic
// to a worktree leaves the rest of the group on the main worktree.
func TestWorktreeCommand_RoutesOnlyTheTopic(t *testing.T) {
	repo := newWorktreeTestRepo(t)
	c := newWorktreeTestChat(t, repo)
	groupKey := c.channelKey
	c.msg.Platform = "test"
	c.msg.ChannelKey = "ch1:topic:r1"
	c.msg.LegacyChannelKey = "ch1"
	c.channelKey = workspaceChannelKey("test", "ch1:topic:r1")
	c.e.migrateLegacyWorkspaceBindings(c.msg)

	c.run("/ws wt topic-task")
	if want := worktreePath(repo, "topic-task"); c.bound() != want {
		t.Fatalf("topic bound to %q, want %q", c.bound(), want)
	}
	if b := c.e.workspaceBindings.Lookup("project:test", groupKey); b == nil || b.Workspace != repo {
		t.Fatalf("group binding = %+v, want it left on %q", b, repo)
	}
}

func TestIsPrivilegedCommandInvocation_WorkspaceWorktree(t *testing.T) {
	for args, want := range map[string]bool{
		"wt feat":       true,
		"worktree":      true,
		"worktree rm x": true,
		"list":          false,
		"route /tmp":    false,
	} {
		if got := isPrivilegedCommandInvocation("workspace", strings.Fields(args)); got != want {
			t.Errorf("/workspace %s privileged = %v, want %v", args, got, want)
		}
	}
}
