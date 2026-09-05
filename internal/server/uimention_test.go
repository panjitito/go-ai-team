//go:build uitest

package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// liveSessionCWD asks the running server which directory a live agent is in, so
// the test can put files where the completion will find them.
func liveSessionCWD(t *testing.T, port int) string {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/sessions", port))
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var rows []struct {
		Kind   string `json:"kind"`
		Status string `json:"status"`
		CWD    string `json:"cwd"`
	}
	if json.NewDecoder(resp.Body).Decode(&rows) != nil {
		return ""
	}
	for _, r := range rows {
		if r.Kind == "agent" && r.Status != "exited" && r.Status != "error" && r.CWD != "" {
			return r.CWD
		}
	}
	return ""
}

// "@" completes a path from the project's files.
//
// The ranking is tested against a corpus in api_find_test.go; what needs a
// browser is the part that is all interaction — that the list only opens on a
// real mention, that the arrow keys move through it, and above all that Enter
// accepts the highlighted path instead of sending half a sentence to the agent.
// That last one is the whole risk: the composer's own handler treats Enter as
// "send", and it is registered first.
func TestUIMentions(t *testing.T) {
	port := 7788
	if _, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/settings", port)); err != nil {
		t.Skip("no server on 7788; start one first")
	}
	// Candidates of its own, so moving through the list is actually exercised.
	// A staging project may hold one matching file, and arrowing down a list of
	// one proves nothing.
	cwd := liveSessionCWD(t, port)
	if cwd == "" {
		t.Skip("no live agent on 7788 to borrow a project from")
	}
	for _, n := range []string{"mention-alpha.md", "mention-beta.md", "mention-gamma.md"} {
		p := filepath.Join(cwd, n)
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Skipf("cannot write into the project: %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(p) })
	}

	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	got := c.evalString(t, `
new Promise(async resolve => {
  const sleep = ms => new Promise(r => setTimeout(r, ms));
  for (let i = 0; i < 60; i++) {
    if (document.querySelectorAll('.card').length) break;
    await sleep(250);
  }
  const live = S.sessions.find(s => s.kind === 'agent' && s.status !== 'exited');
  if (!live) return resolve('NO LIVE SESSION');
  openTerm(live.id);
  for (let i = 0; i < 60; i++) { await sleep(300); if (document.querySelector('#composerBox')) break; }
  const box = document.querySelector('#composerBox');
  if (!box) return resolve('NO COMPOSER');

  // sendComposer must never run during this test: a stray message would go to a
  // real agent. Swapped out, and every Enter below is checked against it.
  let sent = 0;
  const realSend = window.sendComposer;
  window.sendComposer = () => { sent++; };

  const type = async v => {
    box.focus();
    box.value = v;
    box.setSelectionRange(v.length, v.length);
    box.dispatchEvent(new Event('input'));
    await sleep(700);
  };
  const key = k => box.dispatchEvent(new KeyboardEvent('keydown',
    { key: k, bubbles: true, cancelable: true }));

  const out = {};
  try {
    // Not a mention: an email address has an "@" in the middle of a word.
    await type('write to someone@example.com about it');
    out.emailOpened = !!document.querySelector('.mention-menu');

    // A real one.
    await type('look at @mention-');
    const menu = document.querySelector('.mention-menu');
    if (!menu) return resolve('NO MENU for @mention-');
    out.rows = [...menu.querySelectorAll('.mention-item .mention-name')].map(n => n.textContent);
    out.firstActive = !!menu.querySelector('.mention-item.active');
    // The composer sits at the bottom, so the list has to open upwards.
    out.above = menu.getBoundingClientRect().bottom <= box.getBoundingClientRect().top + 1;
    out.onScreen = menu.getBoundingClientRect().top >= 0;

    // Arrow keys move the highlight.
    key('ArrowDown');
    await sleep(150);
    const items = [...document.querySelectorAll('.mention-item')];
    out.movedTo = items.findIndex(n => n.classList.contains('active'));

    // Enter accepts, and must not send.
    key('Enter');
    await sleep(250);
    out.sentOnAccept = sent;
    out.value = box.value;
    out.menuGone = !document.querySelector('.mention-menu');

    // With no list open, Enter is the send key again.
    await type('plain message');
    key('Enter');
    await sleep(150);
    out.sentAfter = sent;

    // Escape dismisses without accepting.
    await type('look at @mention-');
    key('Escape');
    await sleep(150);
    out.escapeClosed = !document.querySelector('.mention-menu');

    box.value = '';
    box.dispatchEvent(new Event('input'));
  } finally {
    window.sendComposer = realSend;
  }
  resolve(JSON.stringify(out));
})`)
	t.Logf("mentions: %s", got)

	if dir := os.Getenv("UISHOT_DIR"); dir != "" {
		// Re-open the list, so the picture is of the thing rather than of the
		// composer after it closed.
		c.evalString(t, `
new Promise(async resolve => {
  const box = document.querySelector('#composerBox');
  box.focus();
  box.value = 'have a look at @mention-';
  box.setSelectionRange(box.value.length, box.value.length);
  box.dispatchEvent(new Event('input'));
  await new Promise(r => setTimeout(r, 800));
  resolve('ok');
})`)
		res := c.call(t, "Page.captureScreenshot", map[string]any{"format": "png"})
		if data, _ := res["data"].(string); data != "" {
			if raw, err := base64.StdEncoding.DecodeString(data); err == nil {
				p := filepath.Join(dir, "mentions.png")
				_ = os.WriteFile(p, raw, 0o644)
				t.Logf("saved %s", p)
			}
		}
	}

	if got == "NO LIVE SESSION" || got == "NO COMPOSER" {
		t.Skip(got)
	}

	var r struct {
		EmailOpened  bool     `json:"emailOpened"`
		Rows         []string `json:"rows"`
		FirstActive  bool     `json:"firstActive"`
		Above        bool     `json:"above"`
		OnScreen     bool     `json:"onScreen"`
		MovedTo      int      `json:"movedTo"`
		SentOnAccept int      `json:"sentOnAccept"`
		Value        string   `json:"value"`
		MenuGone     bool     `json:"menuGone"`
		SentAfter    int      `json:"sentAfter"`
		EscapeClosed bool     `json:"escapeClosed"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("unreadable: %v (%s)", err, got)
	}

	if r.EmailOpened {
		t.Error("an email address opened the path list; \"@\" only starts a mention at a word boundary")
	}
	if len(r.Rows) == 0 {
		t.Error("no candidates offered for a path that exists")
	}
	if !r.FirstActive {
		t.Error("nothing is highlighted, so Enter would have nothing to accept")
	}
	if !r.Above || !r.OnScreen {
		t.Errorf("list placement wrong: above=%v onScreen=%v", r.Above, r.OnScreen)
	}
	if r.MovedTo != 1 {
		t.Errorf("ArrowDown moved the highlight to %d, want 1", r.MovedTo)
	}
	if r.SentOnAccept != 0 {
		t.Error("Enter sent the message instead of accepting the highlighted path")
	}
	if !r.MenuGone {
		t.Error("the list stayed open after accepting")
	}
	if !wantsPath(r.Value) {
		t.Errorf("composer reads %q — accepting should have inserted a path", r.Value)
	}
	if r.SentAfter != 1 {
		t.Errorf("with no list open Enter must send again (sent=%d)", r.SentAfter)
	}
	if !r.EscapeClosed {
		t.Error("Escape did not dismiss the list")
	}
}

func wantsPath(v string) bool {
	return len(v) > len("look at @") && v[:len("look at @")] == "look at @"
}
