package session

import (
	"strings"
	"time"
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
	// The prompt goes in as a bracketed paste, then Enter arrives separately.
	//
	// Claude Code turns bracketed paste on (ESC[?2004h). In that mode a terminal
	// tells the application where pasted content starts and ends, and anything
	// arriving unwrapped in a fast burst is ambiguous: the text landed in the
	// composer but the trailing return was swallowed as part of the paste, so
	// the prompt sat there typed and unsent while the agent looked idle for a
	// reason nothing on screen explained.
	//
	// Wrapping the text makes it unambiguously a paste, and the return that
	// follows is unambiguously a keypress.
	if err := m.Write(sessionID, []byte(pasteStart+text+pasteEnd)); err != nil {
		return err
	}
	time.Sleep(submitGap)
	return m.Write(sessionID, []byte("\r"))
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
