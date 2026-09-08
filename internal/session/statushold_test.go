package session

import (
	"testing"
	"time"

	"github.com/panjitito/go-ai-team/internal/claudefs"
)

// A scrape that finds nothing must not erase a reading that was right a moment
// ago.
//
// This is the reported bug. The strip above the composer emptied and refilled
// on its own, because the parser reads terminal text, the CLI repaints its
// footer with cursor moves rather than by appending, and a noisy tool call
// pushes the line out of the window. Every miss blanked the whole strip.
func TestStatusLineIsHeldThroughAMiss(t *testing.T) {
	s := &Session{}
	t0 := time.Now()

	good := StatusLine{
		Model: "Opus 5", Mode: "auto",
		Context: 42, HasContext: true,
		FiveHour: 13, HasFiveHour: true,
		Weekly: 34, HasWeekly: true,
		Cost: 1.23, HasCost: true,
	}
	if got := s.holdLine(good, t0); got.FiveHour != 13 || got.AgeSeconds != 0 {
		t.Fatalf("a fresh reading came back as %+v", got)
	}

	// A miss two seconds later keeps everything, and says how old it is.
	got := s.holdLine(StatusLine{}, t0.Add(2*time.Second))
	if !got.HasFiveHour || got.FiveHour != 13 {
		t.Errorf("the five-hour reading was lost on one miss: %+v", got)
	}
	if !got.HasWeekly || !got.HasContext || !got.HasCost || got.Model != "Opus 5" {
		t.Errorf("something else was lost: %+v", got)
	}
	if got.AgeSeconds != 2 {
		t.Errorf("age = %d, want 2", got.AgeSeconds)
	}

	// A later scrape that works replaces it and resets the age.
	fresh := StatusLine{FiveHour: 20, HasFiveHour: true}
	got = s.holdLine(fresh, t0.Add(30*time.Second))
	if got.FiveHour != 20 || got.AgeSeconds != 0 {
		t.Errorf("a new reading came back as %+v", got)
	}
	// And the fresh one is what is held from now on, not a merge with the old.
	// Holding a stale ctx beside a current 5h would present one reading as if
	// it were all from the same moment.
	got = s.holdLine(StatusLine{}, t0.Add(31*time.Second))
	if got.HasContext {
		t.Errorf("a field the newest scrape did not have came back: %+v", got)
	}
}

// Held forever would be worse than blank. An agent that stopped hours ago
// should not still be showing the window it had.
func TestStatusLineIsNotHeldForever(t *testing.T) {
	s := &Session{}
	t0 := time.Now()
	s.holdLine(StatusLine{FiveHour: 13, HasFiveHour: true}, t0)

	if got := s.holdLine(StatusLine{}, t0.Add(statusLineHold-time.Second)); !got.HasFiveHour {
		t.Error("dropped just inside the hold")
	}
	if got := s.holdLine(StatusLine{}, t0.Add(statusLineHold+time.Second)); got.HasFiveHour {
		t.Errorf("still showing a reading %v old: %+v", statusLineHold, got)
	}
}

// The wide read is for when the narrow one is not working, and only then.
//
// Widening unconditionally was the first attempt and cost 34 ms a poll, on
// every open conversation, on a machine already running agents. The hold is
// what makes the narrow read sufficient: while a reading is fresh there is
// nothing further back worth finding.
func TestScanWindowWidensOnlyWhenItHasTo(t *testing.T) {
	s := &Session{}
	t0 := time.Now()

	// Nothing read yet: look as far as possible.
	if got := s.scanWindow(t0); got != statusLineFarTail {
		t.Errorf("first read looked at %d bytes, want the far window", got)
	}

	// A reading in hand: the near window is enough.
	s.holdLine(StatusLine{FiveHour: 13, HasFiveHour: true}, t0)
	if got := s.scanWindow(t0.Add(time.Second)); got != statusLineTail {
		t.Errorf("looked at %d bytes with a fresh reading in hand", got)
	}
	if got := s.scanWindow(t0.Add(statusLineRescan - time.Second)); got != statusLineTail {
		t.Errorf("widened at %v, before the rescan interval", statusLineRescan-time.Second)
	}

	// Held long enough that the narrow read is clearly not finding it.
	if got := s.scanWindow(t0.Add(statusLineRescan)); got != statusLineFarTail {
		t.Errorf("still on the near window %v after the last reading", statusLineRescan)
	}

	// Cheap by a wide margin, which is the point of the near one.
	if statusLineTail >= statusLineFarTail {
		t.Error("the near window is not narrower than the far one")
	}
}

// Nothing ever read is not the same as something read and lost, and neither is
// a reason to invent a zero.
func TestStatusLineWithNothingEverRead(t *testing.T) {
	s := &Session{}
	got := s.holdLine(StatusLine{}, time.Now())
	if got.any() || got.AgeSeconds != 0 {
		t.Errorf("invented a reading: %+v", got)
	}
}

// The two halves have to agree: what `go-ai-team statusline` prints is what
// this parses. They are in different packages and nothing but this connects
// them, so a change to either format would otherwise be found by a user.
func TestOurStatusLineParsesBackExactly(t *testing.T) {
	var p claudefs.StatusPayload
	p.Workspace.CurrentDir = `C:\Users\dev\Projects\api`
	p.Model.DisplayName = "Opus 5"
	p.ContextWindow.UsedPercentage = f(42)
	p.Cost.TotalCostUSD = f(1.23)
	p.RateLimits.FiveHour.UsedPercentage = f(13)
	p.RateLimits.SevenDay.UsedPercentage = f(34)

	line := claudefs.StatusLineText(p)
	got := ParseStatusLine(line)

	if got.Model != "Opus 5" {
		t.Errorf("model = %q from %q", got.Model, line)
	}
	if !got.HasContext || got.Context != 42 {
		t.Errorf("context = %d (%v) from %q", got.Context, got.HasContext, line)
	}
	if !got.HasCost || got.Cost != 1.23 {
		t.Errorf("cost = %v (%v) from %q", got.Cost, got.HasCost, line)
	}
	if !got.HasFiveHour || got.FiveHour != 13 {
		t.Errorf("five hour = %d (%v) from %q", got.FiveHour, got.HasFiveHour, line)
	}
	if !got.HasWeekly || got.Weekly != 34 {
		t.Errorf("weekly = %d (%v) from %q", got.Weekly, got.HasWeekly, line)
	}

	// A partial payload round-trips as partial, rather than as zeroes.
	var bare claudefs.StatusPayload
	bare.Model.DisplayName = "Sonnet 5"
	got = ParseStatusLine(claudefs.StatusLineText(bare))
	if got.HasFiveHour || got.HasWeekly || got.HasContext || got.HasCost {
		t.Errorf("a model-only line parsed as having numbers: %+v", got)
	}
	if got.Model != "Sonnet 5" {
		t.Errorf("model = %q", got.Model)
	}
}

// The window has to be wide enough to survive a tool that prints a lot. This is
// the other half of the reported bug: the reading was in the buffer and outside
// the eight kilobytes the parser looked at.
func TestStatusLineSurvivesNoisyOutput(t *testing.T) {
	var p claudefs.StatusPayload
	p.Model.DisplayName = "Opus 5"
	p.RateLimits.FiveHour.UsedPercentage = f(13)
	line := claudefs.StatusLineText(p)

	noise := make([]byte, 40<<10)
	for i := range noise {
		noise[i] = 'x'
	}
	got := ParseStatusLine(line + "\n" + string(noise))
	if !got.HasFiveHour {
		t.Errorf("40 KB of tool output hid the reading; the window is %d bytes", statusLineTail)
	}
	if got.Model != "Opus 5" {
		t.Errorf("the model was lost behind the same output: %+v", got)
	}
}

// The window is scanned on every poll of every open conversation, so widening
// it had to stay cheap. Run with -bench to see the number behind the comment on
// statusLineTail.
func BenchmarkParseStatusLine(b *testing.B) {
	var p claudefs.StatusPayload
	p.Model.DisplayName = "Opus 5"
	p.ContextWindow.UsedPercentage = f(42)
	p.RateLimits.FiveHour.UsedPercentage = f(13)
	p.RateLimits.SevenDay.UsedPercentage = f(34)

	// Both windows. The near one runs on every poll of every open conversation
	// and the far one at most once every twenty seconds per session, so they
	// are allowed to cost very different amounts.
	for _, w := range []struct {
		name string
		n    int
	}{{"near", statusLineTail}, {"far", statusLineFarTail}} {
		noise := make([]byte, w.n)
		for i := range noise {
			noise[i] = byte('a' + i%26)
		}
		tail := claudefs.StatusLineText(p) + "\n" + string(noise)
		b.Run(w.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = ParseStatusLine(tail)
			}
		})
	}
}

func f(v float64) *float64 { return &v }
