package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 最初のコミットには、おすすめの設定で置いたファイルだけを入れ、ほかのファイルは残す
func TestCreateInitialCommit(t *testing.T) {
	repo := t.TempDir()
	if _, err := runGitIn(repo, "init", "-q", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		".gitignore":        "ignored/\n",
		claudeSettingsPath:  "{}\n",
		"ignored/x":         "x\n",
		"main.gs":           "function f() {}\n",
		".github/other.yml": "other\n",
	}
	for path, content := range files {
		path = filepath.Join(repo, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{}
	a.setDir(repo)
	// 人がコミット対象にしたものも、最初のコミットには入れない
	if _, err := a.runGit("add", "main.gs"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	if err := a.CreateInitialCommit(); err != nil {
		t.Fatal(err)
	}

	out, err := a.runGit("ls-tree", "-r", "--name-only", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Fields(out), []string{".claude/settings.json", ".gitignore"}; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("committed %v; want %v", got, want)
	}
	// 入れたファイルは変更として出ず、コミット対象にしていたものはそのまま
	out, err = a.runGit("status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Split(strings.TrimSpace(out), "\n"), []string{"A  main.gs", "?? .github/"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("status %q; want %q", got, want)
	}
}

// おすすめの設定のファイルがなければ、中身が空のコミットを作る
func TestCreateInitialCommitEmpty(t *testing.T) {
	repo := t.TempDir()
	if _, err := runGitIn(repo, "init", "-q", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	a := &App{}
	a.setDir(repo)
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	if err := a.CreateInitialCommit(); err != nil {
		t.Fatal(err)
	}
	if out, _ := a.runGit("ls-tree", "-r", "HEAD"); out != "" {
		t.Errorf("tree is not empty: %q", out)
	}
}
