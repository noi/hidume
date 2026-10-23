.PHONY: dev build clean

# ホットリロード付きで起動する
dev:
	wails dev

# 配布用にビルドする（build/bin/ に出力）
build:
	wails build

clean:
	rm -rf build/bin frontend/dist
