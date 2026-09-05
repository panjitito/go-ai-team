package session

import (
	"fmt"
	"time"
)

// Sending the CLI one of its own slash commands.
//
// This looks like sending a prompt and is not, and the difference broke the run
// bar in a way that took a live session to see. A prompt is proved delivered by
// the transcript growing — Claude Code appends the user's turn the moment it
// accepts one, which is evidence that owes nothing to how the screen was drawn.
// A slash command writes nothing to the transcript. It is handled inside the CLI
// and never becomes a turn.
//
// So every /model and /effort sent from the bar blocked for the full delivery
// budget and then reported "the CLI did not accept the message" — over a session
// that had switched model perfectly a second earlier. A red toast on every
// successful action.
//
// The proof here is the composer instead: the command is typed onto the prompt
// line, and a CLI that has taken it clears that line. Watching the text arrive
// and then leave is direct evidence the command was consumed, and it needs no
// knowledge of what the command does.

// SendCommand types a slash command and confirms the CLI took it.
func (m *Manager) SendCommand(sessionID, text string) error {
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

	// Shares the send lock with prompts: pressing Enter to confirm one of these
	// while a message is half-typed would submit somebody else's text.
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	s.mu.Lock()
	quietSince := s.lastOut
	s.mu.Unlock()

	if err := m.Write(s.ID, []byte(pasteStart+text+pasteEnd)); err != nil {
		return err
	}
	time.Sleep(submitGap)
	if err := m.Write(s.ID, []byte("\r")); err != nil {
		return err
	}

	// Polled quickly, because the whole signal is catching the line before and
	// after the CLI clears it.
	sawTyped := false
	deadline := time.Now().Add(commandBudget)
	for time.Now().Before(deadline) {
		time.Sleep(commandCheck)

		s.mu.Lock()
		gone := s.terminal()
		s.mu.Unlock()
		if gone {
			return nil
		}

		switch deliveryState(m.Tail(s.ID, tailWindow), text) {
		case deliveryTyped:
			// Sitting on the prompt line unsent. Enter is free to repeat: on an
			// empty composer it does nothing.
			sawTyped = true
			if err := m.Write(s.ID, []byte("\r")); err != nil {
				return err
			}
		default:
			if sawTyped {
				// Arrived, and then left the prompt line. Taken.
				return nil
			}
		}
	}

	// Still on the prompt line is not enough to call it a failure. Several of
	// these commands leave their own text on screen after acting — /help draws a
	// panel with "❯ /help" still above it — and reporting an error over a command
	// that plainly worked is worse than saying nothing.
	//
	// Silence is the tell. A CLI that took the command redraws: the panel, the
	// confirmation line, at minimum the status line. One that is not listening
	// produces nothing at all, and that is the only case worth an error.
	s.mu.Lock()
	silent := s.lastOut.Equal(quietSince)
	s.mu.Unlock()
	if silent && deliveryState(m.Tail(s.ID, tailWindow), text) == deliveryTyped {
		return fmt.Errorf("%q is typed into the agent's terminal but the CLI has not taken it. Open the Terminal tab and press Enter", text)
	}
	return nil
}

const (
	// commandBudget is short on purpose. A slash command is answered by the CLI
	// itself, with no model call in the way, so anything that has not been taken
	// in a couple of seconds is not going to be.
	commandBudget = 4 * time.Second
	// commandCheck is fast enough to catch the prompt line between the paste
	// landing and the CLI clearing it.
	commandCheck = 150 * time.Millisecond
)
