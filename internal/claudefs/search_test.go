package claudefs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTranscript lays out a transcript where the real ones live.
func writeTranscript(t *testing.T, dir, cwd, sessionID string, lines ...string) string {
	t.Helper()
	p := filepath.Join(dir, "projects", EncodeCWD(cwd))
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(p, sessionID+".jsonl")
	if err := os.WriteFile(f, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

func searchUserLine(when, text string) string {
	return fmt.Sprintf(
		`{"type":"user","uuid":"u1","timestamp":%q,"message":{"role":"user","content":[{"type":"text","text":%q}]}}`,
		when, text)
}

func searchAssistantLine(when, text string) string {
	return fmt.Sprintf(
		`{"type":"assistant","uuid":"a1","timestamp":%q,"message":{"role":"assistant","content":[{"type":"text","text":%q}]}}`,
		when, text)
}

func searchToolLine(when, name, input string) string {
	return fmt.Sprintf(
		`{"type":"assistant","uuid":"t1","timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":"x","name":%q,"input":%s}]}}`,
		when, name, input)
}

func TestSearchFindsAcrossConversations(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "acct-work")
	spare := filepath.Join(home, "acct-spare")
	cwd := `C:\proj\mis`

	writeTranscript(t, work, cwd, "aaa",
		searchUserLine("2026-09-01T10:00:00Z", "please fix the invoice rounding"),
		searchAssistantLine("2026-09-01T10:01:00Z", "The rounding was in the tax column."))
	writeTranscript(t, spare, cwd, "bbb",
		searchAssistantLine("2026-09-03T22:15:00Z", "Nothing about money here at all."),
		searchToolLine("2026-09-03T22:16:00Z", "Bash", `{"command":"grep -r invoice ."}`))

	got := Search(SearchOpts{Dirs: []string{work, spare}, CWD: cwd, Query: "invoice"})

	if len(got.Hits) != 2 {
		t.Fatalf("%d hits, want 2: %+v", len(got.Hits), got.Hits)
	}
	// Newest first, whichever account it came from.
	if !got.Hits[0].When.After(got.Hits[1].When) {
		t.Errorf("hits are not newest first: %v then %v", got.Hits[0].When, got.Hits[1].When)
	}
	if got.Hits[0].SessionID != "bbb" || got.Hits[0].Dir != spare {
		t.Errorf("first hit = %s in %s", got.Hits[0].SessionID, got.Hits[0].Dir)
	}
	// A tool call is a place an answer can hide, so it is searched too.
	if !strings.Contains(got.Hits[0].Snippet, "grep -r invoice") {
		t.Errorf("tool snippet = %q", got.Hits[0].Snippet)
	}
	if got.Hits[1].Role != "user" {
		t.Errorf("role = %q, want the person who asked", got.Hits[1].Role)
	}
	if got.Truncated {
		t.Error("a two-file search claims it ran out of budget")
	}
	if got.Files != 2 || got.Total != 2 {
		t.Errorf("read %d of %d files", got.Files, got.Total)
	}
}

func TestSearchIsCaseInsensitiveAndSnips(t *testing.T) {
	home := t.TempDir()
	cwd := `C:\proj\mis`
	long := strings.Repeat("padding ", 60) + "the ROUNDING bug" + strings.Repeat(" trailing", 60)
	writeTranscript(t, home, cwd, "aaa", searchAssistantLine("2026-09-01T10:00:00Z", long))

	got := Search(SearchOpts{Dirs: []string{home}, CWD: cwd, Query: "rounding"})
	if len(got.Hits) != 1 {
		t.Fatalf("%d hits", len(got.Hits))
	}
	h := got.Hits[0]
	if !strings.Contains(strings.ToLower(h.Snippet), "rounding") {
		t.Errorf("snippet lost the match: %q", h.Snippet)
	}
	// A snippet is a window, not the message.
	if len(h.Snippet) > 3*snippetPad {
		t.Errorf("snippet is %d bytes; it should be a window around the match", len(h.Snippet))
	}
	if !strings.HasPrefix(h.Snippet, "…") || !strings.HasSuffix(h.Snippet, "…") {
		t.Errorf("a window into a longer message must say so: %q", h.Snippet)
	}
	// The whole message is still there for when the window is not enough.
	if !strings.Contains(h.Text, "padding") || !strings.Contains(h.Text, "trailing") {
		t.Error("the full text was not kept")
	}
}

// A raw byte match that is not in anything a person reads is not a hit. The
// prefilter is deliberately crude; this is what keeps the results honest.
func TestSearchDropsPlumbingMatches(t *testing.T) {
	home := t.TempDir()
	cwd := `C:\proj\mis`
	writeTranscript(t, home, cwd, "deadbeef-cafe",
		`{"type":"assistant","uuid":"deadbeef-cafe-1111","timestamp":"2026-09-01T10:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":"nothing to see"}]}}`)

	got := Search(SearchOpts{Dirs: []string{home}, CWD: cwd, Query: "deadbeef"})
	if len(got.Hits) != 0 {
		t.Errorf("a uuid matched and was reported as a message: %+v", got.Hits)
	}
	// It still counts as read, so the caller knows the file was looked at.
	if got.Files != 1 {
		t.Errorf("read %d files", got.Files)
	}
}

func TestSearchEmptyQueryFindsNothing(t *testing.T) {
	home := t.TempDir()
	writeTranscript(t, home, `C:\p`, "aaa", searchAssistantLine("2026-09-01T10:00:00Z", "hello"))
	for _, q := range []string{"", "   "} {
		got := Search(SearchOpts{Dirs: []string{home}, CWD: `C:\p`, Query: q})
		if len(got.Hits) != 0 {
			t.Errorf("query %q returned %d hits", q, len(got.Hits))
		}
		if got.Hits == nil {
			t.Errorf("query %q returned nil, and a list is never null", q)
		}
	}
}

// Without a working directory it searches every project in every account.
func TestSearchAcrossEveryProject(t *testing.T) {
	home := t.TempDir()
	writeTranscript(t, home, `C:\proj\one`, "aaa", searchAssistantLine("2026-09-01T10:00:00Z", "the kestrel flies"))
	writeTranscript(t, home, `C:\proj\two`, "bbb", searchAssistantLine("2026-09-02T10:00:00Z", "the kestrel lands"))

	scoped := Search(SearchOpts{Dirs: []string{home}, CWD: `C:\proj\one`, Query: "kestrel"})
	if len(scoped.Hits) != 1 {
		t.Errorf("scoped to one project found %d hits", len(scoped.Hits))
	}
	all := Search(SearchOpts{Dirs: []string{home}, Query: "kestrel"})
	if len(all.Hits) != 2 {
		t.Errorf("across every project found %d hits", len(all.Hits))
	}
}

// Running out of budget says so, because "nothing found" and "nothing found in
// the part I read" are different answers.
func TestSearchBudgetIsHonest(t *testing.T) {
	home := t.TempDir()
	cwd := `C:\proj\mis`
	for i := 0; i < 6; i++ {
		writeTranscript(t, home, cwd, fmt.Sprintf("s%d", i),
			searchAssistantLine("2026-09-01T10:00:00Z", strings.Repeat("x", 4000)+" needle"))
	}
	got := Search(SearchOpts{Dirs: []string{home}, CWD: cwd, Query: "needle", Budget: 5000})
	if !got.Truncated {
		t.Error("a search that stopped early does not admit it")
	}
	if got.Files >= 6 {
		t.Errorf("read %d files on a 5 kB budget", got.Files)
	}
	if got.Total != 6 {
		t.Errorf("Total = %d, want all 6 so the UI can say how much was skipped", got.Total)
	}

	// And a limit stops it too, without claiming to have read everything.
	few := Search(SearchOpts{Dirs: []string{home}, CWD: cwd, Query: "needle", Limit: 2})
	if len(few.Hits) != 2 {
		t.Errorf("limit 2 returned %d hits", len(few.Hits))
	}
}

func TestSearchDeadline(t *testing.T) {
	home := t.TempDir()
	cwd := `C:\p`
	for i := 0; i < 4; i++ {
		writeTranscript(t, home, cwd, fmt.Sprintf("s%d", i),
			searchAssistantLine("2026-09-01T10:00:00Z", "needle"))
	}
	// A deadline already in the past stops before the first file.
	got := Search(SearchOpts{Dirs: []string{home}, CWD: cwd, Query: "needle", Deadline: time.Nanosecond})
	if !got.Truncated {
		t.Error("an expired deadline did not stop the search")
	}
}
