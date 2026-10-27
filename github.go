package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
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
	// マージできるか。CLEAN / UNSTABLE / BLOCKED / BEHIND / DIRTY / DRAFT / HAS_HOOKS / UNKNOWN
	MergeStateStatus string `json:"mergeStateStatus"`
	// 見ている時点の PR の先頭のコミット。マージの間に増えたコミットまでマージしないために使う
	HeadRefOid string  `json:"headRefOid"`
	Checks     []Check `json:"checks"`
	// リポジトリの設定で選べるマージの種別（merge / squash / rebase）。オープンな PR のときだけ埋める
	MergeMethods []string `json:"mergeMethods"`
	// GitHub で前回選んだマージの種別。MergeMethods に入っていなければ空
	DefaultMergeMethod string `json:"defaultMergeMethod"`
}

// Check は PR の Checks（GitHub Actions などのチェック）の 1 つ
type Check struct {
	Name string `json:"name"`
	// pass / fail / pending / skip
	State string `json:"state"`
	URL   string `json:"url"`
}

// rollupItem は statusCheckRollup の 1 件。CheckRun と StatusContext（古い Status API）の 2 種類が混ざる
type rollupItem struct {
	Typename     string `json:"__typename"`
	Name         string `json:"name"`
	WorkflowName string `json:"workflowName"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	DetailsURL   string `json:"detailsUrl"`
	Context      string `json:"context"`
	State        string `json:"state"`
	TargetURL    string `json:"targetUrl"`
}

func toCheck(r rollupItem) Check {
	if r.Typename == "StatusContext" {
		c := Check{Name: r.Context, URL: r.TargetURL}
		switch r.State {
		case "SUCCESS":
			c.State = "pass"
		case "FAILURE", "ERROR":
			c.State = "fail"
		default:
			c.State = "pending"
		}
		return c
	}
	c := Check{Name: r.Name, URL: r.DetailsURL}
	if r.WorkflowName != "" {
		c.Name = r.WorkflowName + " / " + r.Name
	}
	switch {
	case r.Status != "COMPLETED":
		c.State = "pending"
	case r.Conclusion == "SUCCESS":
		c.State = "pass"
	case r.Conclusion == "NEUTRAL" || r.Conclusion == "SKIPPED":
		c.State = "skip"
	default:
		c.State = "fail"
	}
	return c
}

// mergeMethodFlags は選べるマージの種別と gh pr merge のオプション。並びは GitHub の画面と同じ
var mergeMethodFlags = []struct{ method, flag string }{
	{"merge", "--merge"},
	{"squash", "--squash"},
	{"rebase", "--rebase"},
}

type repoMergeSettings struct {
	MergeCommitAllowed       bool   `json:"mergeCommitAllowed"`
	SquashMergeAllowed       bool   `json:"squashMergeAllowed"`
	RebaseMergeAllowed       bool   `json:"rebaseMergeAllowed"`
	ViewerDefaultMergeMethod string `json:"viewerDefaultMergeMethod"`
}

// methods はリポジトリの設定で許されているマージの種別と、その中での既定を返す
func (r repoMergeSettings) methods() ([]string, string) {
	allowed := map[string]bool{"merge": r.MergeCommitAllowed, "squash": r.SquashMergeAllowed, "rebase": r.RebaseMergeAllowed}
	methods := []string{}
	def := ""
	for _, m := range mergeMethodFlags {
		if !allowed[m.method] {
			continue
		}
		methods = append(methods, m.method)
		if strings.EqualFold(r.ViewerDefaultMergeMethod, m.method) {
			def = m.method
		}
	}
	return methods, def
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
		"--json", "number,title,state,isDraft,url,mergeable,mergeStateStatus,headRefOid,statusCheckRollup")
	if err != nil {
		return nil, err
	}
	var prs []struct {
		PullRequest
		StatusCheckRollup []rollupItem `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal([]byte(out), &prs); err != nil {
		return nil, err
	}
	if len(prs) == 0 {
		return nil, nil
	}
	pr := prs[0].PullRequest
	pr.Checks = []Check{}
	for _, r := range prs[0].StatusCheckRollup {
		pr.Checks = append(pr.Checks, toCheck(r))
	}
	pr.MergeMethods = []string{}
	if pr.State == "OPEN" {
		// 設定はおすすめ設定などで変わるので、毎回確かめる。取れなくても PR の表示は続ける
		out, err := a.runNetwork("gh", "repo", "view", "--json",
			"mergeCommitAllowed,squashMergeAllowed,rebaseMergeAllowed,viewerDefaultMergeMethod")
		var r repoMergeSettings
		if err == nil && json.Unmarshal([]byte(out), &r) == nil {
			pr.MergeMethods, pr.DefaultMergeMethod = r.methods()
		}
	}
	return &pr, nil
}

// MergePullRequest は PR を method（merge / squash / rebase）でマージする。
// headOid は画面に出していた PR の先頭のコミットで、その後にプッシュされたコミットがあればマージしない
func (a *App) MergePullRequest(number int, method, headOid string) error {
	flag := ""
	for _, m := range mergeMethodFlags {
		if m.method == method {
			flag = m.flag
		}
	}
	if flag == "" {
		return fmt.Errorf("マージの種別が正しくありません: %q", method)
	}
	if number <= 0 {
		return fmt.Errorf("PR の番号が正しくありません: %d", number)
	}
	if !commitOid.MatchString(headOid) {
		return fmt.Errorf("PR のコミットが正しくありません: %q", headOid)
	}
	if err := requireGh(); err != nil {
		return err
	}
	// ブランチの削除はリポジトリの設定（マージしたらブランチを消す）に任せ、手元のブランチには触れない
	_, err := a.runNetwork("gh", "pr", "merge", strconv.Itoa(number), flag, "--match-head-commit", headOid)
	return err
}

// ClosePullRequest は PR をマージせずにクローズする。ブランチは消さない
func (a *App) ClosePullRequest(number int) error {
	if number <= 0 {
		return fmt.Errorf("PR の番号が正しくありません: %d", number)
	}
	if err := requireGh(); err != nil {
		return err
	}
	_, err := a.runNetwork("gh", "pr", "close", strconv.Itoa(number))
	return err
}

var commitOid = regexp.MustCompile(`^[0-9a-f]{40}$`)

// PullRequestDefaults は PR を作るときの初期値
type PullRequestDefaults struct {
	// マージ先の候補。先頭がデフォルトブランチ
	Bases []string `json:"bases"`
	Title string   `json:"title"`
	// 1 つ目のマージ先より後のコミットの件名を並べたもの
	Body string `json:"body"`
}

// GetPullRequestDefaults は PR のマージ先の候補と、タイトル・本文の初期値を返す
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
	subjects := a.subjectsSince(d.Bases[0])
	d.Title = s.Branch
	if len(subjects) == 1 {
		d.Title = subjects[0]
	}
	d.Body = subjectList(subjects)
	return d, nil
}

func (a *App) subjectsSince(base string) []string {
	out, err := a.runGit("log", "--no-show-signature", "--format=%s", "refs/remotes/origin/"+base+"..HEAD")
	if err != nil {
		return nil
	}
	return strings.FieldsFunc(out, func(r rune) bool { return r == '\n' })
}

func subjectList(subjects []string) string {
	var body strings.Builder
	for _, subject := range subjects {
		body.WriteString("- " + subject + "\n")
	}
	return body.String()
}

var branchName = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// CreatePullRequest は push 済みの今のブランチから base への PR を作り、その URL を返す
func (a *App) CreatePullRequest(base, title, body string, draft bool) (string, error) {
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
	body = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, strings.ReplaceAll(body, "\r\n", "\n")))
	if err := requireGh(); err != nil {
		return "", err
	}

	if len(a.subjectsSince(base)) == 0 {
		return "", fmt.Errorf("origin/%s との差分がないので PR を作れません", base)
	}
	// タイトルや本文が - で始まっても、オプションと取り違えられないように = でつなぐ
	args := []string{"pr", "create", "--base", base, "--head", s.Branch, "--title=" + title, "--body=" + body}
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
