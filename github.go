package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// PullRequest は今のブランチの PR
type PullRequest struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	// OPEN / MERGED / CLOSED
	State   string `json:"state"`
	IsDraft bool   `json:"isDraft"`
	URL     string `json:"url"`
	// GitHub の判定。MERGEABLE / CONFLICTING / UNKNOWN
	Mergeable string `json:"mergeable"`
}

// GetPullRequest は今のブランチから出ている最新の PR を返す。なければ nil
func (a *App) GetPullRequest() (*PullRequest, error) {
	s := a.GetPushStatus()
	if s.Branch == "" || !s.HasOrigin {
		return nil, nil
	}
	if err := requireGh(); err != nil {
		return nil, err
	}
	out, err := a.runNetwork("gh", "pr", "list", "--head", s.Branch, "--state", "all", "--limit", "1",
		"--json", "number,title,state,isDraft,url,mergeable")
	if err != nil {
		return nil, err
	}
	var prs []PullRequest
	if err := json.Unmarshal([]byte(out), &prs); err != nil {
		return nil, err
	}
	if len(prs) == 0 {
		return nil, nil
	}
	return &prs[0], nil
}

// PullRequestDefaults は PR を作るときの初期値
type PullRequestDefaults struct {
	// マージ先の候補。先頭がデフォルトブランチ
	Bases []string `json:"bases"`
	Title string   `json:"title"`
}

// GetPullRequestDefaults は PR のマージ先の候補とタイトルの初期値を返す
func (a *App) GetPullRequestDefaults() (PullRequestDefaults, error) {
	s := a.GetPushStatus()
	d := PullRequestDefaults{Bases: []string{}}
	out, err := a.runGit("for-each-ref", "refs/remotes/origin", "--format=%(refname:lstrip=3)")
	if err != nil {
		return d, err
	}
	if s.DefaultBranch != "" {
		d.Bases = append(d.Bases, s.DefaultBranch)
	}
	for _, b := range strings.Fields(out) {
		if b != "HEAD" && b != s.Branch && b != s.DefaultBranch {
			d.Bases = append(d.Bases, b)
		}
	}
	if len(d.Bases) == 0 {
		return d, fmt.Errorf("マージ先にできるブランチが origin にありません")
	}
	// コミットが 1 つならその件名、複数ならブランチ名をタイトルの初期値にする
	d.Title = s.Branch
	if subjects := a.subjectsSince(d.Bases[0]); len(subjects) == 1 {
		d.Title = subjects[0]
	}
	return d, nil
}

func (a *App) subjectsSince(base string) []string {
	out, err := a.runGit("log", "--no-show-signature", "--format=%s", "refs/remotes/origin/"+base+"..HEAD")
	if err != nil {
		return nil
	}
	return strings.FieldsFunc(out, func(r rune) bool { return r == '\n' })
}

var branchName = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// CreatePullRequest は push 済みの今のブランチから base への PR を作り、その URL を返す。
// 本文には base より後のコミットの件名を並べる
func (a *App) CreatePullRequest(base, title string, draft bool) (string, error) {
	s := a.GetPushStatus()
	switch {
	case s.Branch == "":
		return "", fmt.Errorf("ブランチにいないので PR を作れません")
	case s.Branch == s.DefaultBranch:
		return "", fmt.Errorf("デフォルトブランチからは PR を作れません")
	case s.Upstream == "" || s.Ahead > 0:
		return "", fmt.Errorf("先にプッシュしてから PR を作ってください")
	}
	if !branchName.MatchString(base) || strings.HasPrefix(base, "-") || base == s.Branch {
		return "", fmt.Errorf("マージ先が正しくありません: %q", base)
	}
	title = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, title))
	if title == "" {
		return "", fmt.Errorf("タイトルを入れてください")
	}
	if err := requireGh(); err != nil {
		return "", err
	}

	var body strings.Builder
	for _, subject := range a.subjectsSince(base) {
		body.WriteString("- " + subject + "\n")
	}
	args := []string{"pr", "create", "--base", base, "--head", s.Branch, "--title", title, "--body", body.String()}
	if draft {
		args = append(args, "--draft")
	}
	out, err := a.runNetwork("gh", args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// OpenURL は GitHub のページを既定のブラウザで開く
func (a *App) OpenURL(url string) error {
	if !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("開けない URL です: %q", url)
	}
	runtime.BrowserOpenURL(a.ctx, url)
	return nil
}
