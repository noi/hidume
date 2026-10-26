package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

static void translateMenu(NSMenu *menu, NSDictionary<NSString *, NSString *> *ja) {
	NSString *title = ja[menu.title];
	if (title != nil) {
		menu.title = title;
	}
	for (NSMenuItem *item in menu.itemArray) {
		NSString *itemTitle = ja[item.title];
		if (itemTitle != nil) {
			item.title = itemTitle;
		}
		if (item.submenu != nil) {
			translateMenu(item.submenu, ja);
		}
	}
}

// Wails の WailsMenu.m が英語で作る項目を、macOS の日本語の言い方に置き換える
static void translateMainMenu(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSString *app = [NSRunningApplication currentApplication].localizedName;
		if (app == nil) {
			app = [[NSProcessInfo processInfo] processName];
		}
		NSDictionary<NSString *, NSString *> *ja = @{
			[@"About " stringByAppendingString:app]: [app stringByAppendingString:@" について"],
			[@"Hide " stringByAppendingString:app]: [app stringByAppendingString:@" を非表示"],
			@"Hide Others": @"ほかを非表示",
			@"Show All": @"すべてを表示",
			[@"Quit " stringByAppendingString:app]: [app stringByAppendingString:@" を終了"],
			@"Edit": @"編集",
			@"Undo": @"取り消す",
			@"Redo": @"やり直す",
			@"Cut": @"カット",
			@"Copy": @"コピー",
			@"Paste": @"ペースト",
			@"Paste and Match Style": @"ペーストしてスタイルを合わせる",
			@"Delete": @"削除",
			@"Select All": @"すべてを選択",
			@"Speech": @"音声読み上げ",
			@"Start Speaking": @"読み上げを開始",
			@"Stop Speaking": @"読み上げを停止",
			@"Window": @"ウインドウ",
			@"Minimize": @"しまう",
			@"Zoom": @"拡大／縮小",
			@"Full Screen": @"フルスクリーンにする",
		};
		translateMenu([NSApp mainMenu], ja);
	});
}
*/
import "C"

// translateMenu はメニューバーの Wails が作った項目を日本語にする。
// Wails は英語の名前を決め打ちで作るので、作り終えたメニューの名前だけを書き換える。動き（コピーなど）はそのまま。
// メインスレッドで、Wails がメニューを設定し終えてから書き換える
func translateMenu() {
	C.translateMainMenu()
}
