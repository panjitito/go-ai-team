package session

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// The permission mode, which is the largest lever in Claude Code and had no
// switch here at all.
//
// It decides whether the agent asks before every action, edits files without
// asking, or refuses to touch anything and writes a plan instead. The CLI puts
// it above the composer with the hint that changes it:
//
//	⏵⏵ auto mode on (shift+tab to cycle)
//	⏸  manual mode on
//	⏵⏵ accept edits on
//	⏸  plan mode on
//
// There is no command that jumps straight to a mode — shift+tab cycles, and
// which modes are in the cycle depends on the model and the configuration, so
// counting presses would be guessing. Instead the mode is read back off the
// terminal after each press and the cycling stops when it says what was asked
// for. If the mode is not in this session's cycle, that is discovered rather
// than assumed, and said plainly.

// Modes, in the order the CLI cycles them.
const (
	ModeAuto        = "auto"
	ModeManual      = "manual"
	ModeAcceptEdits = "acceptEdits"
	ModePlan        = "plan"
)

// ModeLabels is what each mode is called on screen.
var ModeLabels = map[string]string{
	ModeAuto:        "auto",
	ModeManual:      "manual",
	ModeAcceptEdits: "accept edits",
	ModePlan:        "plan",
}

// modeMarkers end the indicator; the name sits just in front of one.
//
// Two literals and a substring search rather than the case-insensitive
// alternation this used to be. Measured over a 64 KB window that pattern cost
// 7.9 ms per call, which is a lot to spend on every poll of every open
// conversation to find two fixed words. Dropping the word boundaries costs
// nothing: the name still has to be found in the sixteen characters in front of
// the marker, so "supermode on" gets looked at and then rejected.
var modeMarkers = []string{"mode on", "edits on"}

// nonLetters splits a run of text into words. Compiled once: it used to be
// built on every call, inside a function reached from a poll.
var nonLetters = regexp.MustCompile(`[^A-Za-z]+`)

// modeNames maps the word the CLI prints to the mode it means.
var modeNames = map[string]string{
	"auto":   ModeAuto,
	"manual": ModeManual,
	"plan":   ModePlan,
	"accept": ModeAcceptEdits,
}

// ParseMode reads the permission mode off the tail of a terminal.
//
// The word can arrive with a hole in it. A partial repaint writes only the
// cells that changed and moves the cursor over the rest, so "manual" reaches us
// as "m nual" when an 'a' was already on screen in that column — a real capture,
// not a corrupt one.
//
// What that means is precise and worth relying on: the text we receive is always
// a *subsequence* of the word that is really there, never a different word. So a
// name is accepted when what we saw could be turned into it by putting back a
// character or two, and the four names are far enough apart that nothing else
// fits.
func ParseMode(tail string) string {
	s := flattenFrame(tail)
	lower := strings.ToLower(s)
	end := len(lower)
	for tries := 0; tries < maxModeCandidates && end > 0; tries++ {
		at := -1
		for _, marker := range modeMarkers {
			if i := strings.LastIndex(lower[:end], marker); i > at {
				at = i
			}
		}
		if at < 0 {
			return ""
		}
		if mode := modeFromWords(s[max(0, at-16):at]); mode != "" {
			return mode
		}
		end = at
	}
	return ""
}

// maxModeCandidates bounds the walk backwards, for the reason
// maxModelCandidates does.
const maxModeCandidates = 12

// modeFromWords tries the last word before the marker, then the last two joined,
// and so on. One attempt covers "…/rc ⏸ plan", where only the final word is the
// name; the other covers "m nual", where a hole split one name into two.
func modeFromWords(before string) string {
	words := nonLetters.Split(strings.ToLower(before), -1)
	var kept []string
	for _, w := range words {
		if w != "" {
			kept = append(kept, w)
		}
	}
	for n := 1; n <= 3 && n <= len(kept); n++ {
		if mode := matchMode(strings.Join(kept[len(kept)-n:], "")); mode != "" {
			return mode
		}
	}
	return ""
}

// matchMode accepts a word that is a subsequence of exactly one mode name, with
// at most two characters missing.
func matchMode(seen string) string {
	if len(seen) < 3 {
		return ""
	}
	found := ""
	for name, mode := range modeNames {
		if len(name)-len(seen) > 2 || len(seen) > len(name) {
			continue
		}
		if !isSubsequence(seen, name) {
			continue
		}
		if found != "" && found != mode {
			// Two names fit, so we do not actually know which. Saying nothing is
			// better than showing the wrong mode next to a button that changes it.
			return ""
		}
		found = mode
	}
	return found
}

func isSubsequence(short, long string) bool {
	i := 0
	for j := 0; i < len(short) && j < len(long); j++ {
		if short[i] == long[j] {
			i++
		}
	}
	return i == len(short)
}

// ModeOf reads the mode one session is in.
func (m *Manager) ModeOf(id string) string {
	s, ok := m.Get(id)
	if !ok {
		return ""
	}
	return ParseMode(string(s.ring.Tail(statusLineTail)))
}

// SetMode cycles shift+tab until the terminal reports the mode asked for.
func (m *Manager) SetMode(id, want string) (string, error) {
	if _, ok := ModeLabels[want]; !ok {
		return "", fmt.Errorf("%q is not a permission mode", want)
	}
	if _, ok := m.Get(id); !ok {
		return "", fmt.Errorf("that session is not running")
	}
	if m.ModeOf(id) == want {
		return want, nil
	}
	// One press per mode in the cycle, plus room to get back round to where it
	// started; more than that and the mode is not on offer here.
	for i := 0; i < modeCycleTries; i++ {
		// Shift+Tab is CSI Z, which is what the terminal sends for the key the
		// CLI's own hint tells you to press.
		if err := m.SendKeys(id, "\x1b[Z"); err != nil {
			return "", err
		}
		time.Sleep(modeSettle)
		if got := m.ModeOf(id); got == want {
			return got, nil
		}
	}
	got := m.ModeOf(id)
	if got == "" {
		return "", fmt.Errorf("could not tell which mode this session is in — change it with shift+tab in the terminal")
	}
	return got, fmt.Errorf("this session does not offer %s mode; it is in %s",
		ModeLabels[want], ModeLabels[got])
}

const (
	// modeCycleTries is generous: four modes, and the cycle may start anywhere.
	modeCycleTries = 6
	// modeSettle is how long the CLI takes to redraw the indicator.
	modeSettle = 350 * time.Millisecond
)
