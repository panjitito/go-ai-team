//go:build uitest

package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The strip above the composer, and the thing it was doing wrong.
//
// It hid every field the moment a scrape came back without it, so a reading
// that was correct a second earlier vanished and came back on its own. The
// server holds the last one now and says how old it is; this checks the page
// draws that rather than blanking, and that an account which will never produce
// the numbers gets told so instead of an empty row.
func TestUIRunBarHoldsAndExplains(t *testing.T) {
	const port = 7851
	startServer(t, port)

	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	if ready := c.evalString(t, `
new Promise(async resolve => {
  for (let i = 0; i < 80; i++) {
    if (typeof updateRunBar === 'function' && typeof runBarEl === 'function') return resolve('ready');
    await new Promise(r => setTimeout(r, 250));
  }
  resolve('timeout');
})`); ready != "ready" {
		t.Fatalf("the UI never finished loading: %s", ready)
	}

	out := c.evalString(t, `
new Promise(async resolve => {
  // A run bar of its own, off to one side, so this does not depend on a
  // conversation being open.
  const host = document.createElement('div');
  host.append(runBarEl('s1'));
  document.body.append(host);

  const read = () => {
    const pct = sel => {
      const n = document.querySelector(sel);
      if (!n || n.style.display === 'none') return null;
      return {
        text: (n.querySelector('.rb-meter-pct') || n).textContent,
        stale: n.classList.contains('rb-stale'),
        title: n.title || '',
      };
    };
    const stat = sel => {
      const n = document.querySelector(sel);
      if (!n || n.style.display === 'none') return null;
      return { text: n.textContent, stale: n.classList.contains('rb-stale') };
    };
    const note = document.querySelector('#rbNote');
    return {
      fiveHour: pct('#rb5h'),
      weekly: pct('#rbWk'),
      ctx: stat('#rbCtx'),
      cost: stat('#rbCost'),
      note: note && note.style.display !== 'none' ? note.textContent : null,
      noteTitle: note ? note.title : '',
      hasWhyButton: !!(note && note.querySelector('button')),
    };
  };

  const sess = { id: 's1', accountId: 'acc_1', accountName: 'EP-Work' };
  const out = {};

  // A reading, fresh.
  updateRunBar({ line: {
    model: 'Opus 5', mode: 'auto',
    context: 42, hasContext: true,
    fiveHour: 13, hasFiveHour: true,
    weekly: 34, hasWeekly: true,
    cost: 1.23, hasCost: true,
    ageSeconds: 0,
  } }, sess);
  out.fresh = read();

  // The same reading, held, because the last scrape found nothing. This is the
  // case that used to blank the strip.
  updateRunBar({ line: {
    model: 'Opus 5', mode: 'auto',
    context: 42, hasContext: true,
    fiveHour: 13, hasFiveHour: true,
    weekly: 34, hasWeekly: true,
    cost: 1.23, hasCost: true,
    ageSeconds: 95,
  } }, sess);
  out.held = read();

  // Nothing at all, on an account that has no status-line command.
  updateRunBar({
    line: { ageSeconds: 0 },
    lineNote: 'This account has no status-line command, so the CLI prints no usage figures and there is nothing to read.',
    lineFixable: true,
  }, sess);
  out.never = read();

  // Nothing yet, on an account that does have one. Different message, and
  // nothing to offer, because it turns up on its own.
  updateRunBar({
    line: { ageSeconds: 0 },
    lineNote: 'Waiting for the first status line from this session.',
    lineFixable: false,
  }, sess);
  out.waiting = read();

  // And back to a reading: the explanation goes away.
  updateRunBar({ line: { fiveHour: 20, hasFiveHour: true, ageSeconds: 0 } }, sess);
  out.recovered = read();

  out.dialogExists = typeof offerStatusLine === 'function';

  host.remove();
  resolve(JSON.stringify(out));
})`)

	t.Logf("runbar: %s", out)

	type meter struct {
		Text  string `json:"text"`
		Stale bool   `json:"stale"`
		Title string `json:"title"`
	}
	type snap struct {
		FiveHour     *meter `json:"fiveHour"`
		Weekly       *meter `json:"weekly"`
		Ctx          *meter `json:"ctx"`
		Cost         *meter `json:"cost"`
		Note         string `json:"note"`
		NoteTitle    string `json:"noteTitle"`
		HasWhyButton bool   `json:"hasWhyButton"`
	}
	var got struct {
		Fresh, Held, Never, Waiting, Recovered snap
		DialogExists                           bool `json:"dialogExists"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unreadable: %v", err)
	}

	// Fresh: everything on screen, nothing dimmed.
	if got.Fresh.FiveHour == nil || got.Fresh.FiveHour.Text != "13%" {
		t.Fatalf("the five-hour meter did not draw: %+v", got.Fresh.FiveHour)
	}
	if got.Fresh.FiveHour.Stale {
		t.Error("a fresh reading was marked stale")
	}
	if got.Fresh.Ctx == nil || got.Fresh.Cost == nil || got.Fresh.Weekly == nil {
		t.Errorf("something did not draw: %+v", got.Fresh)
	}
	if got.Fresh.Note != "" {
		t.Errorf("explained a strip that has numbers on it: %q", got.Fresh.Note)
	}

	// Held: still there, marked, and saying how old.
	if got.Held.FiveHour == nil {
		t.Fatal("a held reading was hidden, which is the bug this is about")
	}
	if got.Held.FiveHour.Text != "13%" {
		t.Errorf("the held value changed to %q", got.Held.FiveHour.Text)
	}
	if !got.Held.FiveHour.Stale {
		t.Error("a reading a minute and a half old was shown as current")
	}
	if !strings.Contains(got.Held.FiveHour.Title, "ago") {
		t.Errorf("nothing says when it was read: %q", got.Held.FiveHour.Title)
	}
	if got.Held.Ctx == nil || !got.Held.Ctx.Stale {
		t.Errorf("the context reading was not held and marked: %+v", got.Held.Ctx)
	}

	// Never: no meters, and an explanation with a way in.
	if got.Never.FiveHour != nil {
		t.Errorf("invented a meter with nothing to show: %+v", got.Never.FiveHour)
	}
	if got.Never.Note == "" {
		t.Error("an empty strip with no explanation, which is what was reported")
	}
	if !got.Never.HasWhyButton {
		t.Error("nothing to click on a strip that will never fill in by itself")
	}
	if !strings.Contains(got.Never.NoteTitle, "status-line command") {
		t.Errorf("the reason is not readable on hover: %q", got.Never.NoteTitle)
	}

	// Waiting: says something different, and offers nothing, because it fixes
	// itself.
	if got.Waiting.Note == "" {
		t.Error("no word about a strip that has not filled in yet")
	}
	if got.Waiting.HasWhyButton {
		t.Error("offered a fix for something that needs none")
	}
	if got.Waiting.Note == got.Never.Note {
		t.Errorf("both reasons read the same: %q", got.Waiting.Note)
	}

	// Recovered: the note goes away again.
	if got.Recovered.Note != "" {
		t.Errorf("the explanation stayed after the numbers came back: %q", got.Recovered.Note)
	}
	if got.Recovered.FiveHour == nil || got.Recovered.FiveHour.Text != "20%" {
		t.Errorf("the new reading did not draw: %+v", got.Recovered.FiveHour)
	}

	if !got.DialogExists {
		t.Error("the why button has nothing to open")
	}
}
