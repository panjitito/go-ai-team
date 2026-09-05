package session

import (
	"regexp"
	"strconv"
	"strings"
)

// Reading the CLI's own status line.
//
// Claude Code renders a status line along the bottom of its terminal, and it is
// the only place several useful numbers appear: how much of the five-hour window
// and the weekly allowance has been spent, how full the context is, and what the
// session has cost. The app was making people switch to the terminal tab and
// read it there.
//
// It is scraped rather than asked for, and that is a deliberate trade. The
// numbers originate in a JSON payload the CLI hands to whatever status-line
// command is configured — rate_limits.five_hour and rate_limits.seven_day, each
// with a used_percentage and a reset time. Getting at that directly would mean
// installing our own status-line command into the user's Claude settings, which
// is their configuration, not ours. Reading what is already on screen costs
// nothing and changes nothing.
//
// The consequence is honest and worth stating: this shows what the status line
// shows. A setup that does not print the usage figures will not have them here
// either.

// StatusLine is what could be read from the bottom of the terminal. Every field
// is optional, because every one of them depends on how the status line is
// configured.
type StatusLine struct {
	Model string `json:"model,omitempty"`
	// Context is the percentage of the context window in use.
	Context int `json:"context,omitempty"`
	// FiveHour and Weekly are the percentages of each rate-limit window spent.
	FiveHour int `json:"fiveHour,omitempty"`
	Weekly   int `json:"weekly,omitempty"`
	// Cost is the session's spend in dollars.
	Cost float64 `json:"cost,omitempty"`

	// Has* say whether the number above was actually found, so a genuine zero
	// is not confused with "not shown". A fresh window really is 0%.
	HasContext  bool `json:"hasContext,omitempty"`
	HasFiveHour bool `json:"hasFiveHour,omitempty"`
	HasWeekly   bool `json:"hasWeekly,omitempty"`
	HasCost     bool `json:"hasCost,omitempty"`
}

var (
	// No word boundary before the family name on purpose. A repaint draws the
	// status line in place and the pieces run together — "/rcSonnet 5" is a real
	// capture — so requiring a boundary silently read the model from an older
	// render further up the buffer. Dropping it can only add matches, and the
	// ones it adds are exactly these.
	slModel    = regexp.MustCompile(`(?i)(Opus|Sonnet|Haiku|Fable)\s*([\d.]+)`)
	slContext  = regexp.MustCompile(`ctx:\s*(\d+)%`)
	slFiveHour = regexp.MustCompile(`5h:\s*(\d+)%`)
	slWeekly   = regexp.MustCompile(`wk:\s*(\d+)%`)
	slCost     = regexp.MustCompile(`\$\s*(\d+\.\d+)`)
)

// ParseStatusLine pulls what it can from the tail of a terminal.
//
// Always the last occurrence of each field. A terminal is a scrollback, so the
// same label appears once per repaint and only the newest one is now.
func ParseStatusLine(tail string) StatusLine {
	clean := stripANSI(tail)
	var s StatusLine

	if m := lastMatch(clean, slModel); m != nil {
		// Title-case the family so "haiku 4.5" and "Haiku 4.5" read the same.
		s.Model = strings.ToUpper(m[1][:1]) + strings.ToLower(m[1][1:]) + " " + m[2]
	}
	if m := lastMatch(clean, slContext); m != nil {
		s.Context, s.HasContext = atoi(m[1]), true
	}
	if m := lastMatch(clean, slFiveHour); m != nil {
		s.FiveHour, s.HasFiveHour = atoi(m[1]), true
	}
	if m := lastMatch(clean, slWeekly); m != nil {
		s.Weekly, s.HasWeekly = atoi(m[1]), true
	}
	if m := lastMatch(clean, slCost); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			s.Cost, s.HasCost = v, true
		}
	}
	return s
}

func lastMatch(s string, re *regexp.Regexp) []string {
	all := re.FindAllStringSubmatch(s, -1)
	if len(all) == 0 {
		return nil
	}
	return all[len(all)-1]
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// StatusLineOf reads the status line for one session.
func (m *Manager) StatusLineOf(id string) StatusLine {
	s, ok := m.Get(id)
	if !ok {
		return StatusLine{}
	}
	// The status line is redrawn at the very end of the buffer, so a short tail
	// is enough and keeps this cheap on a poll.
	return ParseStatusLine(string(s.ring.Tail(statusLineTail)))
}

// statusLineTail is how much of the end of the terminal to read. Generous
// enough to survive a repaint that scrolls the line, small enough to scan often.
const statusLineTail = 8 << 10
