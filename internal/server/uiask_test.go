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
	"time"
)

// The permission prompt, answered from the conversation, against a real CLI.
//
// TestUIAskPanel checks the panel renders from a made-up question; this checks
// the part no fixture can: that a live Claude Code, asked to do something it
// must get permission for, produces a box this app recognises, and that
// clicking the button actually answers it. The proof is a file on disk.
func TestUIAskLive(t *testing.T) {
	port := 7788
	if _, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/settings", port)); err != nil {
		t.Skip("no server on 7788; start one first")
	}
	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	name := fmt.Sprintf("ask-%d.txt", time.Now().Unix())

	got := c.evalString(t, fmt.Sprintf(`
new Promise(async resolve => {
  const sleep = ms => new Promise(r => setTimeout(r, ms));
  const out = {};
  for (let i = 0; i < 60; i++) {
    if (document.querySelectorAll('.card').length) break;
    await sleep(250);
  }
  const live = S.sessions.find(s => s.kind === 'agent' && s.status !== 'exited');
  if (!live) return resolve(JSON.stringify({ error: 'NO LIVE SESSION' }));
  out.cwd = live.cwd;
  openTerm(live.id);
  for (let i = 0; i < 60; i++) { await sleep(400); if (document.querySelector('#composerBox')) break; }

  // Manual mode, or the auto-mode classifier may simply decide this is safe and
  // never ask — and then there is nothing to test.
  const conv0 = await api('/sessions/' + live.id + '/conversation');
  window.__askTestPriorMode = (conv0.line || {}).mode || '';
  window.__askTestSession = live.id;
  await api('/sessions/' + live.id + '/mode', { method: 'POST', body: { mode: 'manual' } });
  await sleep(1500);

  const box = document.querySelector('#composerBox');
  box.value = 'Create a file named %s containing the word ok. Nothing else.';
  box.dispatchEvent(new Event('input'));
  await sendComposer(live.id);

  // Wait for the panel.
  let panel = null;
  for (let i = 0; i < 90; i++) {
    await sleep(1000);
    panel = document.querySelector('.ask');
    if (panel) break;
  }
  if (!panel) return resolve(JSON.stringify({ ...out, error: 'NO PANEL: status ' +
    (document.querySelector('#chatStatus') || {}).textContent }));

  out.question = (panel.querySelector('.ask-q') || {}).textContent || '';
  out.options = [...panel.querySelectorAll('.ask-opt .ask-label')].map(n => n.textContent);
  out.status = ((document.querySelector('#chatStatus') || {}).textContent || '').trim();
  out.thinkingHidden = !document.querySelector('.thinking');
  out.bannerGone = !panel.parentElement.querySelector('.banner-main');
  out.msgsDrawn = document.querySelectorAll('#chatBody .msg').length;
  out.jsErrors = String(window.__uiErrors || '').slice(0, 300);
  // The tool the CLI is asking about is still pending, and the conversation has
  // to keep rendering around it. It did not: the signature that decides whether
  // to repaint read .length off a result that is not there while a tool is
  // running, threw before anything was drawn, and the poll swallowed it. The
  // view froze for exactly as long as the question stayed on screen.
  out.toolCardShown = !!document.querySelector('#chatBody .tool');
  resolve(JSON.stringify(out));
})`, name))

	t.Logf("live ask: %s", got)

	if dir := os.Getenv("UISHOT_DIR"); dir != "" {
		res := c.call(t, "Page.captureScreenshot", map[string]any{"format": "png"})
		if data, _ := res["data"].(string); data != "" {
			if raw, err := base64.StdEncoding.DecodeString(data); err == nil {
				p := filepath.Join(dir, "ask-panel.png")
				_ = os.WriteFile(p, raw, 0o644)
				t.Logf("saved %s", p)
			}
		}
	}

	// Answering happens after the screenshot, so the picture is of the question
	// rather than of the empty panel that follows it.
	cleared := c.evalString(t, `
new Promise(async resolve => {
  const panel = document.querySelector('.ask');
  if (!panel) return resolve('GONE BEFORE THE CLICK');
  panel.querySelector('.ask-opt').click();
  await new Promise(r => setTimeout(r, 1200));
  resolve(document.querySelector('.ask') ? 'STILL UP' : 'cleared');
})`)
	if cleared != "cleared" {
		t.Errorf("after answering: %s — a second click would send a stray digit", cleared)
	}

	var r struct {
		Error          string   `json:"error"`
		CWD            string   `json:"cwd"`
		Question       string   `json:"question"`
		Options        []string `json:"options"`
		Status         string   `json:"status"`
		ThinkingHidden bool     `json:"thinkingHidden"`
		BannerGone     bool     `json:"bannerGone"`
		MsgsDrawn      int      `json:"msgsDrawn"`
		JSErrors       string   `json:"jsErrors"`
		ToolCardShown  bool     `json:"toolCardShown"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("unreadable: %v", err)
	}
	if r.Error != "" {
		t.Skipf("%s", r.Error)
	}

	if !strings.Contains(r.Question, name) {
		t.Errorf("the question does not name the file it is asking about: %q", r.Question)
	}
	if len(r.Options) < 2 {
		t.Errorf("options = %v, want at least a yes and a no", r.Options)
	}
	// "waiting" is what the session status says whether it is waiting for the
	// model or waiting for you. Only one of those is worth interrupting for.
	if !strings.Contains(r.Status, "needs you") {
		t.Errorf("status = %q, want it to say the agent needs you", r.Status)
	}
	if r.JSErrors != "" {
		t.Errorf("the page threw while a question was up: %s", r.JSErrors)
	}
	if r.MsgsDrawn == 0 {
		t.Error("the conversation went blank while a question was up")
	}
	if !r.ToolCardShown {
		t.Error("the pending tool the question is about was never drawn; the view is frozen")
	}
	if !r.ThinkingHidden {
		t.Error("the working indicator is still spinning over a question the agent is waiting on")
	}
	if !r.BannerGone {
		t.Error("the go-to-the-terminal banner is showing next to buttons that answer it here")
	}
	// Put the mode back. A session left in manual asks permission for everything,
	// which is not the state the next test expects to find it in.
	c.evalString(t, `
new Promise(async resolve => {
  const was = window.__askTestPriorMode, id = window.__askTestSession;
  if (!was || was === 'manual' || !id) return resolve('nothing to restore');
  try { await api('/sessions/' + id + '/mode', { method: 'POST', body: { mode: was } }); }
  catch (e) { return resolve('restore failed: ' + e.message); }
  resolve('restored ' + was);
})`)

	// The point of the whole feature: the click reached the CLI.
	target := filepath.Join(r.CWD, name)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(target); err == nil {
			_ = os.Remove(target)
			return
		}
		time.Sleep(time.Second)
	}
	t.Errorf("answering Yes never produced %s", target)
}
