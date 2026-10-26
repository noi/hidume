---
name: hidume-release
description: hidume のリリースを行う。origin/main の先頭に semver のタグを切って push し、GitHub Actions の release ワークフローで dmg 付きの Release が作られるのを見届ける。タグ（バージョン）は必ずユーザーと合意してから切る。
disable-model-invocation: true
---

# hidume のリリース

semver のタグ（`v1.2.3`、プレリリースなら `v1.2.3-rc.1`）を push すると、`.github/workflows/release.yml` が
macOS 向けにビルドし、`hidume-<タグ>-macos-universal.dmg` を添付した Release を作る。
このスキルはタグを決めて push し、ワークフローの完了まで確認する。

## 守ること

- **タグ（バージョン）を自分で決めない。** 候補と根拠は示してよいが、どのタグにするかはユーザーに選んでもらう。
  ユーザーが明示的に選ぶか入力するまで、タグを作らない・push しない
- タグを作る前に、タグ名と対象のコミットを提示して最終確認をとる。返事が曖昧なら作らない
- タグは公開されるとワークフローが走り Release ができるので、作り直しや削除はユーザーに言われない限りしない
- 引数でバージョンを渡されたとき（例: `/hidume-release v0.6.0`）も、下の検証をして最終確認をとる

## 手順

### 1. 最新の状態を取ってくる

```sh
git fetch origin --tags --prune --prune-tags
```

ローカルにないタグがリモートにあることがあるので、必ず `--tags` 付きで取る。

### 2. リリースする対象を確かめる

対象は `origin/main` の先頭のコミット。

```sh
git rev-parse origin/main
git log -1 --format='%h %s' origin/main
git tag --points-at origin/main
```

- `origin/main` にすでにタグが付いていたら、リリースする変更がないことを伝えて止める
- 直前のタグは `git tag --list 'v*' --sort=-v:refname | head -1` で取る（プレリリースも含めて一番新しいもの）。
  プレリリースではない最新のタグも `git tag --list 'v*' --sort=-v:refname | grep -v -- - | head -1` で控えておく

### 3. 前回のタグからの変更をまとめる

```sh
git log --oneline --no-merges <直前のタグ>..origin/main
git log --oneline --merges <直前のタグ>..origin/main
```

コミットは Conventional Commits なので、種類ごとに数えて要約する。

- 破壊的変更（`feat!:` や本文の `BREAKING CHANGE:`）
- `feat:`（機能追加）
- `fix:`（バグ修正）
- それ以外（`chore:` `docs:` `refactor:` など）

マージされた PR の番号とタイトルも並べる（マージコミットの件名 `Merge pull request #N from ...` の番号から
`gh pr view N --json title --jq .title` で引ける）。

### 4. タグの候補を示してユーザーに選んでもらう

変更のまとめと一緒に、直前のタグから計算した候補を AskUserQuestion で示す。

- パッチ（例: `v0.5.0` → `v0.5.1`）
- マイナー（例: `v0.5.0` → `v0.6.0`）
- メジャー（例: `v0.5.0` → `v1.0.0`）
- プレリリース（例: `v0.6.0-rc.1`。直前がプレリリースなら番号を 1 つ進めたもの、`v0.6.0-rc.1` → `v0.6.0-rc.2` も候補にする。
  プレリリースを正式版にする `v0.6.0-rc.2` → `v0.6.0` も候補にする）

semver に沿った目安（0.x の間は破壊的変更や機能追加はマイナー、修正だけならパッチ）で 1 つに「(Recommended)」を付けてよいが、
あくまで提案にとどめる。選択肢にない値は「Other」で入力してもらう。

### 5. 選ばれたタグを検証する

次のどれかに当てはまったら理由を伝えて選び直してもらう。

- `^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$` に合わない（`v` がない、`0.6` のように欠けているなど）。
  ワークフローはこの形のタグでしか起動しない
- すでにローカルかリモートにある（`git tag --list <タグ>`、`git ls-remote --tags origin <タグ>`）
- 直前のタグより小さいか等しい（`printf '%s\n' <直前のタグ> <タグ> | sort -V` で確かめる。
  `sort -V` は `v0.6.0-rc.1` を `v0.6.0` より後ろに並べるので、プレリリースと正式版の比較は自分で判断する）

### 6. 最終確認をとってタグを作る

次をまとめて提示し、「このタグを作って push してよいか」を確認する。

- タグ名
- 対象のコミット（`origin/main` の短い SHA と件名）
- プレリリースかどうか（`-` を含むと Release はプレリリースになる）
- 配られ方。正式版はワークフローが終わると、使っている人全員のアプリにアップデートとして通知される
  （`updater.go` は GitHub の最新リリースを見るので）。プレリリースは通知されないので、配る前に試したいときはこちらを使う

OK をもらったら、これまでと同じ軽量タグを `origin/main` のコミットに作って push する。

```sh
git tag <タグ> <origin/main の SHA>
git push origin <タグ>
```

ワークツリーやチェックアウトしているブランチには関係なく、確認した SHA に付ける。

### 7. ワークフローを見届ける

```sh
gh run list --workflow release.yml --limit 3
gh run watch <run の ID> --exit-status
```

ビルドに 5 分ほどかかるので、`gh run watch` はバックグラウンドで走らせて完了を待つ。

- 成功したら `gh release view <タグ>` で Release の URL と添付の dmg を確かめて伝える
- 失敗したら `gh run view <run の ID> --log-failed` で原因を調べて伝える。タグの削除や作り直しは、ユーザーに確認してから行う
