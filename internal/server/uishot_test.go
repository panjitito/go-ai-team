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

// TestUIChatScreenshot renders the conversation view in the test's own headless
// Chrome and saves a PNG, so the look can be checked without touching anybody's
// browser.
func TestUIChatScreenshot(t *testing.T) {
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
  // Open the live agent, which lands on the conversation view.
  const live = S.sessions.find(s => s.kind === 'agent' && s.status !== 'exited');
  if (!live) return resolve('NO LIVE SESSION');
  openTerm(live.id);
  for (let i = 0; i < 40; i++) {
    await sleep(400);
    if (document.querySelectorAll('.msg').length) break;
  }
  const msgs = document.querySelectorAll('.msg').length;
  const inputs = document.querySelectorAll('#chatBody textarea, .composer textarea').length;
  const termVisible = document.querySelector('#termHost')?.style.display !== 'none';
  resolve(JSON.stringify({ msgs, inputs, termVisible,
    md: document.querySelectorAll('.md').length,
    tools: document.querySelectorAll('.tool').length,
    badge: document.querySelector('#termTokens')?.textContent }));
})`)
	t.Logf("chat view: %s", got)

	// A pass that checked nothing is worse than no test: without a live agent
	// this used to photograph an empty screen and report success.
	if strings.Contains(got, "NO LIVE SESSION") || strings.Contains(got, "NO COMPOSER") {
		t.Skipf("%s — start an agent first", got)
	}
	var v struct {
		Msgs        int    `json:"msgs"`
		Inputs      int    `json:"inputs"`
		TermVisible bool   `json:"termVisible"`
		Badge       string `json:"badge"`
	}
	if err := json.Unmarshal([]byte(got), &v); err != nil {
		t.Fatalf("unreadable result: %v", err)
	}
	if v.Msgs == 0 {
		t.Error("the conversation rendered no messages")
	}
	if v.Inputs != 1 {
		t.Errorf("found %d inputs in chat mode, want exactly 1", v.Inputs)
	}
	if v.TermVisible {
		t.Error("the terminal is visible in chat mode, which is the stacked-input problem")
	}
	if v.Badge == "" || v.Badge == "—" {
		t.Errorf("token badge reads %q; it should carry the session's figures", v.Badge)
	}

	res := c.call(t, "Page.captureScreenshot", map[string]any{"format": "png"})
	data, _ := res["data"].(string)
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(raw) == 0 {
		t.Fatalf("no screenshot: %v", err)
	}
	out := filepath.Join(os.TempDir(), "goaiteam-chat-view.png")
	if d := os.Getenv("UISHOT_DIR"); d != "" {
		out = filepath.Join(d, "chat-view.png")
	}
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("saved %s (%d bytes)", out, len(raw))
}
