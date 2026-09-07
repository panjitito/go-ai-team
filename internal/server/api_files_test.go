package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// projectRoot is a temp directory standing in for a project. Symlinks are
// resolved here too, or every comparison fails on a machine where the temp
// directory is itself a link.
func projectRoot(t *testing.T) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// Everything this API touches must be inside the project. It takes a project id
// and a relative path, and a route that can be talked out of that is a remote
// file manager rather than an editor.
func TestProjectPathStaysInside(t *testing.T) {
	root := projectRoot(t)

	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "sub", "deep", "a.go"), []byte("package a"), 0o600)

	// A secret one directory up, which is what an escape would be reaching for.
	outside := filepath.Join(filepath.Dir(root), "secret.txt")
	os.WriteFile(outside, []byte("SECRET"), 0o600)
	t.Cleanup(func() { os.Remove(outside) })

	good := []string{"", ".", "ok.txt", "sub", "sub/deep", "sub/deep/a.go"}
	for _, rel := range good {
		if _, _, err := resolveInside(root, rel); err != nil {
			t.Errorf("projectPath(%q) refused a path inside the project: %v", rel, err)
		}
	}

	// Every one of these is refused on every platform, which is the point of
	// the list. filepath is separator-aware, so on Linux it reads `..\secret`
	// as one oddly-named file and a drive letter as an ordinary directory
	// name; a check that only holds on the machine it was written on is not a
	// check. See paths.go.
	bad := []string{
		"..", "../secret.txt", "../../etc/passwd",
		`..\secret.txt`, `..\..\windows\system32`,
		"sub/../../secret.txt",
		"sub/deep/../../../secret.txt",
		`sub\..\..\secret.txt`,
		"/etc/passwd",
		`C:\Windows\System32\drivers\etc\hosts`, "C:/Windows",
	}
	for _, rel := range bad {
		if abs, _, err := resolveInside(root, rel); err == nil {
			t.Errorf("projectPath(%q) escaped to %q", rel, abs)
		}
	}
}

// A symlink inside the project pointing out of it passes any textual check, so
// the resolved path is what gets tested.
func TestProjectPathRefusesEscapingSymlink(t *testing.T) {
	root := projectRoot(t)

	outside := filepath.Join(filepath.Dir(root), "outside-target.txt")
	if err := os.WriteFile(outside, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(outside) })

	link := filepath.Join(root, "escape.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks not available here: %v", err)
	}
	if abs, _, err := resolveInside(root, "escape.txt"); err == nil {
		t.Errorf("a symlink out of the project resolved to %q instead of being refused", abs)
	}
}

// A binary file must be recognised rather than rendered as mojibake, and never
// offered for editing.
func TestIsBinary(t *testing.T) {
	text := [][]byte{
		[]byte("package main\n\nfunc main() {}\n"),
		[]byte("# heading\n\nsome prose — with unicode ✓\n"),
		[]byte(""),
		[]byte("{\"a\":1}"),
	}
	for _, b := range text {
		if isBinary(b) {
			t.Errorf("text treated as binary: %q", string(b[:min(len(b), 30)]))
		}
	}
	bin := [][]byte{
		{0x89, 'P', 'N', 'G', 0, 0, 0},
		{'M', 'Z', 0x90, 0x00, 0x03, 0x00, 0x00, 0x00},
		append([]byte("looks like text for a while"), 0x00),
	}
	for _, b := range bin {
		if !isBinary(b) {
			t.Errorf("binary not detected: %v", b[:min(len(b), 8)])
		}
	}
}

// The hash is what makes a save conditional, so it has to actually change when
// the contents do.
func TestHashOf(t *testing.T) {
	a := hashOf([]byte("hello"))
	if a == "" {
		t.Fatal("empty hash")
	}
	if a != hashOf([]byte("hello")) {
		t.Error("hash is not stable")
	}
	if a == hashOf([]byte("hello ")) {
		t.Error("a one-character change produced the same hash")
	}
	if a == hashOf(nil) {
		t.Error("empty and non-empty hash the same")
	}
}

// The tree must not bury a repository under its own machinery.
func TestSkipDirs(t *testing.T) {
	for _, d := range []string{".git", "node_modules", "__pycache__"} {
		if !skipDirs[d] {
			t.Errorf("%s should be skipped", d)
		}
	}
	for _, d := range []string{"internal", "src", "cmd", "docs"} {
		if skipDirs[d] {
			t.Errorf("%s should not be skipped", d)
		}
	}
}

func TestHuman(t *testing.T) {
	cases := map[int64]string{
		0: "0 B", 512: "512 B", 2048: "2 KB", 3 << 20: "3.0 MB",
	}
	for in, want := range cases {
		if got := human(in); !strings.Contains(got, strings.Fields(want)[0]) {
			t.Errorf("human(%d) = %q, want something like %q", in, got, want)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
