package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebURL(t *testing.T) {
	for remote, want := range map[string]string{
		"https://github.com/noi/hidume.git":               "https://github.com/noi/hidume",
		"https://github.com/noi/hidume":                   "https://github.com/noi/hidume",
		"https://x-access-token:secret@github.com/noi/a/": "https://github.com/noi/a",
		"git@github.com:noi/hidume.git":                   "https://github.com/noi/hidume",
		"ssh://git@github.example.com:2222/org/repo.git":  "https://github.example.com/org/repo",
		"git@gitlab.com:group/sub/project.git":            "https://gitlab.com/group/sub/project",
		"/Users/noi/repos/hidume.git":                     "",
		"file:///Users/noi/repos/hidume.git":              "",
		"https://github.com/only-owner":                   "",
		"":                                                "",
	} {
		if got := webURL(remote); got != want {
			t.Errorf("webURL(%q) = %q, want %q", remote, got, want)
		}
	}
}

// マージし終えたワークツリーは、ブランチごと片付けて本体に戻る。消すと困るものがあれば断る
func TestRemoveWorktree(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	home, err := filepath.EvalSymlinks(os.Getenv("HOME"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	commit := []string{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m"}
	remote := filepath.Join(home, "remote.git")
	repo := filepath.Join(home, "repo")
	for _, step := range [][]string{
		{"init", "-q", "--bare", "-b", "main", remote},
		{"init", "-q", "-b", "main", repo},
	} {
		if _, err := runGitIn(home, step...); err != nil {
			t.Fatal(err)
		}
	}
	for _, step := range [][]string{
		append(commit, "init"),
		{"remote", "add", "origin", remote},
		{"push", "-q", "-u", "origin", "main"},
	} {
		if _, err := runGitIn(repo, step...); err != nil {
			t.Fatal(err)
		}
	}
	worktree := filepath.Join(home, ".hidume", "worktrees", "repo-x", "noi", "done")
	if _, err := runGitIn(repo, "worktree", "add", "-q", "-b", "noi/done", worktree); err != nil {
		t.Fatal(err)
	}
	a := &App{}
	a.setDir(worktree)
	if !a.GetPushStatus().Managed {
		t.Fatal("hidume のワークツリーとして扱われていない")
	}

	// まだプッシュしていないコミットがあれば断る
	if _, err := runGitIn(worktree, append(commit, "work")...); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RemoveWorktree(); err == nil || !strings.Contains(err.Error(), "プッシュしていない") {
		t.Fatalf("unpushed: %v", err)
	}
	if _, err := runGitIn(worktree, "push", "-q", "-u", "origin", "noi/done"); err != nil {
		t.Fatal(err)
	}
	// コミットしていない変更があれば断る
	writeFiles(t, worktree, map[string]string{"note.txt": "wip"})
	if _, err := a.RemoveWorktree(); err == nil || !strings.Contains(err.Error(), "コミットしていない") {
		t.Fatalf("dirty: %v", err)
	}
	if err := os.Remove(filepath.Join(worktree, "note.txt")); err != nil {
		t.Fatal(err)
	}

	// origin の main が進んでいたら、本体の main を早送りしておく
	other := filepath.Join(home, "other")
	if _, err := runGitIn(home, "clone", "-q", remote, other); err != nil {
		t.Fatal(err)
	}
	for _, step := range [][]string{append(commit, "merged"), {"push", "-q", "origin", "main"}} {
		if _, err := runGitIn(other, step...); err != nil {
			t.Fatal(err)
		}
	}

	got, err := a.RemoveWorktree()
	if err != nil || got != repo || a.dir() != repo {
		t.Fatalf("RemoveWorktree() = %q, %v (dir %q)", got, err, a.dir())
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Errorf("worktree still exists: %v", err)
	}
	if a.refExists("refs/heads/noi/done") {
		t.Error("branch still exists")
	}
	if out, _ := a.runGit("log", "-1", "--format=%s", "main"); strings.TrimSpace(out) != "merged" {
		t.Errorf("main was not fast-forwarded: %q", out)
	}

	// 本体は片付けない
	if _, err := a.RemoveWorktree(); err == nil {
		t.Error("main worktree must not be removed")
	}
}

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
