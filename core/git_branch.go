package core

import (
	"os"
	"path/filepath"
	"strings"
)

// gitBranch returns the branch checked out in the git work tree that contains
// dir, the short commit for a detached HEAD, or "" when dir is not in a work
// tree. It reads HEAD directly instead of running git: the reply footer asks
// on every reply.
func gitBranch(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	dir = filepath.Clean(dir)
	for {
		dotGit := filepath.Join(dir, ".git")
		if info, err := os.Stat(dotGit); err == nil {
			gitDir := dotGit
			if !info.IsDir() {
				// Worktrees and submodules have a ".git" file pointing at
				// their git dir.
				gitDir = gitDirFromFile(dotGit, dir)
				if gitDir == "" {
					return ""
				}
			}
			return branchFromHead(filepath.Join(gitDir, "HEAD"))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// gitDirFromFile reads a ".git" file ("gitdir: <path>").
func gitDirFromFile(path, workTree string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return ""
	}
	gitDir = strings.TrimSpace(gitDir)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(workTree, gitDir)
	}
	return gitDir
}

func branchFromHead(headPath string) string {
	data, err := os.ReadFile(headPath)
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(data))
	if ref, ok := strings.CutPrefix(head, "ref:"); ok {
		return strings.TrimPrefix(strings.TrimSpace(ref), "refs/heads/")
	}
	if len(head) >= 7 {
		return head[:7]
	}
	return ""
}
