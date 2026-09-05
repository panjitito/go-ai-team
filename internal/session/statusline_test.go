package session

import "testing"

// Real status lines, captured from a running terminal. The spacing is what it
// actually looks like after the escape sequences are stripped — a TUI redraws in
// place, so words run together and the same line appears many times over.
const realStatus = "go-ai-team (main) Haiku4.5 $0.00automodeunavailableforthismodel" +
	"⏸manualmodeon·←1agent/rcSonnet 5 $0.0 5h:18% wk:2%"

const realStatusFull = "\x1b[2K ▝▝ ▝▝  ~\\Documents\\Projects\\mis-dashboard\r\n" +
	"mis-dashboard \x1b[32m(main)\x1b[0m Opus 5 (1M context) ctx:67% $880.76 5h:10% wk:25%\r\n"

func TestParseStatusLine(t *testing.T) {
	s := ParseStatusLine(realStatusFull)
	if s.Model != "Opus 5" {
		t.Errorf("model = %q, want Opus 5", s.Model)
	}
	if !s.HasContext || s.Context != 67 {
		t.Errorf("context = %d (has=%v), want 67", s.Context, s.HasContext)
	}
	if !s.HasFiveHour || s.FiveHour != 10 {
		t.Errorf("5h = %d (has=%v), want 10", s.FiveHour, s.HasFiveHour)
	}
	if !s.HasWeekly || s.Weekly != 25 {
		t.Errorf("weekly = %d (has=%v), want 25", s.Weekly, s.HasWeekly)
	}
	if !s.HasCost || s.Cost != 880.76 {
		t.Errorf("cost = %v (has=%v), want 880.76", s.Cost, s.HasCost)
	}
}

// A terminal is a scrollback: the same labels appear once per repaint, and only
// the newest is now. Reading the first would show numbers from minutes ago.
func TestParseStatusLineTakesTheNewest(t *testing.T) {
	older := "Haiku 4.5 ctx:5% $0.10 5h:1% wk:1%\n"
	newer := "Opus 5 ctx:67% $880.76 5h:10% wk:25%\n"
	s := ParseStatusLine(older + newer)
	if s.Model != "Opus 5" || s.Context != 67 || s.FiveHour != 10 || s.Weekly != 25 || s.Cost != 880.76 {
		t.Errorf("read the stale render: %+v", s)
	}
}

// The words run together in a repainted line, which must not stop the numbers
// being found.
func TestParseStatusLineSquashedRepaint(t *testing.T) {
	s := ParseStatusLine(realStatus)
	if s.Model != "Sonnet 5" {
		t.Errorf("model = %q, want Sonnet 5", s.Model)
	}
	if s.FiveHour != 18 || s.Weekly != 2 {
		t.Errorf("5h/wk = %d/%d, want 18/2", s.FiveHour, s.Weekly)
	}
}

// Zero is a real reading. A fresh window really has used none of itself, and
// showing nothing there would be wrong in a different way from showing 0%.
func TestParseStatusLineZeroIsNotMissing(t *testing.T) {
	s := ParseStatusLine("Haiku 4.5 ctx:0% $0.00 5h:0% wk:0%")
	for _, c := range []struct {
		name string
		has  bool
	}{
		{"context", s.HasContext}, {"fiveHour", s.HasFiveHour},
		{"weekly", s.HasWeekly}, {"cost", s.HasCost},
	} {
		if !c.has {
			t.Errorf("%s read as absent when it was zero", c.name)
		}
	}
	if s.Context != 0 || s.FiveHour != 0 || s.Weekly != 0 || s.Cost != 0 {
		t.Errorf("zeros came back as %+v", s)
	}
}

// A status line that does not print the usage figures must not produce invented
// ones. This shows what the CLI shows, and nothing else.
func TestParseStatusLineAbsentFields(t *testing.T) {
	s := ParseStatusLine("go-ai-team (main) Haiku 4.5")
	if s.Model != "Haiku 4.5" {
		t.Errorf("model = %q", s.Model)
	}
	for _, c := range []struct {
		name string
		has  bool
	}{
		{"context", s.HasContext}, {"fiveHour", s.HasFiveHour},
		{"weekly", s.HasWeekly}, {"cost", s.HasCost},
	} {
		if c.has {
			t.Errorf("%s was reported present when the line does not print it", c.name)
		}
	}

	empty := ParseStatusLine("")
	if empty.Model != "" || empty.HasFiveHour {
		t.Errorf("an empty terminal produced %+v", empty)
	}
}

// A repaint can run the next thing on the line into the version, and the last
// occurrence in the buffer is then the garbled one. "Haiku 4.52." in the run bar
// came from a live session that had said "Haiku 4.5" cleanly a dozen times
// first, which is what makes the boundary worth requiring.
func TestParseStatusLineIgnoresAGarbledVersion(t *testing.T) {
	const buf = "work Haiku 4.5 $0.00 5h:5% wk:6%\n" +
		"work Haiku 4.5 ctx:5% $0.01 5h:5% wk:6%\n" +
		"work Haiku 4.52."
	if got := ParseStatusLine(buf).Model; got != "Haiku 4.5" {
		t.Errorf("model = %q, want Haiku 4.5", got)
	}
}

// But a terminal that only ever produced the awkward reading still gets a model
// out of it: something slightly wrong beats an em-dash.
func TestParseStatusLineFallsBackToTheLooseReading(t *testing.T) {
	if got := ParseStatusLine("work Haiku 4.52.").Model; got == "" {
		t.Error("no model at all from a line that plainly names one")
	}
}

func TestParseStatusLineModelNames(t *testing.T) {
	cases := map[string]string{
		"Opus 5 (1M context)": "Opus 5",
		"Sonnet 5":            "Sonnet 5",
		"Haiku4.5":            "Haiku 4.5",
		"Fable 5.1":           "Fable 5.1",
		"opus 4.8":            "Opus 4.8",
		"Sonnet 5 · ←":        "Sonnet 5",
	}
	for in, want := range cases {
		if got := ParseStatusLine(in).Model; got != want {
			t.Errorf("ParseStatusLine(%q).Model = %q, want %q", in, got, want)
		}
	}
}
