//go:build uitest

package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The reported bug: every changed file opened onto "No textual diff (binary, or
// no change)".
//
// The project had no git repository. The endpoint said so, in those words, with
// a 200 and a body — and the caller decided what it had been given by asking
// whether the body was empty. It was not empty, so a sentence went into the diff
// parser, which found no hunks in it and reported the file as unchanged. The
// answer was in the response the whole time.
func TestUIDiffMessageIsNotADiff(t *testing.T) {
	const port = 7829
	startServer(t, port)

	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	if ready := c.evalString(t, `
new Promise(async resolve => {
  for (let i = 0; i < 80; i++) {
    if (typeof renderDiff === 'function' && typeof openChangedFile === 'function'
        && document.querySelectorAll('#tabbar .tab').length) {
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
    const NO_REPO = 'This project is not a git repository, so there is no diff to show.';
    const REAL = [
      'diff --git a/notes.md b/notes.md',
      'index 1111111..2222222 100644',
      '--- a/notes.md',
      '+++ b/notes.md',
      '@@ -1,2 +1,2 @@',
      ' keep this line',
      '-the old line',
      '+the new line',
    ].join('\n');

    // What the parser thinks each of them is.
    out.knowsMessage = looksLikeDiff(NO_REPO);
    out.knowsDiff = looksLikeDiff(REAL);
    out.knowsEmpty = looksLikeDiff('');
    // A message that happens to mention a diff is still a message.
    out.knowsProse = looksLikeDiff('There is no diff to show for this file.');

    const text = n => (n.textContent || '').trim();

    // The reported symptom, directly.
    out.messageShown = text(renderDiff(NO_REPO));
    // A real diff still draws rows, which is the thing not to break.
    const real = renderDiff(REAL);
    out.realRows = real.querySelectorAll('.dw-add, .dw-del, .diff-row, .drow').length
      || real.querySelectorAll('td, .dl').length;
    out.realHasOld = text(real).includes('the old line');
    out.realHasNew = text(real).includes('the new line');
    // The changed word, marked, on the side it belongs to. The "after" column
    // used to draw the fragment from the "before" one.
    DIFF.split = true;
    const split = renderDiff(REAL);
    out.marks = [...split.querySelectorAll('.dw-add, .dw-del')].map(n => n.textContent);
    out.cells = [...split.querySelectorAll('td.dc')].map(n => (n.textContent || '').trim())
      .filter(x => x);
    // An empty answer is still the honest "nothing to show".
    out.emptyShown = text(renderDiff(''));

    // --- and the rail, end to end against a fake server -------------------
    S.projects = [{ id: 'p1', name: 'no-repo', path: 'C:\\work\\noRepo' }];
    S.selectedProject = 'p1';
    S.agents = [{ id: 'a1', projectId: 'p1', name: 'Dev' }];
    S.sessions = [{ id: 's1', agentId: 'a1', projectId: 'p1', kind: 'agent',
                    status: 'waiting', switchLog: [] }];

    const realFetch = window.fetch;
    const realApi = window.api;
    window.fetch = async () => ({
      ok: true,
      headers: { get: k => (k === 'X-Diff-Kind' ? 'message' : null) },
      text: async () => NO_REPO,
    });
    window.api = async p => {
      if (p.startsWith('/files/read')) return { content: 'the file as it stands\n' };
      return realApi(p);
    };

    await openChangedFile('s1', { rel: 'app/Http/Kernel.php', name: 'Kernel.php', path: 'C:\\work\\noRepo\\app\\Http\\Kernel.php' });
    await sleep(250);
    const modalText = text(document.querySelector('#railDiff') || document.createElement('div'));
    out.railSaysWhy = modalText.includes('not a git repository');
    out.railStillShowsTheFile = modalText.includes('the file as it stands');
    out.railAvoidsTheLie = !modalText.includes('No textual diff');
    closeModal();
    await sleep(80);

    // --- a file the agent wrote outside the project -----------------------
    const away = {
      rel: 'C:\\Users\\TITO\\.claude-uniair\\projects\\x\\memory\\arena-deathmatch-wip.md',
      name: 'arena-deathmatch-wip.md',
      path: 'C:\\Users\\TITO\\.claude-uniair\\projects\\x\\memory\\arena-deathmatch-wip.md',
    };
    out.knowsOutside = outsideProject(away.rel);
    out.knowsInside = outsideProject('app/Http/Kernel.php');

    let asked = 0;
    window.fetch = async () => { asked++; return { ok: false, headers: { get: () => null }, text: async () => '' }; };
    await openChangedFile('s1', away);
    await sleep(200);
    const awayText = text(document.querySelector('#railDiff') || document.createElement('div'));
    out.outsideExplained = awayText.includes('outside the project');
    out.outsideShowsThePath = awayText.includes('arena-deathmatch-wip.md');
    out.outsideAskedGit = asked;
    out.outsideButtons = [...document.querySelectorAll('#overlay .modal-foot button')].map(b => b.textContent);
    out.modalTitle = (document.querySelector('#overlay h2') || {}).textContent;
    closeModal();

    // And the row itself says so before it is clicked.
    stopChatPoll();
    S.chatMode = 'chat';
    openAgent('s1');
    await sleep(200);
    stopChatPoll();
    RAIL.sig = '';
    renderRail({ messages: [], files: [
      { rel: 'app/Http/Kernel.php', name: 'Kernel.php', path: 'x', edits: 1 },
      away,
    ] }, 's1');
    await sleep(80);
    const rows = [...document.querySelectorAll('.rail-item')];
    out.rowCount = rows.length;
    out.awayMarked = rows.map(r => r.classList.contains('away'));
    out.awayDir = rows.map(r => (r.querySelector('.rail-dir') || {}).textContent || '');

    window.fetch = realFetch;
    window.api = realApi;
    resolve(JSON.stringify(out));
  } catch (e) {
    resolve(JSON.stringify({ threw: String(e && e.stack || e) }));
  }
})`)
	t.Logf("diffmsg: %s", got)
	if strings.Contains(got, `"threw"`) {
		t.Fatalf("the page threw: %s", got)
	}

	var r struct {
		KnowsMessage        bool     `json:"knowsMessage"`
		KnowsDiff           bool     `json:"knowsDiff"`
		KnowsEmpty          bool     `json:"knowsEmpty"`
		KnowsProse          bool     `json:"knowsProse"`
		MessageShown        string   `json:"messageShown"`
		RealRows            int      `json:"realRows"`
		RealHasOld          bool     `json:"realHasOld"`
		RealHasNew          bool     `json:"realHasNew"`
		Marks               []string `json:"marks"`
		Cells               []string `json:"cells"`
		EmptyShown          string   `json:"emptyShown"`
		RailSaysWhy         bool     `json:"railSaysWhy"`
		RailStillShows      bool     `json:"railStillShowsTheFile"`
		RailAvoidsTheLie    bool     `json:"railAvoidsTheLie"`
		KnowsOutside        bool     `json:"knowsOutside"`
		KnowsInside         bool     `json:"knowsInside"`
		OutsideExplained    bool     `json:"outsideExplained"`
		OutsideShowsThePath bool     `json:"outsideShowsThePath"`
		OutsideAskedGit     int      `json:"outsideAskedGit"`
		OutsideButtons      []string `json:"outsideButtons"`
		ModalTitle          string   `json:"modalTitle"`
		RowCount            int      `json:"rowCount"`
		AwayMarked          []bool   `json:"awayMarked"`
		AwayDir             []string `json:"awayDir"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("unreadable: %v (%s)", err, got)
	}

	if r.KnowsMessage || r.KnowsProse {
		t.Error("a sentence is being read as a diff, which is how this started")
	}
	if !r.KnowsDiff {
		t.Error("a real diff is not recognised as one")
	}
	if r.KnowsEmpty {
		t.Error("nothing at all counts as a diff")
	}

	// The heart of it: the reason, in the server's words, instead of a claim
	// that the file did not change.
	if !strings.Contains(r.MessageShown, "not a git repository") {
		t.Errorf("the message was replaced by %q", r.MessageShown)
	}
	if strings.Contains(r.MessageShown, "No textual diff") {
		t.Errorf("still reporting an explanation as an unchanged file: %q", r.MessageShown)
	}
	// A real diff still draws.
	if r.RealRows == 0 || !r.RealHasOld || !r.RealHasNew {
		t.Errorf("a real diff stopped rendering: %d rows, old=%v new=%v",
			r.RealRows, r.RealHasOld, r.RealHasNew)
	}
	// The marked fragment belongs to the side it is drawn on.
	if len(r.Marks) != 2 || r.Marks[0] != "old" || r.Marks[1] != "new" {
		t.Errorf("inline marks = %v, want the before word then the after word", r.Marks)
	}
	for _, c := range r.Cells {
		if strings.Contains(c, "the old line") && strings.Contains(c, "the new line") {
			t.Errorf("one cell holds both versions: %q", c)
		}
	}
	if !strings.Contains(r.EmptyShown, "No textual diff") {
		t.Errorf("an empty answer now says %q; that one really is no change", r.EmptyShown)
	}

	if !r.RailSaysWhy {
		t.Error("the rail does not pass on why there was no diff")
	}
	if !r.RailStillShows {
		t.Error("the rail stopped showing the file it could not diff")
	}
	if !r.RailAvoidsTheLie {
		t.Error("the rail is still saying the file did not change")
	}

	if !r.KnowsOutside || r.KnowsInside {
		t.Errorf("outside/inside = %v/%v", r.KnowsOutside, r.KnowsInside)
	}
	if !r.OutsideExplained || !r.OutsideShowsThePath {
		t.Errorf("a file outside the project: explained=%v path=%v",
			r.OutsideExplained, r.OutsideShowsThePath)
	}
	if r.OutsideAskedGit != 0 {
		t.Errorf("asked git about a path outside the repository %d times", r.OutsideAskedGit)
	}
	for _, b := range r.OutsideButtons {
		if strings.Contains(b, "Open in Files") {
			t.Error("offering to open a file the browser will refuse")
		}
	}
	// The title used to be the whole absolute path, which is what the screenshot
	// in the report was mostly made of.
	if strings.Contains(r.ModalTitle, ":\\") || len(r.ModalTitle) > 60 {
		t.Errorf("modal title = %q, want the file's name", r.ModalTitle)
	}

	if r.RowCount != 2 || len(r.AwayMarked) != 2 || r.AwayMarked[0] || !r.AwayMarked[1] {
		t.Errorf("rows = %d, marked = %v", r.RowCount, r.AwayMarked)
	}
	if len(r.AwayDir) != 2 || r.AwayDir[1] != "outside the project" {
		t.Errorf("row subtitles = %v", r.AwayDir)
	}
}
