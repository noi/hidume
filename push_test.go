package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// ワークツリーで開いていても、gh repo create --source にはリポジトリ本体を渡す
func TestMainWorktree(t *testing.T) {
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if _, err := runGitIn(repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	worktree := filepath.Join(t.TempDir(), "noi", "try")
	if _, err := runGitIn(repo, "worktree", "add", "-q", "-b", "noi/try", worktree); err != nil {
		t.Fatal(err)
	}

	a := &App{}
	for _, dir := range []string{repo, worktree} {
		a.setDir(dir)
		got, err := a.mainWorktree()
		if err != nil || got != repo {
			t.Errorf("mainWorktree() in %s = %q, %v; want %q", dir, got, err, repo)
		}
	}

	// ワークツリーでは --git-dir が .git にならないことが、gh が断る理由
	a.setDir(worktree)
	if out, _ := a.runGit("rev-parse", "--git-dir"); strings.TrimSpace(out) == ".git" {
		t.Errorf("worktree git dir is .git: the workaround may no longer be needed")
	}
}
