//go:build uitest

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// After Send there must be something on screen saying the agent is working.
//
// Between pressing Send and the first words coming back there can be a long
// silence — the CLI is thinking or reading files, and none of that reaches the
// transcript until a turn is written. The view showed the message and then
// nothing, while the terminal one click away was visibly busy.
func TestUIThinkingIndicator(t *testing.T) {
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
  for (let i = 0; i < 40; i++) { await sleep(400); if (document.querySelector('#composerBox')) break; }
  const box = document.querySelector('#composerBox');
  if (!box) return resolve(JSON.stringify({ error: 'NO COMPOSER' }));

  // Start from an idle agent. If one is still finishing an earlier turn the
  // indicator is correctly already up, and "did it appear on Send" cannot be
  // asked yet.
  let settled = false;
  for (let i = 0; i < 90; i++) {
    if (!document.querySelector('.thinking')) { settled = true; break; }
    await sleep(1000);
  }
  if (!settled) return resolve(JSON.stringify({ error: 'AGENT NEVER WENT IDLE' }));
  const before = !!document.querySelector('.thinking');

  box.value = 'Reply with exactly: THINKING-OK';
  box.dispatchEvent(new Event('input'));
  sendComposer(live.id);

  // Immediately: within a frame, long before the 1.5s poll could have run.
  await sleep(120);
  const rightAway = !!document.querySelector('.thinking');
  const labelAtOnce = (document.querySelector('.thinking-what') || {}).textContent;

  // The elapsed count must actually advance, on its own clock.
  await sleep(2300);
  const stillThere = !!document.querySelector('.thinking');
  const elapsed = (document.querySelector('.thinking-for') || {}).textContent;

  // And it must go away once the agent stops.
  let cleared = false, waited = 0;
  for (let i = 0; i < 60; i++) {
    await sleep(1000); waited++;
    if (!document.querySelector('.thinking')) { cleared = true; break; }
  }
  resolve(JSON.stringify({
    before, rightAway, labelAtOnce, stillThere, elapsed, cleared, waited,
    dots: document.querySelectorAll('.thinking-dots i').length,
  }));
})`)
	t.Logf("thinking: %s", got)

	var r struct {
		Error       string `json:"error"`
		Before      bool   `json:"before"`
		RightAway   bool   `json:"rightAway"`
		LabelAtOnce string `json:"labelAtOnce"`
		StillThere  bool   `json:"stillThere"`
		Elapsed     string `json:"elapsed"`
		Cleared     bool   `json:"cleared"`
		Waited      int    `json:"waited"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("unreadable: %v", err)
	}
	if r.Error != "" {
		t.Skipf("%s", r.Error)
	}

	if r.Before {
		t.Error("the indicator was already showing before anything was sent")
	}
	if !r.RightAway {
		t.Error("nothing appeared within 120ms of Send — the silent gap is exactly the problem")
	}
	if r.LabelAtOnce == "" {
		t.Error("the indicator has no label")
	}
	if !r.StillThere {
		t.Error("the indicator vanished while the agent was still working")
	}
	if r.Elapsed == "" || r.Elapsed == "0s" {
		t.Errorf("elapsed reads %q after 2.4s; the clock is not ticking", r.Elapsed)
	}
	if !r.Cleared {
		t.Errorf("the indicator never went away (waited %ds after the agent finished)", r.Waited)
	}
}
