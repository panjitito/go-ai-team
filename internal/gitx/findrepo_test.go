package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func mkRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := run(context.Background(), dir, "init"); err != nil {
		t.Skipf("git is not available here: %v", err)
	}
}

// A project that *contains* the repository, which is the layout that did not
// work: a working folder with the checkout in it beside notes and a script.
func TestFindRepoOneLevelDown(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, "logo-vector"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(project, "app")
	mkRepo(t, repo)

	// The precondition, and the reason this was reported: the folder itself is
	// not a repository.
	if IsRepo(project) {
		t.Fatal("the project directory is a repository; this is not testing the reported layout")
	}

	got, ok := FindRepo(project)
	if !ok {
		t.Fatal("the repository one level down was not found")
	}
	if !sameDir(t, got, repo) {
		t.Errorf("found %q, want %q", got, repo)
	}
}

// The ordinary layout keeps working, including from a subdirectory — git walks
// up on its own and always did.
func TestFindRepoAtOrAboveTheProject(t *testing.T) {
	root := t.TempDir()
	mkRepo(t, root)

	if got, ok := FindRepo(root); !ok || !sameDir(t, got, root) {
		t.Errorf("at the root: %q %v", got, ok)
	}

	sub := filepath.Join(root, "packages", "web")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := FindRepo(sub); !ok || !sameDir(t, got, root) {
		t.Errorf("from a subdirectory: %q %v, want the repository root", got, ok)
	}
}

// Two repositories under one folder is a question this cannot answer, and
// guessing at it would be worse than saying so.
func TestFindRepoRefusesToGuess(t *testing.T) {
	project := t.TempDir()
	mkRepo(t, filepath.Join(project, "one"))
	mkRepo(t, filepath.Join(project, "two"))

	if got, ok := FindRepo(project); ok {
		t.Errorf("picked %q out of two candidates", got)
	}
}

func TestFindRepoWithNothingToFind(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "just-a-folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := FindRepo(project); ok {
		t.Errorf("found %q where there is no repository", got)
	}
	if _, ok := FindRepo(""); ok {
		t.Error("found a repository for no directory at all")
	}
}

// sameDir compares two paths as the filesystem sees them, since a temp
// directory arrives through a symlink on some machines and as an 8.3 name on
// others.
func sameDir(t *testing.T, a, b string) bool {
	t.Helper()
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}
	return filepath.Clean(ra) == filepath.Clean(rb)
}
