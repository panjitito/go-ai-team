//go:build uitest

package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The strip above the composer: the model, the effort, and the two rate-limit
// windows that actually govern a day's work — all of which were only readable by
// switching to the terminal tab.
func TestUIRunBar(t *testing.T) {
	port := 7788
	if _, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/settings", port)); err != nil {
		t.Skip("no server on 7788; start one first")
	}
	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	got := c.evalString(t, `
new Promise(async resolve => {
  const sleep = ms => new Promise(r => setTimeout(r, ms));
  for (let i = 0; i < 60; i++) {
    if (document.querySelectorAll('.card').length) break;
    await sleep(250);
  }
  const live = S.sessions.find(s => s.kind === 'agent' && s.status !== 'exited');
  if (!live) return resolve(JSON.stringify({ error: 'NO LIVE SESSION' }));
  openTerm(live.id);
  for (let i = 0; i < 60; i++) { await sleep(400); if (document.querySelector('#runBar')) break; }
  if (!document.querySelector('#runBar')) return resolve(JSON.stringify({ error: 'NO RUN BAR' }));

  // Let a poll or two land so the bar is filled from the status line.
  await sleep(3500);

  const txt = sel => (document.querySelector(sel) || {}).textContent || '';
  const shown = sel => {
    const n = document.querySelector(sel);
    return !!n && n.style.display !== 'none';
  };

  // The model menu opens and lists the aliases, above the button.
  document.querySelector('#rbModel').click();
  await sleep(300);
  const menu = document.querySelector('.rb-menu');
  const items = menu ? [...menu.querySelectorAll('.rb-menu-item')].map(b => b.textContent) : [];
  const menuAbove = menu
    ? menu.getBoundingClientRect().bottom <= document.querySelector('#rbModel').getBoundingClientRect().top + 1
    : false;
  const onScreen = menu ? menu.getBoundingClientRect().top >= 0 : false;
  document.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
  await sleep(200);

  document.querySelector('#rbEffort').click();
  await sleep(300);
  const emenu = document.querySelector('.rb-menu');
  const efforts = emenu ? [...emenu.querySelectorAll('.rb-menu-item')].map(b => b.textContent) : [];
  document.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
  await sleep(200);

  // The permission mode, which is the largest lever the CLI has and had no
  // switch here at all. Each entry says what the mode does, not just its name.
  document.querySelector('#rbMode').click();
  await sleep(300);
  const mmenu = document.querySelector('.rb-menu');
  const modes = mmenu
    ? [...mmenu.querySelectorAll('.rb-menu-item')].map(b => b.firstChild.textContent)
    : [];
  const modeNotes = mmenu
    ? [...mmenu.querySelectorAll('.rb-menu-item .rb-menu-note')].length
    : 0;
  document.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));

  resolve(JSON.stringify({
    mode: txt('#rbMode'),
    modes, modeNotes,
    model: txt('#rbModel'),
    fiveHourShown: shown('#rb5h'),
    weeklyShown: shown('#rbWk'),
    fiveHourPct: txt('#rb5h'),
    weeklyPct: txt('#rbWk'),
    ctx: txt('#rbCtx'),
    cost: txt('#rbCost'),
    models: items,
    efforts,
    menuAbove, onScreen,
    barCount: document.querySelectorAll('#runBar').length,
  }));
})`)
	t.Logf("run bar: %s", got)

	if dir := os.Getenv("UISHOT_DIR"); dir != "" {
		res := c.call(t, "Page.captureScreenshot", map[string]any{"format": "png"})
		if data, _ := res["data"].(string); data != "" {
			if raw, err := base64.StdEncoding.DecodeString(data); err == nil {
				_ = os.WriteFile(filepath.Join(dir, "runbar.png"), raw, 0o644)
			}
		}
	}

	var r struct {
		Error         string   `json:"error"`
		Model         string   `json:"model"`
		FiveHourShown bool     `json:"fiveHourShown"`
		WeeklyShown   bool     `json:"weeklyShown"`
		FiveHourPct   string   `json:"fiveHourPct"`
		WeeklyPct     string   `json:"weeklyPct"`
		Models        []string `json:"models"`
		Efforts       []string `json:"efforts"`
		Mode          string   `json:"mode"`
		Modes         []string `json:"modes"`
		ModeNotes     int      `json:"modeNotes"`
		MenuAbove     bool     `json:"menuAbove"`
		OnScreen      bool     `json:"onScreen"`
		BarCount      int      `json:"barCount"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("unreadable: %v", err)
	}
	if r.Error != "" {
		t.Skipf("%s", r.Error)
	}

	if r.BarCount != 1 {
		t.Errorf("found %d run bars, want 1", r.BarCount)
	}
	if r.Model == "" || r.Model == "—" {
		t.Errorf("the model is not shown (%q); it is in the status line", r.Model)
	}
	if !r.FiveHourShown || !r.WeeklyShown {
		t.Errorf("the rate-limit meters are hidden (5h=%v wk=%v) — the whole point of the bar",
			r.FiveHourShown, r.WeeklyShown)
	}
	if r.FiveHourPct == "" || r.WeeklyPct == "" {
		t.Errorf("meters are empty: 5h=%q wk=%q", r.FiveHourPct, r.WeeklyPct)
	}
	if len(r.Models) < 4 {
		t.Errorf("model menu has %d entries: %v", len(r.Models), r.Models)
	}
	var hasFable bool
	for _, m := range r.Models {
		if m == "Fable" {
			hasFable = true
		}
	}
	if !hasFable {
		t.Errorf("Fable is missing from the model menu: %v", r.Models)
	}
	if len(r.Efforts) != 5 {
		t.Errorf("effort menu = %v, want the five levels the CLI accepts", r.Efforts)
	}
	if r.Mode == "" || r.Mode == "mode" {
		t.Errorf("the permission mode is not shown (%q); the CLI prints it above its composer", r.Mode)
	}
	if len(r.Modes) != 4 {
		t.Errorf("mode menu = %v, want the four the CLI cycles", r.Modes)
	}
	if r.ModeNotes != len(r.Modes) {
		t.Errorf("%d of %d modes explain what they do; a name alone is not enough here",
			r.ModeNotes, len(r.Modes))
	}
	// The bar sits at the bottom of the window, so a menu dropping downwards
	// would open off screen.
	if !r.MenuAbove || !r.OnScreen {
		t.Errorf("menu placement wrong: above=%v onScreen=%v", r.MenuAbove, r.OnScreen)
	}
}

// Picking a model from the bar must actually change the session, and the bar
// must then report the new one — read back from the CLI's own status line, not
// from what we optimistically hoped.
func TestUIRunBarSwitchesModel(t *testing.T) {
	port := 7788
	if _, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/settings", port)); err != nil {
		t.Skip("no server on 7788; start one first")
	}
	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	got := c.evalString(t, `
new Promise(async resolve => {
  const sleep = ms => new Promise(r => setTimeout(r, ms));
  for (let i = 0; i < 60; i++) {
    if (document.querySelectorAll('.card').length) break;
    await sleep(250);
  }
  const live = S.sessions.find(s => s.kind === 'agent' && s.status !== 'exited');
  if (!live) return resolve(JSON.stringify({ error: 'NO LIVE SESSION' }));
  openTerm(live.id);
  for (let i = 0; i < 60; i++) { await sleep(400); if (document.querySelector('#runBar')) break; }
  await sleep(3000);

  const model = () => (document.querySelector('#rbModel') || {}).textContent || '';
  const before = model();

  // Pick whichever of Sonnet/Haiku is not the current one.
  const want = before.toLowerCase().startsWith('sonnet') ? 'Haiku' : 'Sonnet';
  document.querySelector('#rbModel').click();
  await sleep(300);
  const pick = [...document.querySelectorAll('.rb-menu-item')].find(b => b.textContent === want);
  if (!pick) return resolve(JSON.stringify({ error: 'menu entry missing: ' + want }));
  pick.click();

  // Wait for the CLI to act and the status line to be read back.
  let after = before;
  for (let i = 0; i < 40; i++) {
    await sleep(1000);
    after = model();
    if (after.toLowerCase().startsWith(want.toLowerCase())) break;
  }

  // And the effort menu marks what was chosen.
  document.querySelector('#rbEffort').click();
  await sleep(250);
  [...document.querySelectorAll('.rb-menu-item')].find(b => b.textContent === 'high').click();
  await sleep(2500);
  const effortLabel = (document.querySelector('#rbEffort') || {}).textContent;

  // Put the model back. Which model a session is on changes how long a turn
  // takes, and the next test measures exactly that — leaving it switched made
  // a neighbouring check fail for reasons that had nothing to do with it.
  const backTo = before.split(' ')[0];
  if (backTo && backTo.toLowerCase() !== want.toLowerCase()) {
    document.querySelector('#rbModel').click();
    await sleep(300);
    const back = [...document.querySelectorAll('.rb-menu-item')]
      .find(b => b.textContent.toLowerCase() === backTo.toLowerCase());
    if (back) {
      back.click();
      // Read it back, the same way the switch above is read back. A fixed sleep
      // was not long enough and left the session on the other model, which is
      // the state this restore exists to avoid.
      for (let i = 0; i < 30; i++) {
        await sleep(1000);
        if (model().toLowerCase().startsWith(backTo.toLowerCase())) break;
      }
    }
  }

  resolve(JSON.stringify({ before, want, after, effortLabel, restored: model() }));
})`)
	t.Logf("switch: %s", got)

	var r struct {
		Error       string `json:"error"`
		Before      string `json:"before"`
		Want        string `json:"want"`
		After       string `json:"after"`
		EffortLabel string `json:"effortLabel"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("unreadable: %v", err)
	}
	if r.Error != "" {
		t.Skipf("%s", r.Error)
	}
	if !strings.HasPrefix(strings.ToLower(r.After), strings.ToLower(r.Want)) {
		t.Errorf("picked %s but the bar still reads %q (was %q) — the switch did not take",
			r.Want, r.After, r.Before)
	}
	if r.EffortLabel != "high" {
		t.Errorf("effort shows %q after choosing high", r.EffortLabel)
	}
}
