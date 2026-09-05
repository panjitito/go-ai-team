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
	"time"
)

// Collapsing the projects panel.
//
// 268px of a window that is mostly conversation, and the button for it was
// hidden above 780px — so on a desktop there was no way to reclaim the space.
// What needs checking is that the space actually comes back, that the choice
// survives a reload, that Ctrl-B does it too, and that none of it breaks the
// phone layout, where the same panel is an overlay rather than a column.
func TestUINavCollapse(t *testing.T) {
	const port = 7813
	startServer(t, port)

	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	if ready := c.evalString(t, `
new Promise(async resolve => {
  for (let i = 0; i < 60; i++) {
    if (typeof toggleNav === 'function' && document.querySelector('#menuBtn')) return resolve('ready');
    await new Promise(r => setTimeout(r, 250));
  }
  resolve('timeout');
})`); ready != "ready" {
		t.Fatalf("the UI never finished loading: %s", ready)
	}

	got := c.evalString(t, `
new Promise(async resolve => {
  const sleep = ms => new Promise(r => setTimeout(r, ms));
  const app = document.querySelector('#app');
  const side = document.querySelector('#sidebar');
  const main = document.querySelector('#main');
  const btn = document.querySelector('#menuBtn');
  const out = {};

  const shown = () => side.getBoundingClientRect().width > 40;
  out.btnVisible = btn.getBoundingClientRect().width > 0;
  out.openAtFirst = shown();
  out.mainBefore = Math.round(main.getBoundingClientRect().width);

  btn.click();
  await sleep(120);
  out.shownAfterClick = shown();
  out.mainAfter = Math.round(main.getBoundingClientRect().width);
  out.titleWhenHidden = btn.title;
  out.stored = localStorage.getItem('goaiteam.nav.collapsed');

  // Ctrl-B brings it back.
  document.dispatchEvent(new KeyboardEvent('keydown',
    { key: 'b', ctrlKey: true, bubbles: true, cancelable: true }));
  await sleep(120);
  out.shownAfterCtrlB = shown();

  // Collapse again and leave it that way for the reload check below.
  btn.click();
  await sleep(120);
  out.collapsedAtEnd = !shown();
  resolve(JSON.stringify(out));
})`)
	t.Logf("nav: %s", got)

	if dir := os.Getenv("UISHOT_DIR"); dir != "" {
		res := c.call(t, "Page.captureScreenshot", map[string]any{"format": "png"})
		if data, _ := res["data"].(string); data != "" {
			if raw, err := base64.StdEncoding.DecodeString(data); err == nil {
				_ = os.WriteFile(filepath.Join(dir, "nav-collapsed.png"), raw, 0o644)
			}
		}
	}

	var r struct {
		BtnVisible      bool   `json:"btnVisible"`
		OpenAtFirst     bool   `json:"openAtFirst"`
		MainBefore      int    `json:"mainBefore"`
		ShownAfterClick bool   `json:"shownAfterClick"`
		MainAfter       int    `json:"mainAfter"`
		TitleWhenHidden string `json:"titleWhenHidden"`
		Stored          string `json:"stored"`
		ShownAfterCtrlB bool   `json:"shownAfterCtrlB"`
		CollapsedAtEnd  bool   `json:"collapsedAtEnd"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("unreadable: %v (%s)", err, got)
	}

	if !r.BtnVisible {
		t.Error("the toggle is not on screen at desktop width, which is where it was missing")
	}
	if !r.OpenAtFirst {
		t.Error("the panel should start open")
	}
	if r.ShownAfterClick {
		t.Error("clicking the toggle did not hide the panel")
	}
	// The point of the feature: the space goes to the main pane.
	if r.MainAfter <= r.MainBefore+200 {
		t.Errorf("main went from %dpx to %dpx — the space was not reclaimed", r.MainBefore, r.MainAfter)
	}
	if r.TitleWhenHidden == "" || !strings.Contains(r.TitleWhenHidden, "Show projects") {
		t.Errorf("the button does not say what it will do: %q", r.TitleWhenHidden)
	}
	if r.Stored != "1" {
		t.Errorf("the choice was not remembered (localStorage = %q)", r.Stored)
	}
	if !r.ShownAfterCtrlB {
		t.Error("Ctrl-B did not bring the panel back")
	}
	if !r.CollapsedAtEnd {
		t.Fatal("expected it collapsed before checking the reload")
	}

	// It has to survive a reload, or remembering it is pointless.
	c.call(t, "Page.reload", map[string]any{})
	c.waitEvent(t, "Page.loadEventFired", 30*time.Second)
	after := c.evalString(t, `
new Promise(async resolve => {
  for (let i = 0; i < 60; i++) {
    if (document.querySelector('#menuBtn')) break;
    await new Promise(r => setTimeout(r, 250));
  }
  await new Promise(r => setTimeout(r, 300));
  const side = document.querySelector('#sidebar');
  resolve(JSON.stringify({
    collapsed: document.querySelector('#app').classList.contains('nav-collapsed'),
    width: Math.round(side.getBoundingClientRect().width),
    title: document.querySelector('#menuBtn').title,
  }));
})`)
	t.Logf("after reload: %s", after)
	var a struct {
		Collapsed bool   `json:"collapsed"`
		Width     int    `json:"width"`
		Title     string `json:"title"`
	}
	if err := json.Unmarshal([]byte(after), &a); err != nil {
		t.Fatalf("unreadable: %v (%s)", err, after)
	}
	if !a.Collapsed || a.Width > 40 {
		t.Errorf("the panel came back after a reload: collapsed=%v width=%d", a.Collapsed, a.Width)
	}
	if !strings.Contains(a.Title, "Show projects") {
		t.Errorf("the button's label is wrong after a reload: %q", a.Title)
	}

	// Narrow, the panel is an overlay rather than a column, and a remembered
	// collapse must not leave the phone layout in a state it does not have.
	c.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": 420, "height": 900, "deviceScaleFactor": 1, "mobile": true,
	})
	phone := c.evalString(t, `
new Promise(async resolve => {
  await new Promise(r => setTimeout(r, 400));
  const app = document.querySelector('#app');
  const side = document.querySelector('#sidebar');
  const btn = document.querySelector('#menuBtn');
  const out = { keptCollapsedClass: app.classList.contains('nav-collapsed') };
  out.hiddenAtRest = side.getBoundingClientRect().width < 40;
  btn.click();
  await new Promise(r => setTimeout(r, 200));
  out.opensAsOverlay = side.getBoundingClientRect().width > 200;
  btn.click();
  await new Promise(r => setTimeout(r, 200));
  out.closesAgain = side.getBoundingClientRect().width < 40;
  out.appWide = document.body.scrollWidth <= window.innerWidth + 2;
  resolve(JSON.stringify(out));
})`)
	t.Logf("phone: %s", phone)
	var p struct {
		KeptCollapsedClass bool `json:"keptCollapsedClass"`
		HiddenAtRest       bool `json:"hiddenAtRest"`
		OpensAsOverlay     bool `json:"opensAsOverlay"`
		ClosesAgain        bool `json:"closesAgain"`
		AppWide            bool `json:"appWide"`
	}
	if err := json.Unmarshal([]byte(phone), &p); err != nil {
		t.Fatalf("unreadable: %v (%s)", err, phone)
	}
	if p.KeptCollapsedClass {
		t.Error("the desktop collapsed class survived onto the phone layout")
	}
	if !p.HiddenAtRest {
		t.Error("the panel should be out of the way on a phone until asked for")
	}
	if !p.OpensAsOverlay {
		t.Error("the toggle does not open the panel on a phone")
	}
	if !p.ClosesAgain {
		t.Error("the toggle does not close it again on a phone")
	}
	if !p.AppWide {
		t.Error("the page is wider than the window on a phone")
	}
}
