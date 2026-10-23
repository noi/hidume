package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// defaultBranch はデフォルトブランチの名前を返す。
// origin の HEAD がわかればそれ、なければ init.defaultBranch の設定、なければ main / master のうちあるほう
func (a *App) defaultBranch() string {
	if out, err := a.runGit("symbolic-ref", "--short", "-q", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimPrefix(strings.TrimSpace(out), "origin/")
	}
	if out, err := a.runGit("config", "--get", "init.defaultBranch"); err == nil && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out)
	}
	current, _ := a.runGit("symbolic-ref", "--short", "-q", "HEAD")
	for _, name := range []string{"main", "master"} {
		if strings.TrimSpace(current) == name || a.refExists("refs/heads/"+name) {
			return name
		}
	}
	return ""
}

func (a *App) refExists(ref string) bool {
	_, err := a.runGit("rev-parse", "--verify", "-q", ref)
	return err == nil
}

// CreateInitialCommit は、まだコミットのないリポジトリに中身が空の最初のコミットを作る。
// コミット対象にしたファイルはそのまま残し、このあと作るブランチでコミットする
func (a *App) CreateInitialCommit() error {
	if a.hasHead() {
		return fmt.Errorf("もうコミットがあります")
	}
	branch, err := a.runGit("symbolic-ref", "--short", "HEAD")
	if err != nil {
		return err
	}
	// 標準入力なしの mktree は空のツリーを作る
	tree, err := a.runGit("mktree")
	if err != nil {
		return err
	}
	commit, err := a.runGit("commit-tree", strings.TrimSpace(tree), "-m", "Initial commit")
	if err != nil {
		return err
	}
	_, err = a.runGit("update-ref", "refs/heads/"+strings.TrimSpace(branch), strings.TrimSpace(commit))
	return err
}

var (
	branchSuffix = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)
	notIDChar    = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
)

// GetBranchPrefix は作業用ブランチの頭に付ける {user_id}。
// gh でログインしている GitHub のユーザー名、なければ git のメールアドレスの @ より前を使う
func (a *App) GetBranchPrefix() (string, error) {
	if a.branchPrefix != "" {
		return a.branchPrefix, nil
	}
	var id string
	if _, err := exec.LookPath("gh"); err == nil {
		if out, err := a.runNetwork("gh", "api", "user", "--jq", ".login"); err == nil {
			id = strings.TrimSpace(out)
		}
	}
	if id == "" {
		if out, err := a.runGit("config", "--get", "user.email"); err == nil {
			id, _, _ = strings.Cut(strings.TrimSpace(out), "@")
		}
	}
	id = strings.Trim(notIDChar.ReplaceAllString(id, "-"), "-.")
	if id == "" {
		return "", fmt.Errorf("ブランチ名に使うユーザー名がわかりません。gh auth login するか、git の user.email を設定してください")
	}
	a.branchPrefix = id
	return id, nil
}

// CreateBranch は {user_id}/{name} のブランチを、hidume が管理するディレクトリにワークツリーとして作り、
// アプリをそのワークツリーに切り替える。作ったワークツリーのパスを返す。
// デフォルトブランチにいるときは今の HEAD から作り、carry が true なら作業中の変更も新しいワークツリーへ移す。
// 作業用ブランチにいるときはデフォルトブランチから作り、変更は移さない（別の作業が混ざるため）
func (a *App) CreateBranch(name string, carry bool) (string, error) {
	name = strings.Trim(strings.TrimSpace(name), "/")
	if !branchSuffix.MatchString(name) {
		return "", fmt.Errorf("ブランチ名に使えるのは英数字と . _ - / だけです: %q", name)
	}
	if !a.hasHead() {
		return "", fmt.Errorf("先に最初のコミットを作ってください")
	}
	prefix, err := a.GetBranchPrefix()
	if err != nil {
		return "", err
	}
	full := prefix + "/" + name
	if _, err := a.runGit("check-ref-format", "--branch", full); err != nil {
		return "", fmt.Errorf("ブランチ名が正しくありません: %q", full)
	}
	if a.refExists("refs/heads/"+full) || a.refExists("refs/remotes/origin/"+full) {
		return "", fmt.Errorf("%s はもうあります。別の名前にしてください", full)
	}
	dir, err := a.worktreeDir(full)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("%s はもう使われています", dir)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}

	base := "HEAD"
	onDefault := a.onDefaultBranch()
	if def := a.defaultBranch(); !onDefault && def != "" && a.refExists("refs/heads/"+def) {
		base = "refs/heads/" + def
	}

	stash := ""
	if carry && onDefault {
		if stash, err = a.stashChanges("hidume: " + full + " へ移す変更"); err != nil {
			return "", err
		}
	}
	if _, err := a.runGit("worktree", "add", "-b", full, dir, base); err != nil {
		if stash != "" {
			// 退避した変更を元の場所に戻す。戻せなくても stash に残っている
			_ = a.restoreStash(a.dir(), stash)
		}
		return "", err
	}
	if stash != "" {
		if err := a.restoreStash(dir, stash); err != nil {
			return "", fmt.Errorf("ワークツリーは作れましたが、変更を戻せませんでした。stash %s に残っています: %w", stash[:7], err)
		}
	}
	a.switchRepository(dir)
	return dir, nil
}

// worktreeDir は hidume がワークツリーを置く場所。
// ~/.hidume/worktrees/<リポジトリ名>-<識別子>/<ブランチ名>（ブランチ名の / はそのまま階層になる）
func (a *App) worktreeDir(branch string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	// ワークツリーから作っても同じ場所にまとまるよう、本体の .git の場所でリポジトリを見分ける
	common, err := a.runGit("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	common = filepath.Clean(strings.TrimSpace(common))
	sum := sha256.Sum256([]byte(common))
	repo := filepath.Base(filepath.Dir(common)) + "-" + hex.EncodeToString(sum[:])[:8]
	return filepath.Join(home, ".hidume", "worktrees", repo, filepath.FromSlash(branch)), nil
}

// stashChanges は未追跡を含む作業中の変更を stash に退避し、その stash のコミット ID を返す。
// 変更がなければ空文字を返す
func (a *App) stashChanges(message string) (string, error) {
	out, err := a.runGit("status", "--porcelain")
	if err != nil || strings.TrimSpace(out) == "" {
		return "", err
	}
	if _, err := a.runGit("stash", "push", "--include-untracked", "-m", message); err != nil {
		return "", err
	}
	return a.revParse("refs/stash")
}

// restoreStash は stash を dir の作業ツリーとコミット対象に戻し、戻せたら stash から消す
func (a *App) restoreStash(dir, stash string) error {
	if _, err := runGitIn(dir, "stash", "apply", "--index", stash); err != nil {
		return err
	}
	// stash の一覧は全ワークツリーで共有なので、番号ではなくコミット ID で自分の分を探して消す
	list, err := a.runGit("stash", "list", "--format=%H")
	if err != nil {
		return nil
	}
	for i, hash := range strings.Fields(list) {
		if hash == stash {
			_, _ = a.runGit("stash", "drop", "-q", fmt.Sprintf("stash@{%d}", i))
			break
		}
	}
	return nil
}

// onDefaultBranch は今デフォルトブランチにいるか
func (a *App) onDefaultBranch() bool {
	current, err := a.runGit("symbolic-ref", "--short", "-q", "HEAD")
	return err == nil && strings.TrimSpace(current) == a.defaultBranch()
}

// Worktree はリポジトリのワークツリー 1 つ分
type Worktree struct {
	Path string `json:"path"`
	// ブランチ名。HEAD が切り離されているときは空
	Branch string `json:"branch"`
	// リポジトリ本体（最初に clone / init した場所）か
	IsMain bool `json:"isMain"`
	// 今アプリで開いているか
	Current bool `json:"current"`
	// hidume が管理するディレクトリにあるか
	Managed bool `json:"managed"`
}

// ListWorktrees は今のリポジトリのワークツリーを、本体を先頭にして返す
func (a *App) ListWorktrees() ([]Worktree, error) {
	out, err := a.runGit("worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	managedRoot := filepath.Join(home, ".hidume", "worktrees") + string(filepath.Separator)
	current := filepath.Clean(a.dir())

	worktrees := []Worktree{}
	for i, block := range strings.Split(strings.TrimSpace(out), "\n\n") {
		var w Worktree
		prunable := false
		for _, line := range strings.Split(block, "\n") {
			key, value, _ := strings.Cut(line, " ")
			switch key {
			case "worktree":
				w.Path = filepath.Clean(value)
			case "branch":
				w.Branch = strings.TrimPrefix(value, "refs/heads/")
			case "bare", "prunable":
				prunable = true
			}
		}
		// 消されたワークツリーや bare リポジトリは開けないので出さない
		if w.Path == "" || prunable {
			continue
		}
		w.IsMain = i == 0
		w.Current = w.Path == current
		w.Managed = strings.HasPrefix(w.Path, managedRoot)
		worktrees = append(worktrees, w)
	}
	return worktrees, nil
}

// SwitchWorktree はアプリで開くワークツリーを切り替える。
// 一覧にあるワークツリーにだけ切り替え、任意のディレクトリは開かない
func (a *App) SwitchWorktree(path string) (string, error) {
	worktrees, err := a.ListWorktrees()
	if err != nil {
		return a.dir(), err
	}
	for _, w := range worktrees {
		if w.Path == filepath.Clean(path) {
			return a.switchRepository(w.Path), nil
		}
	}
	return a.dir(), fmt.Errorf("このリポジトリのワークツリーではありません: %s", path)
}
