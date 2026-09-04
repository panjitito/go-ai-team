//go:build windows

package desktop

import (
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/webviewloader"
	"golang.org/x/sys/windows"
)

// Available reports whether a native window can be opened, and what is missing
// when it cannot. The check runs before anything is shown, so the caller can
// fall back to a browser without a window flashing on screen first.
func Available() (bool, string) {
	v, err := webviewloader.GetInstalledVersion()
	if err != nil || v == "" {
		return false, "the WebView2 runtime is not installed. It ships with Windows 11 and with Microsoft Edge; " +
			"install it from https://go.microsoft.com/fwlink/p/?LinkId=2124703 or run with --browser app to use Chrome instead"
	}
	return true, v
}

// Run opens the window and blocks until it is closed.
//
// It must be called from the goroutine locked to the process's first thread:
// this creates a Win32 window and pumps its message loop, and Windows requires
// both to happen on the same thread.
func Run(o Opts) error {
	runtime.LockOSThread()

	if ok, why := Available(); !ok {
		return fmt.Errorf("%w: %s", ErrUnsupported, why)
	}

	width, height := 1280, 860
	if o.Start.Valid() {
		width, height = o.Start.W, o.Start.H
	}

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		DataPath:  o.DataDir,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title: o.Title,
			Width: uint(width), Height: uint(height),
			// Centre only when there is no remembered position to restore.
			Center: !o.Start.Valid(),
		},
	})
	if w == nil {
		return fmt.Errorf("%w: the WebView2 runtime is present but the window could not be created", ErrUnsupported)
	}
	defer w.Destroy()

	hwnd := windows.HWND(uintptr(w.Window()))
	setAppIcon(hwnd)

	// Show the window a second time, deliberately.
	//
	// Windows ignores the argument to the *first* ShowWindow call in a process
	// and uses whatever the launcher put in STARTUPINFO instead. The binding
	// makes exactly one such call, so a launcher that asked for minimized or
	// hidden — a shortcut set to "Minimized", a scheduler, a background shell —
	// gets an app whose window never appears. It is running, listening and
	// invisible, which looks exactly like a crash. The second call is the one
	// that is actually obeyed.
	if o.Start.Valid() {
		place(hwnd, o.Start)
	} else {
		show(hwnd, swShowNormal)
	}
	procSetForegroundWindow.Call(uintptr(hwnd))

	// The window's geometry has to be sampled while it still exists. There is no
	// close hook here, and by the time Run returns the window is destroyed and
	// its rectangle is gone, so the last known good value is what gets saved.
	last := o.Start
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(900 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if b, ok := bounds(hwnd); ok {
					last = b
				}
			}
		}
	}()

	if o.Ready != nil {
		o.Ready(w.Terminate)
	}

	w.Navigate(o.URL)
	w.Run()
	close(stop)

	if o.OnClose != nil {
		o.OnClose(last)
	}
	return nil
}

// ---------------------------------------------------------------- win32

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procGetWindowPlace      = user32.NewProc("GetWindowPlacement")
	procSetWindowPlace      = user32.NewProc("SetWindowPlacement")
	procSendMessageW        = user32.NewProc("SendMessageW")
	procLoadImageW          = user32.NewProc("LoadImageW")
	procGetSystemMetric     = user32.NewProc("GetSystemMetrics")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
)

// windowPlacement mirrors the Win32 WINDOWPLACEMENT structure.
//
// Placement rather than GetWindowRect on purpose: it reports the *restored*
// rectangle even while the window is maximized, so maximising and quitting does
// not lose the size the window had before, and a maximized window reopens
// maximized instead of filling the screen as a normal window.
type windowPlacement struct {
	length           uint32
	flags            uint32
	showCmd          uint32
	minPositionX     int32
	minPositionY     int32
	maxPositionX     int32
	maxPositionY     int32
	rcNormalPosition struct{ Left, Top, Right, Bottom int32 }
	rcDevice         struct{ Left, Top, Right, Bottom int32 }
}

const (
	swShowNormal    = 1
	swShowMaximized = 3

	wmSetIcon   = 0x0080
	iconSmall   = 0
	iconBig     = 1
	imageIcon   = 1
	lrLoadFromF = 0x00000010
	lrDefSize   = 0x00000040
	lrShared    = 0x00008000

	smCXIcon    = 11
	smCYIcon    = 12
	smCXSmIcon  = 49
	smCYSmIcon  = 50
	imgFromFile = lrLoadFromF | lrShared
)

func bounds(hwnd windows.HWND) (Bounds, bool) {
	var wp windowPlacement
	wp.length = uint32(unsafe.Sizeof(wp))
	r, _, _ := procGetWindowPlace.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&wp)))
	if r == 0 {
		return Bounds{}, false
	}
	n := wp.rcNormalPosition
	return Bounds{
		X: int(n.Left), Y: int(n.Top),
		W: int(n.Right - n.Left), H: int(n.Bottom - n.Top),
		Maximized: wp.showCmd == swShowMaximized,
	}, true
}

func place(hwnd windows.HWND, b Bounds) {
	var wp windowPlacement
	wp.length = uint32(unsafe.Sizeof(wp))
	if r, _, _ := procGetWindowPlace.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&wp))); r == 0 {
		return
	}
	wp.rcNormalPosition.Left = int32(b.X)
	wp.rcNormalPosition.Top = int32(b.Y)
	wp.rcNormalPosition.Right = int32(b.X + b.W)
	wp.rcNormalPosition.Bottom = int32(b.Y + b.H)
	wp.showCmd = swShowNormal
	if b.Maximized {
		wp.showCmd = swShowMaximized
	}
	procSetWindowPlace.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&wp)))
	// SetWindowPlacement is not subject to the first-ShowWindow rule, but a
	// window that arrived hidden needs telling to appear regardless.
	show(hwnd, uintptr(wp.showCmd))
}

// setAppIcon gives the window its own icon, which is what makes it a distinct
// application in the taskbar and Alt-Tab rather than an anonymous window.
//
// Silent when there is no icon file: an app that refuses to start because it
// could not find a picture of itself would be worse than one with a blank one.
func setAppIcon(hwnd windows.HWND) {
	path, ok := iconPath()
	if !ok {
		return
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return
	}
	for _, ic := range []struct {
		which  uintptr
		cx, cy uintptr
	}{
		{iconBig, sysMetric(smCXIcon), sysMetric(smCYIcon)},
		{iconSmall, sysMetric(smCXSmIcon), sysMetric(smCYSmIcon)},
	} {
		h, _, _ := procLoadImageW.Call(0, uintptr(unsafe.Pointer(p)), imageIcon, ic.cx, ic.cy, imgFromFile)
		if h != 0 {
			procSendMessageW.Call(uintptr(hwnd), wmSetIcon, ic.which, h)
		}
	}
}

func sysMetric(i uintptr) uintptr {
	v, _, _ := procGetSystemMetric.Call(i)
	return v
}

// show applies a show command to the window. See the note in Run: this is the
// call that Windows actually obeys.
func show(hwnd windows.HWND, cmd uintptr) {
	procShowWindow.Call(uintptr(hwnd), cmd)
}
