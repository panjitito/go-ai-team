// Package desktop opens the UI in a real application window.
//
// The product bet was that a browser beats Electron, and the browser part of
// that is still right: the UI is a web page, so it also opens from a phone and
// costs nothing to ship. What was wrong was needing *Chrome* to see it — the app
// borrowed somebody else's browser, which meant a Chrome dependency, a profile
// directory to manage, and a window that was still recognisably a browser
// pretending not to be one.
//
// Windows already has an embedded web view: WebView2, part of Edge and present
// on every Windows 11 machine. Using it directly gives a real window with its
// own taskbar button and icon, showing the same page and nothing around it. The
// binding is pure Go, so the single no-cgo binary and its size are unchanged.
//
// The HTTP server keeps running exactly as before. The window is a client of it,
// not a replacement for it, which is what keeps the phone working.
package desktop

import "errors"

// ErrUnsupported means this build or this machine cannot open a native window,
// and the caller should fall back to a browser.
var ErrUnsupported = errors.New("no native window available on this system")

// Bounds is a window's position and size, remembered between runs so the app
// opens where it was left rather than jumping to the middle of the screen.
type Bounds struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	W         int  `json:"w"`
	H         int  `json:"h"`
	Maximized bool `json:"maximized"`
}

// Valid reports whether bounds are worth restoring. Zero values mean "never
// saved", and a window smaller than this is one nobody chose.
func (b Bounds) Valid() bool { return b.W >= 480 && b.H >= 360 }

// Opts describes the window to open.
type Opts struct {
	// URL is the local server to show.
	URL string
	// Title is the window and taskbar caption.
	Title string
	// DataDir is where the web view keeps its own profile. Kept inside the
	// app's state directory so it never touches a browsing profile.
	DataDir string
	// Start is the geometry to open with. Ignored unless Valid.
	Start Bounds
	// Ready is called once the window exists, with a function that closes it.
	// This is how Ctrl-C in the terminal takes the window down with it instead
	// of leaving it on screen after the process has gone.
	Ready func(stop func())

	// OnClose is called once the window has closed, with the geometry it had.
	// This is the only chance to persist it: after Run returns the window is
	// already gone.
	OnClose func(Bounds)
}
