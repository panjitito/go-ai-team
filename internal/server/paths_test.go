package server

import "testing"

// These helpers exist because filepath gives a different answer on Linux than
// on Windows for the same string, and three checks in this package were built
// on the Windows answer. Every case here is one of those, and every one must
// hold identically on both platforms — so there is no runtime.GOOS anywhere in
// this file, on purpose.
func TestBaseName(t *testing.T) {
	cases := map[string]string{
		`C:\Users\dev\Projects\api`:     "api",
		`C:\Users\dev\Projects\api\`:    "",
		"/home/dev/projects/api":        "api",
		`..\..\windows\system32\evil`:   "evil",
		"../../etc/passwd":              "passwd",
		`mixed/separators\in one\path`:  "path",
		"screenshot.png":                "screenshot.png",
		"":                              "",
		`\\server\share\report.xlsx`:    "report.xlsx",
		"trailing/slash/":               "",
		`C:\a.txt`:                      "a.txt",
		"no-separators-at-all":          "no-separators-at-all",
		`one\two`:                       "two",
		"one/two":                       "two",
		`C:/mixed\case/Path\Thing.md`:   "Thing.md",
		"deep/deep/deep/deep/deep/x.go": "x.go",
	}
	for in, want := range cases {
		if got := baseName(in); got != want {
			t.Errorf("baseName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToSlash(t *testing.T) {
	cases := map[string]string{
		`..\secret.txt`:      "../secret.txt",
		`a\b\c`:              "a/b/c",
		"a/b/c":              "a/b/c",
		`C:\Windows\System`:  "C:/Windows/System",
		"":                   "",
		"nothing-to-do-here": "nothing-to-do-here",
		`mixed/one\two`:      "mixed/one/two",
	}
	for in, want := range cases {
		if got := toSlash(in); got != want {
			t.Errorf("toSlash(%q) = %q, want %q", in, got, want)
		}
	}
}
