package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx context.Context
	// git コマンドを実行するリポジトリのルート。画面からの呼び出しと変更の監視が同時に触るので mu で守る
	mu      sync.RWMutex
	repoDir string
	// 作業用ブランチの頭に付ける {user_id}。一度調べたら覚えておく
	branchPrefix string
	// ディレクトリごとの Claude Code との会話
	chats chatStore
}

// StatusEntry は git status の 1 ファイル分
type StatusEntry struct {
	Index    string `json:"index"`
	WorkTree string `json:"workTree"`
	Path     string `json:"path"`
	OrigPath string `json:"origPath"`
}

// Commit はコミットツリーに描く 1 コミット分
type Commit struct {
	Hash    string   `json:"hash"`
	Parents []string `json:"parents"`
	Author  string   `json:"author"`
	Time    int64    `json:"time"`
	Refs    []string `json:"refs"`
	Subject string   `json:"subject"`
	// 今のブランチにあって、どのリモートにもまだないコミットか
	Unpushed bool `json:"unpushed"`
}

func (a *App) dir() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.repoDir
}

func (a *App) setDir(dir string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.repoDir = dir
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.openInitialRepository()
	go a.watch(ctx)
}

// openInitialRepository は、前回開いたリポジトリがまだあればそれを開く。
// 選択をクリアしたときや、まだ選んでいないときは何も開かない
func (a *App) openInitialRepository() {
	if saved := loadConfig().Repository; saved != "" {
		if root, err := repoRoot(saved); err == nil {
			a.setDir(root)
		}
	}
}

// switchRepository は repoDir を切り替えて、次回の起動用に保存する。
// 保存に失敗しても切り替え自体は済んでいるので、エラーにはしない
func (a *App) switchRepository(root string) string {
	a.setDir(root)
	_ = updateConfig(func(c *config) { c.Repository = root })
	return root
}

// GetRepository は現在のリポジトリのパスを返す。何も開いていなければ空
func (a *App) GetRepository() string {
	return a.dir()
}

// ClearRepository はリポジトリの選択を外し、次の起動でも開かないようにする
func (a *App) ClearRepository() error {
	a.setDir("")
	return updateConfig(func(c *config) { c.Repository = "" })
}

// RepoSelection はディレクトリ選択の結果
type RepoSelection struct {
	// 現在のリポジトリのパス。切り替えたときは新しいパス
	Repo string `json:"repo"`
	// リポジトリでないディレクトリが選ばれたときの、そのパス。git init するかはフロント側で聞く
	NeedsInit string `json:"needsInit"`
}

// SelectRepository はダイアログでディレクトリを選ばせ、リポジトリならそこに切り替える
func (a *App) SelectRepository() (RepoSelection, error) {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "リポジトリを選択",
		DefaultDirectory: a.dir(),
		// 新しく作ったフォルダーを選べば、そのまま git init するか聞く流れに進む
		CanCreateDirectories: true,
	})
	if err != nil || dir == "" {
		return RepoSelection{Repo: a.dir()}, err
	}
	root, err := repoRoot(dir)
	if err != nil {
		return RepoSelection{Repo: a.dir(), NeedsInit: dir}, nil
	}
	return RepoSelection{Repo: a.switchRepository(root)}, nil
}

// InitRepository は dir で git init して、そのリポジトリに切り替える
func (a *App) InitRepository(dir string) (string, error) {
	if _, err := runGitIn(dir, "init"); err != nil {
		return a.dir(), err
	}
	root, err := repoRoot(dir)
	if err != nil {
		return a.dir(), err
	}
	return a.switchRepository(root), nil
}

// GetStatus は現在のリポジトリの git status を返す
func (a *App) GetStatus() ([]StatusEntry, error) {
	out, err := a.runGit("status", "--porcelain=v1", "-z")
	if err != nil {
		return nil, err
	}
	entries := []StatusEntry{}
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		e := StatusEntry{Index: f[0:1], WorkTree: f[1:2], Path: f[3:]}
		// リネームとコピーは直後のフィールドに元のパスが入る
		if (e.Index == "R" || e.Index == "C") && i+1 < len(fields) {
			i++
			e.OrigPath = fields[i]
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// GetCommits は今のブランチ（ヘッドブランチ）とベースブランチのコミットを、トポロジカル順に最大 limit 件返す。
// ほかのブランチやタグは出さない
func (a *App) GetCommits(limit int) ([]Commit, error) {
	// git init 直後などコミットがまだないときは空にする
	if !a.hasHead() {
		return []Commit{}, nil
	}
	revs, shown := a.treeRefs()
	out, err := a.runGit(append([]string{"log", "--topo-order", "--decorate=short",
		fmt.Sprintf("--max-count=%d", limit),
		"--format=%H%x1f%P%x1f%an%x1f%at%x1f%D%x1f%s%x1e", "--end-of-options"}, revs...)...)
	if err != nil {
		return nil, err
	}
	unpushed := a.unpushedCommits(limit)
	commits := []Commit{}
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue
		}
		f := strings.SplitN(rec, "\x1f", 6)
		if len(f) < 6 {
			continue
		}
		t, _ := strconv.ParseInt(f[3], 10, 64)
		c := Commit{
			Hash:     f[0],
			Parents:  strings.Fields(f[1]),
			Author:   f[2],
			Time:     t,
			Refs:     []string{},
			Subject:  f[5],
			Unpushed: unpushed[f[0]],
		}
		for _, ref := range strings.Split(f[4], ", ") {
			if shown[ref] || strings.HasPrefix(ref, "HEAD -> ") {
				c.Refs = append(c.Refs, ref)
			}
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// treeRefs はコミットツリーに出すコミットの起点と、ラベルとして出すブランチ名（--decorate=short の形）を返す。
// 今のブランチとその追跡先、ベースブランチの手元と origin だけにする
func (a *App) treeRefs() (revs []string, shown map[string]bool) {
	revs = []string{"HEAD"}
	shown = map[string]bool{"HEAD": true}
	if out, err := a.runGit("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err == nil {
		upstream := strings.TrimSpace(out)
		revs = append(revs, "@{upstream}")
		shown[upstream] = true
	}
	if base := a.defaultBranch(); base != "" {
		for _, ref := range []string{"refs/heads/" + base, "refs/remotes/origin/" + base} {
			if a.refExists(ref) {
				revs = append(revs, ref)
				shown[strings.TrimPrefix(strings.TrimPrefix(ref, "refs/heads/"), "refs/remotes/")] = true
			}
		}
	}
	return revs, shown
}

// Stage は指定したファイルをステージする
func (a *App) Stage(paths []string) error {
	_, err := a.runGit(append([]string{"add", "--"}, paths...)...)
	return err
}

// Unstage は指定したファイルのステージを取り消す
func (a *App) Unstage(paths []string) error {
	// コミットがまだないと restore --staged が使えないので、インデックスから外す
	if !a.hasHead() {
		_, err := a.runGit(append([]string{"rm", "--cached", "-r", "-q", "--"}, paths...)...)
		return err
	}
	_, err := a.runGit(append([]string{"restore", "--staged", "--"}, paths...)...)
	return err
}

// StageAll は未追跡を含む全ファイルの変更をステージする
func (a *App) StageAll() error {
	_, err := a.runGit("add", "--all")
	return err
}

// Commit はコミット対象をコミットする
func (a *App) Commit(message string) error {
	// デフォルトブランチでは作業しない
	if a.onDefaultBranch() {
		return fmt.Errorf("デフォルトブランチにはコミットしません。作業用のブランチを作ってください")
	}
	_, err := a.runGit("commit", "-m", message)
	return err
}

// CommitAll は未追跡を含む全ファイルの変更をコミットする
func (a *App) CommitAll(message string) error {
	if a.onDefaultBranch() {
		return fmt.Errorf("デフォルトブランチにはコミットしません。作業用のブランチを作ってください")
	}
	if err := a.StageAll(); err != nil {
		return err
	}
	return a.Commit(message)
}

var commitHash = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// RevertTo は、hash の時点のファイル構成をそのまま中身にしたコミットを HEAD の上に積み、
// hash より後の変更を打ち消す。履歴は書き換えないので force push はいらない
func (a *App) RevertTo(hash string) error {
	// 引数がオプションとして解釈されないよう、コミット ID の形だけ受け付ける
	if !commitHash.MatchString(hash) {
		return fmt.Errorf("コミットの指定が正しくありません: %q", hash)
	}
	head, err := a.revParse("HEAD")
	if err != nil {
		return err
	}
	if _, err := a.runGit("merge-base", "--is-ancestor", hash, head); err != nil || strings.HasPrefix(head, hash) {
		return fmt.Errorf("HEAD より前のコミットしか選べません")
	}
	tree, err := a.revParse(hash + "^{tree}")
	if err != nil {
		return err
	}
	headTree, err := a.revParse("HEAD^{tree}")
	if err != nil {
		return err
	}
	if tree == headTree {
		return fmt.Errorf("もう %s と同じ状態です", hash[:7])
	}
	subject, err := a.runGit("log", "-1", "--no-show-signature", "--format=%s", hash)
	if err != nil {
		return err
	}
	message := fmt.Sprintf("Revert to %s \"%s\"\n\n%s より後の変更を打ち消す。", hash[:7], strings.TrimSpace(subject), hash[:7])
	commit, err := a.runGit("commit-tree", tree, "-p", head, "-m", message)
	if err != nil {
		return err
	}
	// 作業ツリーの変更や未追跡ファイルを上書きしそうなときは git が止める
	_, err = a.runGit("merge", "--ff-only", "--no-edit", strings.TrimSpace(commit))
	return err
}

func (a *App) revParse(rev string) (string, error) {
	out, err := a.runGit("rev-parse", "--verify", "--end-of-options", rev)
	return strings.TrimSpace(out), err
}

func (a *App) hasHead() bool {
	_, err := a.runGit("rev-parse", "--verify", "-q", "HEAD")
	return err == nil
}

// unpushedCommits は今のブランチのコミットのうち、どのリモート追跡ブランチからも辿れないもの
func (a *App) unpushedCommits(limit int) map[string]bool {
	set := map[string]bool{}
	if !a.hasHead() {
		return set
	}
	out, err := a.runGit("rev-list", fmt.Sprintf("--max-count=%d", limit), "HEAD", "--not", "--remotes")
	if err != nil {
		return set
	}
	for _, hash := range strings.Fields(out) {
		set[hash] = true
	}
	return set
}

func repoRoot(dir string) (string, error) {
	out, err := runGitIn(dir, "rev-parse", "--show-toplevel")
	return strings.TrimSpace(out), err
}

func (a *App) runGit(args ...string) (string, error) {
	return runGitIn(a.dir(), args...)
}

// errNoRepository はリポジトリを開いていないときのエラー
var errNoRepository = fmt.Errorf("リポジトリが選ばれていません")

func runGitIn(dir string, args ...string) (string, error) {
	// 空だとアプリを起動したディレクトリで動いてしまう
	if dir == "" {
		return "", errNoRepository
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// 読み取りのついでに .git/index を書き換えない。変更の監視が自分の書き込みに反応しないようにする
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
