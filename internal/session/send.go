package session

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/uniair/go-ai-team/internal/claudefs"
	"github.com/uniair/go-ai-team/internal/store"
)

// Sending a prompt into a CLI that may not be listening yet.
//
// A terminal UI drops input typed before it has finished drawing its prompt.
// Writing straight to the PTY therefore loses the first message whenever
// somebody starts an agent and types immediately — which is exactly what people
// do. The bytes vanish with no error, the agent sits idle, and it looks like the
// app swallowed the message.
//
// So the first prompt of a session waits for the CLI to go quiet before it is
// typed. After that the session is known to be listening and writes go straight
// through, because adding a delay to every subsequent message would make the UI
// feel sluggish for no reason.

// readyQuiet is how long the CLI must produce no output before it is considered
// ready for input. Long enough to outlast the gaps in a startup banner, short
// enough not to feel like a hang.
const readyQuiet = 900 * time.Millisecond

// readyWait bounds the wait. If the CLI never settles, the prompt is sent
// anyway: a message the user can see land badly beats one silently discarded.
const readyWait = 45 * time.Second

// SendPrompt delivers a prompt, waiting for the session to be ready the first
// time. A carriage return is what submits in these interfaces; a newline is
// treated as a line break inside the composer.
func (m *Manager) SendPrompt(sessionID, text string) error {
	s, ok := m.Get(sessionID)
	if !ok {
		return errNotFound
	}

	s.mu.Lock()
	settled := s.inputReady
	s.mu.Unlock()

	if !settled {
		m.waitReady(s)
		s.mu.Lock()
		s.inputReady = true
		s.mu.Unlock()
	}
	return m.deliverPrompt(s, text)
}

// deliverPrompt types the prompt and then confirms it actually arrived.
//
// Two different things go wrong, and only one of them was handled before:
//
//   - The return is swallowed. Claude Code turns bracketed paste on
//     (ESC[?2004h), and in that mode text plus a trailing return in one burst
//     reads as a single paste. The prompt lands in the composer and sits there,
//     typed and unsent. Wrapping the text in the paste markers and sending the
//     return separately fixes that one.
//
//   - The paste never arrives. While the CLI is still connecting it is not
//     reading input at all, and the bytes go nowhere. The composer is left
//     showing its own placeholder, so nothing on screen suggests a message was
//     ever sent.
//
// Waiting longer before typing only makes the second rarer, never impossible,
// and the two look identical from the outside. So the terminal is read back:
// text on the prompt line needs another return, text absent entirely needs to be
// typed again.
func (m *Manager) deliverPrompt(s *Session, text string) error {
	// One prompt at a time per session. Without this, confirming one message
	// while the next is being typed would press Enter into somebody else's
	// half-written text.
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	// The terminal is not a reliable witness to a submission.
	//
	// It was the only signal at first, and it is wrong in both directions: a busy
	// TUI scrolls a sent message out of the window in under a second, which reads
	// as "never arrived", and treating that as failure retyped a message that had
	// already gone. Treating it as success instead lost messages outright —
	// measured, one send in three returned OK having delivered nothing.
	//
	// The transcript settles it. Claude Code appends the user's turn to its JSONL
	// the moment a message is accepted, so the file appearing or growing is proof
	// of delivery that owes nothing to how the screen was drawn.
	grew := m.transcriptGrew(s)

	// Typed exactly once. Never again.
	//
	// Retyping a prompt that could not be seen on screen was tried, in four
	// increasingly careful versions, and every one of them sent somebody's
	// message twice. The reason is simple and does not yield to tuning: for a
	// few seconds after a message is accepted, an accepted message and a dropped
	// one look identical — the composer is empty either way, the text is gone
	// from the screen either way, and the transcript has not been written yet
	// either way. Any rule that retypes inside that window eventually retypes a
	// message that had already gone.
	//
	// A duplicate is silent and wrong and cannot be taken back. A failure is
	// visible, and the person can simply press send again. So this types once,
	// presses Enter as often as it likes — which is free, and fixes the common
	// case of a swallowed return — and if the transcript never shows the turn,
	// says so instead of guessing.
	if err := m.Write(s.ID, []byte(pasteStart+text+pasteEnd)); err != nil {
		return err
	}
	time.Sleep(submitGap)
	if err := m.Write(s.ID, []byte("\r")); err != nil {
		return err
	}

	landed := false
	deadline := time.Now().Add(deliverBudget)
	for time.Now().Before(deadline) {
		time.Sleep(submitCheck)
		s.mu.Lock()
		gone := s.terminal()
		s.mu.Unlock()
		if gone {
			return nil
		}

		// The proof, and the only thing that counts as one.
		if grew() {
			return nil
		}

		// A box on screen means the CLI took the message and is now asking
		// something about it. Enter here would answer that question — pick
		// whatever the cursor happens to be on — rather than submit anything.
		// Exactly this turned `/theme` into "theme set" without anyone choosing.
		if _, asking := ParseAsk(m.Tail(s.ID, askTail)); asking {
			return nil
		}

		// Still sitting on the prompt line: press Enter again. Safe only because
		// of the check above — on an empty composer it does nothing, but there is
		// no such thing as a harmless Enter while a picker is up.
		if deliveryState(m.Tail(s.ID, tailWindow), text) == deliveryTyped {
			landed = true
			if err := m.Write(s.ID, []byte("\r")); err != nil {
				return err
			}
		}
	}

	// One last look: the transcript may have grown as the budget ran out.
	if grew() {
		return nil
	}

	if landed {
		return fmt.Errorf("the message is typed into the agent's terminal but the CLI has not taken it. Open the Terminal tab and press Enter")
	}
	return fmt.Errorf("the CLI did not accept the message — nothing was written to its transcript. It may still be starting up; press send again")
}

// delivery is what the terminal says happened to a prompt.
type delivery int

const (
	// deliveryMissing: the text is nowhere to be seen, which is ambiguous. It
	// may never have arrived, or it may have gone through and scrolled out of
	// the window being read — a busy TUI redraws enough to push it out in under
	// a second. Nothing may be retyped on this alone.
	deliveryMissing delivery = iota
	// deliveryTyped: the text is on the prompt line, waiting for a return.
	deliveryTyped
	// deliverySent: the text is on screen but not on the prompt line, so the CLI
	// has taken it.
	deliverySent
	// deliveryIdle: the composer is showing its own placeholder suggestion. That
	// is positive evidence of an empty composer on an idle CLI — nothing was
	// typed and nothing is being worked on — which is the one state where
	// retyping cannot produce a duplicate.
	deliveryIdle
)

// composerPlaceholder is the hint Claude Code shows in an empty composer. It is
// only sometimes there — often the composer is simply blank — so its absence
// says nothing.
var composerPlaceholder = regexp.MustCompile(`^\s*Try\s+"`)

// deliveryState reads the terminal to decide what happened to a prompt.
func deliveryState(tail, text string) delivery {
	want := squash(text)
	if len(want) > 40 {
		want = want[:40]
	}
	if want == "" {
		return deliverySent
	}
	clean := stripANSI(tail)

	i := strings.LastIndex(clean, "❯")
	if i >= 0 {
		if strings.Contains(squash(clean[i:]), want) {
			return deliveryTyped
		}
	}
	if strings.Contains(squash(clean), want) {
		return deliverySent
	}
	// An empty composer with the text nowhere on screen. On its own this is not
	// enough to act on — it is also what a just-submitted message looks like —
	// but paired with a transcript that has not grown, and seen several times
	// running, it is the CLI sitting there having never received the paste.
	if i >= 0 && composerEmpty(clean[i+len("❯"):]) {
		return deliveryIdle
	}
	return deliveryMissing
}

// composerEmpty reports whether the prompt line holds nothing typed.
func composerEmpty(after string) bool {
	line := after
	if n := strings.IndexByte(line, '\n'); n >= 0 {
		line = line[:n]
	}
	// The box the composer is drawn in is not content.
	line = strings.Trim(line, " \t ─│┌┐└┘")
	return line == "" || composerPlaceholder.MatchString(line)
}

// squash removes whitespace so wrapped and re-indented text still compares.
func squash(s string) string {
	var b strings.Builder
	for _, r := range s {
		if !unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

const (
	// The bracketed-paste markers, as defined by the terminal that enables the
	// mode. Everything between them is content, never keys.
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"

	// submitGap separates the paste from the return that submits it. Long enough
	// for the application to have consumed the paste, short enough to be
	// imperceptible.
	submitGap = 140 * time.Millisecond

	// submitCheck is how long to wait before reading the terminal back.
	submitCheck = 700 * time.Millisecond

	// deliverBudget is how long to keep trying before saying so. Long enough to
	// outlast a slow connect, short enough that a stuck send is reported rather
	// than hung on.
	deliverBudget = 20 * time.Second

	// tailWindow is how much of the terminal to read back. Wide enough that a
	// submitted prompt is still visible above the composer after the CLI has
	// redrawn, which is what tells "sent" apart from "never arrived".
	tailWindow = 32 << 10
)

// waitReady blocks until the session stops producing output, or the deadline.
func (m *Manager) waitReady(s *Session) {
	deadline := time.Now().Add(readyWait)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		last := s.lastOut
		gone := s.terminal()
		s.mu.Unlock()
		if gone {
			return
		}
		// Nothing has ever been printed: the process is still starting, so
		// there is no quiet period to measure yet.
		if !last.IsZero() && time.Since(last) >= readyQuiet {
			return
		}
		time.Sleep(120 * time.Millisecond)
	}
}

// MarkInputReady records that a session is known to accept input, so a later
// prompt is not delayed. Used after the first successful delivery.
func (m *Manager) MarkInputReady(sessionID string) {
	if s, ok := m.Get(sessionID); ok {
		s.mu.Lock()
		s.inputReady = true
		s.mu.Unlock()
	}
}

// SendKeys writes raw keystrokes with no readiness wait. This is what the
// terminal view uses: a person typing into a live terminal has already seen
// whether it is listening, and delaying their keystrokes would be wrong.
func (m *Manager) SendKeys(sessionID string, data string) error {
	if strings.TrimSpace(data) != "" {
		m.MarkInputReady(sessionID)
	}
	return m.Write(sessionID, []byte(data))
}

// transcriptGrew returns a check that reports whether the CLI has written a new
// turn since it was created.
//
// Claude Code appends the user's message to its JSONL transcript the moment it
// accepts one, so this is proof of delivery that does not depend on how the
// terminal happened to redraw. For a session's very first message there is no
// transcript yet, and the file appearing at all is the same proof.
//
// The session id may not be known yet when a prompt is sent, so it is looked up
// again on each check rather than captured once.
func (m *Manager) transcriptGrew(s *Session) func() bool {
	s.mu.Lock()
	dir, cwd, pid, provider := s.AccountDir, s.CWD, s.PID, s.Provider
	sid := s.ClaudeSessionID
	s.mu.Unlock()

	if provider != store.ProviderClaude || dir == "" || cwd == "" {
		return func() bool { return false }
	}

	find := func() (string, bool) {
		id := sid
		if id == "" {
			s.mu.Lock()
			id = s.ClaudeSessionID
			s.mu.Unlock()
		}
		if id == "" && pid != 0 {
			if meta := claudefs.FindSessionMeta([]string{dir}, pid); meta != nil {
				id = meta.SessionID
			}
		}
		if id == "" {
			return "", false
		}
		return claudefs.FindTranscript(dir, cwd, id)
	}

	base := int64(-1)
	if p, ok := find(); ok {
		if st, err := os.Stat(p); err == nil {
			base = st.Size()
		}
	}

	return func() bool {
		p, ok := find()
		if !ok {
			return false
		}
		st, err := os.Stat(p)
		if err != nil {
			return false
		}
		// -1 means there was no transcript when we started, so its existence is
		// itself the new turn.
		return st.Size() > base
	}
}
