package session

import (
	"regexp"
	"strings"
)

// Detecting when the CLI is asking something in its own interface.
//
// The conversation view renders from the transcript, which is the right source
// for everything the agent says. But some questions never reach the transcript:
// the trust prompt on a new folder, a permission request, the model picker,
// /login. Those are drawn straight to the terminal and answered with arrow keys.
//
// If the chat view simply showed nothing, the agent would appear to hang. So the
// terminal tail is checked for the shapes those prompts take, and the UI offers
// to switch to the terminal rather than leaving the person stuck.

var promptPatterns = []*regexp.Regexp{
	// The selection cursor Claude Code draws next to the highlighted option.
	regexp.MustCompile(`(?m)^\s*[❯>]\s+\S`),
	regexp.MustCompile(`(?i)enter to confirm`),
	regexp.MustCompile(`(?i)esc to cancel`),
	regexp.MustCompile(`(?i)do you (want|trust)`),
	regexp.MustCompile(`(?i)\(y/n\)`),
	regexp.MustCompile(`(?i)yes,? I trust`),
	regexp.MustCompile(`(?i)press enter to`),
	regexp.MustCompile(`(?i)select an option`),
	// The sign-in flow, which is the other thing that needs the terminal.
	regexp.MustCompile(`(?i)paste (the )?code`),
	regexp.MustCompile(`(?i)/login`),
}

// promptTitles maps a recognised prompt to a short description, so the banner
// says what is being asked instead of just that something is.
var promptTitles = []struct {
	re    *regexp.Regexp
	title string
}{
	{regexp.MustCompile(`(?i)trust this folder|quick safety check`), "It is asking whether you trust this folder"},
	{regexp.MustCompile(`(?i)paste (the )?code|/login`), "It is waiting for you to sign in"},
	{regexp.MustCompile(`(?i)do you want to`), "It is asking permission to do something"},
	{regexp.MustCompile(`(?i)select an option|choose`), "It is waiting for you to choose an option"},
}

// NeedsTerminal reports whether the tail of a terminal looks like an
// interactive prompt awaiting a keypress, and what it appears to be asking.
func NeedsTerminal(tail string) (bool, string) {
	clean := stripANSI(tail)
	// Only the last part matters: an old prompt further up the scrollback has
	// already been answered.
	if len(clean) > 4000 {
		clean = clean[len(clean)-4000:]
	}
	hit := false
	for _, p := range promptPatterns {
		if p.MatchString(clean) {
			hit = true
			break
		}
	}
	if !hit {
		return false, ""
	}
	for _, t := range promptTitles {
		if t.re.MatchString(clean) {
			return true, t.title
		}
	}
	return true, "It is waiting for an answer in the terminal"
}

// PromptTail returns the readable last lines of a terminal, for showing the
// question inline without making the person switch views to read it.
func PromptTail(tail string, lines int) string {
	clean := stripANSI(tail)
	clean = strings.ReplaceAll(clean, "\r", "\n")
	var kept []string
	var prev string
	for _, l := range strings.Split(clean, "\n") {
		l = strings.TrimRight(l, " \t")
		if strings.TrimSpace(l) == "" || l == prev {
			continue
		}
		prev = l
		kept = append(kept, l)
	}
	if len(kept) > lines {
		kept = kept[len(kept)-lines:]
	}
	return strings.Join(kept, "\n")
}

// Tail returns the recent terminal bytes for prompt detection.
func (m *Manager) Tail(id string, n int) string {
	s, ok := m.Get(id)
	if !ok {
		return ""
	}
	b := s.ring.Snapshot()
	if n > 0 && len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}
