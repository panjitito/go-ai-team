package session

import (
	"strings"
	"testing"
)

// The rewind picker, captured from a real session that had made two edits. This
// is the shape flat text cannot represent: a list with no numbers, each entry
// followed by its own subtitle, and a cursor on one of them.
func TestScreenRecoversThePickerRows(t *testing.T) {
	rows := Screen(fixture(t, "rewind.bin"))
	if len(rows) == 0 {
		t.Fatal("no rows at all")
	}

	// From the dialog's own header down. Above it is the conversation, which
	// quotes the same prompts back and would match every search here.
	from := 0
	for _, r := range rows {
		if strings.Contains(r.Text, "Rewind") {
			from = r.Row
		}
	}
	if from == 0 {
		t.Fatalf("no dialog header; rows: %v", texts(rows))
	}
	find := func(want string) (ScreenRow, bool) {
		for _, r := range rows {
			if r.Row > from && strings.Contains(r.Text, want) {
				return r, true
			}
		}
		return ScreenRow{}, false
	}

	one, ok := find("one.txt containing 1")
	if !ok {
		t.Fatalf("the first option is missing; rows: %v", texts(rows))
	}
	two, ok := find("two.txt containing 2")
	if !ok {
		t.Fatalf("the second option is missing; rows: %v", texts(rows))
	}
	cur, ok := find("(current)")
	if !ok {
		t.Fatalf("the cursor row is missing; rows: %v", texts(rows))
	}

	// The whole point: these are separate rows, in this order.
	if !(one.Row < two.Row && two.Row < cur.Row) {
		t.Errorf("rows out of order: one=%d two=%d current=%d", one.Row, two.Row, cur.Row)
	}
	// And an option is not merged with its own subtitle, which is the thing flat
	// text cannot do.
	if strings.Contains(one.Text, "+1") {
		t.Errorf("the option swallowed its subtitle: %q", one.Text)
	}
	if strings.Contains(one.Text, "two.txt containing") {
		t.Errorf("the two options ran together: %q", one.Text)
	}
	if !strings.Contains(cur.Text, "❯") {
		t.Errorf("the cursor is not on the row it marks: %q", cur.Text)
	}
	if _, ok := find("Esc to cancel"); !ok {
		t.Errorf("the footer is missing; rows: %v", texts(rows))
	}
}

func texts(rs []ScreenRow) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Text
	}
	return out
}

func TestScreenPositioning(t *testing.T) {
	// Absolute moves put text on the row they name, in the order they name.
	rows := Screen("\x1b[5;1Hfive\x1b[3;1Hthree\x1b[9;1Hnine")
	if len(rows) != 3 {
		t.Fatalf("got %d rows: %v", len(rows), texts(rows))
	}
	if rows[0].Text != "three" || rows[1].Text != "five" || rows[2].Text != "nine" {
		t.Errorf("rows = %v, want three/five/nine", texts(rows))
	}
	if rows[0].Row != 3 {
		t.Errorf("first row is %d, want 3", rows[0].Row)
	}
}

// A row written twice shows what it says now, not both at once.
func TestScreenLastWriteWins(t *testing.T) {
	rows := Screen("\x1b[2;1Hold text\x1b[2;1Hnew text")
	if len(rows) != 1 || rows[0].Text != "new text" {
		t.Errorf("rows = %v, want just the newer one", texts(rows))
	}
}

// Text further right on the same row is a continuation of it, which is how a
// list entry and its right-hand column arrive.
func TestScreenSameRowFurtherRight(t *testing.T) {
	rows := Screen("\x1b[4;1HCreate a.txt\x1b[4;40Ha.txt +1")
	if len(rows) != 1 {
		t.Fatalf("got %d rows: %v", len(rows), texts(rows))
	}
	if !strings.Contains(rows[0].Text, "Create a.txt") || !strings.Contains(rows[0].Text, "a.txt +1") {
		t.Errorf("row = %q, want both pieces", rows[0].Text)
	}
}

// Only the last frame. Everything before a screen clear is scrollback.
func TestScreenTakesTheLastFrame(t *testing.T) {
	rows := Screen("\x1b[3;1Hstale\x1b[2J\x1b[3;1Hfresh")
	if len(rows) != 1 || rows[0].Text != "fresh" {
		t.Errorf("rows = %v, want only what is on screen now", texts(rows))
	}
}

// Newlines still advance, for the parts of a session that are ordinary output.
func TestScreenPlainLines(t *testing.T) {
	rows := Screen("one\r\ntwo\r\nthree")
	if len(rows) != 3 || rows[0].Text != "one" || rows[2].Text != "three" {
		t.Errorf("rows = %v", texts(rows))
	}
}
