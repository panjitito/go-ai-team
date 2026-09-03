package session

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Quota-limit detection.
//
// The detector is deliberately narrow. It must fire on "you have hit the limit"
// and never on "you are approaching the limit", a usage panel, or a percentage,
// because a false positive spends a second subscription the user did not intend
// to spend. Everything here is anchored to phrasing that only appears once the
// CLI has actually stopped.

var limitPatterns = []*regexp.Regexp{
	// Claude Code: "Claude usage limit reached. Your limit will reset at 3pm."
	regexp.MustCompile(`(?i)usage limit reached`),
	regexp.MustCompile(`(?i)you(?:'ve|ve| have) (?:reached|hit) your (?:usage )?limit`),
	regexp.MustCompile(`(?i)(?:5-hour|five-hour|weekly|daily) limit reached`),
	// Codex: "You've hit your usage limit." / "rate limit exceeded"
	regexp.MustCompile(`(?i)rate limit (?:exceeded|reached)`),
	regexp.MustCompile(`(?i)quota exceeded`),
	regexp.MustCompile(`(?i)out of (?:credits|quota)`),
	regexp.MustCompile(`(?i)insufficient (?:credits|quota)`),
	// HTTP-shaped surfacing.
	regexp.MustCompile(`(?i)429 .*too many requests`),
}

// Phrases that look like a limit but are only a warning. If one of these
// matches, the line is ignored outright.
var warningPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)approaching`),
	regexp.MustCompile(`(?i)nearing`),
	regexp.MustCompile(`(?i)will reach`),
	regexp.MustCompile(`(?i)about to (?:reach|hit)`),
	regexp.MustCompile(`(?i)\b\d{1,2}% (?:of|used|remaining)`),
	regexp.MustCompile(`(?i)heads up`),
	// Our own announcement of a completed switch must never re-trigger.
	regexp.MustCompile(`(?i)go ai team`),
}

// resetPatterns pull the provider's own reset time out of the message so a
// benched account comes back exactly when its window reopens rather than after
// an arbitrary cooldown.
var resetPatterns = []*regexp.Regexp{
	// "resets at 3pm", "reset at 15:00", "resets at 3:30pm (Asia/Jakarta)"
	regexp.MustCompile(`(?i)reset(?:s|ting)? at (\d{1,2})(?::(\d{2}))?\s*(am|pm)?`),
	// "try again in 42 minutes", "retry in 2 hours"
	regexp.MustCompile(`(?i)(?:try again|retry|available again) in (\d+)\s*(second|minute|hour|day)s?`),
}

// LimitHit reports whether a chunk of terminal output says the account is out of
// quota, and how long to bench it for.
type LimitHit struct {
	Matched  bool
	Line     string
	ResetAt  time.Time
	BenchFor time.Duration
	HasReset bool
}

// DefaultBench is used when the provider gives no reset time. Claude's rolling
// window is five hours, so this errs on the short side and simply retries.
const DefaultBench = 1 * time.Hour

// DetectLimit scans recent terminal output for a genuine usage-limit message.
func DetectLimit(chunk string) LimitHit {
	for _, raw := range strings.Split(chunk, "\n") {
		line := strings.TrimSpace(stripANSI(raw))
		if line == "" || len(line) > 600 {
			continue
		}
		skip := false
		for _, w := range warningPatterns {
			if w.MatchString(line) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		for _, p := range limitPatterns {
			if !p.MatchString(line) {
				continue
			}
			hit := LimitHit{Matched: true, Line: line, BenchFor: DefaultBench}
			if at, d, ok := parseReset(line); ok {
				hit.HasReset = true
				if !at.IsZero() {
					hit.ResetAt = at
					hit.BenchFor = time.Until(at)
				} else {
					hit.BenchFor = d
					hit.ResetAt = time.Now().Add(d)
				}
			}
			if hit.BenchFor <= 0 {
				hit.BenchFor = DefaultBench
				hit.ResetAt = time.Now().Add(DefaultBench)
			}
			return hit
		}
	}
	return LimitHit{}
}

// parseReset extracts an absolute reset time or a relative delay from a line.
func parseReset(line string) (time.Time, time.Duration, bool) {
	if m := resetPatterns[0].FindStringSubmatch(line); m != nil {
		hour, err := strconv.Atoi(m[1])
		if err != nil || hour > 24 {
			return time.Time{}, 0, false
		}
		minute := 0
		if m[2] != "" {
			minute, _ = strconv.Atoi(m[2])
		}
		switch strings.ToLower(m[3]) {
		case "pm":
			if hour < 12 {
				hour += 12
			}
		case "am":
			if hour == 12 {
				hour = 0
			}
		}
		now := time.Now()
		at := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
		if !at.After(now) {
			at = at.Add(24 * time.Hour)
		}
		return at, 0, true
	}
	if m := resetPatterns[1].FindStringSubmatch(line); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return time.Time{}, 0, false
		}
		var unit time.Duration
		switch strings.ToLower(m[2]) {
		case "second":
			unit = time.Second
		case "minute":
			unit = time.Minute
		case "hour":
			unit = time.Hour
		case "day":
			unit = 24 * time.Hour
		}
		return time.Time{}, time.Duration(n) * unit, true
	}
	return time.Time{}, 0, false
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07]*\x07|\r`)

// stripANSI removes escape sequences so pattern matching sees plain text.
func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }
