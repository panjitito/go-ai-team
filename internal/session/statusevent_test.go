package session

import (
	"testing"
	"time"
)

func testManager() *Manager {
	return &Manager{
		sessions: map[string]*Session{},
		evSubs:   map[chan Event]struct{}{},
	}
}

// A turn that ends quietly has to say so.
//
// The UI keeps no timer; it draws whatever the last event told it. Token events
// arrive while an agent is producing tokens and stop when it stops, so the last
// one before a quiet finish always says "working" — and if the flip to waiting
// is silent, the spinner never stops. This is the event that stops it.
func TestStatusChangeIsAnnounced(t *testing.T) {
	m := testManager()
	events, done := m.Subscribe()
	defer done()

	s := &Session{ID: "s1", AgentID: "a1", Status: StatusWorking, lastOut: time.Now()}
	m.sessions[s.ID] = s

	// Still drawing: nothing to say.
	m.refreshStatus(s)
	if got := drain(events); len(got) != 0 {
		t.Fatalf("a session that has not changed emitted %v", got)
	}
	if s.Status != StatusWorking {
		t.Fatalf("status = %s while output was still flowing", s.Status)
	}

	// Quiet for longer than the threshold: the turn is over.
	s.lastOut = time.Now().Add(-2 * idleAfter)
	m.refreshStatus(s)

	got := drain(events)
	if len(got) != 1 {
		t.Fatalf("finishing a turn emitted %d events, want 1", len(got))
	}
	if got[0].Type != "session.status" {
		t.Errorf("event type = %q", got[0].Type)
	}
	if got[0].SessionID != "s1" || got[0].AgentID != "a1" {
		t.Errorf("event does not say who finished: %+v", got[0])
	}
	// The payload is what the UI redraws from, so it has to carry the new
	// status, not the one that has just been left behind.
	pub, ok := got[0].Payload.(PublicSession)
	if !ok {
		t.Fatalf("payload is %T, and the UI patches its session from it", got[0].Payload)
	}
	if pub.Status != StatusWaiting {
		t.Errorf("payload status = %s, want waiting", pub.Status)
	}

	// Once said, not said again every five seconds for as long as it sits there.
	m.refreshStatus(s)
	if got := drain(events); len(got) != 0 {
		t.Errorf("a session that is still waiting emitted %v", got)
	}

	// And back again when it starts drawing.
	s.lastOut = time.Now()
	m.refreshStatus(s)
	got = drain(events)
	if len(got) != 1 || got[0].Payload.(PublicSession).Status != StatusWorking {
		t.Errorf("starting work again emitted %v", got)
	}
}

// A session that has ended is not going to change its mind, and one that has
// never produced a byte has nothing to go on.
func TestStatusSilentForTerminalAndUnstarted(t *testing.T) {
	m := testManager()
	events, done := m.Subscribe()
	defer done()

	for _, s := range []*Session{
		{ID: "gone", Status: StatusExited, lastOut: time.Now().Add(-time.Hour)},
		{ID: "broken", Status: StatusError, lastOut: time.Now().Add(-time.Hour)},
		{ID: "fresh", Status: StatusStarting},
	} {
		m.refreshStatus(s)
		if got := drain(events); len(got) != 0 {
			t.Errorf("%s emitted %v", s.ID, got)
		}
	}
}

func drain(ch <-chan Event) []Event {
	var out []Event
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		case <-time.After(50 * time.Millisecond):
			return out
		}
	}
}
