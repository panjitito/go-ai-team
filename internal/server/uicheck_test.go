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

	_ = c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
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
	exe := "../../go-ai-team.exe"
	if _, err := os.Stat(exe); err != nil {
		exe = "../../go-ai-team"
		if _, err := os.Stat(exe); err != nil {
			t.Skip("build the binary first: go build -o go-ai-team.exe .")
		}
	}
	abs, _ := filepath.Abs(exe)
	cmd := exec.Command(abs, "--port", fmt.Sprint(port), "--browser", "none")
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

	tabs := []string{"Agents", "Split", "Board", "Review", "Terminals", "Stats"}
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
