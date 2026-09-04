//go:build uitest

package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Signing in must open the terminal, not the conversation view.
//
// The conversation view renders the transcript a Claude agent writes. A sign-in
// PTY has no transcript and never will, so opening it in chat mode showed an
// empty pane with the terminal — the thing you actually sign in with — hidden
// behind it. The instruction "type /login and press Enter" then appeared to do
// nothing, because what was being typed into was not on screen.
//
// This spawns a real sign-in PTY, which only opens the CLI. It authenticates
// nothing and types no credentials.
func TestUILoginOpensTerminal(t *testing.T) {
	port := 7788
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	if _, err := http.Get(base + "/api/settings"); err != nil {
		t.Skip("no server on 7788; start one first")
	}

	// Any account will do — the view must behave the same for all of them.
	accs := getJSON[[]map[string]any](t, base+"/api/accounts")
	if len(accs) == 0 {
		t.Skip("no accounts registered")
	}
	id, _ := accs[0]["id"].(string)
	name, _ := accs[0]["name"].(string)

	res := postJSON[map[string]any](t, base+"/api/accounts/"+id+"/login", nil)
	sessWrap, _ := res["session"].(map[string]any)
	if sessWrap == nil {
		t.Fatalf("no session in the login response: %v", res)
	}
	sid, _ := sessWrap["id"].(string)
	t.Logf("sign-in session %s on %s", sid, name)
	defer func() {
		_, _ = http.Post(base+"/api/sessions/"+sid+"/stop", "application/json", nil)
	}()

	c := launchChrome(t)
	c.openTarget(t, base+"/")

	got := c.evalString(t, fmt.Sprintf(`
new Promise(async resolve => {
  const sleep = ms => new Promise(r => setTimeout(r, ms));
  for (let i = 0; i < 60; i++) {
    if (S.sessions && S.sessions.length) break;
    await sleep(250);
  }
  await loadAll();
  openTerm(%q);
  await sleep(1500);

  const termHost = document.querySelector('#termHost');
  const comp = document.querySelector('.composer-wrap');
  const vis = e => !!e && e.style.display !== 'none';
  resolve(JSON.stringify({
    chatMode: S.chatMode,
    chatPref: S.chatPref,
    terminalVisible: vis(termHost),
    composerVisible: vis(comp),
    chatBodyVisible: vis(document.querySelector('#chatBody')),
    toggleOffered: document.querySelectorAll('[data-mode]').length,
    // Only inputs a person can actually type into. xterm keeps its own
    // offscreen textarea — that one IS the terminal's input, so it is the
    // single input this view should have.
    visibleComposers: (comp && vis(comp) ? 1 : 0),
    terminalAcceptsTyping: !!(termHost && termHost.querySelector('textarea')),
    title: (document.querySelector('.chat-title') || {}).textContent,
    termRows: (termHost && termHost.innerText || '').trim().length,
  }));
})`, sid))
	t.Logf("sign-in view: %s", got)

	var r struct {
		ChatMode              string `json:"chatMode"`
		ChatPref              string `json:"chatPref"`
		TerminalVisible       bool   `json:"terminalVisible"`
		ComposerVisible       bool   `json:"composerVisible"`
		ChatBodyVisible       bool   `json:"chatBodyVisible"`
		ToggleOffered         int    `json:"toggleOffered"`
		VisibleComposers      int    `json:"visibleComposers"`
		TerminalAcceptsTyping bool   `json:"terminalAcceptsTyping"`
		Title                 string `json:"title"`
		TermRows              int    `json:"termRows"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("unreadable result: %v", err)
	}

	if r.ChatMode != "term" {
		t.Errorf("sign-in opened in %q mode, want \"term\": there is nothing to show in chat", r.ChatMode)
	}
	if !r.TerminalVisible {
		t.Error("the terminal is hidden on the sign-in view; it is the only way to sign in")
	}
	if r.ChatBodyVisible {
		t.Error("the empty conversation pane is covering the terminal")
	}
	if r.ComposerVisible {
		t.Error("the composer is shown next to the terminal: two inputs again, and it pastes rather than types")
	}
	if r.ToggleOffered != 0 {
		t.Errorf("the Chat/Terminal toggle is offered on a session with no conversation (%d buttons)", r.ToggleOffered)
	}
	if r.VisibleComposers != 0 {
		t.Errorf("found %d visible composer inputs on a sign-in view, want 0 — you type into the terminal", r.VisibleComposers)
	}
	if !r.TerminalAcceptsTyping {
		t.Error("the terminal has no input element, so typing cannot reach the CLI")
	}
	if !strings.Contains(r.Title, "Sign in") {
		t.Errorf("title = %q, want it to say Sign in", r.Title)
	}
	if r.TermRows == 0 {
		t.Error("the terminal is empty: the CLI's output is not reaching it")
	}
	// Opening a sign-in must not change what agents open in.
	if r.ChatPref == "term" {
		t.Error("opening a sign-in changed the remembered preference to terminal")
	}
}

func getJSON[T any](t *testing.T, url string) T {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out T
	b, _ := io.ReadAll(res.Body)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("GET %s: %v (%s)", url, err, truncate(string(b)))
	}
	return out
}

func postJSON[T any](t *testing.T, url string, body io.Reader) T {
	t.Helper()
	res, err := http.Post(url, "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		t.Fatalf("POST %s: %d %s", url, res.StatusCode, truncate(string(b)))
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("POST %s: %v (%s)", url, err, truncate(string(b)))
	}
	return out
}

func truncate(s string) string {
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
