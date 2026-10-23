package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AddToGitignore はリポジトリ直下の .gitignore に paths をそのパスだけに当たる形で足す。すでにある行は足さない
func (a *App) AddToGitignore(paths []string) error {
	dir := a.dir()
	if dir == "" {
		return errNoRepository
	}
	path := filepath.Join(dir, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		have[strings.TrimSpace(line)] = true
	}
	var add []string
	for _, p := range paths {
		if p == "" || strings.ContainsAny(p, "\r\n") {
			return fmt.Errorf("パスが正しくありません: %q", p)
		}
		line := ignorePattern(p)
		if !have[line] {
			have[line] = true
			add = append(add, line)
		}
	}
	if len(add) == 0 {
		return nil
	}
	text := strings.Join(add, "\n") + "\n"
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		text = "\n" + text
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ignorePattern は path だけに当たる .gitignore の行を作る。
// 頭に / を付けてほかのディレクトリの同名ファイルに当たらないようにし、パターンとして読まれる文字はエスケープする
func ignorePattern(path string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`).Replace(path)
	// 末尾の空白は無視されるので、エスケープして残す
	if trimmed := strings.TrimRight(escaped, " "); trimmed != escaped {
		escaped = trimmed + strings.Repeat(`\ `, len(escaped)-len(trimmed))
	}
	return "/" + escaped
}
