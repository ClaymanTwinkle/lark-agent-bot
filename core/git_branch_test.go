package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeGitFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGitBranch(t *testing.T) {
	repo := t.TempDir()
	writeGitFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/feature/footer\n")
	sub := filepath.Join(repo, "src", "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	detached := t.TempDir()
	writeGitFile(t, filepath.Join(detached, ".git", "HEAD"), "0123456789abcdef0123456789abcdef01234567\n")

	// A worktree's .git is a file pointing (here relatively) at its git dir.
	worktree := t.TempDir()
	writeGitFile(t, filepath.Join(worktree, "gitdir", "HEAD"), "ref: refs/heads/wt-branch\n")
	writeGitFile(t, filepath.Join(worktree, ".git"), "gitdir: gitdir\n")

	for name, tc := range map[string]struct{ dir, want string }{
		"repo root":     {repo, "feature/footer"},
		"subdirectory":  {sub, "feature/footer"},
		"detached HEAD": {detached, "0123456"},
		"worktree":      {worktree, "wt-branch"},
		"not a repo":    {t.TempDir(), ""},
		"empty":         {"", ""},
	} {
		if got := gitBranch(tc.dir); got != tc.want {
			t.Errorf("%s: gitBranch(%q) = %q, want %q", name, tc.dir, got, tc.want)
		}
	}
}

func TestReplyFooterWorkDirShowsTheBranch(t *testing.T) {
	repo := t.TempDir()
	writeGitFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")

	got := replyFooterWorkDir(nil, &stubAgent{}, repo)
	if !strings.HasSuffix(got, " (main)") {
		t.Fatalf("footer workdir = %q, want the branch after the path", got)
	}
	if plain := replyFooterWorkDir(nil, &stubAgent{}, t.TempDir()); strings.Contains(plain, "(") {
		t.Fatalf("footer workdir = %q, want no branch outside a git repo", plain)
	}
}

// newGitRepo creates a real git work tree with one committed file, or skips
// the test when git is not installed.
func newGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	writeGitFile(t, filepath.Join(repo, "tracked.txt"), "v1\n")
	run("add", "tracked.txt")
	run("commit", "-q", "-m", "init")
	return repo
}

func TestGitDirty(t *testing.T) {
	for name, change := range map[string]func(repo string){
		"clean":     func(string) {},
		"modified":  func(repo string) { writeGitFile(t, filepath.Join(repo, "tracked.txt"), "v2\n") },
		"untracked": func(repo string) { writeGitFile(t, filepath.Join(repo, "new.txt"), "new\n") },
		"staged": func(repo string) {
			writeGitFile(t, filepath.Join(repo, "staged.txt"), "s\n")
			cmd := exec.Command("git", "add", "staged.txt")
			cmd.Dir = repo
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git add: %v\n%s", err, out)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			repo := newGitRepo(t)
			change(repo)
			if got, want := gitDirty(repo), name != "clean"; got != want {
				t.Fatalf("gitDirty() = %v, want %v", got, want)
			}
		})
	}
	if gitDirty(t.TempDir()) {
		t.Fatal("gitDirty() = true outside a git repo")
	}
}

func TestReplyFooterWorkDirMarksUncommittedChanges(t *testing.T) {
	repo := newGitRepo(t)
	if got := replyFooterWorkDir(nil, &stubAgent{}, repo); !strings.HasSuffix(got, " (main)") {
		t.Fatalf("clean tree: footer workdir = %q, want (main)", got)
	}
	writeGitFile(t, filepath.Join(repo, "tracked.txt"), "v2\n")
	if got := replyFooterWorkDir(nil, &stubAgent{}, repo); !strings.HasSuffix(got, " (main*)") {
		t.Fatalf("dirty tree: footer workdir = %q, want (main*)", got)
	}
}
