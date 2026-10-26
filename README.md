# hidume

<img src="assets/mascot/hidume_x8.png" alt="ヒヅメ" width="128" align="right">

マスコットの「ヒヅメくん」がとなりで手伝ってくれる、git クライアントのデスクトップアプリ。
[Claude Code](https://claude.com/claude-code) と組み合わせて、コミットメッセージや PR の文面を書いたり、
エラーの相談に乗ってもらったりできる。

Go + [Wails v2](https://wails.io/) 製で、フロントエンドは React + TypeScript（Vite）。

## できること

- 変更のステージ・コミット・リバートと、コミットツリーの表示
- ブランチの作成（ワークツリーで作業し、PR のマージ後に片付けられる）
- push・pull・force push・ベースブランチのマージ
- GitHub での PR の作成とリポジトリの作成（`gh` を使う）
- Claude Code によるコミットメッセージと PR の文面の生成、チャットでの相談
- 秘密情報を含みそうなファイルをコミットする前の警告
- おすすめのリポジトリ設定（PR 必須のルールセット、スカッシュマージ、.gitignore、GAS の CI・CD など）を 1 つずつ有効化
- [clasp](https://github.com/google/clasp) を使った Google Apps Script の開発（GAS への反映・取り込み）
  - GAS は開いているリポジトリの `gas/{project}/` に取り込むか新しく作る。1 つのリポジトリに複数置け、GAS のファイルは `gas/{project}/src/` に入る
  - リポジトリ直下に `.clasp.json` があるリポジトリも、そのまま扱える
  - 同じ GAS のファイルを、ドキュメントごとに紐づく別々の GAS にも反映できる（「反映先を追加」）。
    反映先は `.clasp.json` の横に置く `.clasp.{name}.json` で、「GAS に反映」はすべての反映先にまとめて反映する
- 初回起動時のセットアップ（Homebrew・git・gh・Claude Code のインストールとログイン。macOS のみ）
- アップデート（起動時と「設定」→「アップデートを確認…」で新しいリリースを確かめ、dmg を入れ替えて起動し直す）

## インストール

macOS のターミナルで次を実行すると、最新リリースを `/Applications` にインストールする
（書き込めないときは `~/Applications`）。

```sh
curl -fsSL https://raw.githubusercontent.com/noi/hidume/main/install.sh | sh
```

curl でダウンロードしたものには隔離属性が付かないので、Gatekeeper に止められずにそのまま開ける。
入れ先を変えたいときは `HIDUME_INSTALL_DIR` を指定する。

```sh
curl -fsSL https://raw.githubusercontent.com/noi/hidume/main/install.sh | HIDUME_INSTALL_DIR=~/Applications sh
```

[Releases](https://github.com/noi/hidume/releases) から dmg をダウンロードして入れてもよい（初回は後述の
Gatekeeper の手順が必要）。

## 使うもの

アプリが呼び出す外部のコマンド。初回のセットアップで入れられる。

- [git](https://git-scm.com/)
- [GitHub CLI（gh）](https://cli.github.com/)
- [Claude Code](https://claude.com/claude-code)
- Google Apps Script を扱うときは Node.js と clasp

設定や会話の記録は `os.UserConfigDir()` の下の `hidume/` に保存する（macOS なら `~/Library/Application Support/hidume/`）。

## 開発

### 必要なもの

- Go 1.25 以上
- Node.js / npm
- Wails CLI: `go install github.com/wailsapp/wails/v2/cmd/wails@latest`

環境チェックは `wails doctor`。

### 起動

```sh
make        # = make dev（wails dev）
```

Vite のホットリロード付きで起動する。ブラウザから Go のメソッドを呼びたい場合は http://localhost:34115 を開く。

### ビルド

```sh
make build  # = wails build
```

成果物は `build/bin/` に出力される。

### リリース

semver のタグ（`v1.2.3`、プレリリースなら `v1.2.3-rc.1`）を push すると、GitHub Actions が
macOS 向け（Universal）にビルドして、`hidume-<タグ>-macos-universal.dmg` を添付した Release を作る。

```sh
git tag v0.1.0
git push origin v0.1.0
```

Claude Code の `/hidume-release` を使うと、前回のタグからの変更をまとめてバージョンの候補を出し、
選んだタグを `origin/main` に切って push し、ワークフローの完了まで見届ける。

署名と公証はしていないので、ダウンロードしたアプリは初回に Gatekeeper に止められる。次のどちらかで開けるようになる。

- システム設定の「プライバシーとセキュリティ」で、「"hidume" は Mac を保護するためにブロックされました」の横の
  「このまま開く」を押す（止められてから 1 時間ほどしか表示されない）
- `xattr -dr com.apple.quarantine /Applications/hidume.app` で隔離属性を外す

macOS 15 以降は、Finder で右クリックして「開く」を選んでも開けない。

ビルドのときにタグのバージョンを `main.version` に埋め込み、アプリはそれと GitHub の最新リリース（プレリリースは除く）を
比べてアップデートを知らせる。アップデートでは Release の dmg を GitHub が出すチェックサムで確かめてから、
動いている `.app` を入れ替えて起動し直す。`make build` など手元のビルドはバージョンが空なので、アップデートを確かめない。

### テスト

```sh
go test ./...
```

`main.go` が `frontend/dist` を埋め込むので、一度もビルドしていないときは先に `make build` か
`cd frontend && npm install && npm run build` でフロントエンドをビルドしておく。

## 構成

| パス | 内容 |
| --- | --- |
| `main.go` | アプリのエントリポイント（ウィンドウ設定・バインド） |
| `app.go` | フロントエンドから呼び出す Go のメソッド（リポジトリ・ステージ・コミットなど） |
| `branch.go` / `merge.go` / `push.go` / `force.go` | ブランチとワークツリー、マージ、push まわり |
| `github.go` | GitHub の PR とリポジトリ（`gh` 経由） |
| `chat.go` / `commitmsg.go` / `prmsg.go` | Claude Code とのチャット、コミットメッセージと PR の文面の生成 |
| `clasp.go` | Google Apps Script（clasp）との連携 |
| `setup.go` | 初回のセットアップ |
| `updater.go` | GitHub の Release からのアップデート |
| `install.sh` | curl で最新リリースを入れるインストールスクリプト |
| `config.go` / `history.go` | 設定と作業履歴の保存 |
| `frontend/` | React + TypeScript のフロントエンド |
| `frontend/wailsjs/` | Wails が自動生成する Go バインディング（手で編集しない） |
| `assets/mascot/` | ヒヅメくんのドット絵と、それを生成するスクリプト |
| `build/` | アイコンや Info.plist などのパッケージング設定 |
| `wails.json` | Wails のプロジェクト設定 |

## ライセンス

[MIT License](LICENSE)
