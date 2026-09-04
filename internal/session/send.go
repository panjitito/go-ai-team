package session

import (
	"fmt"
	"strings"
	"time"
	"unicode"
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

	// landed records that the text was seen on screen at least once. Once it has
	// been, it is never typed again — only submitted. Re-pasting something that
	// did arrive appends it a second time and sends the message twice.
	landed := false
	pastes := 0

	deadline := time.Now().Add(deliverBudget)
	for {
		if !landed {
			if pastes >= maxPastes {
				return fmt.Errorf("the CLI did not accept the message. It may still be starting up — try again in a moment")
			}
			if err := m.Write(s.ID, []byte(pasteStart+text+pasteEnd)); err != nil {
				return err
			}
			pastes++
			time.Sleep(submitGap)
		}
		if err := m.Write(s.ID, []byte("\r")); err != nil {
			return err
		}

		time.Sleep(submitCheck)
		s.mu.Lock()
		gone := s.terminal()
		s.mu.Unlock()
		if gone {
			return nil
		}

		switch deliveryState(m.Tail(s.ID, tailWindow), text) {
		case deliverySent:
			return nil
		case deliveryTyped:
			// It arrived and is waiting on the prompt line. Keep pressing Enter,
			// but never type it again.
			landed = true
		case deliveryMissing:
			if landed {
				// Seen before and gone now, with the echo already scrolled away.
				// Treat that as sent rather than risk sending it twice.
				return nil
			}
		}

		if time.Now().After(deadline) {
			if landed {
				return fmt.Errorf("the message is typed into the agent's terminal but the CLI has not taken it. Open the Terminal tab and press Enter")
			}
			return fmt.Errorf("the CLI did not accept the message. It may still be starting up — try again in a moment")
		}
	}
}

// delivery is what the terminal says happened to a prompt.
type delivery int

const (
	// deliveryMissing: the text is nowhere on screen. It never arrived.
	deliveryMissing delivery = iota
	// deliveryTyped: the text is on the prompt line, waiting for a return.
	deliveryTyped
	// deliverySent: the text is on screen but not on the prompt line, so the CLI
	// has taken it.
	deliverySent
)

// deliveryState reads the terminal to decide which of the three happened.
func deliveryState(tail, text string) delivery {
	want := squash(text)
	if len(want) > 40 {
		want = want[:40]
	}
	if want == "" {
		return deliverySent
	}
	clean := stripANSI(tail)
	if i := strings.LastIndex(clean, "❯"); i >= 0 {
		if strings.Contains(squash(clean[i:]), want) {
			return deliveryTyped
		}
	}
	if strings.Contains(squash(clean), want) {
		return deliverySent
	}
	return deliveryMissing
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

	// maxPastes bounds re-typing a prompt that never arrived. More than this at
	// a CLI that is not reading would just be shouting.
	maxPastes = 3

	// deliverBudget is how long to keep trying before saying so. Long enough to
	// outlast a slow connect, short enough that a stuck send is reported rather
	// than hung on.
	deliverBudget = 12 * time.Second

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
