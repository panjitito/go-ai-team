//go:build uitest

// UI smoke test.
//
// This drives a Chrome instance the test owns: headless, on a throwaway profile
// in a temp directory, spoken to over the DevTools protocol. It deliberately
// does not use a browser extension or any profile a person is signed into —
// attaching a debugger to somebody's real browser to run tests is not something
// a test suite should do, and a profile with their accounts in it is not a test
// fixture.
//
// Run it explicitly, because it needs Chrome and a built binary:
//
//	go test -tags uitest ./internal/server/ -run TestUI -v
package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/uniair/go-ai-team/internal/browser"
)

// chrome is a headless Chrome the test owns end to end.
type chrome struct {
	cmd     *exec.Cmd
	wsURL   string
	conn    *websocket.Conn
	nextID  int
	profile string
	// events records CDP events seen while waiting for a command reply, so a
	// later waitEvent does not miss one that already arrived.
	events []string
}

// launchChrome starts headless Chrome on a fresh profile and connects to it.
func launchChrome(t *testing.T) *chrome {
	t.Helper()
	b, ok := browser.Find()
	if !ok {
		t.Skip("no Chrome-family browser on this machine")
	}

	profile := t.TempDir()
	cmd := exec.Command(b.Path,
		"--headless=new",
		"--remote-debugging-port=0",
		"--user-data-dir="+profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-search-engine-choice-screen",
		// A test machine often has no GPU worth using, and a crashed GPU
		// process makes the failure look like the app's fault.
		"--disable-gpu",
		"--window-size=1440,940",
		"about:blank",
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("could not start %s: %v", b.Name, err)
	}

	c := &chrome{cmd: cmd, profile: profile}
	t.Cleanup(c.close)

	// Chrome writes the port it actually chose into the profile directory.
	portFile := filepath.Join(profile, "DevToolsActivePort")
	deadline := time.Now().Add(30 * time.Second)
	var port string
	for time.Now().Before(deadline) {
		if f, err := os.Open(portFile); err == nil {
			sc := bufio.NewScanner(f)
			if sc.Scan() {
				port = strings.TrimSpace(sc.Text())
			}
			f.Close()
			if port != "" {
				break
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	if port == "" {
		t.Fatal("Chrome never reported a DevTools port")
	}
	c.wsURL = "http://127.0.0.1:" + port
	return c
}

func (c *chrome) close() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
}

// openTarget attaches to a fresh blank tab, then navigates and waits for load.
//
// Creating the target directly at the URL looks simpler but races: the page is
// still navigating when the first evaluate arrives, and CDP answers
// "Execution context was destroyed". Attaching to about:blank first, then
// driving the navigation ourselves, means there is a context to talk to before
// and after.
func (c *chrome) openTarget(t *testing.T, url string) {
	t.Helper()
	req, err := http.NewRequest("PUT", c.wsURL+"/json/new?about:blank", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("could not open a target: %v", err)
	}
	defer resp.Body.Close()

	var target struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&target); err != nil {
		t.Fatalf("could not read the target: %v", err)
	}
	if target.WebSocketDebuggerURL == "" {
		t.Fatal("the target has no debugger URL")
	}

	conn, _, err := websocket.DefaultDialer.Dial(target.WebSocketDebuggerURL, nil)
	if err != nil {
		t.Fatalf("could not attach to the target: %v", err)
	}
	c.conn = conn

	c.call(t, "Page.enable", nil)
	c.call(t, "Runtime.enable", nil)
	c.call(t, "Page.navigate", map[string]any{"url": url})
	c.waitEvent(t, "Page.loadEventFired", 30*time.Second)
}

// cdpReadBudget bounds one Runtime.evaluate. Longer than any scenario in here,
// so an over-run is reported by the test that over-ran rather than as a socket
// error from the harness.
const cdpReadBudget = 240 * time.Second

// call sends one CDP command and returns its result, ignoring the events that
// arrive interleaved with it.
func (c *chrome) call(t *testing.T, method string, params map[string]any) map[string]any {
	t.Helper()
	c.nextID++
	id := c.nextID
	msg := map[string]any{"id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if err := c.conn.WriteJSON(msg); err != nil {
		t.Fatalf("CDP write %s: %v", method, err)
	}

	// Generous, because some of these evaluate a whole scenario in the page —
	// wait for an agent to go idle, send it a prompt, wait for a real model to
	// answer. When that ran past the deadline the failure read as "CDP read: i/o
	// timeout", which says nothing about the app and sent me looking in the
	// wrong place. A test that is genuinely stuck still fails, on its own
	// timeout, with its own message.
	_ = c.conn.SetReadDeadline(time.Now().Add(cdpReadBudget))
	for {
		var raw map[string]any
		if err := c.conn.ReadJSON(&raw); err != nil {
			t.Fatalf("CDP read after %s: %v", method, err)
		}
		gotID, ok := raw["id"].(float64)
		if !ok || int(gotID) != id {
			// An event, or a reply to something else. Keep the ones a caller
			// may be waiting for.
			if m, ok := raw["method"].(string); ok {
				c.events = append(c.events, m)
			}
			continue
		}
		if e, ok := raw["error"]; ok {
			t.Fatalf("CDP %s failed: %v", method, e)
		}
		res, _ := raw["result"].(map[string]any)
		return res
	}
}

// waitEvent blocks until a named CDP event arrives.
func (c *chrome) waitEvent(t *testing.T, method string, limit time.Duration) {
	t.Helper()
	for _, m := range c.events {
		if m == method {
			return
		}
	}
	deadline := time.Now().Add(limit)
	_ = c.conn.SetReadDeadline(deadline)
	for time.Now().Before(deadline) {
		var raw map[string]any
		if err := c.conn.ReadJSON(&raw); err != nil {
			t.Fatalf("waiting for %s: %v", method, err)
		}
		if m, _ := raw["method"].(string); m == method {
			return
		}
	}
	t.Fatalf("%s never fired", method)
}

// eval runs JavaScript in the page and returns its value.
//
// awaitPromise is on so a test can await fetches and timers, which every
// meaningful assertion about this UI needs.
func (c *chrome) eval(t *testing.T, expr string) any {
	t.Helper()
	res := c.call(t, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
		"awaitPromise":  true,
	})
	if ex, ok := res["exceptionDetails"]; ok {
		t.Fatalf("page threw: %v (while evaluating: %s)", ex, expr)
	}
	inner, _ := res["result"].(map[string]any)
	return inner["value"]
}

// evalString is eval for expressions that return a string.
func (c *chrome) evalString(t *testing.T, expr string) string {
	t.Helper()
	v := c.eval(t, expr)
	s, _ := v.(string)
	return s
}

// startServer builds nothing and assumes the binary exists; it runs the real
// app so the test exercises the same code a user would.
func startServer(t *testing.T, port int) {
	t.Helper()
	// The dev build first, when there is one.
	//
	// Windows locks a running executable, so `go build -o go-ai-team.exe` fails
	// outright while somebody is using the app — and somebody using the app is
	// the normal case on the machine this is developed on. Building to
	// go-ai-team-dev.exe instead leaves their copy alone, and the tests then have
	// to prefer the new binary or they would be checking yesterday's.
	exe := ""
	for _, c := range []string{
		"../../go-ai-team-dev.exe", "../../go-ai-team-dev",
		"../../go-ai-team.exe", "../../go-ai-team",
	} {
		if _, err := os.Stat(c); err == nil {
			exe = c
			break
		}
	}
	if exe == "" {
		t.Skip("build the binary first: go build -o go-ai-team-dev.exe .")
	}
	abs, _ := filepath.Abs(exe)
	cmd := exec.Command(abs, "--port", fmt.Sprint(port), "--browser", "none")

	// A state directory of its own.
	//
	// The app keeps its state under the home directory, so without this a test
	// server shares one with whatever the person running the tests has open —
	// two processes writing the same state file, over their real accounts and
	// projects. Nothing here means to change any of that, but "means to" is not
	// a safety property, and a test suite is not entitled to somebody's live
	// configuration.
	home := t.TempDir()
	cmd.Env = append(os.Environ(), "USERPROFILE="+home, "HOME="+home)

	if err := cmd.Start(); err != nil {
		t.Fatalf("could not start the server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/settings", port))
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("the server never came up")
}

// TestUIViewsRender walks every tab and asserts none of them crashes.
//
// The value here is catching the class of bug that unit tests cannot: a view
// that throws on an empty collection, or a layout that pushes the page wider
// than the window. Both have happened.
func TestUIViewsRender(t *testing.T) {
	const port = 7799
	startServer(t, port)

	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	// Wait for the app to finish its first load rather than sleeping blindly.
	ready := c.evalString(t, `
new Promise(async resolve => {
  for (let i = 0; i < 60; i++) {
    if (document.querySelectorAll('#tabbar .tab').length > 0) return resolve('ready');
    await new Promise(r => setTimeout(r, 250));
  }
  resolve('timeout');
})`)
	if ready != "ready" {
		t.Fatalf("the UI never finished loading: %s", ready)
	}

	if errs := c.evalString(t, `String(window.__uiErrors || '')`); errs != "" {
		t.Errorf("errors during load: %s", errs)
	}

	tabs := []string{"Agents", "Split", "Board", "Review", "Files", "Terminals", "Stats"}
	for _, name := range tabs {
		out := c.evalString(t, fmt.Sprintf(`
new Promise(async resolve => {
  const t = [...document.querySelectorAll('#tabbar .tab')].find(x => x.textContent === %q);
  if (!t) return resolve('TAB MISSING');
  t.click();
  await new Promise(r => setTimeout(r, 1500));
  const main = document.querySelector('#main');
  const txt = (main.textContent || '').replace(/\s+/g, ' ');
  if (txt.includes('Could not load this view')) return resolve('CRASH: ' + txt.slice(0, 120));
  resolve('ok');
})`, name))
		if out != "ok" {
			t.Errorf("%s view: %s", name, out)
		}
	}

	// The layout must not be wider than the window: a grid blowout pushed the
	// topbar off screen once and no unit test could have seen it.
	over := c.eval(t, `document.body.scrollWidth - window.innerWidth`)
	if n, ok := over.(float64); ok && n > 2 {
		t.Errorf("the page is %.0fpx wider than the window; something is blowing out the grid", n)
	}
}

// TestUIAskPanel renders a permission prompt and asserts it is answerable.
//
// The panel is fed a question directly rather than waiting for a real agent to
// ask one: the parser is tested against captured terminals in
// internal/session, and what needs checking here is the other half — that the
// options become buttons, that the one the CLI has selected is marked, that a
// four-sentence label does not blow the layout out, and that the panel goes
// away when the question does.
func TestUIAskPanel(t *testing.T) {
	const port = 7803
	startServer(t, port)

	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	if ready := c.evalString(t, `
new Promise(async resolve => {
  for (let i = 0; i < 60; i++) {
    if (typeof renderAsk === 'function') return resolve('ready');
    await new Promise(r => setTimeout(r, 250));
  }
  resolve('timeout');
})`); ready != "ready" {
		t.Fatalf("the UI never finished loading: %s", ready)
	}

	got := c.evalString(t, `(() => {
  const host = document.createElement('div');
  host.id = 'chatBanner';
  document.querySelector('#main').append(host);
  const ask = { question: 'Do you want to create hello.txt?', cancel: true, options: [
    { number: 1, label: 'Yes', selected: false },
    { number: 2, label: 'Yes, and switch to accept edits (auto-approve file edits and common file commands) for this session (shift+tab)', selected: true },
    { number: 3, label: 'No', selected: false },
  ]};
  renderAsk({ ask }, 'ses_test');
  const opts = [...host.querySelectorAll('.ask-opt')];
  if (opts.length !== 3) return 'WRONG COUNT ' + opts.length;
  if (!host.textContent.includes('create hello.txt?')) return 'QUESTION MISSING';
  if (!opts[1].classList.contains('selected')) return 'SELECTION NOT MARKED';
  if (opts[0].classList.contains('selected')) return 'WRONG ROW MARKED';
  if (!opts[2].classList.contains('no')) return 'DECLINE NOT MARKED';
  if (opts[0].querySelector('.ask-num').textContent !== '1') return 'NUMBER MISSING';
  if (host.scrollWidth > document.querySelector('#main').clientWidth + 2) return 'PANEL TOO WIDE';
  renderAsk({}, 'ses_test');
  if (host.querySelector('.ask-opt')) return 'PANEL STAYED UP';
  if (host.style.display !== 'none') return 'PANEL NOT HIDDEN';
  host.remove();
  return 'ok';
})()`)
	if got != "ok" {
		t.Errorf("ask panel: %s", got)
	}
}

// TestUIPlan renders the agent's plan in the run bar.
//
// The parser that rebuilds the plan is checked against real transcripts in
// internal/claudefs; this is the other half — that a plan reaches the bar as
// something you can read at a glance, that the step being worked on is the one
// named, and that a long subject truncates instead of shoving the rate-limit
// meters off the end.
func TestUIPlan(t *testing.T) {
	const port = 7805
	startServer(t, port)

	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	if ready := c.evalString(t, `
new Promise(async resolve => {
  for (let i = 0; i < 60; i++) {
    if (typeof updateRunBar === 'function') return resolve('ready');
    await new Promise(r => setTimeout(r, 250));
  }
  resolve('timeout');
})`); ready != "ready" {
		t.Fatalf("the UI never finished loading: %s", ready)
	}

	got := c.evalString(t, `(() => {
  const host = document.querySelector('#main');
  host.innerHTML = '';
  host.append(runBarEl('ses_test'));
  const tasks = [
    { id: '1', subject: 'Scaffold the project', status: 'completed' },
    { id: '2', subject: 'Write the database schema and a seeder that produces a year of plausible data',
      activeForm: 'Writing the database schema', status: 'in_progress' },
    { id: '3', subject: 'Build the backend', status: 'pending' },
    { id: '4', subject: 'Deploy it', status: 'pending' },
  ];
  updateRunBar({ line: {}, tasks });

  const pill = document.querySelector('#rbPlan');
  if (!pill || pill.style.display === 'none') return 'PILL HIDDEN';
  const text = pill.textContent;
  if (!text.includes('1/4')) return 'NO COUNT: ' + text;
  // The step it is on, in the form that reads as something happening.
  if (!text.includes('Writing the database schema')) return 'NO CURRENT STEP: ' + text;
  if (pill.getBoundingClientRect().width > 360) return 'PILL TOO WIDE';
  if (document.querySelector('#runBar').scrollWidth > document.querySelector('#runBar').clientWidth + 2)
    return 'PLAN PUSHED THE BAR OUT';

  pill.click();
  const menu = document.querySelector('.rb-menu');
  if (!menu) return 'NO POPOVER';
  const rows = [...menu.querySelectorAll('.rb-menu-item')];
  if (rows.length !== 4) return 'ROWS ' + rows.length;
  if (!rows[0].classList.contains('is-done')) return 'FINISHED ROW NOT MARKED';
  if (rows[2].classList.contains('is-done')) return 'PENDING ROW MARKED DONE';
  if (!rows[1].classList.contains('active')) return 'CURRENT ROW NOT MARKED';
  if (!rows[3].textContent.includes('Deploy it')) return 'LAST ROW: ' + rows[3].textContent;
  document.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));

  // No plan, no pill: an agent that never made one must not show an empty box.
  updateRunBar({ line: {}, tasks: [] });
  if (document.querySelector('#rbPlan').style.display !== 'none') return 'PILL STAYED UP';
  return 'ok';
})()`)
	if got != "ok" {
		t.Errorf("plan in the run bar: %s", got)
	}
}

// TestUIThinkingBlock renders the model's reasoning, folded.
//
// Worth stating plainly: on the Claude Code build measured while writing this,
// thinking blocks reach the transcript with an empty body and only a signature,
// so in practice nothing appears. Older sessions on this machine do carry the
// text — 8,890 of 17,525 blocks — so the path is real, and this keeps it honest
// for whenever the text comes back.
func TestUIThinkingBlock(t *testing.T) {
	const port = 7807
	startServer(t, port)

	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	if ready := c.evalString(t, `
new Promise(async resolve => {
  for (let i = 0; i < 60; i++) {
    if (typeof messageEl === 'function') return resolve('ready');
    await new Promise(r => setTimeout(r, 250));
  }
  resolve('timeout');
})`); ready != "ready" {
		t.Fatalf("the UI never finished loading: %s", ready)
	}

	got := c.evalString(t, `(() => {
  const host = document.querySelector('#main');
  host.innerHTML = '';
  const node = messageEl({ role: 'assistant', when: new Date().toISOString(), blocks: [
    { kind: 'thinking', text: 'First I should check what the caller expects.\n\nThen the edge cases.' },
    { kind: 'text', text: 'Here is the answer.' },
  ]}, new Set());
  host.append(node);

  const card = node.querySelector('.tool.think');
  if (!card) return 'NO CARD';
  if (card.classList.contains('open')) return 'OPEN BY DEFAULT';
  // Folded, it must still say enough to decide whether to unfold it.
  const sum = (card.querySelector('.tool-sum') || {}).textContent || '';
  if (!sum.includes('what the caller expects')) return 'NO SUMMARY: ' + sum;
  if (card.querySelector('.tool-detail').offsetHeight !== 0) return 'BODY VISIBLE WHILE FOLDED';

  card.querySelector('.tool-head').click();
  if (!card.classList.contains('open')) return 'DID NOT UNFOLD';
  if (!card.textContent.includes('edge cases')) return 'BODY MISSING AFTER UNFOLD';

  // An empty one is what the current CLI actually writes, and it must not
  // produce a card at all.
  const empty = messageEl({ role: 'assistant', blocks: [{ kind: 'thinking', text: '' }] }, new Set());
  return empty.querySelector('.tool.think') ? 'EMPTY THINKING DREW A CARD' : 'ok';
})()`)
	if got != "ok" {
		t.Errorf("thinking block: %s", got)
	}
}

// TestUINeedsYou marks the agent that has stopped to ask something.
//
// The point of running several at once is that they do not finish together, so
// the one waiting on a person has to be findable without opening each in turn.
// Three places say so: the card, the pill on it, and the window caption — the
// last of which is readable from the taskbar without the app in front of you.
func TestUINeedsYou(t *testing.T) {
	const port = 7809
	startServer(t, port)

	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	if ready := c.evalString(t, `
new Promise(async resolve => {
  for (let i = 0; i < 60; i++) {
    if (typeof updateWaitingCount === 'function' && typeof agentCard === 'function') return resolve('ready');
    await new Promise(r => setTimeout(r, 250));
  }
  resolve('timeout');
})`); ready != "ready" {
		t.Fatalf("the UI never finished loading: %s", ready)
	}

	got := c.evalString(t, `(() => {
  const agent = { id: 'agt_x', projectId: 'prj_x', name: 'Probe', role: 'Backend', color: '#8b5cf6' };
  S.agents = [agent];
  const base = { id: 'ses_x', agentId: 'agt_x', kind: 'agent', status: 'waiting',
                 accountName: 'acct', accountColor: '#888', switchLog: [] };

  // Waiting on the model: ordinary, and must stay ordinary.
  S.sessions = [{ ...base }];
  let card = agentCard(agent);
  if (card.classList.contains('asking')) return 'PLAIN WAITING WAS MARKED';
  if (card.textContent.includes('needs you')) return 'PLAIN WAITING SAYS NEEDS YOU';
  updateWaitingCount();
  if (document.title !== 'Go AI Team') return 'TITLE COUNTED A PLAIN WAIT: ' + document.title;

  // Waiting on a person.
  S.sessions = [{ ...base, needsYou: true, question: 'Do you want to create hello.txt?' }];
  card = agentCard(agent);
  if (!card.classList.contains('asking')) return 'CARD NOT MARKED';
  if (!card.textContent.includes('needs you')) return 'PILL MISSING';
  const pill = card.querySelector('.pill.needs-you');
  if (!pill) return 'PILL NOT STYLED';
  if (!(pill.getAttribute('title') || '').includes('hello.txt')) return 'QUESTION NOT ON HOVER';
  updateWaitingCount();
  if (document.title !== '(1) Go AI Team') return 'TITLE: ' + document.title;

  // Two of them, and then none.
  S.sessions = [
    { ...base, needsYou: true },
    { ...base, id: 'ses_y', agentId: 'agt_y', needsYou: true },
  ];
  updateWaitingCount();
  if (document.title !== '(2) Go AI Team') return 'TITLE FOR TWO: ' + document.title;

  // An exited session cannot still be waiting on you.
  S.sessions = [{ ...base, needsYou: true, status: 'exited' }];
  updateWaitingCount();
  if (document.title !== 'Go AI Team') return 'EXITED STILL COUNTED: ' + document.title;

  S.sessions = [];
  S.agents = [];
  updateWaitingCount();
  return 'ok';
})()`)
	if got != "ok" {
		t.Errorf("needs-you marking: %s", got)
	}
}

// TestUIWorktreePanel lists the checkouts and guards the one destructive button.
//
// The server side is covered end to end against real git and real agents; what
// needs a browser is the panel's judgement: that the project's own checkout is
// never offered for deletion, that a tree an agent is still running in is not
// either, and that a tree the app did not make is shown but not owned.
func TestUIWorktreePanel(t *testing.T) {
	const port = 7811
	startServer(t, port)

	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	if ready := c.evalString(t, `
new Promise(async resolve => {
  for (let i = 0; i < 60; i++) {
    if (typeof paintWorktrees === 'function') return resolve('ready');
    await new Promise(r => setTimeout(r, 250));
  }
  resolve('timeout');
})`); ready != "ready" {
		t.Fatalf("the UI never finished loading: %s", ready)
	}

	got := c.evalString(t, `
new Promise(async resolve => {
  const rows = [
    { path: 'C:/proj', branch: 'main', main: true, running: false },
    { path: 'C:/state/worktrees/p/a1', branch: 'agent/alpha-a1', main: false,
      agentId: 'a1', agentName: 'Alpha', running: true },
    { path: 'C:/state/worktrees/p/a2', branch: 'agent/beta-a2', main: false,
      agentId: 'a2', agentName: 'Beta', running: false },
    { path: 'C:/somewhere/else', branch: 'hand-made', main: false, running: false },
  ];
  const realApi = window.api;
  window.api = async p => (String(p).startsWith('/worktrees') ? rows : realApi(p));
  try {
    await openWorktrees({ id: 'p', name: 'proj' });
    await new Promise(r => setTimeout(r, 400));
    const trs = [...document.querySelectorAll('.wt-table tr')];
    const out = {
      rows: trs.length,
      text: trs.map(tr => tr.textContent.replace(/\s+/g, ' ').trim()),
      removable: trs.map(tr => {
        const b = tr.querySelector('button');
        return !b ? 'none' : (b.disabled ? 'disabled' : 'enabled');
      }),
    };
    resolve(JSON.stringify(out));
  } finally {
    window.api = realApi;
  }
})`)
	t.Logf("worktrees: %s", got)

	var r struct {
		Rows      int      `json:"rows"`
		Text      []string `json:"text"`
		Removable []string `json:"removable"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("unreadable: %v (%s)", err, got)
	}
	if r.Rows != 4 {
		t.Fatalf("got %d rows, want 4", r.Rows)
	}
	// The project itself, an agent still running, an agent stopped, and one the
	// app did not make. Only the third is ours to delete.
	want := []string{"none", "disabled", "enabled", "none"}
	for i, w := range want {
		if r.Removable[i] != w {
			t.Errorf("row %d (%s): remove is %q, want %q", i, r.Text[i], r.Removable[i], w)
		}
	}
	if !strings.Contains(r.Text[0], "the project itself") {
		t.Errorf("row 0 does not say it is the project: %q", r.Text[0])
	}
	if !strings.Contains(r.Text[1], "Alpha") || !strings.Contains(r.Text[1], "running") {
		t.Errorf("row 1 does not name the agent and its state: %q", r.Text[1])
	}
	if !strings.Contains(r.Text[3], "unknown") {
		t.Errorf("a tree the app did not make should say so: %q", r.Text[3])
	}
}

// TestUIPanelsOpen asserts every topbar panel opens and renders content.
func TestUIPanelsOpen(t *testing.T) {
	const port = 7801
	startServer(t, port)

	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	if ready := c.evalString(t, `
new Promise(async resolve => {
  for (let i = 0; i < 60; i++) {
    if (document.querySelector('#accountsBtn')) return resolve('ready');
    await new Promise(r => setTimeout(r, 250));
  }
  resolve('timeout');
})`); ready != "ready" {
		t.Fatalf("the UI never finished loading: %s", ready)
	}

	panels := map[string]string{
		"promptsBtn":  "Prompt library",
		"skillsBtn":   "Skills library",
		"rolesBtn":    "Roles",
		"autoBtn":     "Automation",
		"envBtn":      "Environment",
		"guardBtn":    "Process guard",
		"doctorBtn":   "Doctor",
		"settingsBtn": "Settings",
		"accountsBtn": "Accounts",
	}
	for id, wantTitle := range panels {
		got := c.evalString(t, fmt.Sprintf(`
new Promise(async resolve => {
  document.querySelector('#overlay')?.querySelector('.modal-head .btn')?.click();
  await new Promise(r => setTimeout(r, 200));
  const b = document.querySelector('#%s');
  if (!b) return resolve('BUTTON MISSING');
  b.click();
  for (let i = 0; i < 60; i++) {
    await new Promise(r => setTimeout(r, 500));
    const ov = document.querySelector('#overlay');
    if (ov) {
      const title = ov.querySelector('.modal-head h2')?.textContent || '';
      const len = (ov.querySelector('.modal-body')?.textContent || '').length;
      return resolve(len > 20 ? title : 'EMPTY BODY: ' + title);
    }
  }
  resolve('DID NOT OPEN');
})`, id))
		if !strings.HasPrefix(got, wantTitle) {
			t.Errorf("panel %s: got %q, expected it to start with %q", id, got, wantTitle)
		}
	}
}
