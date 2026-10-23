# hidume

Go + [Wails v2](https://wails.io/) 製のデスクトップアプリ。フロントエンドは React + TypeScript（Vite）。

## 必要なもの

- Go 1.25 以上
- Node.js / npm
- Wails CLI: `go install github.com/wailsapp/wails/v2/cmd/wails@latest`

環境チェックは `wails doctor`。

## 開発

```sh
make        # = make dev（wails dev）
```

Vite のホットリロード付きで起動する。ブラウザから Go のメソッドを呼びたい場合は http://localhost:34115 を開く。

## ビルド

```sh
make build  # = wails build
```

成果物は `build/bin/` に出力される。

## 構成

| パス | 内容 |
| --- | --- |
| `main.go` | アプリのエントリポイント（ウィンドウ設定・バインド） |
| `app.go` | フロントエンドから呼び出す Go のメソッド |
| `frontend/` | React + TypeScript のフロントエンド |
| `frontend/wailsjs/` | Wails が自動生成する Go バインディング（手で編集しない） |
| `build/` | アイコンや Info.plist などのパッケージング設定 |
| `wails.json` | Wails のプロジェクト設定 |
