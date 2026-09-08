package session

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Reading the CLI's own status line.
//
// Claude Code renders a status line along the bottom of its terminal, and it is
// the only place several useful numbers appear: how much of the five-hour window
// and the weekly allowance has been spent, how full the context is, and what the
// session has cost. The app was making people switch to the terminal tab and
// read it there.
//
// It is scraped rather than asked for. The numbers originate in a JSON payload
// the CLI hands to whatever status-line command is configured:
// rate_limits.five_hour and rate_limits.seven_day, each with a used_percentage.
// Reading what is already on screen costs nothing and changes nothing about
// somebody's setup, so that is the default.
//
// The consequence was worth stating and then turned out to be worth acting on:
// this shows what the status line shows, and an account with no status-line
// command prints no usage figures, so the strip was permanently blank with
// nothing explaining why. Two accounts out of three on the machine this was
// written for were in that state. There is nowhere else to get the figures
// either, because a transcript carries token counts and no rate limits at all.
//
// So the app can supply the line as well as read it. `go-ai-team statusline` is
// that command, installing it is opt-in per account and reversible, and the API
// says which of the two situations a blank strip is in. See
// internal/claudefs/statusline.go.

// StatusLine is what could be read from the bottom of the terminal. Every field
// is optional, because every one of them depends on how the status line is
// configured.
type StatusLine struct {
	Model string `json:"model,omitempty"`
	// Mode is the permission mode: auto, manual, acceptEdits or plan. The CLI
	// prints it above the composer, next to the shift+tab hint that changes it.
	Mode string `json:"mode,omitempty"`
	// Context is the percentage of the context window in use.
	Context int `json:"context"`
	// FiveHour and Weekly are the percentages of each rate-limit window spent.
	FiveHour int `json:"fiveHour"`
	Weekly   int `json:"weekly"`
	// Cost is the session's spend in dollars.
	Cost float64 `json:"cost"`

	// Has* say whether the number above was actually found, so a genuine zero
	// is not confused with "not shown". A fresh window really is 0%.
	//
	// None of these carry omitempty, and neither do the numbers. That would undo
	// the entire point of the pair: a real $0.00 would be dropped from the JSON,
	// arrive as undefined next to a hasCost of true, and render as "$NaN".
	HasContext  bool `json:"hasContext"`
	HasFiveHour bool `json:"hasFiveHour"`
	HasWeekly   bool `json:"hasWeekly"`
	HasCost     bool `json:"hasCost"`

	// AgeSeconds is how long ago this reading was actually scraped. Zero means
	// it came from the terminal as it is now; anything else is a value being
	// held because the latest scrape found nothing, and the UI says so rather
	// than presenting it as current.
	AgeSeconds int `json:"ageSeconds"`
}

var (
	// No word boundary before the family name on purpose. A repaint draws the
	// status line in place and the pieces run together — "/rcSonnet 5" is a real
	// capture — so requiring a boundary silently read the model from an older
	// render further up the buffer. Dropping it can only add matches, and the
	// ones it adds are exactly these.
	//
	// Two of them, and the order matters. A repaint can run the next thing on the
	// line straight into the version — "Haiku 4.5" with "2." from whatever
	// followed — and a greedy [\d.]+ swallowed that whole, so the bar read
	// "Haiku 4.52.". The strict pattern requires the version to end at a token
	// boundary, which the garbled one does not, and the buffer holds dozens of
	// clean renders to find instead. The loose one is still there for a terminal
	// that only ever produced the awkward reading: a slightly wrong version beats
	// no model at all.
	slModel      = regexp.MustCompile(`(?i)(Opus|Sonnet|Haiku|Fable)\s*(\d+(?:\.\d+)?)(?:\s|$)`)
	slModelLoose = regexp.MustCompile(`(?i)(Opus|Sonnet|Haiku|Fable)\s*(\d+(?:\.\d+)*)`)
	slContext    = regexp.MustCompile(`ctx:\s*(\d+)%`)
	slFiveHour   = regexp.MustCompile(`5h:\s*(\d+)%`)
	slWeekly     = regexp.MustCompile(`wk:\s*(\d+)%`)
	slCost       = regexp.MustCompile(`\$\s*(\d+\.\d+)`)
)

// ParseStatusLine pulls what it can from the tail of a terminal.
//
// Always the last occurrence of each field. A terminal is a scrollback, so the
// same label appears once per repaint and only the newest one is now.
func ParseStatusLine(tail string) StatusLine {
	clean := stripANSI(tail)
	var s StatusLine

	if m := lastModelMatch(clean); m != nil {
		// Title-case the family so "haiku 4.5" and "Haiku 4.5" read the same.
		s.Model = strings.ToUpper(m[1][:1]) + strings.ToLower(m[1][1:]) + " " + m[2]
	}
	s.Mode = ParseMode(lastBytes(tail, modeTail))
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

// lastBytes is the tail of s, cut on a rune boundary so a multi-byte character
// is never split into something the parser then reads as garbage.
func lastBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for i := 0; i < 4 && i < len(s); i++ {
		if utf8.RuneStart(s[i]) {
			return s[i:]
		}
	}
	return s
}

// modelFamilies are the names to look for, lower case, for a substring search.
var modelFamilies = []string{"opus", "sonnet", "haiku", "fable"}

// lastModelMatch finds the newest model badge without running a case-insensitive
// alternation across the whole window.
//
// The regex way costs what it costs: measured over 64 KB, slModel took 8.1 ms
// and slModelLoose 10.7 ms, against 3.8 microseconds for `5h:\s*(\d+)%`. Go's
// engine can drive a plain literal at about a byte a nanosecond and can do
// nothing of the kind with `(?i)(Opus|Sonnet|Haiku|Fable)`, so this finds the
// candidates with LastIndex over a lower-cased copy and runs the pattern on the
// short segment after each one. Same answer, and the whole parse went from
// 34 ms to 0.67 ms on the window it uses.
//
// Strict first, everywhere, before the loose pattern is tried anywhere. The
// order matters for the reason the patterns explain: a repaint can run the next
// token into the version and the loose reading of that is wrong, so a strict
// match further back beats a loose one nearer the end.
func lastModelMatch(clean string) []string {
	lower := strings.ToLower(clean)
	for _, re := range []*regexp.Regexp{slModel, slModelLoose} {
		if m := scanBackFor(clean, lower, modelFamilies, re); m != nil {
			return m
		}
	}
	return nil
}

// scanBackFor walks the occurrences of any of subs from the end, newest first,
// and returns the first that the pattern matches.
//
// modelSegment bounds how much is handed to the pattern. It has to be longer
// than any badge so a match cannot be cut in half, and the match is rejected if
// it runs to the end of the segment while the segment is not the end of the
// buffer, because `$` inside a slice would otherwise accept "Haiku 4." as a
// version.
func scanBackFor(clean, lower string, subs []string, re *regexp.Regexp) []string {
	const modelSegment = 48
	end := len(lower)
	for tries := 0; tries < maxModelCandidates && end > 0; tries++ {
		at := -1
		for _, sub := range subs {
			if i := strings.LastIndex(lower[:end], sub); i > at {
				at = i
			}
		}
		if at < 0 {
			return nil
		}
		stop := min(at+modelSegment, len(clean))
		seg := clean[at:stop]
		if loc := re.FindStringSubmatchIndex(seg); loc != nil {
			if loc[1] < len(seg) || stop == len(clean) {
				return re.FindStringSubmatch(seg)
			}
		}
		end = at
	}
	return nil
}

// maxModelCandidates bounds the walk. A buffer that says "opus" a thousand
// times in prose is a conversation about models, not a thousand status lines,
// and the scan must not become linear in how often the word appears.
const maxModelCandidates = 12

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// StatusLineOf reads the status line for one session, keeping the last reading
// that worked when this one finds nothing.
func (m *Manager) StatusLineOf(id string) StatusLine {
	s, ok := m.Get(id)
	if !ok {
		return StatusLine{}
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	fresh := ParseStatusLine(string(s.ring.Tail(s.scanWindow(now))))
	return s.holdLine(fresh, now)
}

// scanWindow is how much of the terminal to read this time.
//
// The near window is enough whenever the CLI has repainted recently, which is
// most of the time, and it is what this cost before. The far one is for the
// case that made the strip go blank: a tool prints forty kilobytes, the newest
// status line ends up well behind that, and a narrow read finds nothing.
//
// Widening unconditionally was the obvious fix and the wrong one. Measured, a
// parse over 64 KB cost 34 ms, and this runs on every poll of every open
// conversation. The hold makes that unnecessary: while a reading is fresh there
// is nothing to gain by looking further back, so the far window is only tried
// when there is nothing to lose either.
//
// Caller holds s.mu.
func (s *Session) scanWindow(now time.Time) int {
	if s.heldAt.IsZero() || now.Sub(s.heldAt) >= statusLineRescan {
		return statusLineFarTail
	}
	return statusLineTail
}

// holdLine merges a fresh scrape over the last one that worked.
//
// The reading is scraped off a terminal, so missing it is normal rather than
// exceptional: the CLI repaints its footer with cursor moves, a long tool
// result pushes the line out of the window, and the next poll comes back with
// nothing. Hiding the strip on that would blank a correct reading a second
// after showing it, which is what it did, and it looked like the app losing
// track rather than a scrape missing once.
//
// So each field keeps its last known value for statusLineHold, and Age says how
// old the answer is. Held forever would be worse than blank: an agent that
// stopped hours ago should not still be showing the five-hour window it had.
//
// Caller holds s.mu.
func (s *Session) holdLine(fresh StatusLine, now time.Time) StatusLine {
	if fresh.any() {
		s.heldLine, s.heldAt = fresh, now
		return fresh
	}
	if s.heldAt.IsZero() || now.Sub(s.heldAt) > statusLineHold {
		return StatusLine{}
	}
	out := s.heldLine
	out.AgeSeconds = int(now.Sub(s.heldAt).Seconds())
	return out
}

// any reports whether the scrape found anything at all.
func (l StatusLine) any() bool {
	return l.HasContext || l.HasFiveHour || l.HasWeekly || l.HasCost ||
		l.Model != "" || l.Mode != ""
}

const (
	// statusLineTail is the ordinary read: enough for a CLI that has repainted
	// recently, and cheap enough to do on every poll.
	statusLineTail = 8 << 10

	// statusLineFarTail is the read for when the near one found nothing. The
	// original reasoning was that the line is redrawn at the very end of the
	// buffer, and it is not always: a repaint addresses the cursor rather than
	// appending, and a tool that prints forty kilobytes leaves the newest
	// status line well behind it. Measured on a live session, the newest
	// readable field sat further back than eight kilobytes while the ring was
	// holding 256 KB of it.
	statusLineFarTail = 64 << 10

	// statusLineRescan is how stale a held reading may get before the far
	// window is tried again. At most one wide read every twenty seconds per
	// session, and none at all while the near one keeps working.
	statusLineRescan = 20 * time.Second

	// statusLineHold is how long a field survives scrapes that cannot find it.
	// Long enough to cover a noisy tool call, short enough that a figure on
	// screen is never badly out of date.
	statusLineHold = 5 * time.Minute

	// modeTail bounds what ParseMode is given. The permission indicator is
	// composer chrome and sits at the end of the buffer, so handing it the far
	// window would cost four times as much to look in a place it is not.
	modeTail = 16 << 10
)
