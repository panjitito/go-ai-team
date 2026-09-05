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

// What is happening in each project, in the tree.
//
// The count alone said a project had agents and nothing about whether they were
// working, waiting on a person, or finished ten minutes ago. Four things need
// checking: that each state is distinguishable, that the finish is noticed at
// all — it is a transition, so it only exists if something remembered the
// previous state — that a question does not get treated as finishing, and that
// the badge survives a status event without its animation restarting.
func TestUIActivity(t *testing.T) {
	const port = 7815
	startServer(t, port)

	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	if ready := c.evalString(t, `
new Promise(async resolve => {
  // The functions exist as soon as the script parses, but loadAll() resolves
  // later and calls render(), which would replace the state injected below with
  // the server's own — empty, on a scratch home. Waiting for the tab bar waits
  // for that first render to have happened.
  for (let i = 0; i < 80; i++) {
    if (typeof paintActivity === 'function' &&
        document.querySelectorAll('#tabbar .tab').length) {
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
  // Wrapped, because an async executor that throws leaves the promise pending
  // for ever and the harness reports a socket timeout instead of the error.
  try {
  const sleep = ms => new Promise(r => setTimeout(r, ms));

  // Four projects, one per state, plus one with nothing running.
  S.projects = ['busy', 'asking', 'idle', 'quiet', 'finishing'].map(id => ({
    id, name: id, path: 'C:\\' + id,
  }));
  S.agents = S.projects.map(p => ({ id: 'a-' + p.id, projectId: p.id, name: p.name, role: 'dev' }));
  const sess = (pid, extra) => Object.assign(
    { id: 's-' + pid, agentId: 'a-' + pid, kind: 'agent', status: 'idle', switchLog: [] }, extra);
  S.sessions = [
    sess('busy', { status: 'working' }),
    sess('asking', { status: 'waiting', needsYou: true }),
    sess('idle', { status: 'waiting' }),
    sess('finishing', { status: 'working' }),
  ];
  renderSidebar();
  paintActivity();
  await sleep(60);

  const badge = id => document.querySelector('.tree-item[data-project="' + id + '"] .activity');
  const cls = id => (badge(id) || {}).className || 'MISSING';
  const anim = id => {
    const n = badge(id);
    return n ? getComputedStyle(n).animationName : 'MISSING';
  };
  const out = {
    busy: cls('busy'), asking: cls('asking'), idle: cls('idle'), quiet: cls('quiet'),
    busyAnim: anim('busy'), askingAnim: anim('asking'), quietText: badge('quiet').textContent,
    busyText: badge('busy').textContent,
    busyTitle: badge('busy').title,
  };

  // The transition. The agent stops working and stays running.
  S.sessions = S.sessions.map(s =>
    s.agentId === 'a-finishing' ? Object.assign({}, s, { status: 'waiting' }) : s);
  paintActivity();
  await sleep(60);
  out.finished = cls('finishing');
  out.finishedAnim = anim('finishing');

  // The other way it finishes: the agent exits, so there is no count left to
  // give the badge a shape. It should still read as "done here", not as a
  // green speck in the margin.
  S.sessions = S.sessions.filter(x => x.agentId !== 'a-finishing');
  ACTIVITY.was.finishing = 'working';
  paintActivity();
  await sleep(60);
  out.goneText = badge('finishing').textContent;
  out.goneCls = cls('finishing');
  out.goneWidth = Math.round(badge('finishing').getBoundingClientRect().width);

  // A status event arriving mid-animation must not restart it: the badge is
  // painted in place, so the element is the same one.
  const before = badge('finishing');
  paintActivity();
  await sleep(60);
  out.sameNode = badge('finishing') === before;
  out.stillFinishing = cls('finishing');

  // Stopping to ask is not finishing, and must not get the green flash.
  S.sessions = [sess('busy', { status: 'working' })];
  paintActivity();
  await sleep(30);
  S.sessions = [sess('busy', { status: 'waiting', needsYou: true })];
  paintActivity();
  await sleep(30);
  out.askingNotDone = cls('busy');

  // Collapsed, the panel is not on screen, so the toggle carries the summary.
  S.sessions = [sess('busy', { status: 'working' })];
  paintActivity();
  document.querySelector('#app').classList.add('nav-collapsed');
  paintActivity();
  await sleep(30);
  out.navWorking = document.querySelector('#menuBtn').className;
  document.querySelector('#app').classList.remove('nav-collapsed');
  paintActivity();
  await sleep(30);
  out.navExpanded = document.querySelector('#menuBtn').className;

  resolve(JSON.stringify(out));
  } catch (e) { resolve(JSON.stringify({ threw: String(e && e.stack || e) })); }
})`)
	t.Logf("activity: %s", got)

	if strings.Contains(got, `"threw"`) {
		t.Fatalf("the page threw: %s", got)
	}

	if dir := os.Getenv("UISHOT_DIR"); dir != "" {
		res := c.call(t, "Page.captureScreenshot", map[string]any{"format": "png"})
		if data, _ := res["data"].(string); data != "" {
			if raw, err := base64.StdEncoding.DecodeString(data); err == nil {
				_ = os.WriteFile(filepath.Join(dir, "activity.png"), raw, 0o644)
			}
		}
	}

	var r struct {
		Busy           string `json:"busy"`
		Asking         string `json:"asking"`
		Idle           string `json:"idle"`
		Quiet          string `json:"quiet"`
		BusyAnim       string `json:"busyAnim"`
		AskingAnim     string `json:"askingAnim"`
		QuietText      string `json:"quietText"`
		BusyText       string `json:"busyText"`
		BusyTitle      string `json:"busyTitle"`
		Finished       string `json:"finished"`
		FinishedAnim   string `json:"finishedAnim"`
		GoneText       string `json:"goneText"`
		GoneCls        string `json:"goneCls"`
		GoneWidth      int    `json:"goneWidth"`
		SameNode       bool   `json:"sameNode"`
		StillFinishing string `json:"stillFinishing"`
		AskingNotDone  string `json:"askingNotDone"`
		NavWorking     string `json:"navWorking"`
		NavExpanded    string `json:"navExpanded"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("unreadable: %v (%s)", err, got)
	}

	if !strings.Contains(r.Busy, "act-working") {
		t.Errorf("a working project is not marked: %q", r.Busy)
	}
	if !strings.Contains(r.Asking, "act-needs") {
		t.Errorf("a project waiting on you is not marked: %q", r.Asking)
	}
	if !strings.Contains(r.Idle, "act-idle") {
		t.Errorf("a running-but-idle project is not marked: %q", r.Idle)
	}
	if strings.Contains(r.Quiet, "act-") || r.QuietText != "" {
		t.Errorf("a project with nothing running should show nothing: %q %q", r.Quiet, r.QuietText)
	}
	// Motion is what carries "working" across a panel of several projects.
	if r.BusyAnim == "none" || r.BusyAnim == "" {
		t.Errorf("working does not animate (%q)", r.BusyAnim)
	}
	// And a thing wanting a decision should not compete with it.
	if r.AskingAnim != "none" {
		t.Errorf("waiting-for-you animates (%q); it is meant to be steady", r.AskingAnim)
	}
	if r.BusyText != "1" {
		t.Errorf("the count is gone: %q", r.BusyText)
	}
	if !strings.Contains(r.BusyTitle, "working") {
		t.Errorf("the badge does not say what it means on hover: %q", r.BusyTitle)
	}

	if !strings.Contains(r.Finished, "just-done") {
		t.Errorf("finishing was not noticed: %q", r.Finished)
	}
	if r.FinishedAnim == "none" || r.FinishedAnim == "" {
		t.Errorf("the finish does not animate (%q)", r.FinishedAnim)
	}
	if r.GoneText != "✓" {
		t.Errorf("an agent that finished and exited leaves %q in the badge", r.GoneText)
	}
	if !strings.Contains(r.GoneCls, "just-done") || r.GoneWidth < 14 {
		t.Errorf("the finished badge has no shape: %q %dpx", r.GoneCls, r.GoneWidth)
	}
	if !r.SameNode {
		t.Error("the badge was replaced by a repaint, which restarts its animation mid-flash")
	}
	if !strings.Contains(r.StillFinishing, "just-done") {
		t.Errorf("a repaint dropped the finish flash: %q", r.StillFinishing)
	}
	if strings.Contains(r.AskingNotDone, "just-done") {
		t.Errorf("stopping to ask a question was reported as finishing: %q", r.AskingNotDone)
	}

	if !strings.Contains(r.NavWorking, "has-activity") || !strings.Contains(r.NavWorking, "act-working") {
		t.Errorf("with the panel collapsed the toggle says nothing: %q", r.NavWorking)
	}
	// Off together, so the element does not claim a state it is not showing.
	if strings.Contains(r.NavExpanded, "has-activity") || strings.Contains(r.NavExpanded, "act-") {
		t.Errorf("the toggle keeps its badge while the panel is open: %q", r.NavExpanded)
	}
}
