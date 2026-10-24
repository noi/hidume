package main

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const networkTimeout = 2 * time.Minute

// PushStatus は push ボタンの状態を決めるための情報
type PushStatus struct {
	// 今のブランチ。HEAD が切り離されているときは空
	Branch    string `json:"branch"`
	HasOrigin bool   `json:"hasOrigin"`
	// 追跡しているリモートブランチ（例: origin/main）。まだないときは空
	Upstream string `json:"upstream"`
	// push されていないコミットの数。Upstream がないときはブランチの全コミット数
	Ahead int `json:"ahead"`
	// Upstream にあって、まだ取り込んでいないコミットの数（最後に fetch した時点）
	Behind int `json:"behind"`
	// デフォルトブランチ（例: main）。わからないときは空
	DefaultBranch string `json:"defaultBranch"`
	// origin のリポジトリのページ（例: https://github.com/owner/repo）。origin がないか、URL の形がわからなければ空
	WebURL string `json:"webUrl"`
	// hidume が作ったワークツリーを開いているか。PR をマージし終えたら片付けられる
	Managed bool `json:"managed"`
}

// GetPushStatus は今のブランチを push できるかどうかの情報を返す
func (a *App) GetPushStatus() PushStatus {
	var s PushStatus
	if out, err := a.runGit("symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		s.Branch = strings.TrimSpace(out)
	}
	origin, err := a.runGit("remote", "get-url", "origin")
	s.HasOrigin = err == nil
	if s.HasOrigin {
		s.WebURL = webURL(strings.TrimSpace(origin))
	}
	if out, err := a.runGit("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err == nil {
		s.Upstream = strings.TrimSpace(out)
	}
	s.DefaultBranch = a.defaultBranch()
	s.Managed = a.managedWorktree()
	if !a.hasHead() {
		return s
	}
	if s.Upstream == "" {
		s.Ahead = a.countCommits("HEAD")
		return s
	}
	s.Ahead = a.countCommits("@{upstream}..HEAD")
	s.Behind = a.countCommits("HEAD..@{upstream}")
	return s
}

// scpRemote は git@github.com:owner/repo.git のような、scp 風の書き方のリモート URL
var scpRemote = regexp.MustCompile(`^(?:[A-Za-z0-9._-]+@)?([A-Za-z0-9.-]+):([^/].*)$`)

// webURL はリモートの URL（https / ssh / scp 風）から、ブラウザで開けるリポジトリのページの URL を作る。
// 作れなければ空を返す
func webURL(remote string) string {
	var host, repoPath string
	if m := scpRemote.FindStringSubmatch(remote); m != nil && !strings.Contains(remote, "://") {
		host, repoPath = m[1], m[2]
	} else if u, err := url.Parse(remote); err == nil && (u.Scheme == "https" || u.Scheme == "http" || u.Scheme == "ssh") {
		// ポートは ssh 用のことがあるので外す。https の URL に入っているユーザー名やトークンも外す
		host, repoPath = u.Hostname(), u.Path
	} else {
		return ""
	}
	repoPath = strings.TrimSuffix(strings.Trim(repoPath, "/"), ".git")
	// owner/repo の形になっていなければ、どこを開けばよいかわからない
	if host == "" || strings.Count(repoPath, "/") < 1 || strings.ContainsAny(repoPath, "?#\\ ") {
		return ""
	}
	return (&url.URL{Scheme: "https", Host: host, Path: "/" + repoPath}).String()
}

func (a *App) countCommits(rng string) int {
	out, err := a.runGit("rev-list", "--count", rng)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

// Push は今のブランチを push する。追跡ブランチがなければ origin に同名で作って追跡する。
// 履歴を書き換える force push はしない
func (a *App) Push() error {
	s := a.GetPushStatus()
	if s.Branch == "" {
		return fmt.Errorf("ブランチにいないのでプッシュできません")
	}
	if s.Upstream != "" {
		_, err := a.runNetwork("git", "push")
		return err
	}
	if !s.HasOrigin {
		return fmt.Errorf("origin が設定されていません")
	}
	// 作ったばかりの空のリモートには、PR のマージ先になるデフォルトブランチを先に送る。
	// 最初に送ったブランチが GitHub のデフォルトブランチになる
	if err := a.pushDefaultBranchIfMissing(s); err != nil {
		return err
	}
	if _, err := a.runNetwork("git", "push", "--set-upstream", "origin", "HEAD:refs/heads/"+s.Branch); err != nil {
		return err
	}
	// origin/HEAD を合わせておく。失敗してもデフォルトブランチの推測が少し弱くなるだけ
	_, _ = a.runNetwork("git", "remote", "set-head", "origin", "--auto")
	return nil
}

func (a *App) pushDefaultBranchIfMissing(s PushStatus) error {
	if s.DefaultBranch == "" || s.Branch == s.DefaultBranch || !a.refExists("refs/heads/"+s.DefaultBranch) {
		return nil
	}
	out, err := a.runNetwork("git", "ls-remote", "--heads", "origin", "refs/heads/"+s.DefaultBranch)
	if err != nil || strings.TrimSpace(out) != "" {
		return err
	}
	ref := "refs/heads/" + s.DefaultBranch
	_, err = a.runNetwork("git", "push", "origin", ref+":"+ref)
	return err
}

// SetOrigin は origin を url で追加する
func (a *App) SetOrigin(url string) error {
	url = strings.TrimSpace(url)
	// 引数がオプションとして解釈されたり、余計な文字が設定に入ったりしないようにする
	if url == "" || strings.HasPrefix(url, "-") || strings.IndexFunc(url, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return fmt.Errorf("URL が正しくありません: %q", url)
	}
	_, err := a.runGit("remote", "add", "origin", url)
	return err
}

var (
	ownerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)
	repoName  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// GitHubOwners はリポジトリを作れる先の候補
type GitHubOwners struct {
	// gh でログインしている個人アカウント
	User string `json:"user"`
	// 所属している Organization
	Orgs []string `json:"orgs"`
}

// GetGitHubOwners は gh でログインしているアカウントと、所属 Organization を返す
func (a *App) GetGitHubOwners() (GitHubOwners, error) {
	owners := GitHubOwners{Orgs: []string{}}
	if err := requireGh(); err != nil {
		return owners, err
	}
	user, err := a.runNetwork("gh", "api", "user", "--jq", ".login")
	if err != nil {
		return owners, err
	}
	owners.User = strings.TrimSpace(user)
	orgs, err := a.runNetwork("gh", "api", "user/orgs", "--paginate", "--jq", ".[].login")
	if err != nil {
		return owners, err
	}
	owners.Orgs = strings.Fields(orgs)
	return owners, nil
}

// CreateGitHubRepository は gh で owner の下に GitHub リポジトリを作り、origin に設定する。
// visibility は "private" / "public" / "internal"（internal は Enterprise 配下の Organization のみ）
func (a *App) CreateGitHubRepository(owner, name, visibility string) error {
	name = strings.TrimSpace(name)
	if !ownerName.MatchString(owner) {
		return fmt.Errorf("作成先が正しくありません: %q", owner)
	}
	if !repoName.MatchString(name) || strings.HasPrefix(name, "-") {
		return fmt.Errorf("リポジトリ名が正しくありません: %q", name)
	}
	if visibility != "private" && visibility != "public" && visibility != "internal" {
		return fmt.Errorf("公開範囲が正しくありません: %q", visibility)
	}
	if err := requireGh(); err != nil {
		return err
	}
	source, err := a.mainWorktree()
	if err != nil {
		return err
	}
	_, err = a.runNetwork("gh", "repo", "create", owner+"/"+name, "--"+visibility, "--source", source, "--remote", "origin")
	return err
}

// mainWorktree はリポジトリ本体（最初に clone / init した場所）のパスを返す。
// gh repo create --source は .git ディレクトリのある場所しかリポジトリと認めず、ワークツリーを渡すと断る。
// origin はワークツリーの間で共有されるので、本体に足せば今のワークツリーでも使える
func (a *App) mainWorktree() (string, error) {
	common, err := a.runGit("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	common = filepath.Clean(strings.TrimSpace(common))
	if filepath.Base(common) != ".git" {
		return "", fmt.Errorf("リポジトリ本体の場所がわかりません（%s）", common)
	}
	return filepath.Dir(common), nil
}

func requireGh() error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("GitHub CLI（gh）が見つかりません。インストールして gh auth login してください")
	}
	return nil
}

// runNetwork はネットワークを使うコマンドをリポジトリで実行する。
// GUI から呼ぶので、認証の入力待ちで止まらないようにし、時間も区切る
func (a *App) runNetwork(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), networkTimeout)
	defer cancel()

	dir := a.dir()
	if dir == "" {
		return "", errNoRepository
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GH_PROMPT_DISABLED=1", "GIT_OPTIONAL_LOCKS=0")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("%s が %s 以内に終わりませんでした", name, networkTimeout)
		}
		return "", fmt.Errorf("%s %s: %s", name, args[0], strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
