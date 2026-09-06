//go:build windows

package desktop

import (
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// NOTIFYICONDATAW has to be exactly the size the shell expects.
//
// cbSize is how Shell_NotifyIcon decides which version of the structure it was
// handed, and a wrong one is rejected with no error worth reading — the icon
// simply never appears, which on a build where closing the window hides to that
// icon would mean an app that cannot be closed. 976 bytes is what the header
// gives on 64-bit, and Go's own alignment is what produces it here, so this
// catches a reordered or mistyped field rather than restating the obvious.
func TestNotifyIconDataLayout(t *testing.T) {
	var nid notifyIconData
	if got := unsafe.Sizeof(nid); got != 976 {
		t.Errorf("sizeof(NOTIFYICONDATAW) = %d, want 976", got)
	}

	// The fields the shell reads first, at the offsets it reads them from.
	for _, c := range []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"cbSize", unsafe.Offsetof(nid.cbSize), 0},
		{"hWnd", unsafe.Offsetof(nid.hWnd), 8},
		{"uID", unsafe.Offsetof(nid.uID), 16},
		{"uFlags", unsafe.Offsetof(nid.uFlags), 20},
		{"uCallbackMessage", unsafe.Offsetof(nid.uCallbackMessage), 24},
		{"hIcon", unsafe.Offsetof(nid.hIcon), 32},
		{"szTip", unsafe.Offsetof(nid.szTip), 40},
		{"szInfo", unsafe.Offsetof(nid.szInfo), 304},
		{"szInfoTitle", unsafe.Offsetof(nid.szInfoTitle), 820},
		{"dwInfoFlags", unsafe.Offsetof(nid.dwInfoFlags), 948},
	} {
		if c.got != c.want {
			t.Errorf("%s is at offset %d, want %d", c.name, c.got, c.want)
		}
	}
}

// A string longer than the buffer must be cut, not written past the end of it,
// and must still be a C string when it gets there.
func TestCopyUTF16(t *testing.T) {
	var buf [8]uint16
	copyUTF16(buf[:], "hello")
	if got := windows.UTF16ToString(buf[:]); got != "hello" {
		t.Errorf("round trip = %q", got)
	}

	// The guard byte after the buffer is the point of the exercise.
	var wide struct {
		field [8]uint16
		after uint16
	}
	wide.after = 0xBEEF
	copyUTF16(wide.field[:], strings.Repeat("x", 40))
	if wide.after != 0xBEEF {
		t.Fatal("copyUTF16 wrote past the end of the buffer")
	}
	if wide.field[len(wide.field)-1] != 0 {
		t.Error("a truncated string was left without its terminating zero")
	}
	if got := windows.UTF16ToString(wide.field[:]); len(got) != 7 {
		t.Errorf("truncated to %q (%d runes), want 7 plus the terminator", got, len(got))
	}
}

// What the icon says on hover, which is also what the balloon says.
func TestWaitingLine(t *testing.T) {
	tray.waiting = 0
	if got := trayTipText(); got != "Go AI Team" {
		t.Errorf("idle tooltip = %q", got)
	}
	tray.waiting = 1
	if got := trayTipText(); !strings.Contains(got, "an agent is waiting") {
		t.Errorf("one waiting = %q", got)
	}
	tray.waiting = 4
	if got := trayTipText(); !strings.Contains(got, "4 agents are waiting") {
		t.Errorf("four waiting = %q", got)
	}
	tray.waiting = 0

	for _, c := range []struct {
		n    int
		want string
	}{{0, "0"}, {1, "1"}, {9, "9"}, {12, "12"}, {103, "103"}} {
		if got := itoa(c.n); got != c.want {
			t.Errorf("itoa(%d) = %q", c.n, got)
		}
	}
}

// Quit from the menu has to actually quit, and take the icon with it.
//
// The menu itself is Windows' code; what is worth pinning is what each choice
// does — a Quit that only hides would leave an app nobody can get out of.
func TestTrayCommand(t *testing.T) {
	was := tray
	t.Cleanup(func() { tray = was })

	quit := 0
	tray = was
	tray.quit = func() { quit++ }
	tray.live = false // no real icon to remove in a test
	tray.hwnd = 0

	trayCommand(menuOpen)
	if quit != 0 {
		t.Error("Open quit the app")
	}
	if tray.hidden {
		t.Error("Open left the window hidden")
	}

	trayCommand(menuQuit)
	if quit != 1 {
		t.Errorf("Quit called the quit function %d times, want 1", quit)
	}

	// Anything else is not a choice anybody made.
	trayCommand(0)
	if quit != 1 {
		t.Errorf("dismissing the menu quit the app (%d)", quit)
	}
}
