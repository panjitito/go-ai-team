//go:build uitest

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// The diff parser is where the review view's correctness lives: line numbers
// come from the hunk headers, and getting them wrong means every number on
// screen points at the wrong line.
//
// Run in the test's own headless Chrome against the real diff.js, rather than a
// copy of the logic in Go that could drift from it.
func TestUIDiffParser(t *testing.T) {
	port := 7788
	if _, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/settings", port)); err != nil {
		skipOrFail(t, "no server on 7788; start one first")
	}
	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	got := c.evalString(t, `
(() => {
  const diff = [
    'diff --git a/main.go b/main.go',
    'index 1111111..2222222 100644',
    '--- a/main.go',
    '+++ b/main.go',
    '@@ -10,6 +10,7 @@ func main() {',
    ' 	ctx := context.Background()',
    '-	log.Println("old")',
    '+	log.Println("new")',
    '+	extra()',
    ' 	run(ctx)',
    ' }',
    '@@ -40,3 +41,2 @@ func other() {',
    '-	gone()',
    ' 	kept()',
  ].join('\n');

  const files = parseDiff(diff);
  const h1 = files[0].hunks[0];
  const h2 = files[0].hunks[1];

  // Numbering starts where the hunk header says and advances per side.
  const rows = h1.rows.map(r => [r.kind, r.oldNo ?? null, r.newNo ?? null]);

  // Pairing puts the replaced line opposite its replacement.
  const pairs = pairRows(h1.rows).map(p => [
    p.left ? p.left.kind : null, p.right ? p.right.kind : null,
  ]);

  // The changed fragment inside an edited line. The middle always comes from
  // the first argument, the line being drawn — the bug this replaced returned
  // both middles and let the caller pick, and the caller picked the other one
  // for an added line, so the "after" column of a split diff showed the word
  // from "before".
  const drawn = inlineParts('log.Println("new")', 'log.Println("old")');
  const otherWay = inlineParts('log.Println("old")', 'log.Println("new")');

  return JSON.stringify({
    files: files.length,
    hunks: files[0].hunks.length,
    label: h1.label,
    rows,
    pairs,
    h2first: [h2.rows[0].kind, h2.rows[0].oldNo ?? null],
    inline: [drawn.pre, drawn.mid, drawn.post],
    inlineOtherWay: otherWay.mid,
    // Metadata lines must not become content.
    hasIndexLine: JSON.stringify(files).includes('index 1111111'),
  });
})()`)
	t.Logf("diff parse: %s", got)

	var r struct {
		Files          int             `json:"files"`
		Hunks          int             `json:"hunks"`
		Label          string          `json:"label"`
		Rows           [][]interface{} `json:"rows"`
		Pairs          [][]interface{} `json:"pairs"`
		H2First        []interface{}   `json:"h2first"`
		Inline         []string        `json:"inline"`
		InlineOtherWay string          `json:"inlineOtherWay"`
		HasIndexLine   bool            `json:"hasIndexLine"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("unreadable: %v", err)
	}

	if r.Files != 1 || r.Hunks != 2 {
		t.Errorf("got %d files / %d hunks, want 1 / 2", r.Files, r.Hunks)
	}
	if r.Label != "func main() {" {
		t.Errorf("hunk label = %q", r.Label)
	}
	if r.HasIndexLine {
		t.Error("git metadata leaked into the rows")
	}

	// -10,6 +10,7: context starts at 10 on both sides, the removal takes old 11,
	// the two additions take new 11 and 12, and the following context resumes at
	// old 12 / new 13.
	want := [][]interface{}{
		{"ctx", 10.0, 10.0},
		{"del", 11.0, nil},
		{"add", nil, 11.0},
		{"add", nil, 12.0},
		{"ctx", 12.0, 13.0},
		{"ctx", 13.0, 14.0},
	}
	if len(r.Rows) != len(want) {
		t.Fatalf("got %d rows, want %d: %v", len(r.Rows), len(want), r.Rows)
	}
	for i := range want {
		for j := range want[i] {
			if fmt.Sprint(r.Rows[i][j]) != fmt.Sprint(want[i][j]) {
				t.Errorf("row %d field %d = %v, want %v (row %v)", i, j, r.Rows[i][j], want[i][j], r.Rows[i])
			}
		}
	}

	// The removal and the first addition sit opposite each other; the second
	// addition has nothing on its left.
	if len(r.Pairs) < 3 {
		t.Fatalf("pairs = %v", r.Pairs)
	}
	if fmt.Sprint(r.Pairs[1]) != "[del add]" {
		t.Errorf("pair 1 = %v, want the removal opposite its replacement", r.Pairs[1])
	}
	if fmt.Sprint(r.Pairs[2]) != "[<nil> add]" {
		t.Errorf("pair 2 = %v, want an addition with nothing opposite", r.Pairs[2])
	}

	// The second hunk restarts numbering from its own header.
	if fmt.Sprint(r.H2First) != "[del 40]" {
		t.Errorf("second hunk starts at %v, want [del 40]", r.H2First)
	}

	// Only "old"/"new" differ; the rest of the line is common.
	if len(r.Inline) != 3 || r.Inline[1] != "new" {
		t.Errorf("inline parts = %q, want the changed word isolated", r.Inline)
	}
	if r.Inline[0] != `log.Println("` || r.Inline[2] != `")` {
		t.Errorf("the common parts are wrong: %q", r.Inline)
	}
	// And the direction: whichever line is being drawn is the one the fragment
	// is taken from.
	if r.InlineOtherWay != "old" {
		t.Errorf("marked %q on the other side, want the fragment from the line being drawn", r.InlineOtherWay)
	}
}
