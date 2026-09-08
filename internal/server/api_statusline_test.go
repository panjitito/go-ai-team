package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/panjitito/go-ai-team/internal/claudefs"
	"github.com/panjitito/go-ai-team/internal/session"
	"github.com/panjitito/go-ai-team/internal/store"
)

// A blank strip has two causes and only one of them is going to fix itself, so
// the answer has to say which. It used to say nothing, which is the bug being
// reported: an empty row that looks like the app losing track of something.
func TestStatusLineNote(t *testing.T) {
	withLine := t.TempDir()
	if err := claudefs.InstallStatusLine(withLine, "go-ai-team", false); err != nil {
		t.Fatal(err)
	}
	without := t.TempDir()

	s := &Server{}

	cases := []struct {
		name    string
		line    session.StatusLine
		dir     string
		wantSay string
		fixable bool
	}{
		{
			name: "a reading on screen explains itself",
			line: session.StatusLine{FiveHour: 13, HasFiveHour: true},
			dir:  without,
		},
		{
			name: "one number is enough to need no explanation",
			line: session.StatusLine{Cost: 1.5, HasCost: true},
			dir:  without,
		},
		{
			name:    "configured and nothing read yet, which arrives on its own",
			dir:     withLine,
			wantSay: "Waiting",
		},
		{
			name:    "no status-line command, which never will",
			dir:     without,
			wantSay: "no status-line command",
			fixable: true,
		},
		{
			// A session with no account bound has a different problem and this
			// is not the place to raise it.
			name: "no account to say anything about",
			dir:  "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			note, fixable := s.statusLineNote(c.line, c.dir)
			if c.wantSay == "" {
				if note != "" {
					t.Errorf("explained a strip that needs none: %q", note)
				}
				return
			}
			if !strings.Contains(note, c.wantSay) {
				t.Errorf("note = %q, want it to mention %q", note, c.wantSay)
			}
			if fixable != c.fixable {
				t.Errorf("fixable = %v, want %v", fixable, c.fixable)
			}
		})
	}
}

// The install endpoint edits a file belonging to whoever signed in with that
// account, so what it refuses matters as much as what it writes.
func TestAccountStatusLineEndpoints(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	acc := &store.Account{Name: "work", Provider: store.ProviderClaude, Dir: dir}
	if err := st.AddAccount(acc); err != nil {
		t.Fatal(err)
	}
	s := &Server{st: st}

	call := func(method string, h http.HandlerFunc, body string) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, "/api/accounts/"+acc.ID+"/statusline", strings.NewReader(body))
		r.SetPathValue("id", acc.ID)
		w := httptest.NewRecorder()
		h(w, r)
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: unreadable (%d): %s", method, w.Code, w.Body.String())
		}
		out["_status"] = float64(w.Code)
		return out
	}

	got := call(http.MethodGet, s.getAccountStatusLine, "")
	if got["configured"] != false {
		t.Errorf("a fresh account reported %v", got["configured"])
	}
	if would, _ := got["wouldInstall"].(string); !strings.Contains(would, "statusline") {
		t.Errorf("nothing shown for what would be written: %q", would)
	}

	got = call(http.MethodPut, s.putAccountStatusLine, "")
	if got["configured"] != true || got["ours"] != true {
		t.Fatalf("install answered %v", got)
	}
	// The surprising part has to be said, because it is what makes somebody
	// think the install failed.
	if note, _ := got["note"].(string); !strings.Contains(note, "Restart") {
		t.Errorf("nothing said about running agents keeping the old line: %q", note)
	}
	if _, ok := claudefs.StatusLineOf(dir); !ok {
		t.Error("nothing reached the settings file")
	}

	got = call(http.MethodDelete, s.deleteAccountStatusLine, "")
	if got["configured"] != false {
		t.Errorf("delete answered %v", got)
	}

	// Somebody else's line is theirs.
	writeSettingsFile(t, dir, `{"statusLine":{"type":"command","command":"node mine.js"}}`)
	got = call(http.MethodPut, s.putAccountStatusLine, "")
	if got["_status"] == float64(http.StatusOK) {
		t.Errorf("it replaced a status line nobody asked it to: %v", got)
	}
	got = call(http.MethodDelete, s.deleteAccountStatusLine, "")
	if got["_status"] == float64(http.StatusOK) {
		t.Errorf("it removed a status line it did not install: %v", got)
	}
	if sl, _ := claudefs.StatusLineOf(dir); sl.Command != "node mine.js" {
		t.Errorf("the existing command became %q", sl.Command)
	}

	// Asked for in so many words, it goes in.
	got = call(http.MethodPut, s.putAccountStatusLine, `{"replace":true}`)
	if got["ours"] != true {
		t.Errorf("replace answered %v", got)
	}
}

func writeSettingsFile(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
