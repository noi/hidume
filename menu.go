package main

import (
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// applicationMenu はメニューバーのメニュー。
// Menu を指定すると Wails の既定のメニューは出ないので、コピー・ペーストなどの編集メニューも自分で並べる
func (a *App) applicationMenu() *menu.Menu {
	m := menu.NewMenuFromItems(menu.AppMenu(), menu.EditMenu(), menu.WindowMenu())
	settings := m.AddSubmenu("設定")
	settings.AddCheckbox("公開リポジトリを作れるようにする", loadConfig().AllowPublicRepository, nil, func(data *menu.CallbackData) {
		// チェックは Wails が切り替えてから呼ぶ
		checked := data.MenuItem.Checked
		_ = updateConfig(func(c *config) { c.AllowPublicRepository = checked })
	})
	settings.AddSeparator()
	// 確かめた結果はヒヅメくんが言うので、フロントに任せる
	settings.AddText("アップデートを確認…", nil, func(*menu.CallbackData) {
		runtime.EventsEmit(a.ctx, updateCheckEvent)
	})
	return m
}

// AllowPublicRepository は GitHub にリポジトリを作るとき「公開で作る」を出すか
func (a *App) AllowPublicRepository() bool {
	return loadConfig().AllowPublicRepository
}
