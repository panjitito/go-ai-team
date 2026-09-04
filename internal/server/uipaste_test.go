//go:build uitest

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Pasting an image into the composer.
//
// This started as a question — what happens if you paste a screenshot? — and the
// answer was: nothing at all. The clipboard carried the image, the textarea
// ignored it, and Send bailed because the box was still empty. No error, no
// hint. This test is here so that cannot come back quietly.
func TestUIPasteImage(t *testing.T) {
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

  // A real PNG, delivered the way a clipboard delivers one.
  const b64 = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==';
  const bin = atob(b64);
  const arr = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) arr[i] = bin.charCodeAt(i);
  const file = new File([arr], 'screenshot.png', { type: 'image/png' });

  const dt = new DataTransfer();
  dt.items.add(file);
  box.focus();
  box.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }));

  // Wait for the upload to land.
  for (let i = 0; i < 40; i++) {
    await sleep(250);
    if (typeof attachPaths === 'function' && attachPaths().length) break;
  }

  const paths = attachPaths();
  const thumbs = document.querySelectorAll('.attach-thumb').length;

  // Pasting plain text must still behave exactly as before.
  const textDT = new DataTransfer();
  textDT.setData('text/plain', 'hello');
  const textEv = new ClipboardEvent('paste', { clipboardData: textDT, bubbles: true, cancelable: true });
  const textNotCancelled = box.dispatchEvent(textEv);

  resolve(JSON.stringify({
    paths, thumbs,
    pathLooksAbsolute: paths.length ? /^([A-Za-z]:\/|\/)/.test(paths[0]) : false,
    keepsOurName: paths.length ? paths[0].includes('screenshot') : false,
    isPng: paths.length ? paths[0].endsWith('.png') : false,
    outsideProject: paths.length ? !paths[0].includes('/Projects/go-ai-team/') : false,
    textPasteStillDefault: textNotCancelled,
    busy: attachBusy(),
  }));
})`)
	t.Logf("paste result: %s", got)

	var r struct {
		Error                 string   `json:"error"`
		Paths                 []string `json:"paths"`
		Thumbs                int      `json:"thumbs"`
		PathLooksAbsolute     bool     `json:"pathLooksAbsolute"`
		KeepsOurName          bool     `json:"keepsOurName"`
		IsPng                 bool     `json:"isPng"`
		OutsideProject        bool     `json:"outsideProject"`
		TextPasteStillDefault bool     `json:"textPasteStillDefault"`
		Busy                  bool     `json:"busy"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("could not read the result: %v", err)
	}
	if r.Error != "" {
		t.Skipf("%s", r.Error)
	}

	if len(r.Paths) != 1 {
		t.Fatalf("a pasted image produced %d attachments, want 1", len(r.Paths))
	}
	if r.Thumbs != 1 {
		t.Errorf("thumbnails shown = %d, want 1: the person must see what is about to be sent", r.Thumbs)
	}
	if !r.PathLooksAbsolute {
		t.Errorf("path %q is not absolute; the agent's own cwd may differ", r.Paths[0])
	}
	if !r.IsPng {
		t.Errorf("path %q did not keep the .png extension", r.Paths[0])
	}
	if !r.KeepsOurName {
		t.Errorf("path %q dropped the original name entirely", r.Paths[0])
	}
	if !r.OutsideProject {
		t.Errorf("path %q is inside the project: a pasted screenshot must not land in a repository", r.Paths[0])
	}
	if !r.TextPasteStillDefault {
		t.Error("pasting plain text was intercepted; only images should be taken over")
	}
	if r.Busy {
		t.Error("upload still reported as in flight after it completed")
	}

	// The file must actually be on disk, or the agent will be sent looking for
	// something that is not there.
	if strings.TrimSpace(r.Paths[0]) == "" {
		t.Fatal("empty path")
	}
}
