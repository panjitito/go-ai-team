package session

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/panjitito/go-ai-team/internal/claudefs"
	"github.com/panjitito/go-ai-team/internal/store"
)

// Auto-switch: when the account an agent runs on is out of quota, hand the same
// conversation to another account that is signed in, and carry on.
//
// The one hard part is that a Claude session physically lives inside the account
// that created it. Relaunching against a different CLAUDE_CONFIG_DIR would
// resume into an empty conversation, which is exactly the loss the feature
// exists to prevent — so the transcript is copied into the incoming account's
// tree first. The outgoing account keeps its own copy untouched.

// handleLimit runs the whole switch, from bench to resume.
func (m *Manager) handleLimit(s *Session, hit LimitHit) {
	// A pattern match is only a suspicion. The terminal shows whatever the agent
	// is displaying, so the words can arrive from a file, a web page or a
	// conversation — that is not hypothetical, it benched a healthy account
	// during testing. Watch the session first: a real limit stops it dead, while
	// content that merely mentions one is followed by the agent carrying on.
	if !m.confirmLimit(s) {
		return
	}

	s.mu.Lock()
	accountID := s.AccountID
	accountName := s.AccountName
	provider := s.Provider
	agentID := s.AgentID
	projectID := s.ProjectID
	cwd := s.CWD
	fromDir := s.AccountDir
	sid := s.ClaudeSessionID
	s.mu.Unlock()

	// Bench the exhausted account first, and do it even when there is nothing to
	// switch to: the next agent launched should not walk into the same wall.
	if accountID != "" {
		until := hit.ResetAt
		if until.IsZero() {
			until = time.Now().Add(hit.BenchFor)
		}
		_, _ = m.st.UpdateAccount(accountID, func(a *store.Account) {
			a.BenchedUntil = until
			a.BenchReason = hit.Line
		})
		m.emit(Event{
			Type: "account.benched", AgentID: agentID,
			Message: fmt.Sprintf("%s is out of quota until %s", accountName, until.Format("15:04")),
		})
	}

	if !m.st.Settings().AutoSwitch {
		m.note(s, "usage limit reached. Auto-switch is off, so the agent stops here.")
		return
	}

	next := m.pickFallback(provider, accountID)
	if next == nil {
		m.note(s, fmt.Sprintf(
			"usage limit reached on %s and no other signed-in account is available. Quota returns at %s.",
			accountName, hit.ResetAt.Format("15:04")))
		return
	}

	m.note(s, fmt.Sprintf("usage limit reached on %s. Handing this conversation to %s.", accountName, next.Name))

	// Carry the transcript across so the resumed session has its real history
	// rather than a summary of it.
	carried := false
	if provider == store.ProviderClaude && sid != "" {
		if err := copyTranscript(fromDir, next.Dir, cwd, sid); err != nil {
			m.note(s, "could not copy the transcript across: "+err.Error()+". Starting a fresh session on "+next.Name+".")
		} else {
			carried = true
		}
	}

	// Stop the exhausted process before starting its replacement.
	_ = m.Stop(s.ID)

	resume := ""
	if carried {
		resume = sid
	}

	agent, _ := m.st.Agent(agentID)
	var args []string
	var env map[string]string
	if agent != nil {
		args = m.agentArgs(agent)
		env = agent.Env
	}

	ns, err := m.Spawn(SpawnOpts{
		Kind:      KindAgent,
		AgentID:   agentID,
		ProjectID: projectID,
		Provider:  provider,
		AccountID: next.ID,
		CWD:       cwd,
		Args:      args,
		Env:       env,
		ResumeID:  resume,
	})
	if err != nil {
		m.emit(Event{
			Type: "switch.failed", AgentID: agentID,
			Message: fmt.Sprintf("could not relaunch on %s: %v", next.Name, err),
		})
		return
	}

	s.mu.Lock()
	count := s.SwitchCount + 1
	prevLog := append([]string(nil), s.SwitchLog...)
	s.mu.Unlock()

	ns.mu.Lock()
	ns.SwitchCount = count
	ns.SwitchLog = append(prevLog, fmt.Sprintf(
		"%s  switched %s -> %s (%s)",
		time.Now().Format("15:04:05"), accountName, next.Name,
		map[bool]string{true: "conversation carried over", false: "fresh session"}[carried]))
	ns.mu.Unlock()

	// Nudge the agent to continue rather than restart. Without this a resumed
	// session often re-plans work it has already done.
	if carried {
		go func() {
			// Give the CLI a moment to finish replaying the transcript.
			time.Sleep(6 * time.Second)
			_ = m.Write(ns.ID, []byte(
				"Continue exactly where you left off. You were interrupted by a usage limit, not by a change of plan; do not restart the task or re-summarise what you already did.\r"))
		}()
	}

	m.emit(Event{
		Type: "switch.done", SessionID: ns.ID, AgentID: agentID,
		Message: fmt.Sprintf("%s took over from %s", next.Name, accountName),
		Payload: ns.Public(),
	})
}

// agentArgs rebuilds an agent's launch flags. Kept here so a relaunch after a
// switch is identical to the original launch.
func (m *Manager) agentArgs(a *store.Agent) []string {
	var args []string
	if a.Model != "" {
		args = append(args, "--model", a.Model)
	}
	if m.st.Settings().SkipPermissions && a.Provider == store.ProviderClaude {
		args = append(args, "--dangerously-skip-permissions")
	}
	if a.ExtraArgs != "" {
		args = append(args, splitArgs(a.ExtraArgs)...)
	}
	return args
}

// AgentArgs is the exported form used when starting an agent normally.
func (m *Manager) AgentArgs(a *store.Agent) []string { return m.agentArgs(a) }

// splitArgs is a small shell-ish splitter that honours quotes, so a flag value
// containing a space survives.
func splitArgs(s string) []string {
	var out []string
	var cur []rune
	var quote rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = cur[:0]
		}
	}
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur = append(cur, r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t':
			flush()
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return out
}

// pickFallback chooses the replacement account: same provider, signed in on
// disk, not benched, not the one that just failed, and not opted out of the
// pool. The opt-out is what stops an employer's subscription from silently
// paying for personal work.
func (m *Manager) pickFallback(p store.Provider, exclude string) *store.Account {
	var best *store.Account
	var bestUsed time.Time
	for _, a := range m.st.AccountsFor(p) {
		if a.ID == exclude || a.ExcludeFromFallback || a.Benched() {
			continue
		}
		if !claudefs.SignedIn(a.Dir) {
			continue
		}
		// Prefer the least recently used account: it is the one most likely to
		// have quota left, and it spreads load instead of draining one plan.
		st := m.accs.Status(a)
		if best == nil || st.LastUsed.Before(bestUsed) {
			best, bestUsed = a, st.LastUsed
		}
	}
	return best
}

// note writes a Go AI Team line into the agent's own terminal, so what happened
// is visible where the user is already looking. The text deliberately contains
// "Go AI Team", which the limit detector treats as a warning phrase and ignores,
// so our own announcement can never re-trigger a switch.
func (m *Manager) note(s *Session, msg string) {
	line := fmt.Sprintf("\r\n\x1b[38;5;208m[Go AI Team]\x1b[0m %s\r\n", msg)
	s.mu.Lock()
	_, _ = s.ring.Write([]byte(line))
	s.SwitchLog = append(s.SwitchLog, time.Now().Format("15:04:05")+"  "+msg)
	for ch := range s.subs {
		select {
		case ch <- []byte(line):
		default:
		}
	}
	s.mu.Unlock()
	m.emit(Event{Type: "session.note", SessionID: s.ID, AgentID: s.AgentID, Message: msg})
}

// copyTranscript places a session's transcript inside the incoming account's
// tree, which is what lets --resume find it there. The source is left alone.
func copyTranscript(fromDir, toDir, cwd, sessionID string) error {
	if fromDir == "" || toDir == "" || sessionID == "" {
		return fmt.Errorf("missing directory or session id")
	}
	src, ok := claudefs.FindTranscript(fromDir, cwd, sessionID)
	if !ok {
		return fmt.Errorf("no transcript found for session %s", sessionID)
	}
	// Mirror the same relative layout inside the destination account so the CLI
	// looks in the place it expects.
	rel, err := filepath.Rel(filepath.Join(fromDir, "projects"), src)
	if err != nil {
		rel = filepath.Join(claudefs.EncodeCWD(cwd), sessionID+".jsonl")
	}
	dst := filepath.Join(toDir, "projects", rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
