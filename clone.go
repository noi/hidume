package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// clone にかかる時間の上限。大きなリポジトリはダウンロードに数分かかることがある
const cloneTimeout = 10 * time.Minute

// 自分のリポジトリの一覧に出す数の上限
const ownedRepositoryLimit = 1000

// GitHubRepository は clone する候補にする GitHub のリポジトリ
type GitHubRepository struct {
	// owner/repo
	NameWithOwner string `json:"nameWithOwner"`
	URL           string `json:"url"`
	IsPrivate     bool   `json:"isPrivate"`
}

// ListOwnedRepositories は gh でログインしているアカウントがオーナーのリポジトリを、最近プッシュした順に返す
func (a *App) ListOwnedRepositories() ([]GitHubRepository, error) {
	repos := []GitHubRepository{}
	if err := requireGh(); err != nil {
		return repos, err
	}
	// リポジトリを開いていなくても一覧は出せるよう、ホームで実行する
	home, err := os.UserHomeDir()
	if err != nil {
		return repos, err
	}
	out, err := runNetworkIn(home, networkTimeout, "", "gh", "repo", "list",
		"--limit", fmt.Sprint(ownedRepositoryLimit), "--json", "nameWithOwner,url,isPrivate")
	if err != nil {
		return repos, err
	}
	if err := json.Unmarshal([]byte(out), &repos); err != nil {
		return repos, err
	}
	return repos, nil
}

// SelectCloneDirectory はダイアログで clone 先のフォルダー（リポジトリを置く親フォルダー）を選ばせる。
// やめたときは空を返す
func (a *App) SelectCloneDirectory() (string, error) {
	initial := loadConfig().CloneDirectory
	if info, err := os.Stat(initial); initial == "" || err != nil || !info.IsDir() {
		initial, _ = os.UserHomeDir()
	}
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:                "clone 先のフォルダーを選択（この中にリポジトリのフォルダーを作る）",
		DefaultDirectory:     initial,
		CanCreateDirectories: true,
	})
}

// CloneRepository は remote を parent の下に clone して、そのリポジトリに切り替える
func (a *App) CloneRepository(remote, parent string) (string, error) {
	remote = strings.TrimSpace(remote)
	name, err := cloneName(remote)
	if err != nil {
		return a.dir(), err
	}
	if info, err := os.Stat(parent); !filepath.IsAbs(parent) || err != nil || !info.IsDir() {
		return a.dir(), fmt.Errorf("clone 先のフォルダーが見つかりません: %q", parent)
	}
	target := filepath.Join(parent, name)
	if _, err := os.Lstat(target); err == nil {
		return a.dir(), fmt.Errorf("%s はもうあります。別のフォルダーを選んでください", target)
	}
	// URL が - で始まってもオプションと取り違えられないよう、-- の後に置く
	if _, err := runNetworkIn(parent, cloneTimeout, "", "git", "clone", "--quiet", "--", remote, target); err != nil {
		// 時間切れで止めると git が途中まで作ったフォルダーを片付けないので、ここで消す。clone の前にはなかったフォルダーだけ
		_ = os.RemoveAll(target)
		return a.dir(), err
	}
	_ = updateConfig(func(c *config) { c.CloneDirectory = parent })
	root, err := repoRoot(target)
	if err != nil {
		return a.dir(), err
	}
	return a.switchRepository(root), nil
}

// cloneName は clone する URL（https / ssh / scp 風）から、clone 先のフォルダー名にするリポジトリ名を取り出す
func cloneName(remote string) (string, error) {
	var repoPath string
	if m := scpRemote.FindStringSubmatch(remote); m != nil && !strings.Contains(remote, "://") {
		repoPath = m[2]
	} else if u, err := url.Parse(remote); err == nil && (u.Scheme == "https" || u.Scheme == "http" || u.Scheme == "ssh") && u.Host != "" {
		repoPath = u.Path
	} else {
		return "", fmt.Errorf("clone できる URL ではありません: %q", remote)
	}
	repoPath = strings.TrimSuffix(strings.TrimRight(repoPath, "/"), ".git")
	name := repoPath[strings.LastIndex(repoPath, "/")+1:]
	if !repoName.MatchString(name) || strings.HasPrefix(name, "-") || strings.Trim(name, ".") == "" {
		return "", fmt.Errorf("URL からリポジトリの名前がわかりません: %q", remote)
	}
	return name, nil
}
