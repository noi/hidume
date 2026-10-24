package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestRecordHistory(t *testing.T) {
	c := config{History: []string{"/a", "/b", "/c"}}
	recordHistory(&c, "/b")
	if !slices.Equal(c.History, []string{"/b", "/a", "/c"}) {
		t.Errorf("前にも開いたディレクトリは先頭に移す: %v", c.History)
	}
	for i := range historyLimit + 5 {
		recordHistory(&c, fmt.Sprintf("/d%d", i))
	}
	if len(c.History) != historyLimit || c.History[0] != fmt.Sprintf("/d%d", historyLimit+4) {
		t.Errorf("新しい順に %d 件まで: %v", historyLimit, c.History)
	}
}

func TestHistory(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	repo := filepath.Join(home, "sales")
	worktree := filepath.Join(home, ".hidume", "worktrees", "sales-x", "noi", "fix")
	if _, err := runGitIn(home, "init", "-q", "-b", "main", repo); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init"},
		{"worktree", "add", "-q", "-b", "noi/fix", worktree},
	} {
		if _, err := runGitIn(repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	gone := filepath.Join(home, "gone")
	if err := os.Mkdir(gone, 0o755); err != nil {
		t.Fatal(err)
	}

	a := &App{}
	a.switchRepository(gone)
	a.switchRepository(repo)
	a.switchRepository(worktree)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	entries, err := a.GetHistory()
	if err != nil {
		t.Fatal(err)
	}
	want := []HistoryEntry{
		{Path: worktree, Repo: "sales", Branch: "noi/fix", Current: true},
		{Path: repo, Repo: "sales", Branch: "main"},
	}
	if !slices.Equal(entries, want) {
		t.Errorf("GetHistory() = %+v, want %+v", entries, want)
	}
	if slices.Contains(loadConfig().History, gone) {
		t.Error("もうないディレクトリは履歴から消す")
	}

	if dir, err := a.SwitchHistory(repo); err != nil || dir != repo || a.dir() != repo {
		t.Errorf("SwitchHistory(repo) = %q, %v", dir, err)
	}
	if h := loadConfig().History; len(h) == 0 || h[0] != repo {
		t.Errorf("切り替えたディレクトリが先頭になる: %v", h)
	}
	if _, err := a.SwitchHistory(home); err == nil {
		t.Error("履歴にないディレクトリには切り替えない")
	}
}
