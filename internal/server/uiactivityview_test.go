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

// The log, read the morning after.
//
// What the server keeps is tested in Go; what needs a browser is the reading of
// it — that the day breaks are right and say "Today" rather than a date, that a
// question and a death stand out from a turn merely ending, that a row for a
// session still running takes you to it and a row for one long gone does not
// pretend to, and that scoping to one project actually asks for one project.
func TestUIActivityView(t *testing.T) {
	const port = 7825
	startServer(t, port)

	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	if ready := c.evalString(t, `
new Promise(async resolve => {
  for (let i = 0; i < 80; i++) {
    if (typeof viewActivity === 'function' && document.querySelectorAll('#tabbar .tab').length) {
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
    S.selectedProject = 'p1';
    S.agents = [{ id: 'a1', projectId: 'p1', name: 'Backend worker' }];
    // One session still running, so a row can lead somewhere, and none for the
    // agent that died overnight.
    S.sessions = [{ id: 's1', agentId: 'a1', projectId: 'p1', kind: 'agent',
                    status: 'waiting', switchLog: [] }];

    const now = new Date();
    const at = (dayOffset, h, m) => {
      const d = new Date(now.getFullYear(), now.getMonth(), now.getDate() - dayOffset, h, m);
      return d.toISOString();
    };
    const rows = [
      { at: at(0, 9, 5), type: 'session.needs-you', sessionId: 's1', agentId: 'a1',
        agentName: 'Backend worker', projectId: 'p1', projectName: 'mis-dashboard',
        text: 'Asked: Which browser should I use?', level: 'warn' },
      { at: at(0, 8, 40), type: 'session.status', sessionId: 's1', agentId: 'a1',
        agentName: 'Backend worker', projectId: 'p1', projectName: 'mis-dashboard',
        text: 'Finished a turn', level: 'info' },
      { at: at(1, 2, 14), type: 'session.exited', sessionId: 'gone', agentId: 'a9',
        agentName: 'Night shift', projectId: 'p2', projectName: 'go-ai-team',
        text: 'Ended with an error: the pty closed', level: 'bad' },
    ];

    // Stand in for the server so the view can be read without a night's history
    // behind it. The query is recorded, because scoping is half the feature.
    const asked = [];
    const realApi = window.tryApi;
    window.tryApi = async path => {
      asked.push(path);
      if (path.startsWith('/activity')) {
        return path.includes('projectId=p1') ? rows.filter(r => r.projectId === 'p1') : rows;
      }
      return realApi(path);
    };

    const out = {};
    const host = document.querySelector('#main');

    // Scoped to the project, which is where it opens.
    ACT.scope = 'project';
    host.innerHTML = '';
    await viewActivity(host);
    out.scopedQuery = asked[asked.length - 1];
    out.scopedRows = host.querySelectorAll('.act-row').length;

    // Everything.
    ACT.scope = 'all';
    host.innerHTML = '';
    asked.length = 0;
    await viewActivity(host);
    out.allQuery = asked[asked.length - 1];

    const read = sel => [...host.querySelectorAll(sel)].map(n => n.textContent);
    out.days = read('.act-day');
    out.times = read('.act-time');
    out.who = read('.act-who');
    out.texts = read('.act-text');
    out.projects = read('.act-proj');

    const items = [...host.querySelectorAll('.act-row')];
    out.levels = items.map(n => n.classList.contains('bad') ? 'bad'
      : n.classList.contains('warn') ? 'warn' : 'info');
    // A row leads somewhere only when there is still a session to lead to.
    out.clickable = items.map(n => n.classList.contains('go'));

    let opened = null;
    const realOpen = window.openTerm;
    window.openTerm = id => { opened = id; };
    items[0].click();
    items[2].click();          // the dead one: nothing should happen
    window.openTerm = realOpen;
    out.opened = opened;

    // Nothing at all reads as nothing, not as a broken view.
    window.tryApi = async () => [];
    host.innerHTML = '';
    await viewActivity(host);
    out.emptyRows = host.querySelectorAll('.act-row').length;
    out.emptyMsg = !!host.querySelector('.empty h3');

    window.tryApi = realApi;
    resolve(JSON.stringify(out));
  } catch (e) {
    resolve(JSON.stringify({ threw: String(e && e.stack || e) }));
  }
})`)
	t.Logf("activity: %s", got)
	if strings.Contains(got, `"threw"`) {
		t.Fatalf("the page threw: %s", got)
	}

	if dir := os.Getenv("UISHOT_DIR"); dir != "" {
		c.evalString(t, `
new Promise(async r => {
  const now = new Date();
  const at = (o, h, m) => new Date(now.getFullYear(), now.getMonth(), now.getDate() - o, h, m).toISOString();
  const row = (o, h, m, who, proj, text, level, sid) =>
    ({ at: at(o, h, m), sessionId: sid || '', agentName: who, projectName: proj, text, level });
  const shot = [
    row(0, 9, 5,  'Backend worker', 'mis-dashboard', 'Asked: Which browser should I use?', 'warn', 's1'),
    row(0, 8, 40, 'Backend worker', 'mis-dashboard', 'Finished a turn', 'info', 's1'),
    row(0, 8, 12, 'Frontend',       'mis-dashboard', 'Started on work', 'info', 's1'),
    row(1, 23, 50,'Reviewer',       'go-ai-team',    'spare took over from work', 'warn'),
    row(1, 23, 49,'Reviewer',       'go-ai-team',    'work is out of quota until 04:00', 'warn'),
    row(1, 2, 14, 'Night shift',    'go-ai-team',    'Ended with an error: the pty closed', 'bad'),
  ];
  window.tryApi = async p => p.startsWith('/activity') ? shot : [];
  S.view = 'activity';
  ACT.scope = 'all';
  renderMain();
  await new Promise(x => setTimeout(x, 500));
  r('ok');
})`)
		res := c.call(t, "Page.captureScreenshot", map[string]any{"format": "png"})
		if data, _ := res["data"].(string); data != "" {
			if raw, err := base64.StdEncoding.DecodeString(data); err == nil {
				_ = os.WriteFile(filepath.Join(dir, "activity.png"), raw, 0o644)
			}
		}
	}

	var r struct {
		ScopedQuery string   `json:"scopedQuery"`
		ScopedRows  int      `json:"scopedRows"`
		AllQuery    string   `json:"allQuery"`
		Days        []string `json:"days"`
		Times       []string `json:"times"`
		Who         []string `json:"who"`
		Texts       []string `json:"texts"`
		Projects    []string `json:"projects"`
		Levels      []string `json:"levels"`
		Clickable   []bool   `json:"clickable"`
		Opened      string   `json:"opened"`
		EmptyRows   int      `json:"emptyRows"`
		EmptyMsg    bool     `json:"emptyMsg"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("unreadable: %v (%s)", err, got)
	}

	if !strings.Contains(r.ScopedQuery, "projectId=p1") {
		t.Errorf("the project scope asked for %q", r.ScopedQuery)
	}
	if r.ScopedRows != 2 {
		t.Errorf("the project scope drew %d rows, want the 2 from that project", r.ScopedRows)
	}
	if strings.Contains(r.AllQuery, "projectId") {
		t.Errorf("Everything still asked for one project: %q", r.AllQuery)
	}

	// Two days, named rather than dated, newest first.
	if len(r.Days) != 2 || r.Days[0] != "Today" || r.Days[1] != "Yesterday" {
		t.Errorf("day headings = %v", r.Days)
	}
	if len(r.Times) != 3 || r.Times[0] != "09:05" || r.Times[2] != "02:14" {
		t.Errorf("times = %v", r.Times)
	}
	if len(r.Who) != 3 || r.Who[2] != "Night shift" {
		t.Errorf("agents = %v", r.Who)
	}
	if len(r.Texts) == 0 || !strings.HasPrefix(r.Texts[0], "Asked:") {
		t.Errorf("texts = %v", r.Texts)
	}
	// Across projects the project has to be on the row, or the log is ambiguous.
	if len(r.Projects) != 3 || r.Projects[2] != "go-ai-team" {
		t.Errorf("projects = %v, want one on every row when showing everything", r.Projects)
	}
	if len(r.Levels) != 3 || r.Levels[0] != "warn" || r.Levels[1] != "info" || r.Levels[2] != "bad" {
		t.Errorf("levels = %v — a question and a death must not read like a turn ending", r.Levels)
	}
	if len(r.Clickable) != 3 || !r.Clickable[0] || r.Clickable[2] {
		t.Errorf("clickable = %v; only a row with a session behind it leads anywhere", r.Clickable)
	}
	if r.Opened != "s1" {
		t.Errorf("clicking opened %q, want the session on that row", r.Opened)
	}
	if r.EmptyRows != 0 || !r.EmptyMsg {
		t.Errorf("an empty log drew %d rows and %v of an explanation", r.EmptyRows, r.EmptyMsg)
	}
}
