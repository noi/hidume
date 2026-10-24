package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"
)

const (
	// brew install にかかる時間の上限。ダウンロードとビルドで数分かかることがある
	installTimeout = 20 * time.Minute
	// ログインしているかを確かめる時間の上限。gh はネットワーク越しにトークンを確かめる
	authCheckTimeout = 15 * time.Second
)

// Homebrew の公式のインストール手順。sudo のパスワードを聞かれるのでターミナルで流す
const brewInstallCommand = `/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"`

// SetupTool は初回のセットアップでやること 1 つ分（道具を入れる、またはログインする）
type SetupTool struct {
	// brew / git / gh / gh-auth（GitHub へのログイン）/ claude / claude-auth（Claude へのログイン）
	Name string `json:"name"`
	// 入っている、またはログイン済みか
	Installed bool `json:"installed"`
}

// SetupStatus は初回のセットアップの状況
type SetupStatus struct {
	// セットアップを終えたか。終えていれば起動時にヒヅメくんの自己紹介から始めない
	Done bool `json:"done"`
	// やる順に並べる。brew 以外は brew で入れ、ログインはその道具を入れてからする
	Tools []SetupTool `json:"tools"`
}

// addToolPaths は Homebrew などの置き場所を PATH に足す。
// Finder から起動した GUI アプリの PATH は /usr/bin:/bin:/usr/sbin:/sbin だけで、
// シェルの設定を読まないので、brew や brew で入れた道具が見つからない
func addToolPaths() {
	home, _ := os.UserHomeDir()
	have := map[string]bool{}
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		have[p] = true
	}
	var add []string
	for _, p := range []string{filepath.Join(home, ".local", "bin"), "/opt/homebrew/bin", "/usr/local/bin"} {
		if !have[p] {
			add = append(add, p)
		}
	}
	if len(add) > 0 {
		// /usr/bin の git より Homebrew の git を先に使うよう、前に足す
		os.Setenv("PATH", strings.Join(append(add, os.Getenv("PATH")), string(os.PathListSeparator)))
	}
}

// GetSetup は初回のセットアップの状況を返す。Homebrew を使うので、macOS 以外ではセットアップしない
func (a *App) GetSetup() SetupStatus {
	// 終えていれば、ログインの確認でネットワークを待たないよう何も調べない
	if goruntime.GOOS != "darwin" || loadConfig().SetupDone {
		return SetupStatus{Done: true, Tools: []SetupTool{}}
	}
	gh, claude := hasCommand("gh"), hasCommand("claude")
	return SetupStatus{
		Tools: []SetupTool{
			{Name: "brew", Installed: hasCommand("brew")},
			{Name: "git", Installed: hasGit()},
			{Name: "gh", Installed: gh},
			{Name: "gh-auth", Installed: gh && ghLoggedIn()},
			{Name: "claude", Installed: claude},
			{Name: "claude-auth", Installed: claude && claudeLoggedIn()},
		},
	}
}

// CompleteSetup はセットアップを終えたことを覚えておく
func (a *App) CompleteSetup() error {
	return updateConfig(func(c *config) { c.SetupDone = true })
}

// InstallTool は道具を入れる、またはログインする。
// パスワードの入力やブラウザでの操作が要るもの（brew とログイン）はターミナルを開いて始めるだけで、終わるのを待たない
func (a *App) InstallTool(name string) error {
	switch name {
	case "brew":
		return openTerminal(brewInstallCommand)
	case "git", "gh":
		return runBrew("install", name)
	case "claude":
		return runBrew("install", "--cask", "claude-code")
	case "gh-auth":
		return openTerminalWith("gh", "auth", "login", "--hostname", "github.com", "--git-protocol", "https", "--web")
	case "claude-auth":
		return openTerminalWith("claude", "auth", "login")
	// ここからは GAS を始めるときの準備
	case "node":
		return runBrew("install", "node")
	case "clasp":
		return installClasp()
	case "clasp-auth":
		return openTerminalWith("clasp", "login")
	case "gas-api":
		// 有効にしたかは確かめられないので、設定ページを開くだけ。済んだら ConfirmGasAPI で覚える
		return a.OpenURL(gasSettingsURL)
	}
	return fmt.Errorf("知らない道具です: %q", name)
}

func hasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// hasGit は git が使えるか。/usr/bin/git は Xcode のコマンドラインツールがないと動かない。
// 実行して確かめるとツールのインストールを勧めるダイアログが出てしまうので、ツールの有無を見る
func hasGit() bool {
	path, err := exec.LookPath("git")
	if err != nil {
		return false
	}
	if path != "/usr/bin/git" {
		return true
	}
	return exec.Command("xcode-select", "-p").Run() == nil
}

// ghLoggedIn は gh で github.com にログインしているか
func ghLoggedIn() bool {
	ctx, cancel := context.WithTimeout(context.Background(), authCheckTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "auth", "status", "--hostname", "github.com")
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1")
	return cmd.Run() == nil
}

// claudeLoggedIn は Claude Code でログインしているか
func claudeLoggedIn() bool {
	ctx, cancel := context.WithTimeout(context.Background(), authCheckTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "claude", "auth", "status").Output()
	if err != nil {
		return false
	}
	var status struct {
		LoggedIn bool `json:"loggedIn"`
	}
	return json.Unmarshal(out, &status) == nil && status.LoggedIn
}

// runBrew は brew を入力待ちなしで実行する
func runBrew(args ...string) error {
	if !hasCommand("brew") {
		return fmt.Errorf("Homebrew（brew）が見つかりません。先に Homebrew を入れてください")
	}
	ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
	defer cancel()

	var output bytes.Buffer
	cmd := exec.CommandContext(ctx, "brew", args...)
	cmd.Env = append(os.Environ(), "NONINTERACTIVE=1", "HOMEBREW_NO_ENV_HINTS=1")
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("brew %s が %s 以内に終わりませんでした", strings.Join(args, " "), installTimeout)
		}
		return fmt.Errorf("brew %s: %s", strings.Join(args, " "), strings.TrimSpace(output.String()))
	}
	return nil
}

// openTerminalWith はターミナルで name を args 付きで実行する。
// 入れたばかりの道具はターミナルのシェルの PATH にまだないことがあるので、見つけた場所で呼ぶ
func openTerminalWith(name string, args ...string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("%s が見つかりません。先に入れてください", name)
	}
	words := []string{shellQuote(path)}
	for _, arg := range args {
		words = append(words, shellQuote(arg))
	}
	return openTerminal(strings.Join(words, " "))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// openTerminal はターミナルの新しいウィンドウで command を実行する
func openTerminal(command string) error {
	// AppleScript の文字列に入れるので、\ と " をエスケープする
	quoted := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(command)
	out, err := exec.Command("osascript",
		"-e", `tell application "Terminal"`,
		"-e", "activate",
		"-e", `do script "`+quoted+`"`,
		"-e", "end tell").CombinedOutput()
	if err != nil {
		return fmt.Errorf("ターミナルを開けませんでした: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
