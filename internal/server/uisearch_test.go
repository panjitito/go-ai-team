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

// Searching what the agents actually said.
//
// The scan itself is tested in internal/claudefs. What needs a browser is the
// reading: that the query is marked in the snippet without a transcript full of
// angle brackets becoming markup, that a conversation still open leads back to
// its agent while an old one says plainly that it is history, that the footer
// admits when the search stopped early, and that a result opens out rather than
// making you go somewhere to read it.
func TestUISearch(t *testing.T) {
	const port = 7827
	startServer(t, port)

	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	if ready := c.evalString(t, `
new Promise(async resolve => {
  for (let i = 0; i < 80; i++) {
    if (typeof openFind === 'function' && document.querySelectorAll('#tabbar .tab').length) {
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
    S.projects = [{ id: 'p1', name: 'mis-dashboard', path: 'C:\\work\\mis' }];
    S.selectedProject = 'p1';

    const answer = {
      files: 12, total: 40, bytes: 264 * 1048576, millis: 968, truncated: true,
      hits: [
        { sessionId: 'aaaaaaaa-1111', dir: 'C:/u/.claude', when: new Date().toISOString(),
          role: 'assistant', account: 'work', liveSession: 's1', agentName: 'Backend worker',
          snippet: '…the ROUNDING was in the <tax> column…',
          text: 'the ROUNDING was in the <tax> column, and the fix is one line.' },
        { sessionId: 'bbbbbbbb-2222', dir: 'C:/u/.claude-ep', sub: 'agent-a19',
          when: new Date(Date.now() - 3 * 86400000).toISOString(),
          role: 'user', account: 'spare', projectName: 'Android',
          cwd: 'C:/work/Android', snippet: 'please fix the rounding',
          text: 'please fix the rounding' },
      ],
    };
    const asked = [];
    const realApi = window.tryApi;
    window.tryApi = async path => {
      asked.push(path);
      if (path.startsWith('/search')) return answer;
      return realApi(path);
    };

    const out = {};

    // Ctrl-Shift-F, from wherever the cursor happens to be.
    document.dispatchEvent(new KeyboardEvent('keydown',
      { key: 'F', ctrlKey: true, shiftKey: true, bubbles: true, cancelable: true }));
    await sleep(150);
    out.openedByKey = !!document.querySelector('#findHost');
    if (!out.openedByKey) return resolve(JSON.stringify(out));

    // Nothing has been asked for yet, so nothing has been searched for.
    out.askedBeforeTyping = asked.filter(p => p.startsWith('/search')).length;

    const input = document.querySelector('#findInput');
    input.value = 'rounding';
    input.dispatchEvent(new KeyboardEvent('keydown',
      { key: 'Enter', bubbles: true, cancelable: true }));
    await sleep(300);

    out.query = asked[asked.length - 1];
    const items = [...document.querySelectorAll('.find-item')];
    out.count = items.length;
    out.marks = [...document.querySelectorAll('.find-snip mark')].map(m => m.textContent);
    // The transcript had a tag in it and it must still be text.
    out.snippetText = (document.querySelector('.find-snip') || {}).textContent;
    out.noInjectedTag = !document.querySelector('.find-snip tax');
    out.who = [...document.querySelectorAll('.find-who')].map(n => n.textContent);
    out.when = [...document.querySelectorAll('.find-when')].map(n => n.textContent);
    out.buttons = items.map(n => !!n.querySelector('button'));
    out.oldLabel = (document.querySelector('.find-old') || {}).textContent;
    out.subPills = items.map(n => [...n.querySelectorAll('.pill')].map(p => p.textContent).join(','));
    out.foot = (document.querySelector('#findFoot') || {}).textContent;

    // Clicking opens the message out; clicking again folds it away.
    items[0].click();
    await sleep(60);
    out.expanded = !!items[0].querySelector('.find-full');
    out.expandedText = (items[0].querySelector('.find-full') || {}).textContent;
    items[0].click();
    await sleep(60);
    out.collapsed = !items[0].querySelector('.find-full');

    // The button on a live conversation goes to the agent, and does not also
    // count as a click on the row.
    let opened = null;
    const realOpen = window.openTerm;
    window.openTerm = id => { opened = id; };
    items[0].querySelector('button').click();
    await sleep(120);
    window.openTerm = realOpen;
    out.opened = opened;
    out.closedAfterOpen = !document.querySelector('#findHost');

    // Scope: everywhere drops the project from the question.
    openFind('rounding');
    await sleep(300);
    setFindScope('all');
    await sleep(300);
    out.allQuery = asked[asked.length - 1];
    // Searching everywhere, an answer has to say where it came from.
    out.projsAll = [...document.querySelectorAll('.find-proj')].map(n => n.textContent);
    setFindScope('project');
    await sleep(300);
    out.projsScoped = [...document.querySelectorAll('.find-proj')].length;

    // Escape closes it.
    document.querySelector('#findInput').dispatchEvent(new KeyboardEvent('keydown',
      { key: 'Escape', bubbles: true, cancelable: true }));
    await sleep(100);
    out.escClosed = !document.querySelector('#findHost');

    // Nothing found says so.
    window.tryApi = async () => ({ hits: [], files: 3, total: 3, bytes: 1024, millis: 12 });
    openFind('nothingatall');
    await sleep(300);
    out.emptyMsg = !!document.querySelector('.pal-empty');
    out.emptyFoot = (document.querySelector('#findFoot') || {}).textContent;
    closeFind();

    window.tryApi = realApi;
    resolve(JSON.stringify(out));
  } catch (e) {
    resolve(JSON.stringify({ threw: String(e && e.stack || e) }));
  }
})`)
	t.Logf("search: %s", got)
	if strings.Contains(got, `"threw"`) {
		t.Fatalf("the page threw: %s", got)
	}

	if dir := os.Getenv("UISHOT_DIR"); dir != "" {
		c.evalString(t, `
new Promise(async r => {
  const now = new Date();
  window.tryApi = async () => ({
    files: 12, total: 40, bytes: 264 * 1048576, millis: 968, truncated: true,
    hits: [
      { sessionId: 'aaaaaaaa-1111', when: now.toISOString(), role: 'assistant',
        account: 'work', liveSession: 's1', agentName: 'Backend worker',
        snippet: '…the rounding was in the tax column, and the fix is one line in the report builder…',
        text: 'the rounding was in the tax column.' },
      { sessionId: 'bbbbbbbb-2222', when: new Date(Date.now() - 86400000).toISOString(),
        role: 'user', account: 'work',
        snippet: 'please fix the rounding on the invoice totals before Friday' },
      { sessionId: 'cccccccc-3333', when: new Date(Date.now() - 5 * 86400000).toISOString(),
        role: 'assistant', account: 'spare',
        snippet: '…Bash — grep -rn "rounding" app/Reports…' },
    ],
  });
  openFind('rounding');
  await new Promise(x => setTimeout(x, 500));
  r('ok');
})`)
		res := c.call(t, "Page.captureScreenshot", map[string]any{"format": "png"})
		if data, _ := res["data"].(string); data != "" {
			if raw, err := base64.StdEncoding.DecodeString(data); err == nil {
				_ = os.WriteFile(filepath.Join(dir, "search.png"), raw, 0o644)
			}
		}
	}

	var r struct {
		OpenedByKey       bool     `json:"openedByKey"`
		AskedBeforeTyping int      `json:"askedBeforeTyping"`
		Query             string   `json:"query"`
		Count             int      `json:"count"`
		Marks             []string `json:"marks"`
		SnippetText       string   `json:"snippetText"`
		NoInjectedTag     bool     `json:"noInjectedTag"`
		Who               []string `json:"who"`
		When              []string `json:"when"`
		Buttons           []bool   `json:"buttons"`
		OldLabel          string   `json:"oldLabel"`
		SubPills          []string `json:"subPills"`
		Foot              string   `json:"foot"`
		Expanded          bool     `json:"expanded"`
		ExpandedText      string   `json:"expandedText"`
		Collapsed         bool     `json:"collapsed"`
		Opened            string   `json:"opened"`
		ClosedAfterOpen   bool     `json:"closedAfterOpen"`
		AllQuery          string   `json:"allQuery"`
		ProjsAll          []string `json:"projsAll"`
		ProjsScoped       int      `json:"projsScoped"`
		EscClosed         bool     `json:"escClosed"`
		EmptyMsg          bool     `json:"emptyMsg"`
		EmptyFoot         string   `json:"emptyFoot"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("unreadable: %v (%s)", err, got)
	}

	if !r.OpenedByKey {
		t.Fatal("Ctrl-Shift-F did not open it")
	}
	if r.AskedBeforeTyping != 0 {
		t.Errorf("%d searches ran before anything was typed; the scan is not free",
			r.AskedBeforeTyping)
	}
	if !strings.Contains(r.Query, "q=rounding") || !strings.Contains(r.Query, "projectId=p1") {
		t.Errorf("asked %q, want the query scoped to the open project", r.Query)
	}
	if r.Count != 2 {
		t.Errorf("%d results drawn, want 2", r.Count)
	}
	// Marked in both, and case-insensitively: the transcript said ROUNDING.
	if len(r.Marks) != 2 || r.Marks[0] != "ROUNDING" || r.Marks[1] != "rounding" {
		t.Errorf("highlights = %v", r.Marks)
	}
	if !strings.Contains(r.SnippetText, "<tax>") || !r.NoInjectedTag {
		t.Errorf("a transcript containing a tag was not kept as text: %q", r.SnippetText)
	}
	if len(r.Who) != 2 || r.Who[0] != "Backend worker" || r.Who[1] != "You" {
		t.Errorf("who said it = %v", r.Who)
	}
	if len(r.When) != 2 || !strings.HasPrefix(r.When[0], "today ") {
		t.Errorf("times = %v, want today named rather than dated", r.When)
	}
	// One conversation is still open and one is not, and they must not look the
	// same: offering to open something that is gone is worse than saying so.
	if len(r.Buttons) != 2 || !r.Buttons[0] || r.Buttons[1] {
		t.Errorf("open buttons = %v, want one only on the live conversation", r.Buttons)
	}
	if !strings.Contains(r.OldLabel, "bbbbbbbb") {
		t.Errorf("the old conversation is not identified: %q", r.OldLabel)
	}
	// Most transcripts on disk are a subagent's, and a hit in one is a different
	// claim from a hit in the conversation.
	if len(r.SubPills) != 2 || r.SubPills[0] != "assistant" || r.SubPills[1] != "user,subagent" {
		t.Errorf("row labels = %v", r.SubPills)
	}
	if !strings.Contains(r.Foot, "2 matches") || !strings.Contains(r.Foot, "12 of 40") {
		t.Errorf("footer = %q, want what was found and how much was read", r.Foot)
	}
	if !strings.Contains(r.Foot, "stopped early") {
		t.Errorf("a truncated search does not admit it: %q", r.Foot)
	}
	if !r.Expanded || !strings.Contains(r.ExpandedText, "one line") {
		t.Errorf("clicking did not open the message out (%v, %q)", r.Expanded, r.ExpandedText)
	}
	if !r.Collapsed {
		t.Error("clicking again did not fold it away")
	}
	if r.Opened != "s1" {
		t.Errorf("the open button went to %q", r.Opened)
	}
	if !r.ClosedAfterOpen {
		t.Error("going to the agent left the search sitting over the top of it")
	}
	if strings.Contains(r.AllQuery, "projectId") {
		t.Errorf("Everywhere still asked for one project: %q", r.AllQuery)
	}
	// Across every project, a result has to say which one it came from — and
	// inside one, saying it on every row is just noise.
	if len(r.ProjsAll) != 1 || r.ProjsAll[0] != "Android" {
		t.Errorf("project labels searching everywhere = %v", r.ProjsAll)
	}
	if r.ProjsScoped != 0 {
		t.Errorf("%d project labels while scoped to one project", r.ProjsScoped)
	}
	if !r.EscClosed {
		t.Error("Escape did not close it")
	}
	if !r.EmptyMsg || !strings.Contains(r.EmptyFoot, "0 matches") {
		t.Errorf("nothing found: message=%v footer=%q", r.EmptyMsg, r.EmptyFoot)
	}
}
