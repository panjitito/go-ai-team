package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uniair/go-ai-team/internal/store"
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
