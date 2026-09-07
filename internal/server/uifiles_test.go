//go:build uitest

package server

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// A picture of the Files view and of the new diff, so the look can be checked
// without opening anybody's browser.
func TestUIFilesShot(t *testing.T) {
	port := 7788
	if _, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/settings", port)); err != nil {
		skipOrFail(t, "no server on 7788; start one first")
	}
	dir := os.Getenv("UISHOT_DIR")
	if dir == "" {
		dir = os.TempDir()
	}

	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	shot := func(name, script string) {
		out := c.evalString(t, script)
		t.Logf("%s: %s", name, out)
		res := c.call(t, "Page.captureScreenshot", map[string]any{"format": "png"})
		data, _ := res["data"].(string)
		raw, err := base64.StdEncoding.DecodeString(data)
		if err != nil || len(raw) == 0 {
			t.Fatalf("no screenshot for %s: %v", name, err)
		}
		p := filepath.Join(dir, name+".png")
		if err := os.WriteFile(p, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("saved %s (%d bytes)", p, len(raw))
	}

	shot("files-view", `
new Promise(async resolve => {
  const sleep = ms => new Promise(r => setTimeout(r, ms));
  for (let i = 0; i < 60; i++) {
    if (document.querySelectorAll('#tabbar .tab').length) break;
    await sleep(250);
  }
  const t = [...document.querySelectorAll('#tabbar .tab')].find(x => x.textContent === 'Files');
  if (!t) return resolve('NO FILES TAB');
  t.click();
  await sleep(1500);
  // Open the first source file in the tree.
  const rows = [...document.querySelectorAll('.tw-row.file')];
  const go = rows.find(r => /\.(go|js|md)$/.test(r.textContent)) || rows[0];
  if (!go) return resolve('NO FILES IN TREE');
  go.click();
  await sleep(1500);
  resolve(JSON.stringify({
    treeRows: document.querySelectorAll('.tw-row').length,
    lines: document.querySelectorAll('.code-body .cl').length,
    gutter: document.querySelectorAll('.code-gutter .ln').length,
    highlighted: document.querySelectorAll('.code-body [class^=tk-]').length,
    name: (document.querySelector('.file-name') || {}).textContent,
  }));
})`)

	shot("diff-view", `
new Promise(async resolve => {
  const sleep = ms => new Promise(r => setTimeout(r, ms));
  const t = [...document.querySelectorAll('#tabbar .tab')].find(x => x.textContent === 'Review');
  if (!t) return resolve('NO REVIEW TAB');
  t.click();
  await sleep(1800);
  // A modified file, not a new one: the word-level marking only applies where a
  // line was edited rather than wholly added.
  const rows = [...document.querySelectorAll('.review-row')];
  const row = rows.find(r => r.textContent.includes('−')) || rows[0];
  if (!row) return resolve('NO CHANGED FILES');
  row.click();
  await sleep(1800);
  resolve(JSON.stringify({
    split: document.querySelectorAll('.diff-table.split').length,
    hunks: document.querySelectorAll('.diff-hunk').length,
    numbered: document.querySelectorAll('.diff-table .dn').length,
    wordMarks: document.querySelectorAll('.dw-add, .dw-del').length,
  }));
})`)
}
