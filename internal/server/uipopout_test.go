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

// A window opened onto one agent.
//
// The pop-out is the same page with a starting position in its address, which
// is what keeps there from being a second UI to maintain. So two things need
// checking: that the address is read and acted on, and that the frame a window
// onto one conversation has no use for stops being drawn.
func TestUIPopoutChat(t *testing.T) {
	const port = 7831
	startServer(t, port)

	c := launchChrome(t)
	// The address a pop-out is given, arrived at the way a pop-out arrives at
	// it: in the URL, before any script has run.
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/?popout=1&session=s1", port))

	if ready := c.evalString(t, `
new Promise(async resolve => {
  for (let i = 0; i < 80; i++) {
    if (typeof goToPopoutTarget === 'function') {
      await new Promise(r => setTimeout(r, 600));
      return resolve('ready');
    }
    await new Promise(r => setTimeout(r, 250));
  }
  resolve('timeout');
})`); ready != "ready" {
		t.Fatalf("the UI never finished loading: %s", ready)
	}

	// Phase one: the address is read, and this server has no such session — so
	// the window says so rather than sitting there looking broken.
	first := c.evalString(t, `
new Promise(resolve => resolve(JSON.stringify({
  on: POPOUT.on,
  session: POPOUT.session,
  bodyClasses: [...document.body.classList],
  goneMessage: document.body.textContent.includes('no longer running'),
})))`)
	t.Logf("popout address: %s", first)

	var addr struct {
		On          bool     `json:"on"`
		Session     string   `json:"session"`
		BodyClasses []string `json:"bodyClasses"`
		GoneMessage bool     `json:"goneMessage"`
	}
	if err := json.Unmarshal([]byte(first), &addr); err != nil {
		t.Fatalf("unreadable: %v (%s)", err, first)
	}
	if !addr.On || addr.Session != "s1" {
		t.Errorf("the window did not read its own address: on=%v session=%q", addr.On, addr.Session)
	}
	if !contains(addr.BodyClasses, "popout") || !contains(addr.BodyClasses, "popout-chat") {
		t.Errorf("body classes = %v", addr.BodyClasses)
	}
	if !addr.GoneMessage {
		t.Error("a pop-out onto a session that is not running says nothing about it")
	}

	// Phase two: the same thing on a page that still has its markup, so the
	// layout can be measured. A window that opened onto a live agent looks like
	// this, and the phase above is what proves the address gets it here.
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))
	if ready := c.evalString(t, `
new Promise(async resolve => {
  for (let i = 0; i < 80; i++) {
    if (typeof goToPopoutTarget === 'function' && document.querySelector('#main')) {
      await new Promise(r => setTimeout(r, 400));
      return resolve('ready');
    }
    await new Promise(r => setTimeout(r, 250));
  }
  resolve('timeout');
})`); ready != "ready" {
		t.Fatalf("the second page never finished loading: %s", ready)
	}

	got := c.evalString(t, `
new Promise(async resolve => {
  const sleep = ms => new Promise(r => setTimeout(r, ms));
  try {
    const out = {};

    S.projects = [{ id: 'p1', name: 'proj', path: 'C:\\proj' }];
    S.agents = [{ id: 'a1', projectId: 'p1', name: 'Backend worker' }];
    S.sessions = [{ id: 's1', agentId: 'a1', projectId: 'p1', kind: 'agent',
                    status: 'waiting', switchLog: [] }];

    POPOUT.on = true;
    POPOUT.session = 's1';
    applyPopoutChrome();
    goToPopoutTarget();
    await sleep(350);
    stopChatPoll();

    out.opened = S.openSession;
    out.project = S.selectedProject;
    out.bodyClasses = [...document.body.classList];

    // The frame a window onto one conversation has no use for.
    const shown = sel => {
      const n = document.querySelector(sel);
      if (!n) return false;
      const s = getComputedStyle(n);
      return s.display !== 'none' && s.visibility !== 'hidden' && n.offsetWidth > 0;
    };
    out.sidebarShown = shown('.sidebar');
    out.tabbarShown = shown('.tabbar');
    out.topbarShown = shown('.topbar');
    out.mainWide = Math.round(document.querySelector('#main').getBoundingClientRect().width);
    out.windowWide = window.innerWidth;
    out.noOverflow = document.body.scrollWidth <= window.innerWidth + 2;

    // The conversation does not offer to pop out the window it is already in.
    out.selfButton = [...document.querySelectorAll('.chat-head button')]
      .some(b => (b.title || '').includes('own window'));

    // And on an ordinary window it does.
    POPOUT.session = '';
    openAgent('s1');
    await sleep(250);
    stopChatPoll();
    out.normalButton = [...document.querySelectorAll('.chat-head button')]
      .some(b => (b.title || '').includes('own window'));

    // What that button sends: a target, never a URL.
    let asked = null;
    const realApi = window.api;
    window.api = async (p, o) => {
      if (p === '/windows') { asked = o.body; return { opened: true, url: 'x' }; }
      return realApi(p, o);
    };
    await popOutSession('s1');
    window.api = realApi;
    out.asked = asked;

    resolve(JSON.stringify(out));
  } catch (e) {
    resolve(JSON.stringify({ threw: String(e && e.stack || e) }));
  }
})`)
	t.Logf("popout chat: %s", got)
	if strings.Contains(got, `"threw"`) {
		t.Fatalf("the page threw: %s", got)
	}

	if dir := os.Getenv("UISHOT_DIR"); dir != "" {
		res := c.call(t, "Page.captureScreenshot", map[string]any{"format": "png"})
		if data, _ := res["data"].(string); data != "" {
			if raw, err := base64.StdEncoding.DecodeString(data); err == nil {
				_ = os.WriteFile(filepath.Join(dir, "popout.png"), raw, 0o644)
			}
		}
	}

	var r struct {
		BodyClasses  []string          `json:"bodyClasses"`
		SidebarShown bool              `json:"sidebarShown"`
		TabbarShown  bool              `json:"tabbarShown"`
		TopbarShown  bool              `json:"topbarShown"`
		MainWide     int               `json:"mainWide"`
		WindowWide   int               `json:"windowWide"`
		NoOverflow   bool              `json:"noOverflow"`
		Opened       string            `json:"opened"`
		Project      string            `json:"project"`
		SelfButton   bool              `json:"selfButton"`
		NormalButton bool              `json:"normalButton"`
		Asked        map[string]string `json:"asked"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("unreadable: %v (%s)", err, got)
	}

	if !contains(r.BodyClasses, "popout") || !contains(r.BodyClasses, "popout-chat") {
		t.Errorf("body classes = %v", r.BodyClasses)
	}
	if r.SidebarShown || r.TabbarShown {
		t.Errorf("a window onto one conversation is still drawing the frame: sidebar=%v tabs=%v",
			r.SidebarShown, r.TabbarShown)
	}
	if !r.TopbarShown {
		t.Error("the topbar went too, and with it every way out of the window")
	}
	// The space the frame gave up has to go to the conversation, not to a gap.
	if r.MainWide < r.WindowWide-40 {
		t.Errorf("the pane is %dpx in a %dpx window; the removed frame left a hole",
			r.MainWide, r.WindowWide)
	}
	if !r.NoOverflow {
		t.Error("the pop-out layout pushed the page wider than the window")
	}
	if r.Opened != "s1" || r.Project != "p1" {
		t.Errorf("opened %q in %q, want the session named in the address", r.Opened, r.Project)
	}
	if r.SelfButton {
		t.Error("offering to pop out the window it is already in")
	}
	if !r.NormalButton {
		t.Error("an ordinary window has no way to pop the conversation out")
	}
	// A target, never a URL: the server composes the address.
	if r.Asked["sessionId"] != "s1" {
		t.Errorf("asked for %v, want a session id", r.Asked)
	}
	if _, ok := r.Asked["url"]; ok {
		t.Error("the client sent a URL; the server is meant to compose it")
	}
}

// A window opened onto a project keeps its tabs, because moving between the
// board and the files is the whole reason to have one.
func TestUIPopoutProject(t *testing.T) {
	const port = 7832
	startServer(t, port)

	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/?popout=1&project=p1", port))

	if ready := c.evalString(t, `
new Promise(async resolve => {
  for (let i = 0; i < 80; i++) {
    if (typeof goToPopoutTarget === 'function') {
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
    const out = {};
    out.classes = [...document.body.classList];

    S.projects = [{ id: 'p1', name: 'proj', path: 'C:\\proj' },
                  { id: 'p2', name: 'other', path: 'C:\\other' }];
    S.selectedProject = 'p2';
    S.agents = [];
    S.sessions = [];
    goToPopoutTarget();
    await sleep(250);
    out.selected = S.selectedProject;

    const shown = sel => {
      const n = document.querySelector(sel);
      if (!n) return false;
      const s = getComputedStyle(n);
      return s.display !== 'none' && n.offsetWidth > 0;
    };
    out.sidebarShown = shown('.sidebar');
    out.tabbarShown = shown('.tabbar');
    out.noOverflow = document.body.scrollWidth <= window.innerWidth + 2;

    // The header does not offer to pop out the project this window already is.
    out.selfButton = [...document.querySelectorAll('.main-head button')]
      .some(b => (b.title || '').includes('own window'));

    // What the button sends, without opening anything.
    let asked = null;
    const realApi = window.api;
    window.api = async (p, o) => {
      if (p === '/windows') { asked = o.body; return { opened: true, url: 'x' }; }
      return realApi(p, o);
    };
    await popOutProject('p2');
    window.api = realApi;
    out.asked = asked;

    resolve(JSON.stringify(out));
  } catch (e) {
    resolve(JSON.stringify({ threw: String(e && e.stack || e) }));
  }
})`)
	t.Logf("popout project: %s", got)
	if strings.Contains(got, `"threw"`) {
		t.Fatalf("the page threw: %s", got)
	}

	var r struct {
		Classes      []string          `json:"classes"`
		Selected     string            `json:"selected"`
		SidebarShown bool              `json:"sidebarShown"`
		TabbarShown  bool              `json:"tabbarShown"`
		NoOverflow   bool              `json:"noOverflow"`
		SelfButton   bool              `json:"selfButton"`
		Asked        map[string]string `json:"asked"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("unreadable: %v (%s)", err, got)
	}

	if !contains(r.Classes, "popout-project") {
		t.Errorf("body classes = %v", r.Classes)
	}
	if r.Selected != "p1" {
		t.Errorf("selected %q, want the project named in the address", r.Selected)
	}
	if r.SidebarShown {
		t.Error("a pop-out is still drawing the project list it was opened out of")
	}
	if !r.TabbarShown {
		t.Error("a project window lost its tabs, which is most of what it is for")
	}
	if !r.NoOverflow {
		t.Error("the layout pushed the page wider than the window")
	}
	if r.SelfButton {
		t.Error("offering to pop out the project this window already is")
	}
	// A target, never a URL: the server composes the address.
	if r.Asked["projectId"] != "p2" {
		t.Errorf("asked for %v, want a project id", r.Asked)
	}
	if _, ok := r.Asked["url"]; ok {
		t.Error("the client sent a URL; the server is meant to compose it")
	}
}

func contains(all []string, want string) bool {
	for _, s := range all {
		if s == want {
			return true
		}
	}
	return false
}
