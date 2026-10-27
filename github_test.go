package main

import (
	"reflect"
	"testing"
)

func TestToCheck(t *testing.T) {
	tests := []struct {
		item rollupItem
		want Check
	}{
		{rollupItem{Typename: "CheckRun", Name: "test", WorkflowName: "CI", Status: "COMPLETED", Conclusion: "SUCCESS", DetailsURL: "https://x"},
			Check{Name: "CI / test", State: "pass", URL: "https://x"}},
		{rollupItem{Typename: "CheckRun", Name: "lint", Status: "IN_PROGRESS"}, Check{Name: "lint", State: "pending"}},
		{rollupItem{Typename: "CheckRun", Name: "lint", Status: "COMPLETED", Conclusion: "SKIPPED"}, Check{Name: "lint", State: "skip"}},
		{rollupItem{Typename: "CheckRun", Name: "lint", Status: "COMPLETED", Conclusion: "TIMED_OUT"}, Check{Name: "lint", State: "fail"}},
		{rollupItem{Typename: "StatusContext", Context: "ci/old", State: "ERROR", TargetURL: "https://y"},
			Check{Name: "ci/old", State: "fail", URL: "https://y"}},
		{rollupItem{Typename: "StatusContext", Context: "ci/old", State: "EXPECTED"}, Check{Name: "ci/old", State: "pending"}},
	}
	for _, tt := range tests {
		if got := toCheck(tt.item); got != tt.want {
			t.Errorf("toCheck(%+v) = %+v, want %+v", tt.item, got, tt.want)
		}
	}
}

func TestMergeMethods(t *testing.T) {
	// スカッシュだけを許すおすすめ設定では、前回マージコミットを選んでいても既定にしない
	methods, def := repoMergeSettings{SquashMergeAllowed: true, ViewerDefaultMergeMethod: "MERGE"}.methods()
	if !reflect.DeepEqual(methods, []string{"squash"}) || def != "" {
		t.Errorf("methods = %v, %q", methods, def)
	}
	methods, def = repoMergeSettings{MergeCommitAllowed: true, SquashMergeAllowed: true, RebaseMergeAllowed: true,
		ViewerDefaultMergeMethod: "REBASE"}.methods()
	if !reflect.DeepEqual(methods, []string{"merge", "squash", "rebase"}) || def != "rebase" {
		t.Errorf("methods = %v, %q", methods, def)
	}
}
