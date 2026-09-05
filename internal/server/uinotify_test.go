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

// Being told an agent wants you, while you are looking at something else.
//
// The browser's own notification machinery is not what is under test here — it
// is stubbed. What is under test is everything around it: which transitions are
// worth raising, that the first sight of a session is not one of them, that a
// wave of them arrives as a single line, and that nothing at all is raised while
// the window is in front of you.
func TestUINotify(t *testing.T) {
	const port = 7823
	startServer(t, port)

	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	if ready := c.evalString(t, `
new Promise(async resolve => {
  for (let i = 0; i < 80; i++) {
    if (typeof alertScan === 'function' && document.querySelectorAll('#tabbar .tab').length) {
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
    const raised = [];
    // A stand-in for the browser's notifications, so the test can read what
    // would have been shown.
    window.Notification = function (title, opts) {
      raised.push({ title, body: (opts || {}).body || '', tag: (opts || {}).tag || '' });
      this.close = () => {};
    };
    window.Notification.permission = 'granted';
    window.Notification.requestPermission = async () => 'granted';

    // Not looking at it. Both halves, because either one is enough to count as
    // watching.
    Object.defineProperty(document, 'hasFocus', { value: () => false, configurable: true });
    Object.defineProperty(document, 'visibilityState',
      { get: () => 'hidden', configurable: true });

    let chimes = 0;
    window.chime = () => { chimes++; };

    S.projects = [{ id: 'p1', name: 'mis-dashboard', path: 'C:\\work\\mis' }];
    S.agents = [
      { id: 'a1', projectId: 'p1', name: 'Backend worker' },
      { id: 'a2', projectId: 'p1', name: 'Frontend' },
      { id: 'a3', projectId: 'p1', name: 'Docs' },
      { id: 'a4', projectId: 'p1', name: 'QA' },
    ];
    const sess = (id, agentId, extra) => Object.assign(
      { id, agentId, projectId: 'p1', kind: 'agent', status: 'working', switchLog: [] }, extra || {});

    const reset = mode => {
      localStorage.setItem('goaiteam.alerts', mode);
      localStorage.setItem('goaiteam.alerts.sound', '1');
      ALERTS.was.clear();
      ALERTS.pending.length = 0;
      raised.length = 0;
      chimes = 0;
    };
    const settle = () => sleep(900);

    const out = {};

    // --- the first sighting is not a change --------------------------------
    reset('all');
    S.sessions = [sess('s1', 'a1', { needsYou: true, question: 'Which browser?' })];
    alertScan();
    await settle();
    out.onFirstSight = raised.length;

    // --- a question ---------------------------------------------------------
    reset('all');
    S.sessions = [sess('s1', 'a1')];
    alertScan();                       // seed
    S.sessions = [sess('s1', 'a1', { needsYou: true, question: 'Which browser?' })];
    alertScan();
    await settle();
    out.needsCount = raised.length;
    out.needsTitle = (raised[0] || {}).title;
    out.needsBody = (raised[0] || {}).body;
    out.needsTag = (raised[0] || {}).tag;
    out.chimed = chimes;

    // Still asking five seconds later is not news.
    alertScan();
    await settle();
    out.afterRepeat = raised.length;

    // --- a turn ending ------------------------------------------------------
    reset('all');
    S.sessions = [sess('s1', 'a1')];
    alertScan();
    S.sessions = [sess('s1', 'a1', { status: 'waiting' })];
    alertScan();
    await settle();
    out.doneTitle = (raised[0] || {}).title;
    out.doneBody = (raised[0] || {}).body;

    // --- "only when it needs me" leaves a finished turn alone ---------------
    reset('needs');
    S.sessions = [sess('s1', 'a1')];
    alertScan();
    S.sessions = [sess('s1', 'a1', { status: 'waiting' })];
    alertScan();
    await settle();
    out.doneWhenNeedsOnly = raised.length;

    // ...but a death still counts.
    S.sessions = [sess('s1', 'a1', { status: 'error', error: 'the process died' })];
    alertScan();
    await settle();
    out.errorWhenNeedsOnly = raised.length;
    out.errorTitle = (raised[0] || {}).title;

    // --- off is off ---------------------------------------------------------
    reset('off');
    S.sessions = [sess('s1', 'a1')];
    alertScan();
    S.sessions = [sess('s1', 'a1', { needsYou: true, question: 'well?' })];
    alertScan();
    await settle();
    out.whenOff = raised.length;
    out.chimeWhenOff = chimes;

    // --- a wave arrives as one line ----------------------------------------
    reset('all');
    S.sessions = [sess('s1', 'a1'), sess('s2', 'a2'), sess('s3', 'a3'), sess('s4', 'a4')];
    alertScan();
    S.sessions = [
      sess('s1', 'a1', { needsYou: true, question: 'one?' }),
      sess('s2', 'a2', { needsYou: true, question: 'two?' }),
      sess('s3', 'a3', { status: 'waiting' }),
      sess('s4', 'a4', { status: 'waiting' }),
    ];
    alertScan();
    await settle();
    out.waveCount = raised.length;
    out.waveTitle = (raised[0] || {}).title;
    out.waveLines = ((raised[0] || {}).body || '').split('\n').length;
    out.waveChimes = chimes;

    // --- looking straight at it --------------------------------------------
    reset('all');
    Object.defineProperty(document, 'hasFocus', { value: () => true, configurable: true });
    Object.defineProperty(document, 'visibilityState',
      { get: () => 'visible', configurable: true });
    S.sessions = [sess('s1', 'a1')];
    alertScan();
    S.sessions = [sess('s1', 'a1', { needsYou: true, question: 'well?' })];
    alertScan();
    await settle();
    out.whileWatching = raised.length;
    out.chimeWhileWatching = chimes;

    // --- a session that has gone is forgotten -------------------------------
    S.sessions = [];
    alertScan();
    out.forgot = ALERTS.was.size;

    // --- the Settings control exists and says what it does ------------------
    const sec = alertsSection();
    out.modes = [...sec.querySelectorAll('#setAlerts option')].map(o => o.value);
    out.hasSound = !!sec.querySelector('#setAlertSound');

    resolve(JSON.stringify(out));
  } catch (e) {
    resolve(JSON.stringify({ threw: String(e && e.stack || e) }));
  }
})`)
	t.Logf("notify: %s", got)
	if strings.Contains(got, `"threw"`) {
		t.Fatalf("the page threw: %s", got)
	}

	if dir := os.Getenv("UISHOT_DIR"); dir != "" {
		c.evalString(t, `
new Promise(async r => {
  await openSettings();
  await new Promise(x => setTimeout(x, 400));
  r('ok');
})`)
		res := c.call(t, "Page.captureScreenshot", map[string]any{"format": "png"})
		if data, _ := res["data"].(string); data != "" {
			if raw, err := base64.StdEncoding.DecodeString(data); err == nil {
				_ = os.WriteFile(filepath.Join(dir, "settings-alerts.png"), raw, 0o644)
			}
		}
	}

	var r struct {
		OnFirstSight       int      `json:"onFirstSight"`
		NeedsCount         int      `json:"needsCount"`
		NeedsTitle         string   `json:"needsTitle"`
		NeedsBody          string   `json:"needsBody"`
		NeedsTag           string   `json:"needsTag"`
		Chimed             int      `json:"chimed"`
		AfterRepeat        int      `json:"afterRepeat"`
		DoneTitle          string   `json:"doneTitle"`
		DoneBody           string   `json:"doneBody"`
		DoneWhenNeedsOnly  int      `json:"doneWhenNeedsOnly"`
		ErrorWhenNeedsOnly int      `json:"errorWhenNeedsOnly"`
		ErrorTitle         string   `json:"errorTitle"`
		WhenOff            int      `json:"whenOff"`
		ChimeWhenOff       int      `json:"chimeWhenOff"`
		WaveCount          int      `json:"waveCount"`
		WaveTitle          string   `json:"waveTitle"`
		WaveLines          int      `json:"waveLines"`
		WaveChimes         int      `json:"waveChimes"`
		WhileWatching      int      `json:"whileWatching"`
		ChimeWhileWatching int      `json:"chimeWhileWatching"`
		Forgot             int      `json:"forgot"`
		Modes              []string `json:"modes"`
		HasSound           bool     `json:"hasSound"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("unreadable: %v (%s)", err, got)
	}

	if r.OnFirstSight != 0 {
		t.Errorf("%d alerts on the first sight of a session — reconnecting would announce the whole board", r.OnFirstSight)
	}
	if r.NeedsCount != 1 {
		t.Fatalf("a question raised %d alerts, want 1", r.NeedsCount)
	}
	if r.NeedsTitle != "Backend worker needs you" {
		t.Errorf("title = %q", r.NeedsTitle)
	}
	if r.NeedsBody != "Which browser?" {
		t.Errorf("body = %q — the question is the useful part", r.NeedsBody)
	}
	if !strings.Contains(r.NeedsTag, "s1") {
		t.Errorf("tag = %q; one notification per session, replacing its own", r.NeedsTag)
	}
	if r.Chimed != 1 {
		t.Errorf("%d chimes for one alert", r.Chimed)
	}
	if r.AfterRepeat != 1 {
		t.Errorf("still asking raised another alert (%d total) — it would repeat every poll", r.AfterRepeat)
	}
	if r.DoneTitle != "Backend worker finished" {
		t.Errorf("finished title = %q", r.DoneTitle)
	}
	if r.DoneBody != "mis-dashboard" {
		t.Errorf("finished body = %q, want the project it was working in", r.DoneBody)
	}
	if r.DoneWhenNeedsOnly != 0 {
		t.Errorf("a finished turn raised %d alerts in the needs-only mode", r.DoneWhenNeedsOnly)
	}
	if r.ErrorWhenNeedsOnly != 1 || !strings.Contains(r.ErrorTitle, "stopped") {
		t.Errorf("a dead session raised %d alerts (%q); that is always worth knowing",
			r.ErrorWhenNeedsOnly, r.ErrorTitle)
	}
	if r.WhenOff != 0 || r.ChimeWhenOff != 0 {
		t.Errorf("off raised %d alerts and %d chimes", r.WhenOff, r.ChimeWhenOff)
	}
	if r.WaveCount != 1 {
		t.Errorf("four at once raised %d notifications, want one summary", r.WaveCount)
	}
	if !strings.Contains(r.WaveTitle, "2 of 4") {
		t.Errorf("summary title = %q, want the count that needs an answer", r.WaveTitle)
	}
	if r.WaveLines != 4 {
		t.Errorf("the summary lists %d of the 4", r.WaveLines)
	}
	if r.WaveChimes != 1 {
		t.Errorf("%d chimes for one wave", r.WaveChimes)
	}
	if r.WhileWatching != 0 || r.ChimeWhileWatching != 0 {
		t.Errorf("looking straight at the app still got %d alerts and %d chimes",
			r.WhileWatching, r.ChimeWhileWatching)
	}
	if r.Forgot != 0 {
		t.Errorf("%d sessions still remembered after they went; the map grows forever", r.Forgot)
	}
	if len(r.Modes) != 3 || r.Modes[0] != "off" {
		t.Errorf("the Settings control offers %v", r.Modes)
	}
	if !r.HasSound {
		t.Error("no way to turn the chime off")
	}
}
