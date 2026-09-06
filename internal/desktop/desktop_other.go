//go:build !windows

package desktop

// Native windows are Windows-only for now. Everywhere else the caller falls back
// to a browser, which is the same UI in a different frame — nothing is lost but
// the window decoration.
func Available() (bool, string) {
	return false, "a native window is only implemented on Windows; the UI opens in a browser here"
}

func Run(o Opts) error { return ErrUnsupported }

// ReleaseOwnConsole is a Windows problem: elsewhere a program launched from a
// file manager does not get a console window in the first place.
func ReleaseOwnConsole(logPath string) bool { return false }

// Attention and StopAttention flash the taskbar button on Windows. There is no
// portable equivalent, and the page's own title already says how many agents
// are waiting, so elsewhere these do nothing rather than approximate it badly.
func Attention()     {}
func StopAttention() {}
