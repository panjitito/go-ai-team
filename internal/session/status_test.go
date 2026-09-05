package session

import (
	"strings"
	"testing"
)

// Tail must return the end of the buffer, including across the wrap, or the
// status is read from the wrong part of the scrollback.
func TestRingTail(t *testing.T) {
	r := newRing(16)

	if got := r.Tail(8); len(got) != 0 {
		t.Errorf("empty ring tail = %q", got)
	}

	r.Write([]byte("abcdefgh"))
	if got := string(r.Tail(4)); got != "efgh" {
		t.Errorf("tail(4) = %q, want efgh", got)
	}
	if got := string(r.Tail(99)); got != "abcdefgh" {
		t.Errorf("tail longer than the content = %q, want abcdefgh", got)
	}

	// Overflow, so the buffer wraps and the seam sits inside the tail.
	r.Write([]byte("ijklmnopqr")) // total 18 into a 16-byte ring
	full := string(r.Snapshot())
	if full != "cdefghijklmnopqr" {
		t.Fatalf("snapshot = %q", full)
	}
	for _, n := range []int{1, 4, 8, 16, 40} {
		want := full
		if n < len(full) {
			want = full[len(full)-n:]
		}
		if got := string(r.Tail(n)); got != want {
			t.Errorf("wrapped tail(%d) = %q, want %q", n, got, want)
		}
	}
}

// Tail must agree with Snapshot at every size, on a ring that has wrapped many
// times — the arithmetic around the seam is where this kind of code goes wrong.
func TestRingTailMatchesSnapshot(t *testing.T) {
	r := newRing(64)
	for i := 0; i < 40; i++ {
		r.Write([]byte(strings.Repeat(string(rune('a'+i%26)), i%11+1)))
		full := string(r.Snapshot())
		for _, n := range []int{0, 1, 7, 32, 64, 100} {
			want := full
			if n < len(full) {
				want = full[len(full)-n:]
			}
			if n == 0 {
				want = ""
			}
			if got := string(r.Tail(n)); got != want {
				t.Fatalf("write %d, tail(%d) = %q, want %q (full %q)", i, n, got, want, full)
			}
		}
	}
}
