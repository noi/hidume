package main

import "testing"

func TestCloneName(t *testing.T) {
	for remote, want := range map[string]string{
		"https://github.com/noi/hidume.git":              "hidume",
		"https://github.com/noi/hidume":                  "hidume",
		"https://github.com/noi/hidume/":                 "hidume",
		"git@github.com:noi/hidume.git":                  "hidume",
		"ssh://git@github.example.com:2222/org/repo.git": "repo",
		"https://gitlab.com/group/sub/project.git":       "project",
		"/Users/noi/repos/hidume.git":                    "",
		"file:///Users/noi/repos/hidume.git":             "",
		"-uhttps://github.com/noi/hidume":                "",
		"https://github.com/":                            "",
		"https://github.com/noi/..":                      "",
		"":                                               "",
	} {
		got, err := cloneName(remote)
		if got != want || (err != nil) != (want == "") {
			t.Errorf("cloneName(%q) = %q, %v, want %q", remote, got, err, want)
		}
	}
}
