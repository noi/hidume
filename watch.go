package main

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	watchInterval = 2 * time.Second
	// リポジトリの状態が変わったときに画面へ送るイベント
	repoChangedEvent = "repo-changed"
)

// watch は、開いているリポジトリの状態を一定間隔で見て、変わったら画面に知らせる。
// ファイルを 1 つずつ監視する代わりに git に状態をまとめて聞くので、.gitignore されたものは自然に対象外になる
func (a *App) watch(ctx context.Context) {
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()

	var last [sha256.Size]byte
	lastDir := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		dir := a.dir()
		sum, ok := fingerprint(dir)
		if !ok {
			continue
		}
		// リポジトリを切り替えた直後は画面が自分で読み直すので、知らせずに基準だけ取り直す
		if dir == lastDir && sum != last {
			runtime.EventsEmit(ctx, repoChangedEvent)
		}
		last, lastDir = sum, dir
	}
}

// fingerprint は画面に出している情報（変更ファイル、ブランチ、各ブランチやタグの位置）の要約
func fingerprint(dir string) ([sha256.Size]byte, bool) {
	status, err := runGitIn(dir, "status", "--porcelain=v2", "--branch", "-z")
	if err != nil {
		return [sha256.Size]byte{}, false
	}
	refs, err := runGitIn(dir, "for-each-ref", "--format=%(objectname) %(refname)")
	if err != nil {
		return [sha256.Size]byte{}, false
	}
	return sha256.Sum256([]byte(status + "\x00" + refs)), true
}
