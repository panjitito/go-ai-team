//go:build uitest

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// Dragging a project into a folder, and back out.
//
// Folders could only be filled by opening a dialog and picking from a dropdown,
// which is not how anyone thinks about putting a thing in a folder.
func TestUIDragIntoFolder(t *testing.T) {
	port := 7788
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	if _, err := http.Get(base + "/api/settings"); err != nil {
		t.Skip("no server on 7788; start one first")
	}

	c := launchChrome(t)
	c.openTarget(t, base+"/")

	got := c.evalString(t, `
new Promise(async resolve => {
  const sleep = ms => new Promise(r => setTimeout(r, ms));
  for (let i = 0; i < 60; i++) {
    if (document.querySelectorAll('#sidebar .tree-item').length) break;
    await sleep(250);
  }

  // A folder and a project of our own, so nothing of the user's is moved.
  const f = await api('/folders', { method: 'POST', body: { name: 'uitest-folder' } });
  const p = await api('/projects', { method: 'POST', body: { name: 'uitest-proj', path: '.' } });
  await loadAll(); render(); await sleep(300);

  const rowFor = name => [...document.querySelectorAll('#sidebar .tree-item')]
    .find(r => r.textContent.includes(name));

  const projRow = rowFor('uitest-proj');
  const folderRow = rowFor('uitest-folder');
  if (!projRow || !folderRow) {
    await api('/folders/' + f.id, { method: 'DELETE' });
    await api('/projects/' + p.id, { method: 'DELETE' });
    return resolve(JSON.stringify({ error: 'rows not found' }));
  }

  const out = { draggable: projRow.getAttribute('draggable') === 'true' };

  // Drag it onto the folder, the way a browser does.
  const dt = new DataTransfer();
  projRow.dispatchEvent(new DragEvent('dragstart', { dataTransfer: dt, bubbles: true }));
  out.rootZoneShown = getComputedStyle(document.querySelector('.drop-root')).display !== 'none';
  folderRow.dispatchEvent(new DragEvent('dragover', { dataTransfer: dt, bubbles: true, cancelable: true }));
  out.targetHighlighted = folderRow.classList.contains('drop-into');
  folderRow.dispatchEvent(new DragEvent('drop', { dataTransfer: dt, bubbles: true, cancelable: true }));
  await sleep(1200);

  let ps = await api('/projects');
  out.movedIn = (ps.find(x => x.id === p.id) || {}).folderId === f.id;

  // And back out, onto the root zone.
  const projRow2 = rowFor('uitest-proj');
  const root = document.querySelector('.drop-root');
  const dt2 = new DataTransfer();
  projRow2.dispatchEvent(new DragEvent('dragstart', { dataTransfer: dt2, bubbles: true }));
  root.dispatchEvent(new DragEvent('dragover', { dataTransfer: dt2, bubbles: true, cancelable: true }));
  root.dispatchEvent(new DragEvent('drop', { dataTransfer: dt2, bubbles: true, cancelable: true }));
  await sleep(1200);

  ps = await api('/projects');
  out.movedOut = !((ps.find(x => x.id === p.id) || {}).folderId);

  // A folder must refuse to be dropped into itself.
  const folderRow2 = rowFor('uitest-folder');
  const dt3 = new DataTransfer();
  folderRow2.dispatchEvent(new DragEvent('dragstart', { dataTransfer: dt3, bubbles: true }));
  const ev = new DragEvent('dragover', { dataTransfer: dt3, bubbles: true, cancelable: true });
  folderRow2.dispatchEvent(ev);
  out.selfDropRefused = !folderRow2.classList.contains('drop-into');
  folderRow2.dispatchEvent(new DragEvent('dragend', { dataTransfer: dt3, bubbles: true }));
  out.zoneHiddenAfter = getComputedStyle(document.querySelector('.drop-root')).display === 'none';

  await api('/folders/' + f.id, { method: 'DELETE' });
  await api('/projects/' + p.id, { method: 'DELETE' });
  await loadAll(); render();
  resolve(JSON.stringify(out));
})`)
	t.Logf("drag: %s", got)

	var r struct {
		Error             string `json:"error"`
		Draggable         bool   `json:"draggable"`
		RootZoneShown     bool   `json:"rootZoneShown"`
		TargetHighlighted bool   `json:"targetHighlighted"`
		MovedIn           bool   `json:"movedIn"`
		MovedOut          bool   `json:"movedOut"`
		SelfDropRefused   bool   `json:"selfDropRefused"`
		ZoneHiddenAfter   bool   `json:"zoneHiddenAfter"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("unreadable: %v", err)
	}
	if r.Error != "" {
		t.Skipf("%s", r.Error)
	}

	if !r.Draggable {
		t.Error("a project row cannot be picked up")
	}
	if !r.RootZoneShown {
		t.Error("the take-it-out drop zone did not appear while dragging")
	}
	if !r.TargetHighlighted {
		t.Error("the folder under the pointer was not highlighted, so there is no sign a drop will land")
	}
	if !r.MovedIn {
		t.Error("dropping a project on a folder did not move it there")
	}
	if !r.MovedOut {
		t.Error("dropping a project on the root zone did not take it out of the folder")
	}
	if !r.SelfDropRefused {
		t.Error("a folder offered to accept itself, which would detach it from the tree")
	}
	if !r.ZoneHiddenAfter {
		t.Error("the drop zone stayed on screen after the drag ended")
	}
}
