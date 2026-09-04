//go:build windows

package desktop

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Hiding the console window that Windows attaches to a console program.
//
// This is a console application on purpose: it has flags, an `mcp` subcommand
// and a startup banner carrying the phone URL and access token, all of which
// need a terminal. But double-clicking it — or launching it from a shortcut —
// then puts a black console box behind the app window, which no desktop
// application does.
//
// The distinction that matters is who owns the console. A process launched from
// Explorer gets one of its own, and nothing else is using it, so hiding it costs
// nothing. A process launched from an existing terminal shares that terminal
// with the shell, and hiding it would take the user's own window away.
// GetConsoleProcessList tells the two apart: it returns how many processes are
// attached, and exactly one means the console is ours alone.

var (
	kernel32            = windows.NewLazySystemDLL("kernel32.dll")
	procGetConsoleWnd   = kernel32.NewProc("GetConsoleWindow")
	procGetConsoleProcs = kernel32.NewProc("GetConsoleProcessList")
)

const swHide = 0

// HideOwnConsole hides the console window if this process is the only thing
// attached to it. It reports whether it hid one.
func HideOwnConsole() bool {
	hwnd, _, _ := procGetConsoleWnd.Call()
	if hwnd == 0 {
		return false // no console at all: already a windowed process
	}
	var pids [4]uint32
	n, _, _ := procGetConsoleProcs.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if n != 1 {
		return false // a shell is using this console too; leave it alone
	}
	show(windows.HWND(hwnd), swHide)
	return true
}
