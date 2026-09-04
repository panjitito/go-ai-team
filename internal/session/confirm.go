package session

import (
	"time"
)

// Confirming a usage limit before acting on it.
//
// The detector reads the terminal, and a terminal shows whatever the agent is
// displaying: a file it read, a web page it fetched, a message someone typed.
// Pattern matching alone therefore cannot tell "the CLI has stopped, you are out
// of quota" from "the agent is discussing rate limits". It really did bench a
// perfectly healthy account because a conversation on screen contained the words
// in quotes.
//
// The discriminator is behaviour, not wording. A genuine limit stops the turn
// dead: no more output, no more tokens, nothing. Content that merely mentions a
// limit appears mid-turn, and the agent keeps working straight through it. So a
// match is treated as a suspicion, and the session is watched briefly before
// anything irreversible happens.
//
// The cost is a few seconds of delay before an account is benched, which is
// nothing next to wrongly spending a second subscription — or wrongly benching
// the only account that was working.

const (
	// limitConfirm is how long a suspected limit is watched. Long enough that an
	// agent mid-turn will visibly continue; short enough that a real limit is
	// handled promptly.
	limitConfirm = 9 * time.Second

	// settleGrace ignores the CLI's own repaint right after printing a message.
	// A limit notice is drawn, and that drawing is output; it is what comes
	// *after* that distinguishes the two cases.
	settleGrace = 2500 * time.Millisecond
)

// confirmLimit watches a session after a suspected limit and reports whether it
// really has stopped.
func (m *Manager) confirmLimit(s *Session) bool {
	s.mu.Lock()
	before := s.Tokens.Total()
	s.mu.Unlock()

	time.Sleep(limitConfirm)

	s.mu.Lock()
	after := s.Tokens.Total()
	sinceOutput := time.Since(s.lastOut)
	ended := s.terminal()
	s.mu.Unlock()

	return limitConfirmed(ended, before, after, sinceOutput)
}

// limitConfirmed is the judgement itself, separated from the waiting so it can
// be reasoned about and tested directly.
func limitConfirmed(ended bool, before, after int64, sinceOutput time.Duration) bool {
	// The process is gone. Whatever happened, there is no live conversation left
	// to hand to another account.
	if ended {
		return false
	}

	// The transcript grew: the agent produced more work after the phrase
	// appeared, so it was never out of quota. This is the check that catches a
	// limit message the agent was merely displaying.
	if after != before {
		return false
	}

	// It is still printing. A stopped CLI does not keep drawing.
	if sinceOutput < settleGrace {
		return false
	}

	return true
}
