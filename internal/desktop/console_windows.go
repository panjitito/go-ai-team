//go:build windows

package desktop

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Getting rid of the console window Windows attaches to a console program.
//
// This is a console application on purpose: it has flags, an `mcp` subcommand
// and a startup banner, all of which need a terminal. But double-clicking it —
// or launching it from a shortcut — then puts a black box behind the app
// window, which no desktop application does.
//
// # Hiding the window does not work, and used to
//
// The obvious move is ShowWindow(GetConsoleWindow(), SW_HIDE), and that is what
// this did. On Windows 11 it silently stops working, because the console is no
// longer drawn by a conhost window belonging to this process: with Windows
// Terminal as the default host — "Let Windows decide", which is the shipped
// setting — the window on screen belongs to WindowsTerminal.exe and
// GetConsoleWindow returns a pseudo-console window that was never visible.
//
// Measured rather than reasoned about. Launching a console program from
// Explorer on this machine, hiding its console window and then listing every
// process with a visible top-level window:
//
//	ShowWindow(SW_HIDE) → GetConsoleWindow() reports visible=false
//	                    → WindowsTerminal window still on screen
//	FreeConsole()       → GetConsoleWindow() returns 0
//	                    → no new visible window at all
//
// So the console is given back rather than hidden. Detaching leaves the console
// session with no client, and the host — conhost or Windows Terminal — closes
// it, which is the only thing that actually removes the box.
//
// # What that costs
//
// The standard handles die with the console, so anything written afterwards
// goes nowhere. They are pointed at a log file first: an app with no console
// still needs somewhere to put "could not open a browser". Ctrl-C goes too,
// which is why this is only called where the window is the way out.
//
// What it does not cost is the pseudo-consoles this app exists to run. That
// looked like the obvious risk — every agent is a ConPTY created by this
// process — so it was measured: a pty opened, a command run in it and its
// output read back, before and after the call. Both work. A pseudoconsole is
// created for a child, not attached to the parent, and detaching from ours has
// nothing to do with it.

var (
	kernel32            = windows.NewLazySystemDLL("kernel32.dll")
	procGetConsoleWnd   = kernel32.NewProc("GetConsoleWindow")
	procGetConsoleProcs = kernel32.NewProc("GetConsoleProcessList")
	procFreeConsole     = kernel32.NewProc("FreeConsole")
)

// logKeep is how large the log may grow before a run starts it again. Small:
// this is for reading the last few minutes when something did not start, not a
// history.
const logKeep = 4 << 20

// ReleaseOwnConsole detaches from the console when this process is the only
// thing attached to it, after redirecting output to logPath. It reports whether
// it let one go.
//
// A process launched from Explorer gets a console of its own and nothing else
// is using it. A process launched from an existing terminal shares that
// terminal with the shell, and taking it away would take the user's own window
// with it — GetConsoleProcessList tells the two apart, and exactly one attached
// process means the console is ours alone.
func ReleaseOwnConsole(logPath string) bool {
	hwnd, _, _ := procGetConsoleWnd.Call()
	if hwnd == 0 {
		return false // no console at all: already a windowed process
	}
	var pids [8]uint32
	n, _, _ := procGetConsoleProcs.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if n != 1 {
		return false // a shell is using this console too; leave it alone
	}

	// Before the handles stop working, not after.
	redirectOutput(logPath)

	if r, _, err := procFreeConsole.Call(); r == 0 {
		log.Printf("could not release the console: %v", err)
		return false
	}
	return true
}

// redirectOutput points this process's output at a file. Failing is not fatal:
// losing the log is worse than a console box, but not by enough to refuse to
// start.
func redirectOutput(logPath string) {
	if logPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return
	}
	if fi, err := os.Stat(logPath); err == nil && fi.Size() > logKeep {
		_ = os.Remove(logPath)
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}

	// Plain ASCII: Windows PowerShell reads a file as the ANSI codepage unless
	// told otherwise, and a log somebody opens with `type` should not arrive as
	// mojibake.
	fmt.Fprintf(f, "\n---- %s ----\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(f, "console released; everything the app prints lands here from now on.\n")

	// Both halves matter. SetStdHandle is what a child process inherits; the
	// package variables are what this process's own fmt and log calls use, and
	// they were read from the old handles at startup.
	_ = windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, windows.Handle(f.Fd()))
	_ = windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(f.Fd()))
	os.Stdout = f
	os.Stderr = f
	log.SetOutput(f)
}
