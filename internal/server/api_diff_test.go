package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/panjitito/go-ai-team/internal/store"
)

// A project with no repository must not look like a file that did not change.
//
// This is the bug as reported: every file in the changed-files rail opened onto
// "No textual diff (binary, or no change)". The endpoint was answering with a
// perfectly good sentence — "This project is not a git repository" — as a bare
// 200 with a body, and the caller decided what it had by asking whether the body
// was empty. It was not empty, so the sentence went into the diff parser, which
// found no hunks in it and reported the file as unchanged.
//
// The fix is that the answer says which kind it is. This pins that.
func TestGitDiffLabelsANonRepoAnswer(t *testing.T) {
	dir := t.TempDir() // a directory, deliberately not a repository

	// A store of its own, so this never reads or writes the real one.
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	p := &store.Project{Name: "no-repo", Path: dir}
	if err := st.AddProject(p); err != nil {
		t.Fatal(err)
	}
	s := &Server{st: st}

	w := httptest.NewRecorder()
	s.gitDiff(w, httptest.NewRequest(http.MethodGet,
		"/api/git/diff?projectId="+p.ID+"&path=notes.md", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "not a git repository") {
		t.Errorf("the reason was dropped: %q", body)
	}
	// The label is the whole point: without it the caller has to guess from the
	// shape of the text, and it guessed wrong for a year.
	if got := w.Header().Get("X-Diff-Kind"); got != "message" {
		t.Errorf("X-Diff-Kind = %q, want \"message\" so the caller need not guess", got)
	}
	// And it must not be mistakable for a diff by anyone who does still look.
	for _, marker := range []string{"diff --git", "@@ ", "--- ", "+++ "} {
		if strings.Contains(body, marker) {
			t.Errorf("the message contains %q, which reads as a diff", marker)
		}
	}
}

// The layout that was actually there: the project is a working folder and the
// checkout is inside it.
//
// The first fix made the app honest — "this project is not a git repository" —
// which was true of the folder and useless about the code in it. The right
// answer is to find the repository, which is what git itself does when you run
// it anywhere inside one.
func TestGitDiffFindsARepositoryBelowTheProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	project := t.TempDir()
	// The things that live beside a checkout, and are not in it.
	if err := os.WriteFile(filepath.Join(project, "notes.txt"), []byte("credentials\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(project, "app")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "init").CombinedOutput(); err != nil {
		t.Skipf("git is not available here: %v (%s)", err, out)
	}
	tracked := filepath.Join(repo, "Kernel.php")
	if err := os.WriteFile(tracked, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", "Kernel.php")
	mustGit(t, repo, "-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "-m", "in")
	if err := os.WriteFile(tracked, []byte("one\nCHANGED\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	p := &store.Project{Name: "working-folder", Path: project}
	if err := st.AddProject(p); err != nil {
		t.Fatal(err)
	}
	s := &Server{st: st}

	get := func(path string) (int, string, string) {
		t.Helper()
		w := httptest.NewRecorder()
		s.gitDiff(w, httptest.NewRequest(http.MethodGet,
			"/api/git/diff?projectId="+p.ID+"&path="+path, nil))
		return w.Code, w.Header().Get("X-Diff-Kind"), w.Body.String()
	}

	// The rail names a file relative to the project: app/Kernel.php.
	code, kind, body := get("app/Kernel.php")
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	if kind != "diff" {
		t.Errorf("X-Diff-Kind = %q, want a real diff (%s)", kind, body)
	}
	if !strings.Contains(body, "CHANGED") {
		t.Errorf("the change is not in the diff:\n%s", body)
	}

	// The review pane names the same file relative to the repository.
	if _, kind, body := get("Kernel.php"); kind != "diff" || !strings.Contains(body, "CHANGED") {
		t.Errorf("repository-relative path gave %q:\n%s", kind, body)
	}

	// A file beside the checkout is in the project and not in the repository,
	// and saying so is more use than an empty pane.
	_, kind, body = get("notes.txt")
	if kind != "message" || !strings.Contains(body, "outside the repository") {
		t.Errorf("a file next to the checkout gave %q: %s", kind, body)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
}
