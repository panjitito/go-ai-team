//go:build uitest

package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ctrl-K: go to any agent, project or panel.
//
// The premise of this app is running a lot of agents at once, and the cost of
// that is getting to the right one. What needs checking is the ordering — an
// agent that wants an answer has to come before one that is merely working —
// that abbreviations find things, and that Enter actually goes there.
func TestUIPalette(t *testing.T) {
	const port = 7821
	startServer(t, port)

	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	if ready := c.evalString(t, `
new Promise(async resolve => {
  for (let i = 0; i < 80; i++) {
    if (typeof openPalette === 'function' && document.querySelectorAll('#tabbar .tab').length) {
      await new Promise(r => setTimeout(r, 400));
      return resolve('ready');
    }
    await new Promise(r => setTimeout(r, 250));
  }
  resolve('timeout');
})`); ready != "ready" {
		t.Fatalf("the UI never finished loading: %s", ready)
	}

	got := c.evalString(t, `
new Promise(async resolve => {
  const sleep = ms => new Promise(r => setTimeout(r, ms));
  try {
    S.projects = [
      { id: 'p1', name: 'mis-dashboard', path: 'C:\\work\\mis' },
      { id: 'p2', name: 'go-ai-team', path: 'C:\\work\\gat' },
    ];
    S.agents = [
      { id: 'a1', projectId: 'p1', name: 'Backend worker', role: 'Backend' },
      { id: 'a2', projectId: 'p1', name: 'Frontend', role: 'Frontend' },
      { id: 'a3', projectId: 'p2', name: 'Reviewer', role: 'Reviewer' },
    ];
    S.sessions = [
      { id: 's1', agentId: 'a1', kind: 'agent', status: 'working', switchLog: [] },
      { id: 's2', agentId: 'a2', kind: 'agent', status: 'waiting', needsYou: true, switchLog: [] },
    ];

    const out = {};
    // Opened by the keyboard, which is the whole point of it.
    document.dispatchEvent(new KeyboardEvent('keydown',
      { key: 'k', ctrlKey: true, bubbles: true, cancelable: true }));
    await sleep(150);
    out.openedByKey = !!document.querySelector('#palHost');
    if (!out.openedByKey) return resolve(JSON.stringify(out));

    const rows = () => [...document.querySelectorAll('.pal-item')].map(n => ({
      kind: n.querySelector('.pal-kind').textContent,
      title: n.querySelector('.pal-title').textContent,
      note: n.querySelector('.pal-note').textContent,
      active: n.classList.contains('active'),
    }));

    // Nothing typed: agents first, and the one waiting on a person above the
    // one that is merely busy.
    const first = rows();
    out.firstKind = first[0].kind;
    out.order = first.slice(0, 3).map(r => r.title);
    out.needsNote = first[0].note;

    // An abbreviation finds it.
    const input = document.querySelector('#palInput');
    input.value = 'bkw';
    input.dispatchEvent(new Event('input'));
    await sleep(80);
    out.fuzzy = rows().map(r => r.title);

    // A panel by name.
    input.value = 'mcp';
    input.dispatchEvent(new Event('input'));
    await sleep(80);
    out.panel = rows()[0] && rows()[0].title;

    // Nonsense matches nothing, and says so rather than showing everything.
    input.value = 'zzzzqqq';
    input.dispatchEvent(new Event('input'));
    await sleep(80);
    out.emptyRows = rows().length;
    out.emptyMsg = !!document.querySelector('.pal-empty');

    // Arrow keys move, Enter goes there.
    input.value = 'reviewer';
    input.dispatchEvent(new Event('input'));
    await sleep(80);
    out.beforeEnter = rows().map(r => r.title)[0];
    let started = null;
    const realStart = window.startAgent;
    window.startAgent = a => { started = a.name; };
    input.dispatchEvent(new KeyboardEvent('keydown',
      { key: 'Enter', bubbles: true, cancelable: true }));
    await sleep(150);
    window.startAgent = realStart;
    out.closedAfterEnter = !document.querySelector('#palHost');
    out.started = started;

    // Escape closes it without doing anything.
    openPalette();
    await sleep(100);
    document.querySelector('#palInput').dispatchEvent(new KeyboardEvent('keydown',
      { key: 'Escape', bubbles: true, cancelable: true }));
    await sleep(100);
    out.escClosed = !document.querySelector('#palHost');

    // And a way in for someone who has never heard of Ctrl-K.
    const btn = document.querySelector('#gotoBtn');
    out.btnHint = btn ? btn.querySelector('kbd').textContent.trim() : '';
    btn.click();
    await sleep(120);
    out.openedByButton = !!document.querySelector('#palHost');
    closePalette();

    resolve(JSON.stringify(out));
  } catch (e) {
    resolve(JSON.stringify({ threw: String(e && e.stack || e) }));
  }
})`)
	t.Logf("palette: %s", got)
	if strings.Contains(got, `"threw"`) {
		t.Fatalf("the page threw: %s", got)
	}

	if dir := os.Getenv("UISHOT_DIR"); dir != "" {
		c.evalString(t, `
new Promise(async r => {
  openPalette();
  await new Promise(x => setTimeout(x, 250));
  r('ok');
})`)
		res := c.call(t, "Page.captureScreenshot", map[string]any{"format": "png"})
		if data, _ := res["data"].(string); data != "" {
			if raw, err := base64.StdEncoding.DecodeString(data); err == nil {
				_ = os.WriteFile(filepath.Join(dir, "palette.png"), raw, 0o644)
			}
		}
	}

	var r struct {
		OpenedByKey      bool     `json:"openedByKey"`
		FirstKind        string   `json:"firstKind"`
		Order            []string `json:"order"`
		NeedsNote        string   `json:"needsNote"`
		Fuzzy            []string `json:"fuzzy"`
		Panel            string   `json:"panel"`
		EmptyRows        int      `json:"emptyRows"`
		EmptyMsg         bool     `json:"emptyMsg"`
		BeforeEnter      string   `json:"beforeEnter"`
		ClosedAfterEnter bool     `json:"closedAfterEnter"`
		Started          string   `json:"started"`
		EscClosed        bool     `json:"escClosed"`
		BtnHint          string   `json:"btnHint"`
		OpenedByButton   bool     `json:"openedByButton"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("unreadable: %v (%s)", err, got)
	}

	if !r.OpenedByKey {
		t.Fatal("Ctrl-K did not open it, which is the only way in")
	}
	if r.FirstKind != "agent" {
		t.Errorf("the list starts with %q; agents are what people are looking for", r.FirstKind)
	}
	// The one that wants an answer, before the one that is merely busy.
	if len(r.Order) < 2 || r.Order[0] != "Frontend" || r.Order[1] != "Backend worker" {
		t.Errorf("order = %v, want the agent needing an answer first", r.Order)
	}
	if !strings.Contains(r.NeedsNote, "needs you") {
		t.Errorf("the first row does not say why it is first: %q", r.NeedsNote)
	}
	if len(r.Fuzzy) == 0 || r.Fuzzy[0] != "Backend worker" {
		t.Errorf("\"bkw\" found %v, want Backend worker first", r.Fuzzy)
	}
	if r.Panel != "MCP servers" {
		t.Errorf("\"mcp\" found %q", r.Panel)
	}
	if r.EmptyRows != 0 || !r.EmptyMsg {
		t.Errorf("nonsense gave %d rows (message=%v); it should say nothing matches",
			r.EmptyRows, r.EmptyMsg)
	}
	if r.BeforeEnter != "Reviewer" {
		t.Errorf("before Enter the highlight was on %q", r.BeforeEnter)
	}
	if !r.ClosedAfterEnter {
		t.Error("it stayed open after choosing something")
	}
	// Reviewer has no session, so going to it starts it.
	if r.Started != "Reviewer" {
		t.Errorf("Enter started %q, want Reviewer", r.Started)
	}
	if !r.EscClosed {
		t.Error("Escape did not close it")
	}
	if !r.OpenedByButton {
		t.Error("the topbar button did not open it, so the feature is undiscoverable")
	}
	if r.BtnHint != "Ctrl K" {
		t.Errorf("the button's key hint reads %q", r.BtnHint)
	}
}
