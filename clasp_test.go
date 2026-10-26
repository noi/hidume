package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseScriptID(t *testing.T) {
	id := "1AbCdEfGhIjKlMnOpQrStUvWxYz_0123456789-abcdefghijklmnopqr"
	for _, input := range []string{
		id,
		"  " + id + "\n",
		"https://script.google.com/home/projects/" + id + "/edit",
		"https://script.google.com/d/" + id + "/edit?usp=sharing",
		"https://script.google.com/u/1/home/projects/" + id,
	} {
		got, err := parseScriptID(input)
		if err != nil || got != id {
			t.Errorf("parseScriptID(%q) = %q, %v", input, got, err)
		}
	}
	for _, input := range []string{"", "abc", "https://docs.google.com/spreadsheets/d/" + id + "/edit", "https://example.com/d/" + id} {
		if got, err := parseScriptID(input); err == nil {
			t.Errorf("parseScriptID(%q) = %q, want error", input, got)
		}
	}
	if _, err := parseScriptID("https://docs.google.com/spreadsheets/d/" + id + "/edit"); !strings.Contains(err.Error(), "拡張機能") {
		t.Errorf("スプレッドシートの URL には、エディタの開き方を案内してほしい: %v", err)
	}
}

func TestReadClaspProject(t *testing.T) {
	dir := t.TempDir()
	if p, err := readClaspProject(dir, "."); p != nil || err != nil {
		t.Fatalf("no .clasp.json: %v, %v", p, err)
	}
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, ".clasp.json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"scriptId":"abcdefghijklmnopqrstuvwxyz","rootDir":"src/","parentId":["parent1"],"scriptExtensions":"gs"}`)
	p, err := readClaspProject(dir, ".")
	if err != nil {
		t.Fatal(err)
	}
	if p.RootDir != "src" || p.ParentID != "parent1" || !slices.Equal(p.ScriptExtensions, []string{".gs"}) ||
		!slices.Equal(p.HTMLExtensions, []string{".html"}) {
		t.Errorf("unexpected project: %+v", p)
	}

	write(`{"scriptId":"abcdefghijklmnopqrstuvwxyz"}`)
	if p, err := readClaspProject(dir, "."); err != nil || p.RootDir != "." || !slices.Equal(p.ScriptExtensions, []string{".js", ".gs"}) {
		t.Errorf("defaults: %+v, %v", p, err)
	}
	for _, bad := range []string{`{"scriptId":"short"}`, `{"scriptId":"abcdefghijklmnopqrstuvwxyz","rootDir":"../x"}`, `{`} {
		write(bad)
		if _, err := readClaspProject(dir, "."); err == nil {
			t.Errorf("%s should fail", bad)
		}
	}
}

// gas/{project}/ の rootDir は .clasp.json の場所から数え、リポジトリからのパスにして持つ
func TestGasProjects(t *testing.T) {
	repo := t.TempDir()
	if projects, err := gasProjects(repo); err != nil || len(projects) != 0 {
		t.Fatalf("no projects: %v, %v", projects, err)
	}
	writeFiles(t, repo, map[string]string{
		".clasp.json":              `{"scriptId":"abcdefghijklmnopqrstuvwxyz","rootDir":"src"}`,
		"gas/b/.clasp.json":        `{"scriptId":"bbbbbbbbbbbbbbbbbbbbbbbbbb","rootDir":"src"}`,
		"gas/a/.clasp.json":        `{"scriptId":"aaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		"gas/not-a-project/README": "",
	})
	projects, err := gasProjects(repo)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range projects {
		got = append(got, p.Dir+":"+p.RootDir)
	}
	if want := []string{".:src", "gas/a:gas/a", "gas/b:gas/b/src"}; !slices.Equal(got, want) {
		t.Errorf("projects = %v; want %v", got, want)
	}

	// プロジェクトのフォルダーの外は指せない
	writeFiles(t, repo, map[string]string{"gas/b/.clasp.json": `{"scriptId":"bbbbbbbbbbbbbbbbbbbbbbbbbb","rootDir":"../a"}`})
	if _, err := gasProjects(repo); err == nil {
		t.Error("rootDir outside the project should fail")
	}

	// 決まった場所のほかは開かない
	a := &App{}
	a.setDir(repo)
	for _, bad := range []string{"gas", "gas/../x", "other/a", "gas/a/b", "/tmp"} {
		if _, _, err := a.gasProject(bad); err == nil {
			t.Errorf("gasProject(%q) should fail", bad)
		}
	}
}

func TestReadSnapshot(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"Code.js":               "function a() {}",
		"lib/util.gs":           "function b() {}",
		"index.html":            "<p>hi</p>",
		"appsscript.json":       "{}",
		"README.md":             "skip",
		"data.json":             "skip",
		"node_modules/x/y.js":   "skip",
		".hidden/z.js":          "skip",
		"lib/.secret.js":        "skip",
		"notes/appsscript.json": "{}",
	}
	for name, content := range files {
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "Code.js"), filepath.Join(root, "link.js")); err != nil {
		t.Fatal(err)
	}
	p := &claspProject{ScriptExtensions: []string{".js", ".gs"}, HTMLExtensions: []string{".html"}}
	snap, err := readSnapshot(root, p)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for name := range snap {
		got = append(got, name)
	}
	slices.Sort(got)
	want := []string{"Code.js", "appsscript.json", "index.html", "lib/util.gs", "notes/appsscript.json"}
	if !slices.Equal(got, want) {
		t.Errorf("files = %v, want %v", got, want)
	}
	if missing, err := readSnapshot(filepath.Join(root, "nope"), p); err != nil || len(missing) != 0 {
		t.Errorf("missing root: %v, %v", missing, err)
	}
}

func TestOverwrittenFiles(t *testing.T) {
	remote := gasSnapshot{"a.js": "1", "b.js": "2-browser", "c.js": "3", "d.js": "4-browser"}
	local := gasSnapshot{"a.js": "1", "b.js": "2-local", "c.js": "3-local"}
	rec := gasSyncRecord{Files: map[string]string{"a.js": "1", "b.js": "2", "c.js": "3"}}

	// 記録があれば、前回から GAS 側で変わったもの（b, 新しく足された d）だけ
	over, lost := overwrittenFiles(remote, local, rec, true)
	if !slices.Equal(over, []string{"b.js", "d.js"}) || !slices.Equal(lost, []string{"d.js"}) {
		t.Errorf("synced: %v %v", over, lost)
	}
	// 記録がなければ、手元と違うものすべて
	over, lost = overwrittenFiles(remote, local, gasSyncRecord{}, false)
	if !slices.Equal(over, []string{"b.js", "c.js", "d.js"}) || !slices.Equal(lost, []string{"d.js"}) {
		t.Errorf("unsynced: %v %v", over, lost)
	}
}

func TestClaspError(t *testing.T) {
	err := claspError("pull", "User has not enabled the Apps Script API. Enable it by visiting https://script.google.com/home/usersettings then retry.")
	if !strings.Contains(err.Error(), "Apps Script API が有効になっていません") || !strings.Contains(err.Error(), "usersettings") {
		t.Errorf("API error: %v", err)
	}
	if err := claspError("pull", "something odd"); err.Error() != "clasp pull: something odd" {
		t.Errorf("unknown error: %v", err)
	}
}

// fakeClasp は GAS の代わりに remote ディレクトリを使う clasp を PATH に置く。
// pull は remote から --project の場所の rootDir へ、push はその逆へコピーする。
// remote の下にスクリプト ID と同じ名前のディレクトリがあれば、そのスクリプトの GAS はそこを使う
func fakeClasp(t *testing.T) (remote string) {
	t.Helper()
	bin := t.TempDir()
	remote = t.TempDir()
	script := `#!/bin/sh
cmd="$1"; shift
project=""
while [ $# -gt 0 ]; do
  case "$1" in
    --project) project="$2"; shift ;;
  esac
  shift
done
dir=$(dirname "$project")
# 本物の clasp 3 と同じく、パスの途中にシンボリックリンクがあれば断る
if [ "$(cd "$dir" && pwd -P)" != "$dir" ]; then
  echo "Security Error: Content directory is a symlink. Possible race attack." >&2
  exit 1
fi
root=$(sed -n 's/.*"rootDir": *"\([^"]*\)".*/\1/p' "$project")
[ -z "$root" ] && root=.
id=$(sed -n 's/.*"scriptId": *"\([^"]*\)".*/\1/p' "$project")
remote="$FAKE_GAS_REMOTE"
[ -d "$remote/$id" ] && remote="$remote/$id"
if [ -f "$remote/.fail" ]; then
  echo "fake failure" >&2
  exit 1
fi
echo "$cmd" >> "$FAKE_GAS_LOG"
case "$cmd" in
  pull) mkdir -p "$dir/$root" && cp -R "$remote/." "$dir/$root/" ;;
  push) rm -rf "$remote"/* && cp -R "$dir/$root/." "$remote/" ;;
  *) echo "unknown $cmd" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "clasp"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_GAS_REMOTE", remote)
	t.Setenv("FAKE_GAS_LOG", filepath.Join(bin, "log"))
	// 設定ファイルを本物の場所に書かない
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	return remote
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		file := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readFile(t *testing.T, file string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestGasPushAndPull(t *testing.T) {
	remote := fakeClasp(t)
	writeFiles(t, remote, map[string]string{"Code.js": "v1", "appsscript.json": "{}"})

	repo := t.TempDir()
	if _, err := runGitIn(repo, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, repo, map[string]string{".clasp.json": `{"scriptId": "abcdefghijklmnopqrstuvwxyz", "rootDir": "src"}`})
	a := &App{}
	a.setDir(repo)

	// 最初の取り込み
	if err := a.PullGas(".", ""); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(repo, "src", "Code.js")); got != "v1" {
		t.Fatalf("pulled Code.js = %q", got)
	}
	projects, err := a.GetGasProjects()
	if err != nil || len(projects) != 1 {
		t.Fatalf("projects after pull: %+v, %v", projects, err)
	}
	if last := projects[0].Targets[0].LastSync; last == nil || last.Action != "pull" || !last.Dirty {
		t.Fatalf("project after pull: %+v", projects[0])
	}
	if _, err := runGitIn(repo, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGitIn(repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-qm", "init"); err != nil {
		t.Fatal(err)
	}

	// 手元だけ変えたなら、上書きされるものはない
	writeFiles(t, repo, map[string]string{"src/Code.js": "v2"})
	plan, err := a.PrepareGasPush(".")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Targets) != 1 || len(plan.Targets[0].Overwritten) != 0 || plan.Targets[0].Unchanged || !plan.Dirty || plan.Commit == "" {
		t.Fatalf("plan: %+v", plan)
	}
	if err := a.PushGas(".", expectAll(plan)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(remote, "Code.js")); got != "v2" {
		t.Fatalf("remote Code.js = %q", got)
	}

	// ブラウザで変更されたら、上書きされるファイルとして出す
	writeFiles(t, remote, map[string]string{"Code.js": "browser", "New.js": "added"})
	plan, err = a.PrepareGasPush(".")
	if err != nil {
		t.Fatal(err)
	}
	if target := plan.Targets[0]; !slices.Equal(target.Overwritten, []string{"Code.js", "New.js"}) || !slices.Equal(target.Lost, []string{"New.js"}) {
		t.Fatalf("plan after browser edit: %+v", plan)
	}

	// 確かめた後に GAS 側が変わったら止める
	writeFiles(t, remote, map[string]string{"Code.js": "browser again"})
	if err := a.PushGas(".", expectAll(plan)); err == nil || !strings.Contains(err.Error(), "止めました") {
		t.Fatalf("stale push: %v", err)
	}
	if got := readFile(t, filepath.Join(remote, "Code.js")); got != "browser again" {
		t.Fatalf("stale push must not overwrite: %q", got)
	}

	// コミットしていない変更があれば取り込まない
	if err := a.PullGas(".", ""); err == nil || !strings.Contains(err.Error(), "コミットしていない") {
		t.Fatalf("pull with dirty tree: %v", err)
	}
	if _, err := runGitIn(repo, "checkout", "--", "src"); err != nil {
		t.Fatal(err)
	}

	// GAS で消されたファイルは消し、手元で足しただけのファイルは残す
	writeFiles(t, repo, map[string]string{"src/LocalOnly.js": "mine"})
	if _, err := runGitIn(repo, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGitIn(repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-qm", "local"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(remote, "appsscript.json")); err != nil {
		t.Fatal(err)
	}
	if err := a.PullGas(".", ""); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(repo, "src", "Code.js")); got != "browser again" {
		t.Errorf("Code.js = %q", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "src", "appsscript.json")); !os.IsNotExist(err) {
		t.Errorf("appsscript.json removed on GAS should be removed locally: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "src", "LocalOnly.js")); err != nil {
		t.Errorf("local-only file should stay: %v", err)
	}

	// 取り込んだ直後は、GAS 側にだけある変更はない（手元だけの LocalOnly.js があるので同じではない）
	plan, err = a.PrepareGasPush(".")
	if err != nil || plan.Targets[0].Unchanged || len(plan.Targets[0].Overwritten) != 0 {
		t.Errorf("plan after pull: %+v, %v", plan, err)
	}
	if err := os.Remove(filepath.Join(repo, "src", "LocalOnly.js")); err != nil {
		t.Fatal(err)
	}
	if plan, err = a.PrepareGasPush("."); err != nil || !plan.Targets[0].Unchanged {
		t.Errorf("plan when same as GAS: %+v, %v", plan, err)
	}
}

// 今のリポジトリの gas/{name}/ に取り込み、そのプロジェクトだけを反映・取り込みする
func TestCloneGasScript(t *testing.T) {
	remote := fakeClasp(t)
	writeFiles(t, remote, map[string]string{"Code.js": "v1", "appsscript.json": "{}"})

	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runGitIn(repo, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, repo, map[string]string{"CLAUDE.md": "# mine", "gas/taken/x": ""})
	a := &App{}
	a.setDir(repo)

	for _, name := range []string{"", "-x", "日本語", "a/b", "taken"} {
		if err := a.CloneGasScript(name, "abcdefghijklmnopqrstuvwxyz"); err == nil {
			t.Errorf("name %q should fail", name)
		}
	}
	if err := a.CloneGasScript("report", "abcdefghijklmnopqrstuvwxyz"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(repo, "gas", "report", "src", "Code.js")); got != "v1" {
		t.Errorf("cloned Code.js = %q", got)
	}
	if got := readFile(t, filepath.Join(repo, "gas", "report", "CLAUDE.md")); !strings.Contains(got, "gas/report") {
		t.Errorf("project CLAUDE.md:\n%s", got)
	}
	// 直下の CLAUDE.md は残したまま書き足し、2 回目は足さない
	if err := appendGasProjectsNote(repo); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(repo, "CLAUDE.md")); !strings.HasPrefix(got, "# mine\n\n## Apps Script") ||
		strings.Count(got, gasProjectsNote) != 1 {
		t.Errorf("root CLAUDE.md:\n%s", got)
	}

	projects, err := a.GetGasProjects()
	if err != nil || len(projects) != 1 || projects[0].Dir != "gas/report" || projects[0].RootDir != "gas/report/src" ||
		len(projects[0].Targets) != 1 || projects[0].Targets[0].LastSync == nil || projects[0].Targets[0].LastSync.Action != "clone" {
		t.Fatalf("projects: %+v, %v", projects, err)
	}

	writeFiles(t, repo, map[string]string{"gas/report/src/Code.js": "v2"})
	plan, err := a.PrepareGasPush("gas/report")
	if err != nil || plan.Targets[0].Unchanged || !plan.Dirty {
		t.Fatalf("plan: %+v, %v", plan, err)
	}
	if err := a.PushGas("gas/report", expectAll(plan)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(remote, "Code.js")); got != "v2" {
		t.Errorf("remote Code.js = %q", got)
	}
	// 直下に .clasp.json はないので、直下のプロジェクトとしては扱えない
	if err := a.PullGas(".", ""); err == nil {
		t.Error("pull without root .clasp.json should fail")
	}
}

// expectAll は確かめたすべての反映先に反映するときの、PushGas に渡す中身
func expectAll(plan GasPushPlan) map[string]string {
	expected := map[string]string{}
	for _, t := range plan.Targets {
		expected[t.Name] = t.Expected
	}
	return expected
}

// .clasp.{name}.json を足すと、同じファイルをどの反映先にも反映し、取り込みは反映先を選んで行う
func TestGasTargets(t *testing.T) {
	remote := fakeClasp(t)
	const (
		mainID  = "mainmainmainmainmainmainmain"
		tokyoID = "tokyotokyotokyotokyotokyo"
		osakaID = "osakaosakaosakaosakaosaka"
	)
	writeFiles(t, remote, map[string]string{
		mainID + "/Code.js":          "v1",
		mainID + "/appsscript.json":  "{}",
		tokyoID + "/Code.js":         "function myFunction() {}",
		tokyoID + "/appsscript.json": "{}",
		osakaID + "/Code.js":         "function myFunction() {}",
	})
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runGitIn(repo, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	a := &App{}
	a.setDir(repo)
	if err := a.CloneGasScript("report", mainID); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, repo, map[string]string{"gas/report/.clasp.json": `{"scriptId":"` + mainID + `","rootDir":"src","parentId":["sheet1"]}`})

	for _, bad := range [][2]string{{"", tokyoID}, {"a.b", tokyoID}, {"tokyo", "abc"}, {"dup", mainID}} {
		if err := a.AddGasTarget("gas/report", bad[0], bad[1]); err == nil {
			t.Errorf("AddGasTarget(%q, %q) should fail", bad[0], bad[1])
		}
	}
	// 開けない GAS は足さない
	writeFiles(t, remote, map[string]string{osakaID + "/.fail": ""})
	if err := a.AddGasTarget("gas/report", "osaka", osakaID); err == nil || fileExists(filepath.Join(repo, "gas/report/.clasp.osaka.json")) {
		t.Errorf("unreachable target: %v", err)
	}
	if err := os.Remove(filepath.Join(remote, osakaID, ".fail")); err != nil {
		t.Fatal(err)
	}
	if err := a.AddGasTarget("gas/report", "tokyo", "https://script.google.com/d/"+tokyoID+"/edit"); err != nil {
		t.Fatal(err)
	}
	if err := a.AddGasTarget("gas/report", "osaka", osakaID); err != nil {
		t.Fatal(err)
	}
	if err := a.AddGasTarget("gas/report", "tokyo", osakaID); err == nil {
		t.Error("same name should fail")
	}
	// 紐づく先は写さない
	if got := readFile(t, filepath.Join(repo, "gas/report/.clasp.tokyo.json")); strings.Contains(got, "sheet1") || !strings.Contains(got, tokyoID) {
		t.Errorf(".clasp.tokyo.json:\n%s", got)
	}

	projects, err := a.GetGasProjects()
	if err != nil || len(projects) != 1 {
		t.Fatalf("projects: %+v, %v", projects, err)
	}
	var names []string
	for _, target := range projects[0].Targets {
		names = append(names, target.Name+":"+target.ScriptID)
	}
	if want := []string{":" + mainID, "osaka:" + osakaID, "tokyo:" + tokyoID}; !slices.Equal(names, want) {
		t.Errorf("targets = %v; want %v", names, want)
	}
	if projects[0].Targets[0].ContainerURL == "" || projects[0].Targets[2].ContainerURL != "" || projects[0].Targets[2].LastSync != nil {
		t.Errorf("targets: %+v", projects[0].Targets)
	}

	// 足したばかりの反映先は記録がないので、GAS 側にあるファイルは上書きされるものとして出す
	writeFiles(t, repo, map[string]string{"gas/report/src/Code.js": "v2"})
	plan, err := a.PrepareGasPush("gas/report")
	if err != nil || len(plan.Targets) != 3 {
		t.Fatalf("plan: %+v, %v", plan, err)
	}
	if first := plan.Targets[0]; first.Unchanged || len(first.Overwritten) != 0 {
		t.Errorf("main plan: %+v", first)
	}
	if osaka := plan.Targets[1]; osaka.Name != "osaka" || !slices.Equal(osaka.Overwritten, []string{"Code.js"}) || len(osaka.Lost) != 0 {
		t.Errorf("osaka plan: %+v", osaka)
	}

	// 1 つでも確かめた後に変わっていたら、どこにも反映しない
	writeFiles(t, remote, map[string]string{tokyoID + "/Code.js": "browser"})
	if err := a.PushGas("gas/report", expectAll(plan)); err == nil || !strings.Contains(err.Error(), "tokyo") {
		t.Fatalf("stale push: %v", err)
	}
	if got := readFile(t, filepath.Join(remote, mainID, "Code.js")); got != "v1" {
		t.Errorf("stale push must not push anywhere: %q", got)
	}

	// 選んだ反映先にだけ反映する
	plan, err = a.PrepareGasPush("gas/report")
	if err != nil {
		t.Fatal(err)
	}
	expected := expectAll(plan)
	delete(expected, "tokyo")
	if err := a.PushGas("gas/report", expected); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{mainID: "v2", osakaID: "v2", tokyoID: "browser"} {
		if got := readFile(t, filepath.Join(remote, id, "Code.js")); got != want {
			t.Errorf("%s Code.js = %q; want %q", id, got, want)
		}
	}
	if err := a.PushGas("gas/report", map[string]string{"nagoya": plan.Targets[0].Expected}); err == nil {
		t.Error("unknown target should fail")
	}

	// 取り込みは選んだ反映先から
	if _, err := runGitIn(repo, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGitIn(repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-qm", "init"); err != nil {
		t.Fatal(err)
	}
	if err := a.PullGas("gas/report", "tokyo"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(repo, "gas/report/src/Code.js")); got != "browser" {
		t.Errorf("pulled from tokyo: %q", got)
	}

	// 外すと設定ファイルだけ消え、GAS 側は残る
	if err := a.RemoveGasTarget("gas/report", ""); err == nil {
		t.Error("main target cannot be removed")
	}
	if err := a.RemoveGasTarget("gas/report", "osaka"); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(repo, "gas/report/.clasp.osaka.json")) || !fileExists(filepath.Join(remote, osakaID, "Code.js")) {
		t.Error("RemoveGasTarget should remove only the config")
	}

	// rootDir が違う反映先は読まない
	writeFiles(t, repo, map[string]string{"gas/report/.clasp.bad.json": `{"scriptId":"badbadbadbadbadbadbadbadbad","rootDir":"other"}`})
	if _, err := a.GetGasProjects(); err == nil || !strings.Contains(err.Error(), "rootDir") {
		t.Errorf("different rootDir: %v", err)
	}
}
