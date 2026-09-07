//go:build windows

package desktop

import (
	"fmt"
	"runtime"
	"sync"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

/* A second window, and a third.

   One window is the wrong shape for the way this app is used. Agents run for
   half an hour at a time and you want to watch one while working in another,
   which on a desk with two monitors means two windows — not two tabs in one.

   Each one gets its own OS thread, because a Win32 window belongs to the thread
   that created it and its message loop has to run there. Measured before it was
   built: two WebView2 windows in one process, on two threads, both open, both
   drawing, distinct handles. In one process rather than one process each,
   because WebView2's user data folder is documented as not shareable between
   applications and sharing it inside a process is the supported case — which
   also means a pop-out is already signed in and already has the session the
   main window has.

   They are deliberately plainer than the main window: no notification icon, no
   remembered geometry, no attention flashing. A pop-out is a view onto
   something, opened when wanted and closed when not, and the main window
   remains the one the app lives in.
*/

// popouts is every extra window currently open, by the URL it was opened on.
//
// Keyed by URL so asking twice for the same agent brings the window you already
// have to the front instead of stacking a second one behind it, which is what
// happens when a button does the obvious thing and nothing remembers.
var popouts struct {
	mu sync.Mutex
	by map[string]*popout
}

type popout struct {
	w    webview2.WebView
	hwnd windows.HWND
}

// OpenWindow opens an extra window on a URL, or raises the one already showing
// it. It returns once the window exists.
func OpenWindow(url, title, dataDir string) error {
	if ok, why := Available(); !ok {
		return fmt.Errorf("%w: %s", ErrUnsupported, why)
	}

	popouts.mu.Lock()
	if popouts.by == nil {
		popouts.by = map[string]*popout{}
	}
	if p, ok := popouts.by[url]; ok && p.hwnd != 0 {
		h := p.hwnd
		popouts.mu.Unlock()
		// Already open. Restoring first matters: a window that was minimised
		// cannot be brought forward, so without this the second click looks
		// like nothing happening.
		show(h, swRestore)
		procSetForegroundWindow.Call(uintptr(h))
		return nil
	}
	popouts.mu.Unlock()

	type made struct {
		p   *popout
		err error
	}
	ready := make(chan made, 1)

	go func() {
		// The window and its message loop, on one thread, for the life of the
		// window. Everything in Win32 requires this and nothing enforces it.
		runtime.LockOSThread()

		w := webview2.NewWithOptions(webview2.WebViewOptions{
			DataPath:  dataDir,
			AutoFocus: true,
			WindowOptions: webview2.WindowOptions{
				Title: title, Width: 1100, Height: 800, Center: true,
			},
		})
		if w == nil {
			ready <- made{err: fmt.Errorf("%w: the window could not be created", ErrUnsupported)}
			return
		}
		hwnd := windows.HWND(uintptr(w.Window()))
		setAppIcon(hwnd)

		p := &popout{w: w, hwnd: hwnd}
		popouts.mu.Lock()
		popouts.by[url] = p
		popouts.mu.Unlock()

		// The same second ShowWindow as the main window, for the same reason:
		// the first call in a process is overridden by the launcher, and a
		// pop-out that never appears reads as a button that does nothing.
		show(hwnd, swShowNormal)
		procSetForegroundWindow.Call(uintptr(hwnd))

		w.Navigate(url)
		ready <- made{p: p}

		w.Run() // until the window is closed, from its X or from CloseWindows
		w.Destroy()

		popouts.mu.Lock()
		if popouts.by[url] == p {
			delete(popouts.by, url)
		}
		popouts.mu.Unlock()
	}()

	m := <-ready
	return m.err
}

// CloseWindows shuts every pop-out. Called when the app is quitting: a window
// left behind would be a view onto a server that has stopped answering, which
// looks like the app hanging rather than the app having gone.
func CloseWindows() {
	popouts.mu.Lock()
	all := make([]*popout, 0, len(popouts.by))
	for _, p := range popouts.by {
		all = append(all, p)
	}
	popouts.mu.Unlock()

	for _, p := range all {
		// Through Dispatch, never Terminate directly: Terminate posts the quit
		// to whichever thread calls it, and this is called from the one that is
		// shutting the app down rather than from the one holding the window.
		w := p.w
		w.Dispatch(w.Terminate)
	}
}

// OpenWindowCount is how many are open, for the tests and for anything that
// wants to know whether closing the last one is closing anything.
func OpenWindowCount() int {
	popouts.mu.Lock()
	defer popouts.mu.Unlock()
	return len(popouts.by)
}
