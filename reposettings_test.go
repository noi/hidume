package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendGitignore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	// 改行で終わっていない既存の行と、おすすめにすでに入っている行
	if err := os.WriteFile(path, []byte("dist/\n.DS_Store\nnode_modules/"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appendGitignore(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.HasPrefix(text, "dist/\n.DS_Store\nnode_modules/\n\n# 秘密の情報") {
		t.Errorf("既存の行のあとに 1 行空けて足していません:\n%s", text)
	}
	for _, line := range []string{".DS_Store", "node_modules/"} {
		if strings.Count(text, line+"\n") != 1 {
			t.Errorf("%s が重複しています:\n%s", line, text)
		}
	}
	if !strings.Contains(text, "\n.clasprc.json\n") || !strings.Contains(text, "\n!.env.example\n") {
		t.Errorf("おすすめの行が足りません:\n%s", text)
	}

	// 足し終えたら、もう足すものはない
	missing, err := missingIgnoreLines(dir)
	if err != nil || len(missing) != 0 {
		t.Errorf("missingIgnoreLines = %v, %v", missing, err)
	}
	if err := appendGitignore(dir); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(path); string(again) != text {
		t.Errorf("2 回目で中身が変わりました:\n%s", again)
	}
}

func TestAppendGitignoreNew(t *testing.T) {
	dir := t.TempDir()
	if err := appendGitignore(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "# 秘密の情報") {
		t.Errorf("新しい .gitignore の頭に空行があります:\n%s", data)
	}
}

func TestWriteNewFileKeepsExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".github", "workflows", "ci.yml")
	if err := writeNewFile(path, "first"); err != nil {
		t.Fatal(err)
	}
	if err := writeNewFile(path, "second"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "first" {
		t.Errorf("既存のファイルを上書きしました: %q", data)
	}
}

func TestCdWorkflowBranch(t *testing.T) {
	w := cdWorkflow("main")
	if !strings.Contains(w, "    branches:\n      - \"main\"\n") {
		t.Errorf("マージ先のブランチが入っていません:\n%s", w)
	}
	if !strings.Contains(w, "secrets."+claspSecretName) {
		t.Errorf("シークレットを使っていません:\n%s", w)
	}
}

func TestAddClaudeDenies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, claudeSettingsPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// 人が書いた設定とルールは残す
	existing := `{"model": "opus", "permissions": {"allow": ["Bash(npm test:*)"], "deny": ["Read(**/.env)", "WebFetch"]}}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := addClaudeDenies(dir); err != nil {
		t.Fatal(err)
	}
	settings, deny, err := readClaudeSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if settings["model"] != "opus" || settings["permissions"].(map[string]any)["allow"].([]any)[0] != "Bash(npm test:*)" {
		t.Errorf("もとの設定が消えました: %v", settings)
	}
	if deny[0] != "Read(**/.env)" || deny[1] != "WebFetch" || len(deny) != 1+len(claudeSecretDenies) {
		t.Errorf("deny = %v", deny)
	}
	if missing, err := missingClaudeDenies(dir); err != nil || len(missing) != 0 {
		t.Errorf("missingClaudeDenies = %v, %v", missing, err)
	}
}

func TestAddClaudeDeniesNew(t *testing.T) {
	dir := t.TempDir()
	if err := addClaudeDenies(dir); err != nil {
		t.Fatal(err)
	}
	settings, deny, err := readClaudeSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if settings["$schema"] == nil || len(deny) != len(claudeSecretDenies) {
		t.Errorf("settings = %v", settings)
	}
}

func TestAddClaudeDeniesBroken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, claudeSettingsPath)
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(`{"permissions": `), 0o644)
	if err := addClaudeDenies(dir); err == nil {
		t.Error("壊れた設定に書き足しました")
	}
	if data, _ := os.ReadFile(path); string(data) != `{"permissions": ` {
		t.Errorf("壊れた設定を書き換えました: %q", data)
	}
}

func TestSetClaudeLanguage(t *testing.T) {
	dir := t.TempDir()
	if ok, err := hasClaudeLanguage(dir); err != nil || ok {
		t.Fatalf("hasClaudeLanguage = %v, %v", ok, err)
	}
	if err := addClaudeDenies(dir); err != nil {
		t.Fatal(err)
	}
	if err := setClaudeLanguage(dir); err != nil {
		t.Fatal(err)
	}
	settings, deny, err := readClaudeSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 先に書いた deny は残す
	if settings["language"] != claudeLanguage || len(deny) != len(claudeSecretDenies) {
		t.Errorf("settings = %v", settings)
	}
	if ok, err := hasClaudeLanguage(dir); err != nil || !ok {
		t.Errorf("hasClaudeLanguage = %v, %v", ok, err)
	}
}

func TestHasClaudeLanguageOther(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, claudeSettingsPath)
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(`{"language": "english"}`), 0o644)
	// 人が決めた言葉は日本語でなくても設定済みとして扱い、書き換えない
	if ok, err := hasClaudeLanguage(dir); err != nil || !ok {
		t.Errorf("hasClaudeLanguage = %v, %v", ok, err)
	}
}
