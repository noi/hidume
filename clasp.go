package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	// clasp の push / pull にかかる時間の上限。ファイルが多いと遅い
	claspTimeout = 3 * time.Minute
	// npm install -g にかかる時間の上限
	npmTimeout = 10 * time.Minute
	// clasp 3 でコマンド名が変わったので、それより古いものは使わない
	claspMinMajor = 3
	claspPackage  = "@google/clasp@3"
	// Apps Script API を有効にする設定ページ
	gasSettingsURL = "https://script.google.com/home/usersettings"
	// 取り込み・新規作成したときに GAS のファイルを置くディレクトリ。README や CLAUDE.md を GAS に送らないよう分ける
	gasRootDir = "src"
	// 今のリポジトリに GAS を足すときは、この下の gas/{project}/ に置く。1 つのリポジトリに複数の GAS を置ける
	gasProjectsDir = "gas"
)

// GAS の新規作成で選べる種類。clasp create-script の --type に渡す値
var gasScriptTypes = []string{"sheets", "docs", "slides", "forms", "standalone"}

var (
	scriptID = regexp.MustCompile(`^[A-Za-z0-9_-]{20,}$`)
	// Apps Script エディタの URL（https://script.google.com/home/projects/<ID>/edit や /d/<ID>/edit）
	scriptURL = regexp.MustCompile(`^https://script\.google\.com/(?:u/\d+/)?(?:home/projects|d|macros/d)/([A-Za-z0-9_-]{20,})(?:[/?#]|$)`)
	// スプレッドシートやドキュメントの URL。スクリプトの URL と間違えて貼られやすい
	docsURL   = regexp.MustCompile(`^https://(?:docs|drive)\.google\.com/`)
	sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// gas/{project}/ の名前。CI / CD のパスにも書くので、記号を含まないものにする
	gasProjectName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// GasSync は最後に GAS と同期（反映・取り込み）したときのこと
type GasSync struct {
	// push（反映）/ pull（取り込み）/ clone（最初の取り込みや新規作成）
	Action string `json:"action"`
	// そのときのブランチとコミット（短いハッシュ）。コミットがまだなければ空
	Branch string `json:"branch"`
	Commit string `json:"commit"`
	// GAS のファイルにコミットしていない変更があったか
	Dirty bool  `json:"dirty"`
	Time  int64 `json:"time"`
}

// gasSyncRecord は設定に保存する同期の記録。GAS 側の中身のハッシュも覚えておき、
// 次に反映するときに、ブラウザのエディタで変更されていないかを確かめる
type gasSyncRecord struct {
	GasSync
	Hash string `json:"hash"`
	// ファイル（GAS のファイルを置くディレクトリからの相対パス）ごとのハッシュ
	Files map[string]string `json:"files"`
}

// GasProject はリポジトリに紐づく Apps Script のプロジェクト
type GasProject struct {
	// .clasp.json を置くディレクトリ（リポジトリからの相対パス）。リポジトリ直下なら "."、ほかは gas/{project}
	Dir      string `json:"dir"`
	ScriptID string `json:"scriptId"`
	// GAS のファイルを置くディレクトリ（リポジトリからの相対パス）
	RootDir   string `json:"rootDir"`
	EditorURL string `json:"editorUrl"`
	// 紐づくスプレッドシートなどの URL。スタンドアロンや、紐づく先が .clasp.json にないときは空
	ContainerURL string `json:"containerUrl"`
	// まだこのアプリで同期していなければ nil
	LastSync *GasSync `json:"lastSync"`
}

// GasPushPlan は GAS への反映で何が起きるかを、実行前に見せるための情報
type GasPushPlan struct {
	// 確かめた時点の GAS 側の中身のハッシュ。PushGas に渡し、これから変わっていたら止める
	Expected string `json:"expected"`
	// 反映すると上書きされる、GAS 側にだけある変更（ブラウザのエディタで変更されたファイル）
	Overwritten []string `json:"overwritten"`
	// Overwritten のうち、手元になくて反映すると消えるファイル
	Lost []string `json:"lost"`
	// 手元と GAS の中身が同じで、反映しても何も変わらない
	Unchanged bool `json:"unchanged"`
	// 反映するブランチとコミット、GAS のファイルにコミットしていない変更があるか
	Branch string `json:"branch"`
	Commit string `json:"commit"`
	Dirty  bool   `json:"dirty"`
	// 前回 GAS と同期したときのブランチ。まだなければ空
	LastBranch string `json:"lastBranch"`
}

// claspProject は .clasp.json のうち hidume が使う項目
type claspProject struct {
	// .clasp.json を置くディレクトリ（リポジトリからの / 区切りの相対パス）。リポジトリ直下なら "."
	Dir      string
	ScriptID string
	// GAS のファイルを置くディレクトリ（リポジトリからの / 区切りの相対パス）。リポジトリ直下なら "."
	RootDir          string
	ParentID         string
	ScriptExtensions []string
	HTMLExtensions   []string
}

// GetGasSetup は GAS を使うための準備の状況を返す。足りないものは初回のセットアップと同じ流れで済ませる
func (a *App) GetGasSetup() SetupStatus {
	addNpmBinPath()
	clasp := claspUpToDate()
	tools := []SetupTool{
		{Name: "node", Installed: hasCommand("node") && hasCommand("npm")},
		{Name: "clasp", Installed: clasp},
		{Name: "clasp-auth", Installed: clasp && claspLoggedIn()},
		{Name: "gas-api", Installed: loadConfig().GasAPIEnabled},
	}
	done := true
	for _, t := range tools {
		done = done && t.Installed
	}
	return SetupStatus{Done: done, Tools: tools}
}

// ConfirmGasAPI は Apps Script API を有効にしたことを覚えておく
func (a *App) ConfirmGasAPI() error {
	return updateConfig(func(c *config) { c.GasAPIEnabled = true })
}

// installClasp は npm で clasp を入れる（古いものが入っていれば上書きする）
func installClasp() error {
	if !hasCommand("npm") {
		return fmt.Errorf("npm が見つかりません。先に Node.js を入れてください")
	}
	ctx, cancel := context.WithTimeout(context.Background(), npmTimeout)
	defer cancel()

	var output bytes.Buffer
	cmd := exec.CommandContext(ctx, "npm", "install", "--global", "--no-fund", "--no-audit", claspPackage)
	cmd.Env = append(os.Environ(), "npm_config_update_notifier=false")
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("npm install が %s 以内に終わりませんでした", npmTimeout)
		}
		if strings.Contains(output.String(), "EACCES") {
			return fmt.Errorf("clasp を入れる場所に書き込めませんでした。Homebrew の Node.js（brew install node）を使うと入れられます\n\n%s",
				strings.TrimSpace(output.String()))
		}
		return fmt.Errorf("npm install: %s", strings.TrimSpace(output.String()))
	}
	addNpmBinPath()
	return nil
}

// addNpmBinPath は npm の global の bin を PATH に足す。
// npm install -g で入れた clasp は、Node.js の入れ方によっては addToolPaths が足した場所にない
func addNpmBinPath() {
	if hasCommand("clasp") || !hasCommand("npm") {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), authCheckTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "npm", "prefix", "--global").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return
	}
	bin := filepath.Join(strings.TrimSpace(string(out)), "bin")
	os.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// claspUpToDate は clasp 3 以上が入っているか
func claspUpToDate() bool {
	if !hasCommand("clasp") {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), authCheckTimeout)
	defer cancel()
	out, err := claspCommand(ctx, os.TempDir(), "--version").Output()
	if err != nil {
		return false
	}
	major, _, _ := strings.Cut(strings.TrimSpace(string(out)), ".")
	n, err := strconv.Atoi(major)
	return err == nil && n >= claspMinMajor
}

// claspLoggedIn は clasp で Google にログインしているか
func claspLoggedIn() bool {
	ctx, cancel := context.WithTimeout(context.Background(), authCheckTimeout)
	defer cancel()
	out, err := claspCommand(ctx, os.TempDir(), "show-authorized-user", "--json").Output()
	if err != nil {
		return false
	}
	var status struct {
		LoggedIn bool `json:"loggedIn"`
	}
	return json.Unmarshal(out, &status) == nil && status.LoggedIn
}

func claspCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "clasp", args...)
	// clasp は dir から上へ .clasp.json を探すので、プロジェクトを扱うときは --project で場所も渡す
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	return cmd
}

// runClasp は clasp を入力待ちなしで dir で実行する。標準入力がつながっていないので、clasp は確認を求めない
func runClasp(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), claspTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := claspCommand(ctx, dir, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("clasp %s が %s 以内に終わりませんでした", args[0], claspTimeout)
		}
		if _, ok := err.(*exec.ExitError); !ok {
			return "", fmt.Errorf("clasp を起動できませんでした: %w", err)
		}
		return "", claspError(args[0], strings.TrimSpace(stderr.String()+"\n"+stdout.String()))
	}
	return stdout.String(), nil
}

// claspError は clasp のよくあるエラーに、何をすればよいかの説明を付ける。元の文面も残す
func claspError(command, output string) error {
	var hint string
	switch {
	case strings.Contains(output, "has not enabled the Apps Script API"):
		hint = "Apps Script API が有効になっていません。設定で有効にしてから、数分おいてやり直してください"
	case strings.Contains(output, "No credentials found"), strings.Contains(output, "invalid_grant"):
		hint = "Google にログインしていないか、ログインの期限が切れています。もう一度ログインしてください"
	case strings.Contains(output, "Requested entity was not found"), strings.Contains(output, "Invalid script ID"):
		hint = "スクリプトが見つかりません。URL が正しいか、そのスクリプトを開ける Google アカウントでログインしているか確かめてください"
	case strings.Contains(output, "does not have permission"):
		hint = "このスクリプトを編集する権限がありません。オーナーに編集者として共有してもらってください"
	case strings.Contains(output, "Content directory is a symlink"):
		hint = "GAS のファイルを置くフォルダーがシンボリックリンク（エイリアス）になっているので、clasp が安全のため止めました。普通のフォルダーにしてください"
	case strings.Contains(output, "Syntax error"):
		hint = "GAS に送ったコードに文法の間違いがあります"
	}
	if hint == "" {
		return fmt.Errorf("clasp %s: %s", command, output)
	}
	return fmt.Errorf("%s\n\nclasp %s: %s", hint, command, output)
}

// gasProjects はリポジトリの GAS のプロジェクトを返す。直下の .clasp.json と、gas/{project}/.clasp.json を見る
func gasProjects(repo string) ([]*claspProject, error) {
	dirs := []string{"."}
	entries, err := os.ReadDir(filepath.Join(repo, gasProjectsDir))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, gasProjectsDir+"/"+e.Name())
		}
	}
	projects := []*claspProject{}
	for _, dir := range dirs {
		p, err := readClaspProject(repo, dir)
		if err != nil {
			return nil, err
		}
		if p != nil {
			projects = append(projects, p)
		}
	}
	return projects, nil
}

// readClaspProject はリポジトリの dir（/ 区切りの相対パス）にある .clasp.json を読む。なければ nil を返す
func readClaspProject(repo, dir string) (*claspProject, error) {
	name := path.Join(dir, ".clasp.json")
	data, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(name)))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var raw struct {
		ScriptID         string          `json:"scriptId"`
		RootDir          string          `json:"rootDir"`
		SrcDir           string          `json:"srcDir"`
		ParentID         json.RawMessage `json:"parentId"`
		ScriptExtensions json.RawMessage `json:"scriptExtensions"`
		HTMLExtensions   json.RawMessage `json:"htmlExtensions"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s を読めませんでした: %w", name, err)
	}
	if !scriptID.MatchString(raw.ScriptID) {
		return nil, fmt.Errorf("%s のスクリプト ID が正しくありません: %q", name, raw.ScriptID)
	}
	root := raw.RootDir
	if root == "" {
		root = raw.SrcDir
	}
	root = path.Clean(filepath.ToSlash(root))
	// clasp は rootDir を .clasp.json の場所から数える
	if root != "." && !filepath.IsLocal(filepath.FromSlash(root)) {
		return nil, fmt.Errorf("%s の rootDir が %s の外を指しています: %q", name, dir, raw.RootDir)
	}
	p := &claspProject{
		Dir:              dir,
		ScriptID:         raw.ScriptID,
		RootDir:          path.Join(dir, root),
		ScriptExtensions: extensions(stringList(raw.ScriptExtensions), ".js", ".gs"),
		HTMLExtensions:   extensions(stringList(raw.HTMLExtensions), ".html"),
	}
	if ids := stringList(raw.ParentID); len(ids) > 0 {
		p.ParentID = ids[0]
	}
	return p, nil
}

// stringList は clasp が文字列でも配列でも受け付ける項目を読む
func stringList(raw json.RawMessage) []string {
	var one string
	if json.Unmarshal(raw, &one) == nil && one != "" {
		return []string{one}
	}
	var list []string
	_ = json.Unmarshal(raw, &list)
	return list
}

// extensions は拡張子を . 付きの小文字にそろえる。指定がなければ defaults を使う
func extensions(list []string, defaults ...string) []string {
	if len(list) == 0 {
		return defaults
	}
	out := []string{}
	for _, ext := range list {
		ext = strings.ToLower(strings.TrimSpace(ext))
		if ext != "" && !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		out = append(out, ext)
	}
	return out
}

// pushable は clasp が GAS に送るファイルか。clasp の既定の無視ルールにならう（.claspignore は見ない）
func (p *claspProject) pushable(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if part == "node_modules" || strings.HasPrefix(part, ".") {
			return false
		}
	}
	if path.Base(rel) == "appsscript.json" {
		return true
	}
	ext := strings.ToLower(path.Ext(rel))
	return slices.Contains(p.ScriptExtensions, ext) || slices.Contains(p.HTMLExtensions, ext)
}

// gasSnapshot は GAS のファイルの中身。相対パス（/ 区切り）からその中身の SHA-256 へ
type gasSnapshot map[string]string

// hash はファイルの集まり全体を 1 つのハッシュにまとめる
func (s gasSnapshot) hash() string {
	paths := make([]string, 0, len(s))
	for p := range s {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	h := sha256.New()
	for _, p := range paths {
		fmt.Fprintf(h, "%s\x00%s\n", p, s[p])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// readSnapshot は root の下の、GAS に送るファイルの中身を読む。シンボリックリンクはたどらない
func readSnapshot(root string, p *claspProject) (gasSnapshot, error) {
	snap := gasSnapshot{}
	err := filepath.WalkDir(root, func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			// GAS のファイルを置くディレクトリがまだないのは、ファイルがないのと同じ
			if file == root && os.IsNotExist(err) {
				return filepath.SkipAll
			}
			return err
		}
		rel, err := filepath.Rel(root, file)
		if err != nil || rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !p.pushable(rel) {
			return nil
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		snap[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	return snap, err
}

// pullSnapshot は GAS 側の今の中身を一時ディレクトリに取ってきて、その中身と場所を返す。
// 呼んだ側が一時ディレクトリを消す
func pullSnapshot(p *claspProject) (gasSnapshot, string, error) {
	tmp, err := os.MkdirTemp("", "hidume-gas-")
	if err != nil {
		return nil, "", err
	}
	// macOS の一時ディレクトリは /var（/private/var へのシンボリックリンク）の下にある
	dir, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		return nil, tmp, err
	}
	config := filepath.Join(dir, ".clasp.json")
	if err := writeClaspConfig(config, map[string]any{
		"scriptId":         p.ScriptID,
		"rootDir":          ".",
		"scriptExtensions": p.ScriptExtensions,
		"htmlExtensions":   p.HTMLExtensions,
	}); err != nil {
		return nil, dir, err
	}
	if _, err := runClasp(dir, "pull", "--project", config); err != nil {
		return nil, dir, err
	}
	local := *p
	local.RootDir = "."
	snap, err := readSnapshot(dir, &local)
	return snap, dir, err
}

func writeClaspConfig(file string, settings map[string]any) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(file, append(data, '\n'), 0o644)
}

// gasProject は今のリポジトリの、dir（GasProject の Dir）にある GAS のプロジェクトを読む。なければエラーにする。
// 返すリポジトリのパスは、clasp に渡せるようシンボリックリンクをたどった先にする
func (a *App) gasProject(dir string) (string, *claspProject, error) {
	if a.dir() == "" {
		return "", nil, errNoRepository
	}
	repo, err := filepath.EvalSymlinks(a.dir())
	if err != nil {
		return "", nil, err
	}
	// 決まった場所のほかは読まない
	if dir != "." && !(path.Dir(dir) == gasProjectsDir && gasProjectName.MatchString(path.Base(dir))) {
		return "", nil, fmt.Errorf("GAS のプロジェクトの場所が正しくありません: %q", dir)
	}
	p, err := readClaspProject(repo, dir)
	if err == nil && p == nil {
		err = fmt.Errorf("%s に .clasp.json がありません", dir)
	}
	return repo, p, err
}

// claspConfig は clasp の --project に渡す .clasp.json のパス
func claspConfig(repo string, p *claspProject) string {
	return filepath.Join(repo, filepath.FromSlash(p.Dir), ".clasp.json")
}

// GetGasProjects は今のリポジトリに紐づく GAS のプロジェクトを返す。直下のものを先に、あとは gas/ の下の名前順に並べる
func (a *App) GetGasProjects() ([]GasProject, error) {
	projects := []GasProject{}
	dir := a.dir()
	if dir == "" {
		return projects, nil
	}
	list, err := gasProjects(dir)
	if err != nil {
		return projects, err
	}
	c := loadConfig()
	for _, p := range list {
		projects = append(projects, gasProjectInfo(p, c))
	}
	return projects, nil
}

func gasProjectInfo(p *claspProject, c config) GasProject {
	project := GasProject{
		Dir:       p.Dir,
		ScriptID:  p.ScriptID,
		RootDir:   p.RootDir,
		EditorURL: "https://script.google.com/d/" + p.ScriptID + "/edit",
	}
	if p.ParentID != "" {
		project.ContainerURL = "https://drive.google.com/open?id=" + url.QueryEscape(p.ParentID)
	}
	if rec, ok := c.GasSync[p.ScriptID]; ok {
		project.LastSync = &rec.GasSync
	}
	return project
}

// gasState は今のブランチとコミット（短いハッシュ）と、GAS のファイルにコミットしていない変更があるかを返す
func (a *App) gasState(p *claspProject) (branch, commit string, dirty bool) {
	if out, err := a.runGit("symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		branch = strings.TrimSpace(out)
	}
	if a.hasHead() {
		if out, err := a.runGit("rev-parse", "--short", "HEAD"); err == nil {
			commit = strings.TrimSpace(out)
		}
	}
	out, err := a.runGit("status", "--porcelain", "--untracked-files=all", "--", ":(literal)"+p.RootDir)
	dirty = err == nil && strings.TrimSpace(out) != ""
	return branch, commit, dirty
}

// saveGasSync は GAS と同期したことを、そのときの GAS 側の中身とともに記録する
func (a *App) saveGasSync(p *claspProject, snap gasSnapshot, action string) error {
	branch, commit, dirty := a.gasState(p)
	rec := gasSyncRecord{
		GasSync: GasSync{Action: action, Branch: branch, Commit: commit, Dirty: dirty, Time: time.Now().Unix()},
		Hash:    snap.hash(),
		Files:   snap,
	}
	return updateConfig(func(c *config) {
		if c.GasSync == nil {
			c.GasSync = map[string]gasSyncRecord{}
		}
		c.GasSync[p.ScriptID] = rec
	})
}

// PrepareGasPush は GAS 側の今の中身を取ってきて、反映すると上書きされる変更がないかを調べる。まだ反映はしない
func (a *App) PrepareGasPush(project string) (GasPushPlan, error) {
	plan := GasPushPlan{Overwritten: []string{}, Lost: []string{}}
	dir, p, err := a.gasProject(project)
	if err != nil {
		return plan, err
	}
	remote, tmp, err := pullSnapshot(p)
	os.RemoveAll(tmp)
	if err != nil {
		return plan, err
	}
	local, err := readSnapshot(filepath.Join(dir, filepath.FromSlash(p.RootDir)), p)
	if err != nil {
		return plan, err
	}
	rec, synced := loadConfig().GasSync[p.ScriptID]
	plan.Expected = remote.hash()
	plan.Unchanged = plan.Expected == local.hash()
	plan.Overwritten, plan.Lost = overwrittenFiles(remote, local, rec, synced)
	// 最初の取り込みは、そのあと作業用ブランチに移るのが普通なので、ブランチが変わったとは扱わない
	if synced && rec.Action != "clone" {
		plan.LastBranch = rec.Branch
	}
	plan.Branch, plan.Commit, plan.Dirty = a.gasState(p)
	return plan, nil
}

// overwrittenFiles は、反映すると上書きされる GAS 側だけの変更と、そのうち手元になくて消えるファイルを返す。
// 前回の同期の記録があれば、そこから GAS 側で変わったファイルだけを数える。手元の変更で置き換わるのは反映の目的なので数えない
func overwrittenFiles(remote, local gasSnapshot, rec gasSyncRecord, synced bool) (overwritten, lost []string) {
	overwritten, lost = []string{}, []string{}
	for file, sum := range remote {
		if local[file] == sum || (synced && rec.Files[file] == sum) {
			continue
		}
		overwritten = append(overwritten, file)
		if _, ok := local[file]; !ok {
			lost = append(lost, file)
		}
	}
	slices.Sort(overwritten)
	slices.Sort(lost)
	return overwritten, lost
}

// PushGas は手元の GAS のファイルで GAS 側を置き換える。
// GAS 側の中身が expected（PrepareGasPush で確かめたもの）から変わっていたら止める
func (a *App) PushGas(project, expected string) error {
	if !sha256Hex.MatchString(expected) {
		return fmt.Errorf("確かめた GAS の中身が正しくありません: %q", expected)
	}
	dir, p, err := a.gasProject(project)
	if err != nil {
		return err
	}
	remote, tmp, err := pullSnapshot(p)
	os.RemoveAll(tmp)
	if err != nil {
		return err
	}
	if remote.hash() != expected {
		return fmt.Errorf("確かめた後に GAS 側の中身が変わったので、反映せずに止めました。もう一度確かめてください")
	}
	// 確認は済ませているので、マニフェスト（appsscript.json）の上書きも聞かずに進める
	if _, err := runClasp(filepath.Dir(claspConfig(dir, p)), "push", "--force", "--project", claspConfig(dir, p)); err != nil {
		return err
	}
	// GAS 側で appsscript.json の書き方が整えられることがあるので、反映後の中身を取ってきて記録する。
	// 取れなければ手元の中身で代わりにする
	after, tmp, err := pullSnapshot(p)
	os.RemoveAll(tmp)
	if err != nil {
		if after, err = readSnapshot(filepath.Join(dir, filepath.FromSlash(p.RootDir)), p); err != nil {
			return err
		}
	}
	return a.saveGasSync(p, after, "push")
}

// PullGas は GAS 側の今の中身を手元に取り込む。手元の GAS のファイルにコミットしていない変更があれば、上書きしないよう断る。
// 取り込んだ差分は git の変更として見えるので、気に入らなければ戻せる
func (a *App) PullGas(project string) error {
	dir, p, err := a.gasProject(project)
	if err != nil {
		return err
	}
	if _, _, dirty := a.gasState(p); dirty {
		return fmt.Errorf("%s にまだコミットしていない変更があるので、取り込むと上書きしてしまいます。先にコミットするか、変更を戻してください", p.RootDir)
	}
	remote, tmp, err := pullSnapshot(p)
	defer os.RemoveAll(tmp)
	if err != nil {
		return err
	}
	root := filepath.Join(dir, filepath.FromSlash(p.RootDir))
	local, err := readSnapshot(root, p)
	if err != nil {
		return err
	}
	for file, sum := range remote {
		if local[file] == sum {
			continue
		}
		if err := copyGasFile(filepath.Join(tmp, filepath.FromSlash(file)), root, file); err != nil {
			return err
		}
	}
	// GAS 側で消されたファイルを消す。前回の同期のときに GAS にあったものだけにし、手元で足しただけのファイルは残す
	rec := loadConfig().GasSync[p.ScriptID]
	for file := range local {
		if _, ok := remote[file]; !ok && rec.Files[file] != "" {
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(file))); err != nil {
				return err
			}
		}
	}
	return a.saveGasSync(p, remote, "pull")
}

// copyGasFile は取ってきた GAS のファイル src を root の下の rel に書く。シンボリックリンクの先には書かない
func copyGasFile(src, root, rel string) error {
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return fmt.Errorf("GAS のファイルのパスが正しくありません: %q", rel)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	dst := filepath.Join(root, filepath.FromSlash(rel))
	if info, err := os.Lstat(dst); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("%s は普通のファイルではないので上書きしません", rel)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// parseScriptID は貼られた Apps Script エディタの URL か、スクリプト ID そのものからスクリプト ID を取り出す
func parseScriptID(input string) (string, error) {
	input = strings.TrimSpace(input)
	if m := scriptURL.FindStringSubmatch(input); m != nil {
		return m[1], nil
	}
	if scriptID.MatchString(input) {
		return input, nil
	}
	if docsURL.MatchString(input) {
		return "", fmt.Errorf("スプレッドシートなどの URL のようです。メニューの「拡張機能」→「Apps Script」で開いたエディタの URL を貼ってください")
	}
	return "", fmt.Errorf("Apps Script エディタの URL ではないようです: %q", input)
}

// CloneGasScript は、貼られた URL の GAS を今のリポジトリの gas/{name}/ に取り込む
func (a *App) CloneGasScript(name, input string) error {
	id, err := parseScriptID(input)
	if err != nil {
		return err
	}
	repo, dir, err := a.newGasProject(name)
	if err != nil {
		return err
	}
	if err := cloneGasScript(dir, id); err != nil {
		os.RemoveAll(dir)
		return err
	}
	return a.finishGasProject(repo, name)
}

// CreateGasScript は、今のリポジトリの gas/{name}/ に GAS を新しく作る。
// kind が sheets などなら、紐づくスプレッドシートなども Google ドライブに作る
func (a *App) CreateGasScript(name, title, kind string) error {
	title, err := checkGasScript(title, kind)
	if err != nil {
		return err
	}
	repo, dir, err := a.newGasProject(name)
	if err != nil {
		return err
	}
	if err := createGasScript(dir, title, kind); err != nil {
		os.RemoveAll(dir)
		return err
	}
	return a.finishGasProject(repo, name)
}

// newGasProject は今のリポジトリに gas/{name}/ を作る。もうあれば、中身を上書きしないよう断る
func (a *App) newGasProject(name string) (repo, dir string, err error) {
	if !gasProjectName.MatchString(name) {
		return "", "", fmt.Errorf("名前は英数字と . _ - で付けてください（例: sales-report）: %q", name)
	}
	if a.dir() == "" {
		return "", "", errNoRepository
	}
	// clasp はパスの途中にシンボリックリンクがあると断る
	if repo, err = filepath.EvalSymlinks(a.dir()); err != nil {
		return "", "", err
	}
	dir = filepath.Join(repo, gasProjectsDir, name)
	if fileExists(dir) {
		return "", "", fmt.Errorf("%s/%s はもうあります。別の名前にしてください", gasProjectsDir, name)
	}
	// clasp が中身を書く前に作っておく
	if err := os.MkdirAll(filepath.Join(dir, gasRootDir), 0o755); err != nil {
		return "", "", err
	}
	return repo, dir, nil
}

// checkGasScript は新しく作る GAS のタイトルと種類を確かめ、前後の空白を除いたタイトルを返す
func checkGasScript(title, kind string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" || strings.IndexFunc(title, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("タイトルが正しくありません: %q", title)
	}
	if !slices.Contains(gasScriptTypes, kind) {
		return "", fmt.Errorf("GAS の種類が正しくありません: %q", kind)
	}
	return title, nil
}

// cloneGasScript は dir に GAS の中身を取り込む。
// clasp clone-script は上のフォルダーの .clasp.json を見つけると断るので、自分で .clasp.json を書いて pull する
func cloneGasScript(dir, id string) error {
	config := filepath.Join(dir, ".clasp.json")
	if err := writeClaspConfig(config, map[string]any{"scriptId": id, "rootDir": gasRootDir}); err != nil {
		return err
	}
	_, err := runClasp(dir, "pull", "--project", config)
	return err
}

// createGasScript は dir に GAS を新しく作る。
// --project にフォルダーを渡し、上のフォルダーの .clasp.json を見つけないようにする。
// 値が - で始まってもオプションと読まれないよう = でつなぐ
func createGasScript(dir, title, kind string) error {
	_, err := runClasp(dir, "create-script", "--title="+title, "--type="+kind, "--rootDir="+gasRootDir, "--project", dir)
	return err
}

// finishGasProject は今のリポジトリに足した gas/{name}/ に CLAUDE.md と .gitignore を置き、
// 取り込んだばかりの手元の中身を GAS 側と同じものとして記録する。リポジトリ直下の CLAUDE.md にも、GAS の置き場所を書き足す
func (a *App) finishGasProject(repo, name string) error {
	dir := gasProjectsDir + "/" + name
	p, err := readClaspProject(repo, dir)
	if err == nil && p == nil {
		err = fmt.Errorf("clasp が .clasp.json を作りませんでした")
	}
	if err != nil {
		return err
	}
	for file, content := range map[string]string{"CLAUDE.md": gasClaudeMd(dir), ".gitignore": gasGitignore} {
		if err := writeNewFile(filepath.Join(repo, filepath.FromSlash(dir), file), content); err != nil {
			return err
		}
	}
	if err := appendGasProjectsNote(repo); err != nil {
		return err
	}
	snap, err := readSnapshot(filepath.Join(repo, filepath.FromSlash(p.RootDir)), p)
	if err != nil {
		return err
	}
	return a.saveGasSync(p, snap, "clone")
}

// appendGasProjectsNote はリポジトリ直下の CLAUDE.md に、gas/ の下に GAS を置いていることを書き足す。
// Claude Code は gas/{project}/CLAUDE.md をそのフォルダーを触るまで読まないので、置き場所だけは直下にも書く
func appendGasProjectsNote(repo string) error {
	file := filepath.Join(repo, "CLAUDE.md")
	data, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.Contains(string(data), gasProjectsNote) {
		return nil
	}
	text := gasProjectsNote
	if len(data) > 0 {
		text = "\n" + text
		if !strings.HasSuffix(string(data), "\n") {
			text = "\n" + text
		}
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// リポジトリ直下の CLAUDE.md に書き足す、GAS の置き場所の説明。もう書いてあるかもこの文面で見分ける
const gasProjectsNote = `## Apps Script（GAS）

- ` + "`" + gasProjectsDir + "/`" + ` の下のフォルダーが、それぞれ 1 つの GAS のプロジェクト。GAS のファイルは ` + "`" + gasProjectsDir + "/{project}/" + gasRootDir + "/`" + ` に置く
- GAS のコードを読み書きする前に、そのフォルダーの CLAUDE.md を読む
`

// gas/{project}/ に置く CLAUDE.md。チャットの Claude が GAS の前提で書けるようにする。dir はそのプロジェクトの場所
func gasClaudeMd(dir string) string {
	return `# Apps Script プロジェクト

このフォルダー（` + dir + `）は Google Apps Script（GAS）のプロジェクトで、clasp で GAS と同期している。

- GAS のファイルは ` + "`" + gasRootDir + "/`" + ` に置く。ここにある .js / .gs / .html と appsscript.json だけが GAS に送られる
- V8 ランタイムの JavaScript で書く。npm のパッケージや import / export は使えない。別のファイルの関数もそのまま呼べる
- ` + "`../`" + ` のほかのフォルダーは別の GAS のプロジェクト。そこの関数は呼べない
- Gmail やドライブなど新しいサービスを使うときは、appsscript.json の oauthScopes も見直す
- 1 回の実行は 6 分までなど GAS には制限がある。スプレッドシートの読み書きは getValues / setValues でまとめて行う
- ` + "`.clasp.json`" + ` のスクリプト ID は書き換えない
- ` + "`clasp push`" + ` や ` + "`clasp pull`" + `、デプロイなど GAS 側を変える操作は実行しない。GAS への反映は Hidume の画面から人が行う
`
}

// gas/{project}/ に置く .gitignore。.clasp.json はスクリプト ID などだけで秘密ではなく、
// ほかの人やワークツリーでもそのまま反映できるようコミットする
const gasGitignore = `# clasp の認証情報
.clasprc.json
creds.json
client_secret*.json
`
