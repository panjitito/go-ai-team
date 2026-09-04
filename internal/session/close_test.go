package session

import (
	"context"
	"testing"

	"github.com/aymanbagabas/go-pty"
)

// countingPty records how many times it is closed.
type countingPty struct{ closes int }

func (c *countingPty) Read([]byte) (int, error)           { return 0, nil }
func (c *countingPty) Write(p []byte) (int, error)        { return len(p), nil }
func (c *countingPty) Close() error                       { c.closes++; return nil }
func (c *countingPty) Name() string                       { return "fake" }
func (c *countingPty) Resize(int, int) error              { return nil }
func (c *countingPty) Fd() uintptr                        { return 0 }
func (c *countingPty) Command(string, ...string) *pty.Cmd { return nil }
func (c *countingPty) CommandContext(context.Context, string, ...string) *pty.Cmd {
	return nil
}

// A pseudo-terminal must be closed exactly once.
//
// On Windows the second ClosePseudoConsole on a closed handle terminates the
// whole process — no panic, no error, nothing logged. Two closes were reachable
// together: Stop closed the pty, then the read loop saw EOF and closed it again.
// Pressing Stop took Go AI Team down with every agent it was running.
func TestPTYIsClosedOnlyOnce(t *testing.T) {
	fake := &countingPty{}
	s := &Session{ID: "s1", pty: fake}

	for i := 0; i < 5; i++ {
		if err := s.closePTY(); err != nil {
			t.Fatalf("closePTY: %v", err)
		}
	}
	if fake.closes != 1 {
		t.Fatalf("pty closed %d times, want exactly 1", fake.closes)
	}
}
