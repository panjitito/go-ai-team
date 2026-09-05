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
	// The selection cursor on a numbered option, which is the shape every one of
	// these prompts actually takes: "❯ 1. Yes".
	//
	// This used to be `^\s*[❯>]\s+\S` — a prompt marker followed by anything —
	// and it was wrong twice over. A bare ">" begins every quoted message Claude
	// Code renders, so an agent that had merely been *talked to* looked like an
	// agent asking a question; and "❯ " is also the empty composer, which is on
	// screen permanently. The banner then sat there claiming an idle agent was
	// waiting for an answer, over a terminal that plainly showed it was not.
	regexp.MustCompile(`(?m)^\s*❯\s+\d+[.)]\s`),
	regexp.MustCompile(`(?im)^\s*❯\s+(yes|no)\b`),
	regexp.MustCompile(`(?i)enter to confirm`),
	regexp.MustCompile(`(?i)esc to cancel`),
	regexp.MustCompile(`(?i)yes,? I trust`),
	regexp.MustCompile(`(?i)do you trust the files`),
	// The sign-in flow, which is the other thing that needs the terminal.
	regexp.MustCompile(`(?i)paste (the )?code here`),
}

// promptTitles maps a recognised prompt to a short description, so the banner
// says what is being asked instead of just that something is.
var promptTitles = []struct {
	re    *regexp.Regexp
	title string
}{
	{regexp.MustCompile(`(?i)trust this folder|quick safety check|do you trust the files`), "It is asking whether you trust this folder"},
	{regexp.MustCompile(`(?i)paste (the )?code here`), "It is waiting for you to sign in"},
	{regexp.MustCompile(`(?i)do you want to`), "It is asking permission to do something"},
}

// NeedsTerminal reports whether the tail of a terminal looks like an
// interactive prompt awaiting a keypress, and what it appears to be asking.
func NeedsTerminal(tail string) (bool, string) {
	clean := stripANSI(tail)
	// Only the current frame. A prompt answered a minute ago is still sitting in
	// the scrollback, and matching that would keep the banner up forever.
	if len(clean) > 2000 {
		clean = clean[len(clean)-2000:]
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

// PromptTail returns what is on the terminal now, for showing the question
// inline without making the person switch views to read it.
//
// Rendered rather than filtered. This used to strip the escape sequences and
// keep the last few distinct lines, which for anything but the simplest prompt
// produced a wall of spinner frames with the actual question crushed onto one
// line at the end — the rewind picker came out as
//
//	Restore the code … before…Create./one.txtcontaining1.Nothingelse.one.txt +1Create./two.txt…❯ (current)Enter to continue
//
// which is unreadable, and this text exists to be read. Drawing the frame onto a
// grid gives back the rows the CLI actually put on screen.
func PromptTail(tail string, lines int) string {
	rows := Screen(tail)
	if len(rows) > lines {
		rows = rows[len(rows)-lines:]
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		// The box rules are the widest thing on screen and say nothing; without
		// trimming them every line of this wraps.
		t := strings.TrimRight(r.Text, " ")
		if strings.Trim(t, "─╌—-") == "" {
			continue
		}
		out = append(out, t)
	}
	return strings.Join(out, "\n")
}

// Tail returns the recent terminal bytes for prompt detection.
func (m *Manager) Tail(id string, n int) string {
	s, ok := m.Get(id)
	if !ok {
		return ""
	}
	// Copy only what was asked for. Snapshotting the whole 256KB ring to keep the
	// last few kilobytes of it is work every prompt delivery was paying for
	// several times over.
	if n > 0 {
		return string(s.ring.Tail(n))
	}
	return string(s.ring.Snapshot())
}
