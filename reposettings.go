package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// おすすめのリポジトリ設定の種類。説明の文面はフロント側で持つ
const (
	// デフォルトブランチを PR を通してだけ更新できるようにする（ルールセット）
	settingRequirePR = "require-pr"
	// マージをスカッシュマージ（PR のタイトルとコミットの一覧）だけにする
	settingSquashMerge = "squash-merge"
	// PR をマージしたら GitHub のブランチを消す
	settingDeleteBranch = "delete-branch"
	// .gitignore にいつも外すものを足す
	settingGitignore = "gitignore"
	// PR ごとに GAS のコードを確かめる CI を置き、通らなければマージできなくする
	settingCI = "ci"
	// デフォルトブランチにマージしたら GAS に反映する
	settingCD = "cd"
	// Claude Code に秘密のファイルを読ませない
	settingClaude = "claude"
	// Claude Code に日本語で答えさせる
	settingClaudeLanguage = "claude-language"
)

const (
	// hidume が作るルールセットの名前。あるかどうかもこの名前で見分ける
	rulesetRequirePR = "hidume: PR を通して更新"
	rulesetCI        = "hidume: CI を必須にする"
	// CI のジョブ名。ルールセットで必須にするチェックの名前と合わせる
	ciCheckName = "gas-check"
	// 必須にするチェックを GitHub Actions のものに限るための ID
	githubActionsAppID = 15368
	ciWorkflowPath     = ".github/workflows/ci.yml"
	cdWorkflowPath     = ".github/workflows/deploy.yml"
	// CD で clasp に渡す認証情報（~/.clasprc.json の中身）を入れる GitHub のシークレット
	claspSecretName = "CLASPRC_JSON"
	// リポジトリに置いてコミットする Claude Code の設定
	claudeSettingsPath = ".claude/settings.json"
)

// RepoSetting はおすすめの設定 1 つ分の状態
type RepoSetting struct {
	ID string `json:"id"`
	// もう設定されているか
	Enabled bool `json:"enabled"`
	// 設定できない理由。設定できるなら空
	Blocked string `json:"blocked"`
}

// RepoSettings はおすすめの設定の一覧
type RepoSettings struct {
	Settings []RepoSetting `json:"settings"`
	// gh のログインに workflow の権限がないか。ないと .github/workflows のファイルをプッシュできない
	NeedsWorkflowScope bool `json:"needsWorkflowScope"`
}

// githubRepo は GitHub の REST API（repos/{owner}/{repo}）のうち使う項目
type githubRepo struct {
	DefaultBranch       string `json:"default_branch"`
	AllowSquashMerge    bool   `json:"allow_squash_merge"`
	AllowMergeCommit    bool   `json:"allow_merge_commit"`
	AllowRebaseMerge    bool   `json:"allow_rebase_merge"`
	SquashMergeTitle    string `json:"squash_merge_commit_title"`
	SquashMergeMessage  string `json:"squash_merge_commit_message"`
	DeleteBranchOnMerge bool   `json:"delete_branch_on_merge"`
	Permissions         struct {
		Admin bool `json:"admin"`
	} `json:"permissions"`
}

// branchRule はデフォルトブランチにかかっているルール（どのルールセットのものも含む）
type branchRule struct {
	Type       string `json:"type"`
	Parameters struct {
		RequiredStatusChecks []statusCheck `json:"required_status_checks"`
	} `json:"parameters"`
}

type statusCheck struct {
	Context string `json:"context"`
}

// repoSettingsState は設定の状態を決めるために調べたこと
type repoSettingsState struct {
	// GitHub の設定を変えられない理由。変えられるなら空
	githubBlocked string
	repo          githubRepo
	rules         []branchRule
	secrets       []string
	gas           bool
	// GAS のプロジェクトのことで CD を置けない理由
	gasBlocked string
}

// GetRepoSettings はおすすめの設定と、それぞれがもう設定されているかを返す。CI と CD は GAS のリポジトリのときだけ出す
func (a *App) GetRepoSettings() (RepoSettings, error) {
	dir := a.dir()
	if dir == "" {
		return RepoSettings{}, errNoRepository
	}
	st := a.repoSettingsState()
	settings := RepoSettings{Settings: []RepoSetting{}}
	add := func(id string, enabled bool, blocked string) {
		if enabled {
			blocked = ""
		}
		settings.Settings = append(settings.Settings, RepoSetting{ID: id, Enabled: enabled, Blocked: blocked})
	}
	add(settingRequirePR, st.hasRule("pull_request"), st.githubBlocked)
	r := st.repo
	add(settingSquashMerge, st.githubBlocked == "" && r.AllowSquashMerge && !r.AllowMergeCommit && !r.AllowRebaseMerge &&
		r.SquashMergeTitle == "PR_TITLE" && r.SquashMergeMessage == "COMMIT_MESSAGES", st.githubBlocked)
	add(settingDeleteBranch, st.githubBlocked == "" && r.DeleteBranchOnMerge, st.githubBlocked)
	ignore, err := missingIgnoreLines(dir)
	if err != nil {
		return settings, err
	}
	add(settingGitignore, len(ignore) == 0, "")
	deny, err := missingClaudeDenies(dir)
	add(settingClaude, err == nil && len(deny) == 0, errorText(err))
	language, err := hasClaudeLanguage(dir)
	add(settingClaudeLanguage, language, errorText(err))
	if st.gas {
		ci := fileExists(filepath.Join(dir, ciWorkflowPath)) && st.hasRequiredCheck(ciCheckName)
		add(settingCI, ci, st.githubBlocked)
		cd := fileExists(filepath.Join(dir, cdWorkflowPath)) && slices.Contains(st.secrets, claspSecretName)
		add(settingCD, cd, firstNonEmpty(st.githubBlocked, st.gasBlocked))
		settings.NeedsWorkflowScope = st.githubBlocked == "" && !a.ghHasWorkflowScope()
	}
	return settings, nil
}

// repoSettingsState は GitHub と手元を調べる。GitHub を読めなくても、手元だけでできる設定は出せるよう、エラーは理由として持つ
func (a *App) repoSettingsState() repoSettingsState {
	var st repoSettingsState
	dir := a.dir()
	if p, err := readClaspProject(dir); err == nil && p != nil {
		st.gas = true
		if out, err := a.runGit("check-ignore", "-q", "--", ".clasp.json"); err == nil && out == "" {
			st.gasBlocked = ".clasp.json が .gitignore で外されているので、GitHub からどの GAS に反映すればよいかわかりません。.clasp.json をコミットしてください"
		}
		if !fileExists(claspCredentialsPath()) {
			st.gasBlocked = firstNonEmpty(st.gasBlocked, "clasp で Google にログインしてから設定してください")
		}
	}
	push := a.GetPushStatus()
	if !push.HasOrigin || push.WebURL == "" || !strings.HasPrefix(push.WebURL, "https://github.com/") {
		st.githubBlocked = "GitHub のリポジトリ（origin）がまだないので設定できません。先にプッシュして GitHub にリポジトリを作ってください"
		return st
	}
	if err := requireGh(); err != nil {
		st.githubBlocked = err.Error()
		return st
	}
	// {owner}/{repo} は gh が今のリポジトリの origin から埋める
	out, err := a.runNetwork("gh", "api", "repos/{owner}/{repo}")
	if err == nil {
		err = json.Unmarshal([]byte(out), &st.repo)
	}
	if err != nil {
		st.githubBlocked = fmt.Sprintf("GitHub のリポジトリの設定を読めませんでした: %v", err)
		return st
	}
	if !st.repo.Permissions.Admin {
		st.githubBlocked = "このリポジトリの管理者（admin）だけが設定できます"
		return st
	}
	// 有料プランでない非公開リポジトリなどでルールを読めなくても、ルールがないものとして続ける
	if out, err := a.runNetwork("gh", "api", "repos/{owner}/{repo}/rules/branches/"+url.PathEscape(st.repo.DefaultBranch)); err == nil {
		_ = json.Unmarshal([]byte(out), &st.rules)
	}
	if st.gas {
		if out, err := a.runNetwork("gh", "secret", "list", "--json", "name", "--jq", ".[].name"); err == nil {
			st.secrets = strings.Fields(out)
		}
	}
	return st
}

func (st repoSettingsState) hasRule(kind string) bool {
	return slices.ContainsFunc(st.rules, func(r branchRule) bool { return r.Type == kind })
}

func (st repoSettingsState) hasRequiredCheck(name string) bool {
	return slices.ContainsFunc(st.rules, func(r branchRule) bool {
		return r.Type == "required_status_checks" &&
			slices.ContainsFunc(r.Parameters.RequiredStatusChecks, func(c statusCheck) bool { return c.Context == name })
	})
}

// ApplyRepoSetting はおすすめの設定を 1 つ設定する。ファイルを置くものは置くだけで、コミットは人がする
func (a *App) ApplyRepoSetting(id string) error {
	dir := a.dir()
	if dir == "" {
		return errNoRepository
	}
	settings, err := a.GetRepoSettings()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(settings.Settings, func(s RepoSetting) bool { return s.ID == id })
	if i < 0 {
		return fmt.Errorf("知らない設定です: %q", id)
	}
	if s := settings.Settings[i]; s.Enabled {
		return nil
	} else if s.Blocked != "" {
		return fmt.Errorf("%s", s.Blocked)
	}
	switch id {
	case settingRequirePR:
		return a.createRuleset(rulesetRequirePR, []map[string]any{
			{"type": "deletion"},
			{"type": "non_fast_forward"},
			{"type": "pull_request", "parameters": map[string]any{
				// 1 人で使うリポジトリでも自分の PR をマージできるよう、承認はいらないことにする
				"required_approving_review_count":   0,
				"dismiss_stale_reviews_on_push":     false,
				"require_code_owner_review":         false,
				"require_last_push_approval":        false,
				"required_review_thread_resolution": false,
			}},
		})
	case settingSquashMerge:
		_, err := a.runNetwork("gh", "api", "--method", "PATCH", "repos/{owner}/{repo}",
			"-F", "allow_squash_merge=true", "-F", "allow_merge_commit=false", "-F", "allow_rebase_merge=false",
			"-f", "squash_merge_commit_title=PR_TITLE", "-f", "squash_merge_commit_message=COMMIT_MESSAGES")
		return githubSettingError(err)
	case settingDeleteBranch:
		_, err := a.runNetwork("gh", "api", "--method", "PATCH", "repos/{owner}/{repo}", "-F", "delete_branch_on_merge=true")
		return githubSettingError(err)
	case settingGitignore:
		return appendGitignore(dir)
	case settingCI:
		if err := writeNewFile(filepath.Join(dir, ciWorkflowPath), ciWorkflow); err != nil {
			return err
		}
		if a.repoSettingsState().hasRequiredCheck(ciCheckName) {
			return nil
		}
		if err := a.createRuleset(rulesetCI, []map[string]any{
			{"type": "required_status_checks", "parameters": map[string]any{
				"strict_required_status_checks_policy": false,
				"required_status_checks": []map[string]any{
					{"context": ciCheckName, "integration_id": githubActionsAppID},
				},
			}},
		}); err != nil {
			return fmt.Errorf("%s は置きましたが、CI を必須にできませんでした。%w", ciWorkflowPath, err)
		}
		return nil
	case settingCD:
		return a.applyCD(dir)
	case settingClaude:
		return addClaudeDenies(dir)
	case settingClaudeLanguage:
		return setClaudeLanguage(dir)
	}
	return fmt.Errorf("知らない設定です: %q", id)
}

// createRuleset はデフォルトブランチにかかるルールセットを作る。管理者もルールを飛ばせないようにする
func (a *App) createRuleset(name string, rules []map[string]any) error {
	body, err := json.Marshal(map[string]any{
		"name":        name,
		"target":      "branch",
		"enforcement": "active",
		"conditions": map[string]any{
			"ref_name": map[string]any{"include": []string{"~DEFAULT_BRANCH"}, "exclude": []string{}},
		},
		"bypass_actors": []any{},
		"rules":         rules,
	})
	if err != nil {
		return err
	}
	_, err = a.runNetworkInput(string(body), "gh", "api", "--method", "POST", "repos/{owner}/{repo}/rulesets", "--input", "-")
	return githubSettingError(err)
}

// githubSettingError は GitHub の設定を変えられなかったときのよくある理由に、説明を付ける
func githubSettingError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "Upgrade to GitHub Pro") || strings.Contains(err.Error(), "make this repository public") {
		return fmt.Errorf("非公開のリポジトリでこの設定を使うには、GitHub の有料プラン（Pro や Team）が必要です。公開リポジトリなら無料で使えます\n\n%w", err)
	}
	return err
}

// applyCD は clasp の認証情報を GitHub のシークレットに登録し、マージしたら GAS に反映するワークフローを置く
func (a *App) applyCD(dir string) error {
	st := a.repoSettingsState()
	if !slices.Contains(st.secrets, claspSecretName) {
		data, err := os.ReadFile(claspCredentialsPath())
		if err != nil {
			return fmt.Errorf("clasp の認証情報を読めませんでした。clasp で Google にログインしてください: %w", err)
		}
		if !json.Valid(data) {
			return fmt.Errorf("clasp の認証情報（%s）が JSON として読めません。clasp で Google にログインし直してください", claspCredentialsPath())
		}
		// 引数に載せるとほかのプロセスから見えるので、標準入力で渡す
		if _, err := a.runNetworkInput(string(data), "gh", "secret", "set", claspSecretName); err != nil {
			return err
		}
	}
	return writeNewFile(filepath.Join(dir, cdWorkflowPath), cdWorkflow(st.repo.DefaultBranch))
}

// ghHasWorkflowScope は gh のログインに workflow の権限があるか。権限の一覧が出ないトークン（細かく権限を決めたものなど）なら、あるものとする
func (a *App) ghHasWorkflowScope() bool {
	out, err := a.runNetwork("gh", "api", "--include", "user")
	if err != nil {
		return true
	}
	for _, line := range strings.Split(out, "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "X-Oauth-Scopes") {
			continue
		}
		for _, scope := range strings.Split(value, ",") {
			if strings.TrimSpace(scope) == "workflow" {
				return true
			}
		}
		return false
	}
	return true
}

// Claude Code に読ませない秘密のファイル。.gitignore のおすすめや、コミット前の警告（secrets.ts）と合わせる
var claudeSecretDenies = []string{
	"Read(**/.env)",
	"Read(**/.env.*)",
	"Read(**/*.pem)",
	"Read(**/*.key)",
	"Read(**/.clasprc.json)",
	"Read(**/creds.json)",
	"Read(**/client_secret*.json)",
	"Read(~/.clasprc.json)",
}

// Claude Code が答える言葉。settings.json の language に書く
const claudeLanguage = "japanese"

// readClaudeSettings はリポジトリの .claude/settings.json を読む。なければ空。
// 知らない項目も書き戻すときに残すよう、型を決めずに読む
func readClaudeSettings(dir string) (map[string]any, []any, error) {
	settings := map[string]any{}
	data, err := os.ReadFile(filepath.Join(dir, claudeSettingsPath))
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &settings); err != nil {
			return nil, nil, fmt.Errorf("%s が JSON として読めないので、書き足せません。直してからもう一度確かめてください", claudeSettingsPath)
		}
	}
	permissions, _ := settings["permissions"].(map[string]any)
	deny, _ := permissions["deny"].([]any)
	if settings["permissions"] != nil && permissions == nil || permissions["deny"] != nil && deny == nil {
		return nil, nil, fmt.Errorf("%s の permissions の書き方がわからないので、書き足せません", claudeSettingsPath)
	}
	return settings, deny, nil
}

// missingClaudeDenies はおすすめのうち、まだ .claude/settings.json の permissions.deny にないものを返す
func missingClaudeDenies(dir string) ([]string, error) {
	_, deny, err := readClaudeSettings(dir)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, rule := range claudeSecretDenies {
		if !slices.Contains(deny, any(rule)) {
			missing = append(missing, rule)
		}
	}
	return missing, nil
}

// addClaudeDenies はおすすめのうち、まだないものを .claude/settings.json の permissions.deny に足す。ほかの設定はそのまま残す
func addClaudeDenies(dir string) error {
	settings, deny, err := readClaudeSettings(dir)
	if err != nil {
		return err
	}
	missing, err := missingClaudeDenies(dir)
	if err != nil || len(missing) == 0 {
		return err
	}
	for _, rule := range missing {
		deny = append(deny, rule)
	}
	permissions, _ := settings["permissions"].(map[string]any)
	if permissions == nil {
		permissions = map[string]any{}
		settings["permissions"] = permissions
	}
	permissions["deny"] = deny
	return writeClaudeSettings(dir, settings)
}

// hasClaudeLanguage は .claude/settings.json で答える言葉が決まっているか。日本語でなくても、人が決めたものとしてそのままにする
func hasClaudeLanguage(dir string) (bool, error) {
	settings, _, err := readClaudeSettings(dir)
	if err != nil {
		return false, err
	}
	language, _ := settings["language"].(string)
	return language != "", nil
}

// setClaudeLanguage は .claude/settings.json の language を日本語にする。ほかの設定はそのまま残す
func setClaudeLanguage(dir string) error {
	settings, _, err := readClaudeSettings(dir)
	if err != nil {
		return err
	}
	settings["language"] = claudeLanguage
	return writeClaudeSettings(dir, settings)
}

func writeClaudeSettings(dir string, settings map[string]any) error {
	if _, ok := settings["$schema"]; !ok {
		settings["$schema"] = "https://json.schemastore.org/claude-code-settings.json"
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, claudeSettingsPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// claspCredentialsPath は clasp login が認証情報を保存する場所
func claspCredentialsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".clasprc.json")
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// writeNewFile は path にファイルを置く。すでにあれば、人が書き換えたものかもしれないので上書きしない
func writeNewFile(path, content string) error {
	if fileExists(path) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// ignoreGroup は .gitignore に足す行のまとまり。足すときは見出しのコメントも付ける
type ignoreGroup struct {
	comment string
	lines   []string
}

// たいていのリポジトリでコミットしないもの
var recommendedIgnores = []ignoreGroup{
	{"# 秘密の情報（パスワードや鍵）", []string{".env", ".env.*", "!.env.example", "*.pem", "*.key"}},
	{"# clasp（GAS）の認証情報", []string{".clasprc.json", "creds.json", "client_secret*.json"}},
	{"# Node.js", []string{"node_modules/", "npm-debug.log*"}},
	{"# Claude Code の個人の設定", []string{".claude/settings.local.json", "CLAUDE.local.md"}},
	{"# エディタ", []string{".vscode/", ".idea/", "*.swp", "*~"}},
	{"# OS が作るファイル", []string{".DS_Store", "Thumbs.db"}},
}

// missingIgnoreLines はおすすめのうち、まだ .gitignore にない行を見出し付きで返す
func missingIgnoreLines(dir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		have[strings.TrimSpace(line)] = true
	}
	var add []string
	for _, g := range recommendedIgnores {
		var lines []string
		for _, line := range g.lines {
			if !have[line] {
				lines = append(lines, line)
			}
		}
		if len(lines) > 0 {
			add = append(add, g.comment)
			add = append(add, lines...)
		}
	}
	return add, nil
}

// appendGitignore はおすすめのうち、まだ .gitignore にない行を末尾に足す
func appendGitignore(dir string) error {
	add, err := missingIgnoreLines(dir)
	if err != nil || len(add) == 0 {
		return err
	}
	path := filepath.Join(dir, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	text := strings.Join(add, "\n") + "\n"
	if len(data) > 0 {
		// 前の行とつながらないよう改行を補い、まとまりの間を 1 行空ける
		if !strings.HasSuffix(string(data), "\n") {
			text = "\n" + text
		}
		text = "\n" + text
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// PR ごとに GAS のコードを確かめる CI。パッケージを入れなくても動くよう、Node.js で文法だけを確かめる。
// package.json に lint や test があればそれも流す
const ciWorkflow = `# hidume が置いた CI。PR ごとに GAS のコードに文法の間違いがないか確かめる
name: ci

on:
  pull_request:

permissions:
  contents: read

jobs:
  check:
    # ブランチのルールでこの名前のチェックを必須にしているので、変えるときはルールも合わせる
    name: ` + ciCheckName + `
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5

      - uses: actions/setup-node@v5
        with:
          node-version: lts/*

      # .clasp.json の rootDir の下の .js / .gs を、実行せずに読み込んで文法を確かめる
      - name: Check syntax
        run: |
          node - <<'EOF'
          const fs = require('fs');
          const path = require('path');
          const vm = require('vm');
          const clasp = JSON.parse(fs.readFileSync('.clasp.json', 'utf8'));
          let failed = false;
          const fail = (file, message) => {
            failed = true;
            console.log(` + "`::error file=${file}::${message}`" + `);
          };
          const walk = (dir) => {
            for (const entry of fs.readdirSync(dir, {withFileTypes: true})) {
              if (entry.name.startsWith('.') || entry.name === 'node_modules') continue;
              const file = path.join(dir, entry.name);
              if (entry.isDirectory()) {
                walk(file);
              } else if (/\.(js|gs)$/i.test(entry.name)) {
                try {
                  new vm.Script(fs.readFileSync(file, 'utf8'), {filename: file});
                } catch (e) {
                  fail(file, e.message);
                  console.log(e.stack);
                }
              } else if (entry.name === 'appsscript.json') {
                try {
                  JSON.parse(fs.readFileSync(file, 'utf8'));
                } catch (e) {
                  fail(file, ` + "`appsscript.json を JSON として読めません: ${e.message}`" + `);
                }
              }
            }
          };
          walk(clasp.rootDir || clasp.srcDir || '.');
          process.exit(failed ? 1 : 0);
          EOF

      - name: Lint and test
        if: hashFiles('package-lock.json') != ''
        run: |
          npm ci
          npm run lint --if-present
          npm run test --if-present
`

// cdWorkflow はデフォルトブランチ branch にマージしたら GAS に反映するワークフローを作る
func cdWorkflow(branch string) string {
	branchJSON, _ := json.Marshal(branch)
	return `# hidume が置いた CD。` + branch + ` にマージしたら、その内容で GAS を置き換える
name: deploy

on:
  push:
    branches:
      - ` + string(branchJSON) + `

# 続けてマージしても、反映が同時に走らないよう 1 つずつ流す
concurrency:
  group: deploy
  cancel-in-progress: false

permissions:
  contents: read

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5

      - uses: actions/setup-node@v5
        with:
          node-version: lts/*

      - name: Install clasp
        run: npm install --global ` + claspPackage + `

      # シークレットはコマンドに埋め込まず、環境変数から書き出す
      - name: Write clasp credentials
        env:
          CLASPRC_JSON: ${{ secrets.` + claspSecretName + ` }}
        run: printf '%s' "$CLASPRC_JSON" > ~/.clasprc.json

      - name: Push to Apps Script
        run: clasp push --force
`
}
