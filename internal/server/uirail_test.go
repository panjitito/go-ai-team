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

// The list of files an agent has changed, beside the conversation.
//
// What collects the list is tested in internal/claudefs; what needs a browser is
// the panel: that it appears only when there is something in it, that a created
// file reads differently from a changed one, that the count reaches the header
// when the panel is closed, and that closing it is remembered.
func TestUIRail(t *testing.T) {
	const port = 7817
	startServer(t, port)

	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	if ready := c.evalString(t, `
new Promise(async resolve => {
  for (let i = 0; i < 80; i++) {
    if (typeof renderRail === 'function' && document.querySelectorAll('#tabbar .tab').length) {
      await new Promise(r => setTimeout(r, 300));
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
    localStorage.removeItem('goaiteam.rail.open');

    // A session to open the conversation on. Nothing is sent to it; the rail is
    // drawn from the poll's data, which is supplied directly below.
    S.projects = [{ id: 'p1', name: 'proj', path: 'C:\\proj' }];
    S.selectedProject = 'p1';
    S.agents = [{ id: 'a1', projectId: 'p1', name: 'Dev', role: 'dev' }];
    S.sessions = [{ id: 's1', agentId: 'a1', projectId: 'p1', kind: 'agent',
                    status: 'working', switchLog: [] }];
    openAgent('s1');
    await sleep(200);
    stopChatPoll();   // the poll would overwrite the data below

    const out = {};
    const rail = () => document.querySelector('#chatRail');
    const shown = () => rail() && rail().style.display !== 'none';

    // Nothing changed yet: no panel at all, rather than an empty box.
    renderRail({ messages: [], files: [] }, 's1');
    await sleep(50);
    out.emptyShown = shown();
    out.emptyBadge = document.querySelector('#railCount').style.display;

    const files = [
      { path: 'C:/proj/internal/server/api.go', rel: 'internal/server/api.go',
        name: 'api.go', edits: 3 },
      { path: 'C:/proj/README.md', rel: 'README.md', name: 'README.md',
        edits: 1, created: true },
    ];
    renderRail({ messages: [], files }, 's1');
    await sleep(50);
    out.shown = shown();
    out.items = [...document.querySelectorAll('.rail-item')].length;
    out.first = (document.querySelector('.rail-item .rail-file') || {}).textContent;
    out.firstDir = (document.querySelector('.rail-item .rail-dir') || {}).textContent;
    out.edits = (document.querySelector('.rail-item .rail-edits') || {}).textContent;
    out.badge = document.querySelector('#railCount').textContent;
    // A created file is marked differently from a changed one.
    const marks = [...document.querySelectorAll('.rail-mark')];
    out.newMarks = marks.filter(m => m.classList.contains('new')).length;
    // And the conversation must still have room.
    const col = document.querySelector('.chat-col');
    out.colWide = Math.round(col.getBoundingClientRect().width) > 300;
    out.noOverflow = document.body.scrollWidth <= window.innerWidth + 2;

    // Closing it is remembered, and the count stays on the header.
    toggleRail();
    await sleep(80);
    out.afterClose = shown();
    out.storedClosed = localStorage.getItem('goaiteam.rail.open');
    out.badgeWhenClosed = document.querySelector('#railCount').textContent;

    toggleRail();
    await sleep(80);
    out.afterReopen = shown();

    // The terminal has no use for it: it is not the conversation.
    S.chatMode = 'term';
    renderRail({ messages: [], files }, 's1');
    await sleep(50);
    out.onTerminal = shown();
    // Left showing, so a screenshot taken after this is of the thing.
    S.chatMode = 'chat';
    RAIL.sig = '';
    renderRail({ messages: [], files }, 's1');
    await sleep(50);

    resolve(JSON.stringify(out));
  } catch (e) {
    resolve(JSON.stringify({ threw: String(e && e.stack || e) }));
  }
})`)
	t.Logf("rail: %s", got)
	if strings.Contains(got, `"threw"`) {
		t.Fatalf("the page threw: %s", got)
	}

	if dir := os.Getenv("UISHOT_DIR"); dir != "" {
		res := c.call(t, "Page.captureScreenshot", map[string]any{"format": "png"})
		if data, _ := res["data"].(string); data != "" {
			if raw, err := base64.StdEncoding.DecodeString(data); err == nil {
				_ = os.WriteFile(filepath.Join(dir, "rail.png"), raw, 0o644)
			}
		}
	}

	var r struct {
		EmptyShown      bool   `json:"emptyShown"`
		EmptyBadge      string `json:"emptyBadge"`
		Shown           bool   `json:"shown"`
		Items           int    `json:"items"`
		First           string `json:"first"`
		FirstDir        string `json:"firstDir"`
		Edits           string `json:"edits"`
		Badge           string `json:"badge"`
		NewMarks        int    `json:"newMarks"`
		ColWide         bool   `json:"colWide"`
		NoOverflow      bool   `json:"noOverflow"`
		AfterClose      bool   `json:"afterClose"`
		StoredClosed    string `json:"storedClosed"`
		BadgeWhenClosed string `json:"badgeWhenClosed"`
		AfterReopen     bool   `json:"afterReopen"`
		OnTerminal      bool   `json:"onTerminal"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("unreadable: %v (%s)", err, got)
	}

	if r.EmptyShown {
		t.Error("an agent that has changed nothing still gets a panel")
	}
	if r.EmptyBadge != "none" {
		t.Errorf("the header badge shows with nothing to count: %q", r.EmptyBadge)
	}
	if !r.Shown || r.Items != 2 {
		t.Errorf("panel shown=%v with %d items, want 2", r.Shown, r.Items)
	}
	if r.First != "api.go" {
		t.Errorf("first file = %q — the list is newest first", r.First)
	}
	if r.FirstDir != "internal/server" {
		t.Errorf("directory = %q, want the path without the filename", r.FirstDir)
	}
	if r.Edits != "×3" {
		t.Errorf("repeat count = %q", r.Edits)
	}
	if r.Badge != "2" {
		t.Errorf("header badge = %q", r.Badge)
	}
	if r.NewMarks != 1 {
		t.Errorf("%d files marked as created, want 1 — a new file is not a changed one", r.NewMarks)
	}
	if !r.ColWide {
		t.Error("the conversation has been squeezed out by the panel")
	}
	if !r.NoOverflow {
		t.Error("the panel pushed the page wider than the window")
	}
	if r.AfterClose {
		t.Error("closing the panel did not close it")
	}
	if r.StoredClosed != "0" {
		t.Errorf("the choice was not remembered (%q)", r.StoredClosed)
	}
	if r.BadgeWhenClosed != "2" {
		t.Errorf("with the panel closed the header should still say how many (%q)", r.BadgeWhenClosed)
	}
	if !r.AfterReopen {
		t.Error("reopening the panel did not reopen it")
	}
	if r.OnTerminal {
		t.Error("the panel is showing over the terminal, which is not the conversation")
	}
}
