package session

import "sync"

// ring is a fixed-size byte buffer holding the tail of a terminal's output.
//
// It exists so the browser can attach to a session that has been running for an
// hour and immediately see context, and so a reconnect after a laptop sleep
// redraws rather than showing an empty screen. Bounded on purpose: an agent that
// cats a large file must not be able to grow this without limit.
type ring struct {
	mu   sync.Mutex
	buf  []byte
	size int
	pos  int // index of the next byte to write

	// total is every byte ever written. Comparing it against size is what tells
	// us whether the buffer has wrapped; deriving that from pos alone is wrong
	// when a write lands exactly on the boundary, leaving pos at 0 on a buffer
	// that is in fact completely full.
	total int64
}

func newRing(size int) *ring {
	return &ring{buf: make([]byte, size), size: size}
}

// Write appends p, discarding the oldest bytes once the buffer is full.
func (r *ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(p)
	if n == 0 {
		return 0, nil
	}
	r.total += int64(n)

	// A write at least as large as the buffer: only its tail can survive.
	if n >= r.size {
		copy(r.buf, p[n-r.size:])
		r.pos = 0
		return n, nil
	}

	written := copy(r.buf[r.pos:], p)
	if written < n {
		copy(r.buf, p[written:])
	}
	r.pos = (r.pos + n) % r.size
	return n, nil
}

// Snapshot returns the buffered bytes in chronological order.
func (r *ring) Snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.total < int64(r.size) {
		out := make([]byte, r.pos)
		copy(out, r.buf[:r.pos])
		return out
	}
	out := make([]byte, 0, r.size)
	out = append(out, r.buf[r.pos:]...)
	out = append(out, r.buf[:r.pos]...)
	return out
}
