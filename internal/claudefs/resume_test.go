package claudefs

import (
	"os"
	"path/filepath"
	"testing"
)

// A session's id is not fixed for the life of the process.
//
// `claude --resume` with no id opens a picker. The CLI announces one id at
// startup and then, when a conversation is chosen, switches to that
// conversation's id and rewrites sessions/<pid>.json. Typing /resume mid-session
// does the same thing.
//
// This matters because the app read the metafile once and kept the answer. It
// was then pointing at a transcript that had never existed: an empty
// conversation view and a token meter reading zero, on a session with hours of
// history and hundreds of dollars of usage behind it. Re-reading is the fix, and
// this pins the behaviour the fix depends on — that the metafile is the current
// truth, not a record of how the process started.
func TestSessionMetaReflectsAResume(t *testing.T) {
	dir := t.TempDir()
	sessions := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	const pid = 30992
	meta := filepath.Join(sessions, "30992.json")

	write := func(sid string) {
		t.Helper()
		body := `{"pid":30992,"sessionId":"` + sid + `","cwd":"C:\\work\\mis-dashboard"}`
		if err := os.WriteFile(meta, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// The id the CLI starts with, before anything is picked.
	write("32c1e68d-30bd-4da4-ac01-c682bd1d9f78")
	got := FindSessionMeta([]string{dir}, pid)
	if got == nil {
		t.Fatal("no metafile found")
	}
	if got.SessionID != "32c1e68d-30bd-4da4-ac01-c682bd1d9f78" {
		t.Fatalf("startup id = %q", got.SessionID)
	}

	// The user picks a conversation from the resume list. The CLI rewrites the
	// metafile, and reading it again must give the new id.
	write("12399466-fda6-4064-9d73-282db45e53ab")
	got = FindSessionMeta([]string{dir}, pid)
	if got == nil {
		t.Fatal("no metafile found after the resume")
	}
	if got.SessionID != "12399466-fda6-4064-9d73-282db45e53ab" {
		t.Errorf("after resume the id is %q; reading once would have kept the stale one",
			got.SessionID)
	}
}

// The transcript for a stale id genuinely does not exist, which is why keeping
// it produced an empty view rather than a wrong one.
func TestFindTranscriptMissesAStaleID(t *testing.T) {
	dir := t.TempDir()
	cwd := `C:\work\mis-dashboard`
	proj := filepath.Join(dir, "projects", EncodeCWD(cwd))
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	real := "12399466-fda6-4064-9d73-282db45e53ab"
	if err := os.WriteFile(filepath.Join(proj, real+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, ok := FindTranscript(dir, cwd, "32c1e68d-30bd-4da4-ac01-c682bd1d9f78"); ok {
		t.Error("a stale session id should not resolve to any transcript")
	}
	if _, ok := FindTranscript(dir, cwd, real); !ok {
		t.Error("the resumed session's transcript should be found")
	}
}
