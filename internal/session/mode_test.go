package session

import "testing"

func TestParseMode(t *testing.T) {
	cases := map[string]string{
		"⏵⏵ auto mode on (shift+tab to cycle)":    ModeAuto,
		"⏸ manual mode on":                        ModeManual,
		"⏵⏵ accept edits on (shift+tab to cycle)": ModeAcceptEdits,
		"⏸ plan mode on (shift+tab to cycle)":     ModePlan,
		// The line the CLI actually draws, with the rest of the footer on it.
		"gat-probe4 Sonnet 5 ctx:6% $0.07 5h:33% wk:3% ● high · /effort ⏸ manual mode on /rc": ModeManual,
		"":                         "",
		"nothing about modes here": "",
		"mode on":                  "",
	}
	for in, want := range cases {
		if got := ParseMode(in); got != want {
			t.Errorf("ParseMode(%q) = %q, want %q", in, got, want)
		}
	}
}

// A partial repaint moves the cursor over cells it is not rewriting, so a
// character that was already on screen never reaches us. The name arrives with
// a hole in it, and this is a real capture, not a corrupt one.
func TestParseModeWithAHoleInTheWord(t *testing.T) {
	cases := map[string]string{
		"⏸ m\x1b[1Cnual mode on":   ModeManual,
		"⏸ ma\x1b[1Cual mode on":   ModeManual,
		"⏵⏵ au\x1b[1Co mode on":    ModeAuto,
		"⏸ pl\x1b[1Cn mode on":     ModePlan,
		"⏵⏵ acce\x1b[1Ct edits on": ModeAcceptEdits,
	}
	for in, want := range cases {
		if got := ParseMode(in); got != want {
			t.Errorf("ParseMode(%q) = %q, want %q", in, got, want)
		}
	}
}

// The scrollback holds every mode the session has ever been in. Only the newest
// is now.
func TestParseModeTakesTheNewest(t *testing.T) {
	s := "⏵⏵ auto mode on\n⏸ manual mode on\n⏵⏵ accept edits on\n⏸ plan mode on\n"
	if got := ParseMode(s); got != ModePlan {
		t.Errorf("ParseMode = %q, want plan", got)
	}
}

// The mode is read out of the same terminal the status line comes from, so the
// real captures should carry it too.
func TestParseModeFromARealTerminal(t *testing.T) {
	if got := ParseMode(fixture(t, "ask_write.bin")); got != ModeManual {
		t.Errorf("mode from a real terminal = %q, want manual", got)
	}
}

// Guessing is worse than saying nothing: the mode sits next to a button that
// changes it, and a wrong reading makes that button do the wrong thing.
func TestMatchModeRefusesAmbiguity(t *testing.T) {
	for _, s := range []string{"an", "al", "aut o", "xyz"} {
		if got := matchMode(s); got != "" {
			t.Errorf("matchMode(%q) = %q, want no answer", s, got)
		}
	}
}
