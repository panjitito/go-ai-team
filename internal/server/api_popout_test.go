package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/uniair/go-ai-team/internal/browser"
	"github.com/uniair/go-ai-team/internal/store"
)

func windowServer(t *testing.T) (*Server, *store.Project) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	p := &store.Project{Name: "proj", Path: t.TempDir()}
	if err := st.AddProject(p); err != nil {
		t.Fatal(err)
	}
	return &Server{
		st:      st,
		baseURL: "http://localhost:7777",
		// None, so nothing tries to put a window on the screen of whoever is
		// running the tests.
		windowMode: browser.ModeNone,
	}, p
}

func askWindow(t *testing.T, s *Server, from string, body map[string]string) (int, windowResp) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/windows", strings.NewReader(string(b)))
	req.RemoteAddr = from
	w := httptest.NewRecorder()
	s.openWindow(w, req)

	var out windowResp
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// The address a pop-out opens on is composed by the server, not taken from the
// caller.
//
// A handler that opens a native window on whatever address it is handed is one
// that will eventually be handed somebody else's, and the Host header is not
// something to trust with a window. So the client names a target and the server
// supplies the rest.
func TestWindowURLIsComposedByTheServer(t *testing.T) {
	s, p := windowServer(t)

	code, got := askWindow(t, s, "127.0.0.1:5555", map[string]string{"projectId": p.ID})
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}

	u, err := url.Parse(got.URL)
	if err != nil {
		t.Fatalf("unparseable url %q: %v", got.URL, err)
	}
	if u.Host != "localhost:7777" {
		t.Errorf("host = %q, want the address the server is listening on", u.Host)
	}
	if u.Query().Get("project") != p.ID {
		t.Errorf("project = %q", u.Query().Get("project"))
	}
	if u.Query().Get("popout") != "1" {
		t.Error("the window is not marked as a pop-out, so it would keep the full frame")
	}
}

// Nothing to open is a request that says so, not one that opens a blank window.
func TestWindowNeedsATarget(t *testing.T) {
	s, _ := windowServer(t)
	if code, _ := askWindow(t, s, "127.0.0.1:5555", map[string]string{}); code != http.StatusBadRequest {
		t.Errorf("status %d for a request naming nothing", code)
	}
	if code, _ := askWindow(t, s, "127.0.0.1:5555",
		map[string]string{"projectId": "nope"}); code != http.StatusBadRequest {
		t.Errorf("status %d for a project that does not exist", code)
	}
}

// A phone asking would otherwise open a window on a desk it cannot see.
//
// It still gets the address, because a second tab on the phone is a perfectly
// reasonable answer and a button that does nothing is not.
func TestWindowFromElsewhereGetsATabInstead(t *testing.T) {
	s, p := windowServer(t)

	code, got := askWindow(t, s, "192.168.1.44:51000", map[string]string{"projectId": p.ID})
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if got.Opened {
		t.Error("opened a window on this machine for a request from another one")
	}
	if got.URL == "" {
		t.Error("no address to fall back to, so the button would do nothing")
	}
	if got.Reason == "" {
		t.Error("no reason given for not opening a window")
	}
}

// Without a UI there is no window to open one from, and saying so beats
// failing.
func TestWindowWithNoUIExplainsItself(t *testing.T) {
	s, p := windowServer(t)

	code, got := askWindow(t, s, "127.0.0.1:5555", map[string]string{"projectId": p.ID})
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if got.Opened {
		t.Error("claims to have opened a window in a mode that has none")
	}
	if !strings.Contains(got.Reason, "without a UI") {
		t.Errorf("reason = %q", got.Reason)
	}
	if got.URL == "" {
		t.Error("the caller was left with no address to open itself")
	}
}
