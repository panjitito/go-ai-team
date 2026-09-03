package session

import (
	"testing"
	"time"
)

// A process can write its last bytes and exit in the same breath. Closing the
// pseudo-terminal the moment Wait returns discards whatever is still buffered,
// which for a short command is the whole output — `echo` came back blank about
// one run in five before this. drainPTY waits for the reader to go quiet first.
func TestDrainPTYWaitsForQuiet(t *testing.T) {
	s := &Session{ring: newRing(4096)}

	// A reader that is still delivering must hold the drain open.
	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		drainPTY(s)
		done <- time.Since(start)
	}()
	for i := 0; i < 6; i++ {
		time.Sleep(60 * time.Millisecond)
		_, _ = s.ring.Write([]byte("still arriving\n"))
	}

	select {
	case took := <-done:
		if took < 300*time.Millisecond {
			t.Errorf("drain returned after %v while output was still arriving", took)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("drainPTY never returned")
	}
}

// It must also give up: a child that inherited the terminal and keeps writing
// cannot be allowed to hold a finished session open forever.
func TestDrainPTYHasADeadline(t *testing.T) {
	s := &Session{ring: newRing(4096)}
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = s.ring.Write([]byte("x"))
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()
	defer close(stop)

	start := time.Now()
	drainPTY(s)
	took := time.Since(start)
	if took > 5*time.Second {
		t.Errorf("drain took %v; the deadline did not hold", took)
	}
	if took < 2*time.Second {
		t.Errorf("drain gave up after only %v; it should run to its deadline while output continues", took)
	}
}

// With nothing arriving it must return promptly, so a normal exit is not slowed.
func TestDrainPTYReturnsQuicklyWhenIdle(t *testing.T) {
	s := &Session{ring: newRing(4096)}
	_, _ = s.ring.Write([]byte("done\n"))
	start := time.Now()
	drainPTY(s)
	if took := time.Since(start); took > 700*time.Millisecond {
		t.Errorf("an idle drain took %v, which would make every exit feel sluggish", took)
	}
}
