package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewerVersion(t *testing.T) {
	tests := []struct {
		latest, current string
		want            bool
	}{
		{"v1.2.4", "v1.2.3", true},
		{"v1.3.0", "v1.2.9", true},
		{"v2.0.0", "v1.10.0", true},
		{"v1.10.0", "v1.9.0", true},
		{"v1.2.3", "v1.2.3", false},
		{"v1.2.2", "v1.2.3", false},
		{"v1.2.3", "v1.2.3-rc.1", true},
		{"v1.2.3-rc.2", "v1.2.3-rc.1", false},
		{"v1.2.3-rc.1", "v1.2.3", false},
		{"v1.2.4-rc.1", "v1.2.3", true},
		{"", "v1.2.3", false},
		{"1.2.4", "v1.2.3", false},
		{"v1.2", "v1.2.3", false},
		{"v1.2.4", "v1.2.x", false},
	}
	for _, tt := range tests {
		if got := newerVersion(tt.latest, tt.current); got != tt.want {
			t.Errorf("newerVersion(%q, %q) = %v, want %v", tt.latest, tt.current, got, tt.want)
		}
	}
}

func TestSwapBundle(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "hidume.app")
	staged := filepath.Join(dir, ".hidume-update.app")
	for path, content := range map[string]string{bundle: "old", staged: "new"} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "version"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := swapBundle(bundle, staged); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(bundle, "version"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("version = %q, want new", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("残っているもの = %v, want hidume.app だけ", entries)
	}
}

func TestSwapBundleRestoresOnFailure(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "hidume.app")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	// 新しいものがないので 2 つ目の rename が失敗する
	if err := swapBundle(bundle, filepath.Join(dir, ".hidume-update.app")); err == nil {
		t.Fatal("err = nil, want error")
	}
	if _, err := os.Stat(bundle); err != nil {
		t.Errorf("元の hidume.app が戻っていない: %v", err)
	}
}
