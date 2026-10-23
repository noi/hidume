package main

import (
	"fmt"
	"regexp"
	"strings"
)

// ForcePushPlan はフォースプッシュで何が起きるかを、実行前に見せるための情報
type ForcePushPlan struct {
	// 上書きするリモートブランチ（例: origin/noi/dev）
	Upstream string `json:"upstream"`
	// 確認した時点のリモートブランチの先頭。ForcePush に渡し、これから変わっていたら止める
	Expected string `json:"expected"`
	// リモートにだけあって、フォースプッシュで消えるコミット（「短いハッシュ 件名」の形）
	Lost []string `json:"lost"`
	// 手元にだけあって、フォースプッシュで送るコミットの数
	Ahead int `json:"ahead"`
}

var objectName = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// forcePushTarget はフォースプッシュしてよい状況かを確かめ、上書きする origin 上のブランチ名を返す
func (a *App) forcePushTarget() (string, error) {
	s := a.GetPushStatus()
	switch {
	case s.Branch == "":
		return "", fmt.Errorf("ブランチにいないのでプッシュできません")
	case s.Branch == s.DefaultBranch:
		return "", fmt.Errorf("デフォルトブランチにはフォースプッシュしません")
	case !strings.HasPrefix(s.Upstream, "origin/"):
		return "", fmt.Errorf("origin のブランチを追跡していないのでフォースプッシュできません")
	}
	remote := strings.TrimPrefix(s.Upstream, "origin/")
	if remote == s.DefaultBranch || !branchName.MatchString(remote) || strings.HasPrefix(remote, "-") {
		return "", fmt.Errorf("%s にはフォースプッシュしません", s.Upstream)
	}
	return remote, nil
}

// PrepareForcePush は追跡しているリモートブランチの最新を取ってきて、
// フォースプッシュすると消えるコミットと送るコミットを調べる。まだプッシュはしない
func (a *App) PrepareForcePush() (ForcePushPlan, error) {
	plan := ForcePushPlan{Lost: []string{}}
	remote, err := a.forcePushTarget()
	if err != nil {
		return plan, err
	}
	tracking := "refs/remotes/origin/" + remote
	if _, err := a.runNetwork("git", "fetch", "--quiet", "origin", "refs/heads/"+remote+":"+tracking); err != nil {
		return plan, err
	}
	out, err := a.runGit("rev-parse", "--verify", tracking)
	if err != nil {
		return plan, err
	}
	plan.Upstream = "origin/" + remote
	plan.Expected = strings.TrimSpace(out)
	lost, err := a.runGit("log", "--no-show-signature", "--format=%h %s", "HEAD.."+plan.Expected)
	if err != nil {
		return plan, err
	}
	plan.Lost = strings.FieldsFunc(lost, func(r rune) bool { return r == '\n' })
	plan.Ahead = a.countCommits(plan.Expected + "..HEAD")
	return plan, nil
}

// ForcePush は今のブランチで origin の追跡ブランチを上書きする。
// --force-with-lease で、リモートが expected（PrepareForcePush で確認した先頭）から動いていたら止める
func (a *App) ForcePush(expected string) error {
	if !objectName.MatchString(expected) {
		return fmt.Errorf("確認したコミットが正しくありません: %q", expected)
	}
	remote, err := a.forcePushTarget()
	if err != nil {
		return err
	}
	ref := "refs/heads/" + remote
	_, err = a.runNetwork("git", "push", "--force-with-lease="+ref+":"+expected, "origin", "HEAD:"+ref)
	if err != nil && strings.Contains(err.Error(), "stale info") {
		return fmt.Errorf("確認した後に origin/%s が更新されたので、上書きせずに止めました。もう一度確認してください", remote)
	}
	return err
}
