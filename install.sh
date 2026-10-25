#!/bin/sh
# 最新リリースの hidume を /Applications にインストールする
#   curl -fsSL https://raw.githubusercontent.com/noi/hidume/main/install.sh | sh
set -eu

REPO="noi/hidume"
APP="hidume.app"

# ダウンロードが途中で切れたときに中途半端に実行されないよう、全体を関数にして最後に呼ぶ
main() {
	if [ "$(uname -s)" != "Darwin" ]; then
		echo "hidume は macOS 専用です" >&2
		exit 1
	fi

	# /releases/latest のリダイレクト先（.../releases/tag/v1.2.3）からタグを取り出す
	latest_url=$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest")
	tag=${latest_url##*/}
	case "$tag" in
	v[0-9]*) ;;
	*)
		echo "最新リリースが見つかりませんでした" >&2
		exit 1
		;;
	esac

	dmg="hidume-${tag}-macos-universal.dmg"
	tmp=$(mktemp -d)
	mnt="$tmp/mnt"
	trap 'hdiutil detach -quiet "$mnt" 2>/dev/null || true; rm -rf "$tmp"' EXIT

	echo "hidume ${tag} をダウンロードしています..."
	curl -fL --progress-bar -o "$tmp/$dmg" "https://github.com/${REPO}/releases/download/${tag}/${dmg}"

	mkdir "$mnt"
	hdiutil attach -quiet -nobrowse -readonly -mountpoint "$mnt" "$tmp/$dmg"

	# HIDUME_INSTALL_DIR で入れ先を変えられる。
	# 指定がなく、管理者でなく /Applications に書き込めないときは ~/Applications に入れる
	dest="${HIDUME_INSTALL_DIR:-/Applications}"
	if [ -z "${HIDUME_INSTALL_DIR:-}" ] && [ ! -w "$dest" ]; then
		dest="$HOME/Applications"
		mkdir -p "$dest"
	fi

	rm -rf "${dest:?}/$APP"
	cp -R "$mnt/$APP" "$dest/"
	# 署名・公証していないので、念のため隔離属性を外して Gatekeeper に止められないようにする
	xattr -dr com.apple.quarantine "$dest/$APP" 2>/dev/null || true

	echo "${dest}/${APP} にインストールしました"
}

main "$@"
