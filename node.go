package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
)

// Node.js の公式のビルド済みバイナリの置き場所
const nodeDistURL = "https://nodejs.org/dist"

// nodeDir は Node.js を入れる場所。ホームの下なので sudo が要らず、npm install -g もそのまま書き込める
func nodeDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "node")
}

// nodeRelease は nodejs.org/dist/index.json の 1 件分
type nodeRelease struct {
	Version string   `json:"version"`
	Files   []string `json:"files"`
	// LTS でなければ false、LTS ならコードネームの文字列
	LTS any `json:"lts"`
}

// installNode は Node.js の最新の LTS を nodejs.org から入れる。
// Homebrew はサポートを終えた macOS ではビルド済みのものがなく、llvm などから何時間もかけてビルドしようとするので使わない
func installNode() error {
	ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
	defer cancel()
	if err := downloadNode(ctx); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("Node.js のダウンロードが %s 以内に終わりませんでした", installTimeout)
		}
		return err
	}
	return nil
}

func downloadNode(ctx context.Context) error {
	arch := map[string]string{"arm64": "arm64", "amd64": "x64"}[goruntime.GOARCH]
	if arch == "" {
		return fmt.Errorf("この Mac（%s）向けの Node.js はありません", goruntime.GOARCH)
	}
	var releases []nodeRelease
	if err := fetchJSON(ctx, nodeDistURL+"/index.json", &releases); err != nil {
		return err
	}
	// 新しい順に並んでいる
	i := slices.IndexFunc(releases, func(r nodeRelease) bool {
		return r.LTS != false && slices.Contains(r.Files, "osx-"+arch+"-tar")
	})
	if i < 0 {
		return fmt.Errorf("この Mac 向けの Node.js の LTS が見つかりませんでした")
	}
	version := releases[i].Version
	name := fmt.Sprintf("node-%s-darwin-%s", version, arch)
	sum, err := nodeChecksum(ctx, version, name+".tar.gz")
	if err != nil {
		return err
	}

	parent := filepath.Dir(nodeDir())
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, "node-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	archive := filepath.Join(tmp, name+".tar.gz")
	if err := downloadFile(ctx, fmt.Sprintf("%s/%s/%s.tar.gz", nodeDistURL, version, name), archive, sum); err != nil {
		return err
	}
	if out, err := exec.CommandContext(ctx, "tar", "-xzf", archive, "-C", tmp).CombinedOutput(); err != nil {
		return fmt.Errorf("Node.js を展開できませんでした: %s", strings.TrimSpace(string(out)))
	}
	// 前に入れたものは入れ替える。npm install -g で入れた clasp も消えるが、入れ直せばいい
	if err := os.RemoveAll(nodeDir()); err != nil {
		return err
	}
	return os.Rename(filepath.Join(tmp, name), nodeDir())
}

// nodeChecksum は SHASUMS256.txt から file の SHA-256 を探す
func nodeChecksum(ctx context.Context, version, file string) (string, error) {
	body, err := fetch(ctx, fmt.Sprintf("%s/%s/SHASUMS256.txt", nodeDistURL, version))
	if err != nil {
		return "", err
	}
	defer body.Close()
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		if sum, f, ok := strings.Cut(scanner.Text(), "  "); ok && f == file {
			return sum, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s のチェックサムが見つかりませんでした", file)
}

// downloadFile は url を path に保存し、SHA-256 が sum と合うか確かめる
func downloadFile(ctx context.Context, url, path, sum string) error {
	body, err := fetch(ctx, url)
	if err != nil {
		return err
	}
	defer body.Close()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, hash), body); err != nil {
		return fmt.Errorf("%s をダウンロードできませんでした: %w", url, err)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != sum {
		return fmt.Errorf("%s のチェックサムが合いません", url)
	}
	return f.Close()
}

func fetchJSON(ctx context.Context, url string, v any) error {
	body, err := fetch(ctx, url)
	if err != nil {
		return err
	}
	defer body.Close()
	return json.NewDecoder(body).Decode(v)
}

func fetch(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s に繋がりませんでした: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return resp.Body, nil
}
