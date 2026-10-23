package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// config は起動をまたいで覚えておく設定
type config struct {
	// 最後に開いたリポジトリのルート
	Repository string `json:"repository"`
	// ディレクトリごとの Claude Code のセッション ID（ディレクトリとセッションは 1 対 1）
	Sessions map[string]string `json:"sessions,omitempty"`
	// 初回のセットアップ（ヒヅメくんの自己紹介と道具のインストール）を終えたか
	SetupDone bool `json:"setupDone,omitempty"`
}

// 読んで書き換えて保存する間に、ほかの書き換えが割り込まないようにする
var configMu sync.Mutex

// updateConfig は設定を読み、change で書き換えて保存する
func updateConfig(change func(c *config)) error {
	configMu.Lock()
	defer configMu.Unlock()
	c := loadConfig()
	change(&c)
	return saveConfig(c)
}

// dataDir は hidume が設定や会話の記録を置く場所
func dataDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hidume"), nil
}

// configPath は設定ファイルの場所（macOS なら ~/Library/Application Support/hidume/config.json）
func configPath() (string, error) {
	dir, err := dataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// loadConfig は設定を読む。ファイルがない・壊れているときは空の設定を返す
func loadConfig() config {
	var c config
	path, err := configPath()
	if err != nil {
		return c
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	_ = json.Unmarshal(data, &c)
	return c
}

func saveConfig(c config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
