package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// version はこのアプリのバージョン（v を付けない 1.2.3）。
// リリースのビルドで -ldflags "-X main.version=1.2.3" で埋め込む。手元のビルドでは空で、アップデートを確かめない
var version = ""

const (
	// 最新のリリース。プレリリースとドラフトは含まない
	latestReleaseURL = "https://api.github.com/repos/" + releaseRepo + "/releases/latest"
	releaseRepo      = "noi/hidume"
	// 最新のリリースを調べる時間の上限
	updateCheckTimeout = 15 * time.Second
	// dmg のダウンロードと差し替えにかかる時間の上限
	updateInstallTimeout = 10 * time.Minute
	// メニューの「アップデートを確認」を押したことをフロントに知らせるイベント
	updateCheckEvent = "update-check"
)

// UpdateInfo はアップデートがあるかの結果
type UpdateInfo struct {
	// 今のバージョン（v1.2.3）。手元のビルドなら空
	Current string `json:"current"`
	// 最新のリリースのバージョン（v1.2.3）。手元のビルドなら空
	Latest string `json:"latest"`
	// 最新のリリースが今より新しいか
	Available bool `json:"available"`
	// 最新のリリースのページ。変更点を見るのに開く
	URL string `json:"url"`
}

// githubRelease は GitHub の API が返すリリースのうち、使うところ
type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		// sha256:<16 進>
		Digest string `json:"digest"`
	} `json:"assets"`
}

// CheckUpdate は GitHub の最新のリリースが今のバージョンより新しいかを調べる
func (a *App) CheckUpdate() (UpdateInfo, error) {
	if version == "" {
		return UpdateInfo{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
	defer cancel()
	var r githubRelease
	if err := fetchJSON(ctx, latestReleaseURL, &r); err != nil {
		return UpdateInfo{}, err
	}
	current := "v" + version
	return UpdateInfo{
		Current:   current,
		Latest:    r.TagName,
		Available: newerVersion(r.TagName, current),
		URL:       r.HTMLURL,
	}, nil
}

// InstallUpdate は最新のリリースの dmg をダウンロードして今の hidume.app と差し替え、起動し直す。
// 動いているアプリのファイルを差し替えても、終了するまでは前の中身のまま動き続ける
func (a *App) InstallUpdate() error {
	if goruntime.GOOS != "darwin" {
		return fmt.Errorf("アップデートは macOS でしかできません")
	}
	bundle, err := appBundle()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), updateInstallTimeout)
	defer cancel()
	if err := installUpdate(ctx, bundle); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("アップデートが %s 以内に終わりませんでした", updateInstallTimeout)
		}
		return err
	}
	if err := relaunch(bundle); err != nil {
		return fmt.Errorf("アップデートしましたが、起動し直せませんでした。hidume を開き直してください: %w", err)
	}
	runtime.Quit(a.ctx)
	return nil
}

func installUpdate(ctx context.Context, bundle string) error {
	var r githubRelease
	if err := fetchJSON(ctx, latestReleaseURL, &r); err != nil {
		return err
	}
	// リリースのワークフローが付ける名前
	name := fmt.Sprintf("hidume-%s-macos-universal.dmg", r.TagName)
	i := -1
	for j, asset := range r.Assets {
		if asset.Name == name {
			i = j
		}
	}
	if i < 0 {
		return fmt.Errorf("%s のリリースに %s がありません", r.TagName, name)
	}
	sum, ok := strings.CutPrefix(r.Assets[i].Digest, "sha256:")
	if !ok {
		return fmt.Errorf("%s のチェックサムがわかりません", name)
	}

	tmp, err := os.MkdirTemp("", "hidume-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	dmg := filepath.Join(tmp, name)
	if err := downloadFile(ctx, r.Assets[i].URL, dmg, sum); err != nil {
		return err
	}
	mnt := filepath.Join(tmp, "mnt")
	if err := os.Mkdir(mnt, 0o700); err != nil {
		return err
	}
	if out, err := exec.CommandContext(ctx, "hdiutil", "attach", "-quiet", "-nobrowse", "-readonly", "-mountpoint", mnt, dmg).CombinedOutput(); err != nil {
		return fmt.Errorf("%s を開けませんでした: %s", name, strings.TrimSpace(string(out)))
	}
	// ctx が切れていても外せるよう、ctx は渡さない
	defer exec.Command("hdiutil", "detach", "-quiet", "-force", mnt).Run()

	// 名前を変えるだけで入れ替えられるよう、今のアプリと同じフォルダーに一度コピーする
	parent := filepath.Dir(bundle)
	staged := filepath.Join(parent, ".hidume-update.app")
	if err := os.RemoveAll(staged); err != nil {
		return fmt.Errorf("%s に書き込めません。アプリを書き込めるフォルダーに置いてください: %w", parent, err)
	}
	defer os.RemoveAll(staged)
	// ditto はシンボリックリンクや拡張属性を保ったままコピーする
	if out, err := exec.CommandContext(ctx, "ditto", filepath.Join(mnt, "hidume.app"), staged).CombinedOutput(); err != nil {
		return fmt.Errorf("%s にコピーできませんでした: %s", parent, strings.TrimSpace(string(out)))
	}
	// 署名・公証していないので、念のため隔離属性を外して Gatekeeper に止められないようにする（install.sh と同じ）
	_ = exec.CommandContext(ctx, "xattr", "-dr", "com.apple.quarantine", staged).Run()
	return swapBundle(bundle, staged)
}

// swapBundle は bundle を staged と入れ替える。入れ替えに失敗したら元に戻す
func swapBundle(bundle, staged string) error {
	old := filepath.Join(filepath.Dir(bundle), ".hidume-old.app")
	if err := os.RemoveAll(old); err != nil {
		return err
	}
	if err := os.Rename(bundle, old); err != nil {
		return fmt.Errorf("今の %s を入れ替えられませんでした: %w", filepath.Base(bundle), err)
	}
	if err := os.Rename(staged, bundle); err != nil {
		_ = os.Rename(old, bundle)
		return fmt.Errorf("新しい %s を置けませんでした: %w", filepath.Base(bundle), err)
	}
	_ = os.RemoveAll(old)
	return nil
}

// appBundle は動いている hidume の .app の場所
func appBundle() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	// <bundle>.app/Contents/MacOS/hidume
	bundle := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	if filepath.Ext(bundle) != ".app" {
		return "", fmt.Errorf("hidume が .app の中から起動されていないので、アップデートできません")
	}
	return bundle, nil
}

// relaunch は、このプロセスが終わるのを待ってから bundle を開くシェルを起動する。
// Finder などから開いたアプリは launchd のジョブで、終わると同じプロセスグループに残ったプロセスを launchd が止める。
// シェルが巻き添えにならないよう、新しいセッション（別のプロセスグループ）で起動する
func relaunch(bundle string) error {
	script := `while kill -0 "$1" 2>/dev/null; do sleep 0.2; done; open "$2"`
	cmd := exec.Command("/bin/sh", "-c", script, "sh", strconv.Itoa(os.Getpid()), bundle)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// newerVersion は latest が current より新しいか（どちらも v1.2.3 か v1.2.3-rc.1 の形）。
// 読めない形のときは新しくないことにする
func newerVersion(latest, current string) bool {
	l, lpre, ok := parseVersion(latest)
	if !ok {
		return false
	}
	c, cpre, ok := parseVersion(current)
	if !ok {
		return false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	// 同じ番号なら、プレリリースより正式なリリースが新しい。プレリリースどうしは比べない
	return lpre == "" && cpre != ""
}

// parseVersion は v1.2.3-rc.1 を [1 2 3] と rc.1 に分ける
func parseVersion(v string) ([3]int, string, bool) {
	var n [3]int
	v, ok := strings.CutPrefix(v, "v")
	if !ok {
		return n, "", false
	}
	v, pre, _ := strings.Cut(v, "-")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return n, "", false
	}
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil || x < 0 {
			return n, "", false
		}
		n[i] = x
	}
	return n, pre, true
}
