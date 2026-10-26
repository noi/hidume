package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	addToolPaths()

	// Create an instance of the app structure
	app := NewApp()

	// Create application with options
	err := wails.Run(&options.App{
		Title:  "hidume",
		Width:  1440,
		Height: 900,
		// 左右のペイン（300px と 400px）と真ん中の変更・コミットの一覧、
		// ヘッダーの吹き出し（入力欄があると 560px）とリポジトリ名が並んでも崩れない大きさ
		MinWidth:  1200,
		MinHeight: 720,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		// Mac を指定しないと Wails はウィンドウの拡大ボタン（緑のボタン）を無効にする
		Mac:       &mac.Options{},
		OnStartup: app.startup,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
