package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on PATH")
	}
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := run(ctx, real, args...); err != nil {
			t.Skipf("git init failed: %v", err)
		}
	}
	return real
}

// A new file is exactly the case where you most want to see the code, and it is
// the one git diff says nothing about — it does not know the file.
//
// The fallback diffs it against nothing, and that has to tolerate exit status 1:
// `git diff --no-index` returns 1 to mean "these differ", which is the reason it
// was called. Treating that as failure threw away a 9KB diff and left the review
// pane reading "No textual diff" on every newly added file.
func TestDiffShowsAnUntrackedFile(t *testing.T) {
	dir := gitRepo(t)
	ctx := context.Background()

	body := "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := Diff(ctx, dir, "new.go", false)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("an untracked file produced an empty diff; the review pane would be blank")
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("the file's contents are missing from the diff:\n%s", out)
	}
	if !strings.Contains(out, "@@") {
		t.Errorf("no hunk header, so line numbers cannot be shown:\n%s", out)
	}
	for _, want := range []string{"+package main", "+func main() {"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q as an addition:\n%s", want, out)
		}
	}
}

// A tracked, modified file still diffs the ordinary way.
func TestDiffShowsAModifiedFile(t *testing.T) {
	dir := gitRepo(t)
	ctx := context.Background()

	p := filepath.Join(dir, "a.txt")
	os.WriteFile(p, []byte("one\ntwo\nthree\n"), 0o600)
	if _, err := run(ctx, dir, "add", "a.txt"); err != nil {
		t.Skip(err)
	}
	if _, err := run(ctx, dir, "commit", "-m", "first"); err != nil {
		t.Skip(err)
	}
	os.WriteFile(p, []byte("one\nTWO\nthree\n"), 0o600)

	out, err := Diff(ctx, dir, "a.txt", false)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(out, "-two") || !strings.Contains(out, "+TWO") {
		t.Errorf("modified file diff is wrong:\n%s", out)
	}
}

// A file with no changes has nothing to show, and must not fall through to the
// untracked path and print the whole file as an addition.
func TestDiffOfAnUnchangedFileIsEmpty(t *testing.T) {
	dir := gitRepo(t)
	ctx := context.Background()

	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("same\n"), 0o600)
	if _, err := run(ctx, dir, "add", "b.txt"); err != nil {
		t.Skip(err)
	}
	if _, err := run(ctx, dir, "commit", "-m", "b"); err != nil {
		t.Skip(err)
	}

	out, err := Diff(ctx, dir, "b.txt", false)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if strings.Contains(out, "+same") {
		t.Errorf("an unchanged file was shown as newly added:\n%s", out)
	}
}
