package core

import (
	"os"
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
