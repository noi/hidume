package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// MergeStatus はベースブランチとの関係と、マージ中の状態
type MergeStatus struct {
	// 取り込む先のベースブランチ（デフォルトブランチ）。origin にないときは空
	Base string `json:"base"`
	// origin/<Base> にあって、まだ取り込んでいないコミットの数（最後に fetch した時点）
	Behind int `json:"behind"`
	// 今 origin/<Base> を取り込むとコンフリクトするファイル
	WouldConflict []string `json:"wouldConflict"`
	// マージの途中か（MERGE_HEAD がある）
	Merging bool `json:"merging"`
	// マージの途中でコンフリクトしているファイル
	Conflicts []string `json:"conflicts"`
}

// GetMergeStatus は、ベースブランチの取り込み具合とコンフリクトの有無を返す。
// 取り込んだ場合のコンフリクトは、作業ツリーに触れない git merge-tree で調べる
func (a *App) GetMergeStatus() MergeStatus {
	s := MergeStatus{WouldConflict: []string{}, Conflicts: []string{}}
	s.Merging = a.refExists("MERGE_HEAD")
	if out, err := a.runGit("diff", "--name-only", "--diff-filter=U"); err == nil {
		s.Conflicts = append(s.Conflicts, strings.Fields(out)...)
	}
	base := a.defaultBranch()
	ref := "refs/remotes/origin/" + base
	if base == "" || !a.hasHead() || !a.refExists(ref) {
		return s
	}
	s.Base = base
	s.Behind = a.countCommits("HEAD.." + ref)
	if s.Behind == 0 || s.Merging {
		return s
	}
	files, err := a.mergeTreeConflicts(ref)
	if err == nil {
		s.WouldConflict = files
	}
	return s
}

// mergeTreeConflicts は HEAD に ref をマージしたときにコンフリクトするファイルを返す
func (a *App) mergeTreeConflicts(ref string) ([]string, error) {
	cmd := exec.Command("git", "merge-tree", "--write-tree", "--name-only", "--no-messages", "HEAD", ref)
	cmd.Dir = a.dir()
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return []string{}, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		// 1 行目はマージ結果のツリー、2 行目以降がコンフリクトしたファイル
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		return lines[1:], nil
	default:
		return nil, err
	}
}

// FetchBase はベースブランチの最新を origin から取ってくる
func (a *App) FetchBase() error {
	base := a.defaultBranch()
	if base == "" {
		return nil
	}
	if _, err := a.runGit("remote", "get-url", "origin"); err != nil {
		return nil
	}
	ref := "refs/heads/" + base
	_, err := a.runNetwork("git", "fetch", "--quiet", "origin", ref+":refs/remotes/origin/"+base)
	return err
}

// MergeBase はベースブランチの最新を fetch し、マージコミットで今のブランチに取り込む。
// コンフリクトしたときはマージの途中で止め、true を返す
func (a *App) MergeBase() (bool, error) {
	if a.onDefaultBranch() {
		return false, fmt.Errorf("デフォルトブランチでは取り込みません")
	}
	if a.refExists("MERGE_HEAD") {
		return false, fmt.Errorf("マージの途中です。先に完了するかやめてください")
	}
	// コミットしていない変更とマージの結果が混ざらないよう、追跡中のファイルに変更がないときだけ取り込む
	if out, err := a.runGit("status", "--porcelain", "--untracked-files=no"); err != nil || strings.TrimSpace(out) != "" {
		if err != nil {
			return false, err
		}
		return false, fmt.Errorf("コミットしていない変更があります。先にコミットしてから取り込んでください")
	}
	if err := a.FetchBase(); err != nil {
		return false, err
	}
	base := a.defaultBranch()
	ref := "refs/remotes/origin/" + base
	if base == "" || !a.refExists(ref) {
		return false, fmt.Errorf("origin にベースブランチが見つかりません")
	}
	if _, err := a.runGit("merge", "--no-ff", "--no-edit", ref); err != nil {
		if a.refExists("MERGE_HEAD") {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

// AbortMerge はマージの途中をやめて、取り込む前に戻す
func (a *App) AbortMerge() error {
	_, err := a.runGit("merge", "--abort")
	return err
}

// ContinueMerge はコンフリクトをすべて解消したマージを、マージコミットにして完了する
func (a *App) ContinueMerge() error {
	s := a.GetMergeStatus()
	if !s.Merging {
		return fmt.Errorf("マージの途中ではありません")
	}
	if len(s.Conflicts) > 0 {
		return fmt.Errorf("まだコンフリクトが %d 件残っています", len(s.Conflicts))
	}
	_, err := a.runGit("commit", "--no-edit")
	return err
}
