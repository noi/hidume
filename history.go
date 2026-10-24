package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// 作業履歴に覚えておくディレクトリの数
const historyLimit = 20

// HistoryEntry は作業履歴の 1 件（開いたことのあるディレクトリ）
type HistoryEntry struct {
	Path string `json:"path"`
	// リポジトリの名前（ワークツリーなら本体のフォルダー名）
	Repo string `json:"repo"`
	// そのディレクトリの今のブランチ。HEAD が切り離されているときは空
	Branch string `json:"branch"`
	// 今アプリで開いているか
	Current bool `json:"current"`
}

// recordHistory は dir を作業履歴の先頭に置く。前にも開いていれば、前の位置からは外す
func recordHistory(c *config, dir string) {
	if dir == "" {
		return
	}
	c.History = slices.DeleteFunc(c.History, func(p string) bool { return p == dir })
	c.History = append([]string{dir}, c.History...)
	if len(c.History) > historyLimit {
		c.History = c.History[:historyLimit]
	}
}

// GetHistory は作業履歴を、最近使った順に返す。
// 片付けたワークツリーなど、もうないディレクトリや git リポジトリでなくなったディレクトリは出さず、履歴からも消す
func (a *App) GetHistory() ([]HistoryEntry, error) {
	current := a.dir()
	entries := []HistoryEntry{}
	var gone []string
	for _, dir := range loadConfig().History {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			gone = append(gone, dir)
			continue
		}
		e, err := historyEntry(dir)
		if err != nil {
			gone = append(gone, dir)
			continue
		}
		e.Current = dir == current
		entries = append(entries, e)
	}
	if len(gone) > 0 {
		if err := updateConfig(func(c *config) {
			c.History = slices.DeleteFunc(c.History, func(p string) bool { return slices.Contains(gone, p) })
		}); err != nil {
			return entries, err
		}
	}
	return entries, nil
}

// historyEntry は dir のリポジトリ名と今のブランチを調べる
func historyEntry(dir string) (HistoryEntry, error) {
	e := HistoryEntry{Path: dir, Repo: filepath.Base(dir)}
	common, err := runGitIn(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return e, err
	}
	if common = filepath.Clean(strings.TrimSpace(common)); filepath.Base(common) == ".git" {
		e.Repo = filepath.Base(filepath.Dir(common))
	}
	if out, err := runGitIn(dir, "symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		e.Branch = strings.TrimSpace(out)
	}
	return e, nil
}

// SwitchHistory は作業履歴にあるディレクトリに切り替える。履歴にない任意のディレクトリは開かない
func (a *App) SwitchHistory(path string) (string, error) {
	if !slices.Contains(loadConfig().History, path) {
		return a.dir(), fmt.Errorf("作業履歴にないディレクトリです: %s", path)
	}
	root, err := repoRoot(path)
	if err != nil {
		return a.dir(), fmt.Errorf("%s はもう開けません: %w", path, err)
	}
	return a.switchRepository(root), nil
}
