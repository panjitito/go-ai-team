package desktop

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// Restoring geometry must not be able to produce a window nobody can use.
//
// The saved value is whatever the window happened to have, and it can be
// nonsense: zero on the very first run, and the -32000 parking coordinates
// Windows gives a minimized window. Reopening at either would look like the app
// failed to start.
func TestBoundsValid(t *testing.T) {
	ok := []Bounds{
		{X: 0, Y: 0, W: 1280, H: 860},
		{X: 140, Y: 60, W: 1010, H: 700, Maximized: true},
		{X: -1200, Y: 200, W: 900, H: 600}, // a second monitor to the left is fine
		{W: 480, H: 360},                   // exactly the floor
	}
	for _, b := range ok {
		if !b.Valid() {
			t.Errorf("%+v should be restorable", b)
		}
	}

	bad := []Bounds{
		{},                                    // never saved
		{X: 100, Y: 100},                      // no size
		{X: -32000, Y: -32000, W: 160, H: 28}, // a minimized window's parking spot
		{W: 479, H: 800},                      // too narrow to use
		{W: 800, H: 359},                      // too short to use
		{W: -1000, H: -1000},                  // nonsense
	}
	for _, b := range bad {
		if b.Valid() {
			t.Errorf("%+v should not be restored", b)
		}
	}
}

// The icon is embedded, so it must actually be there and actually be an icon.
func TestEmbeddedIcon(t *testing.T) {
	b := IconBytes()
	if len(b) < 1024 {
		t.Fatalf("embedded icon is %d bytes; it is missing or truncated", len(b))
	}
	// ICONDIR: reserved must be 0, type must be 1 (icon, not cursor).
	if got := binary.LittleEndian.Uint16(b[0:2]); got != 0 {
		t.Errorf("reserved = %d, want 0: this is not an .ico", got)
	}
	if got := binary.LittleEndian.Uint16(b[2:4]); got != 1 {
		t.Errorf("type = %d, want 1 (icon)", got)
	}
	n := int(binary.LittleEndian.Uint16(b[4:6]))
	if n < 3 {
		t.Errorf("only %d sizes in the icon; the taskbar, Alt-Tab and Explorer all want different ones", n)
	}

	// Every entry must point at real bytes inside the file.
	small, large := false, false
	for i := 0; i < n; i++ {
		e := b[6+i*16:]
		w := int(e[0])
		if w == 0 {
			w = 256 // 0 means 256 in the ICO format
		}
		size := binary.LittleEndian.Uint32(e[8:12])
		off := binary.LittleEndian.Uint32(e[12:16])
		if off == 0 || size == 0 || int(off+size) > len(b) {
			t.Errorf("entry %d (%dpx) points outside the file: offset %d size %d of %d",
				i, w, off, size, len(b))
		}
		if w <= 32 {
			small = true
		}
		if w >= 128 {
			large = true
		}
	}
	if !small {
		t.Error("no small size: the title bar and taskbar would scale a big one badly")
	}
	if !large {
		t.Error("no large size: Explorer's big icon views would scale up a small one")
	}
}

// EnsureIcon has to be idempotent: it runs on every launch, and rewriting the
// file each time would churn something another process may have open.
func TestEnsureIcon(t *testing.T) {
	dir := t.TempDir()
	path, ok := EnsureIcon(dir)
	if !ok {
		t.Fatal("EnsureIcon reported failure on a writable directory")
	}
	if filepath.Dir(path) != dir {
		t.Errorf("wrote to %s, want it inside %s", path, dir)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("icon not written: %v", err)
	}
	if st.Size() != int64(len(IconBytes())) {
		t.Errorf("wrote %d bytes, embedded is %d", st.Size(), len(IconBytes()))
	}

	// A second call must return the same answer without disturbing the file.
	again, ok2 := EnsureIcon(dir)
	if !ok2 || again != path {
		t.Errorf("second call = (%q, %v), want (%q, true)", again, ok2, path)
	}
	st2, _ := os.Stat(path)
	if !st2.ModTime().Equal(st.ModTime()) {
		t.Error("the icon was rewritten on the second call")
	}
}
