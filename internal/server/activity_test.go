package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uniair/go-ai-team/internal/session"
)

// The events that are worth a line, and the ones that would drown it.
func TestActivityDescribe(t *testing.T) {
	s := &Server{log: &journal{}}

	cases := []struct {
		name  string
		ev    session.Event
		keep  bool
		text  string
		level string
	}{
		{
			name: "a question is the whole point",
			ev: session.Event{
				Type: "session.needs-you", SessionID: "s1",
				Message: "Which browser should I use?",
			},
			keep: true, text: "Asked: Which browser should I use?", level: "warn",
		},
		{
			name: "a turn ending",
			ev: session.Event{
				Type: "session.status", SessionID: "s1",
				Payload: session.PublicSession{ID: "s1", Status: session.StatusWaiting},
			},
			keep: true, text: "Finished a turn", level: "info",
		},
		{
			name: "starting to draw again is not news",
			ev: session.Event{
				Type: "session.status", SessionID: "s1",
				Payload: session.PublicSession{ID: "s1", Status: session.StatusWorking},
			},
			keep: false,
		},
		{
			name: "a death, and why",
			ev: session.Event{
				Type: "session.exited", SessionID: "s1",
				Payload: session.PublicSession{ID: "s1", Error: "the pty closed"},
			},
			keep: true, text: "Ended with an error: the pty closed", level: "bad",
		},
		{
			name: "a clean exit is not a failure",
			ev: session.Event{
				Type: "session.exited", SessionID: "s1",
				Payload: session.PublicSession{ID: "s1"},
			},
			keep: true, text: "Session ended", level: "info",
		},
		{
			name: "which account it started on",
			ev: session.Event{
				Type: "session.started", SessionID: "s1",
				Payload: session.PublicSession{ID: "s1", AccountName: "work"},
			},
			keep: true, text: "Started on work", level: "info",
		},
		{
			name: "the handover at a quota limit",
			ev: session.Event{
				Type: "switch.done", SessionID: "s2",
				Message: "spare took over from work",
			},
			keep: true, text: "spare took over from work", level: "warn",
		},
		{
			// Every busy agent, every few seconds. It would be the only thing in
			// the log.
			name: "token counters are not events",
			ev: session.Event{
				Type: "session.tokens", SessionID: "s1",
				Payload: session.PublicSession{ID: "s1"},
			},
			keep: false,
		},
		{
			name: "bookkeeping is not either",
			ev:   session.Event{Type: "session.identified", SessionID: "s1"},
			keep: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := s.describe(c.ev)
			if ok != c.keep {
				t.Fatalf("kept = %v, want %v", ok, c.keep)
			}
			if !c.keep {
				return
			}
			if got.Text != c.text {
				t.Errorf("text = %q, want %q", got.Text, c.text)
			}
			if got.Level != c.level {
				t.Errorf("level = %q, want %q", got.Level, c.level)
			}
			if got.At.IsZero() {
				t.Error("no timestamp, and the time is the reason to keep it")
			}
		})
	}
}

// A long question has to survive as one readable line.
func TestActivityOneLine(t *testing.T) {
	got := oneLine("  a question\nsplit  over\tlines  ", 200)
	if got != "a question split over lines" {
		t.Errorf("flattened to %q", got)
	}
	long := oneLine(strings.Repeat("x", 500), 40)
	if n := len([]rune(long)); n != 40 {
		t.Errorf("length %d, want 40", n)
	}
	if !strings.HasSuffix(long, "…") {
		t.Errorf("a truncated line does not say it was truncated: %q", long)
	}
}

// The ring holds a bounded number, newest first, and does not keep what it
// dropped.
func TestActivityJournalBounds(t *testing.T) {
	j := &journal{}
	for i := 0; i < activityKeep+250; i++ {
		j.add(Activity{Text: string(rune('a' + i%26)), Type: "t"})
	}
	if n := len(j.entries); n != activityKeep {
		t.Fatalf("holding %d entries, want %d", n, activityKeep)
	}
	if c := cap(j.entries); c > activityKeep*3 {
		t.Errorf("backing array is %d for %d entries — the dropped ones are still alive",
			c, activityKeep)
	}

	got := j.list(3)
	if len(got) != 3 {
		t.Fatalf("asked for 3, got %d", len(got))
	}
	// Newest first: the last one added is the first one back.
	last := j.entries[len(j.entries)-1]
	if got[0].Text != last.Text {
		t.Errorf("first row is %q, want the newest (%q)", got[0].Text, last.Text)
	}

	if n := len(j.list(0)); n != activityKeep {
		t.Errorf("list(0) returned %d, want everything", n)
	}
	if got := (&journal{}).list(10); got == nil || len(got) != 0 {
		t.Errorf("an empty journal returned %v, and a list endpoint never returns null", got)
	}
}

// The endpoint: newest first, filtered by project, capped, and never null.
func TestActivityEndpoint(t *testing.T) {
	s := &Server{log: &journal{}}
	for i := 0; i < 5; i++ {
		s.log.add(Activity{Text: "p1 " + string(rune('a'+i)), ProjectID: "p1"})
		s.log.add(Activity{Text: "p2 " + string(rune('a'+i)), ProjectID: "p2"})
	}

	get := func(q string) []Activity {
		t.Helper()
		w := httptest.NewRecorder()
		s.listActivity(w, httptest.NewRequest(http.MethodGet, "/api/activity"+q, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status %d", w.Code)
		}
		if strings.TrimSpace(w.Body.String()) == "null" {
			t.Fatal("answered null; a list endpoint always returns []")
		}
		var out []Activity
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("unreadable: %v (%s)", err, w.Body.String())
		}
		return out
	}

	all := get("")
	if len(all) != 10 {
		t.Fatalf("got %d rows, want all 10", len(all))
	}
	if all[0].Text != "p2 e" {
		t.Errorf("first row is %q, want the newest", all[0].Text)
	}

	// One busy project must not push a quiet one out of its own list.
	only := get("?projectId=p1")
	if len(only) != 5 {
		t.Fatalf("filtered to %d rows, want 5", len(only))
	}
	for _, a := range only {
		if a.ProjectID != "p1" {
			t.Fatalf("a %s row came back in the p1 list", a.ProjectID)
		}
	}

	if n := len(get("?limit=3")); n != 3 {
		t.Errorf("limit=3 returned %d", n)
	}
	if n := len(get("?limit=nonsense")); n != 10 {
		t.Errorf("a limit that is not a number returned %d, want the default", n)
	}

	empty := (&Server{log: &journal{}})
	w := httptest.NewRecorder()
	empty.listActivity(w, httptest.NewRequest(http.MethodGet, "/api/activity", nil))
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Errorf("an empty log answered %s", got)
	}
}
