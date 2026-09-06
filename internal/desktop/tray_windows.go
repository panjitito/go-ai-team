//go:build windows

package desktop

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

/* Closing the window should not stop the work.

   This app exists to leave agents running while you go and do something else,
   and the X in the corner ended every one of them. That is right for a document
   window and wrong here: the window is a view onto a server that is perfectly
   happy without it.

   So closing hides to the notification area and the agents carry on. The icon
   is the way back, and its menu holds the Quit that the X used to be.

   Two things follow from hiding, and both are handled here rather than left as
   surprises.

   A hidden window has no taskbar button, so Attention's flash lands nowhere.
   While the app is in the tray, an agent's question raises a balloon from the
   icon instead — the native equivalent, and it works whether or not the browser
   notifications in Settings were ever switched on.

   And somebody who closes a window expects it to be closed. The first time it
   hides, the icon says what happened. Once, not every time: a program that
   explains itself repeatedly is a program nobody reads.
*/

var (
	shell32               = windows.NewLazySystemDLL("shell32.dll")
	procShellNotifyIcon   = shell32.NewProc("Shell_NotifyIconW")
	procSetWindowLongPtr  = user32.NewProc("SetWindowLongPtrW")
	procCallWindowProc    = user32.NewProc("CallWindowProcW")
	procCreatePopupMenu   = user32.NewProc("CreatePopupMenu")
	procAppendMenuW       = user32.NewProc("AppendMenuW")
	procTrackPopupMenu    = user32.NewProc("TrackPopupMenu")
	procDestroyMenu       = user32.NewProc("DestroyMenu")
	procPostMessageW      = user32.NewProc("PostMessageW")
	procIsWindowVisible   = user32.NewProc("IsWindowVisible")
	procGetCursorPos      = user32.NewProc("GetCursorPos")
	procRegisterWindowMsg = user32.NewProc("RegisterWindowMessageW")
)

const (
	wmDestroy = 0x0002
	wmClose   = 0x0010
	wmNull    = 0x0000

	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205

	// The messages this file adds. WM_APP begins the range reserved for an
	// application's own use, so nothing else can collide with them.
	wmApp      = 0x8000
	wmTrayIcon = wmApp + 1
	wmTrayTip  = wmApp + 2

	// GWLP_WNDPROC is -4, and Go will not convert a negative constant to
	// uintptr, so it is written as the value that becomes on the wire.
	gwlpWndProc = ^uintptr(3) // -4

	nimAdd    = 0x0
	nimModify = 0x1
	nimDelete = 0x2

	nifMessage = 0x01
	nifIcon    = 0x02
	nifTip     = 0x04
	nifInfo    = 0x10

	niifInfo = 0x1

	mfString    = 0x0000
	mfSeparator = 0x0800

	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100

	swHide    = 0
	swRestore = 9

	menuOpen = 1
	menuQuit = 2

	// balloonEvery rate-limits the notifications. Several agents finishing
	// together would otherwise be several toasts, and a pile of toasts is how
	// people learn to turn an application off.
	balloonEvery = 30 * time.Second
)

// notifyIconData mirrors Win32 NOTIFYICONDATAW.
//
// Go's field alignment on amd64 matches C's here, so there is no padding
// written by hand — but cbSize has to be exactly right or the shell rejects the
// call without saying why, and a test pins the size at the 976 bytes the header
// gives.
type notifyIconData struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         [16]byte
	hBalloonIcon     uintptr
}

type point struct{ X, Y int32 }

// The tray's state, touched only on the window's own thread. Everything that
// reads it does so from the window procedure, and everything outside gets there
// by posting a message.
var tray struct {
	hwnd    uintptr
	icon    uintptr
	oldProc uintptr
	live    bool
	hidden  bool
	quit    func()
	// restoreTo is the show command the window had before it was hidden, so a
	// maximized window comes back maximized.
	restoreTo uintptr
	explained bool
	waiting   int
	lastNote  time.Time
}

// taskbarCreated is broadcast when Explorer restarts. Without listening for it,
// an Explorer crash takes the icon away for good and the app becomes both
// unreachable and unquittable.
var taskbarCreated uint32

// installTray adds the notification icon and puts this file's handler in front
// of the web view's.
//
// It reports whether the icon is actually there. The caller must not change
// what closing the window does unless it is — an app that swallows WM_CLOSE
// with no icon to click is an app that cannot be closed at all.
func installTray(hwnd windows.HWND, quit func()) bool {
	tray.hwnd = uintptr(hwnd)
	tray.quit = quit
	tray.icon = loadTrayIcon()

	if m, err := windows.UTF16PtrFromString("TaskbarCreated"); err == nil {
		v, _, _ := procRegisterWindowMsg.Call(uintptr(unsafe.Pointer(m)))
		taskbarCreated = uint32(v)
	}

	// Subclass first: if adding the icon then fails, the window procedure is
	// still ours and simply passes everything through.
	old, _, _ := procSetWindowLongPtr.Call(tray.hwnd, gwlpWndProc,
		windows.NewCallback(trayWndProc))
	tray.oldProc = old

	if !addTrayIcon(trayTipText()) {
		return false
	}
	tray.live = true
	return true
}

func removeTray() {
	if !tray.live {
		return
	}
	nid := notifyIconData{hWnd: tray.hwnd, uID: 1}
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	tray.live = false
}

func addTrayIcon(tip string) bool {
	nid := notifyIconData{
		hWnd:             tray.hwnd,
		uID:              1,
		uFlags:           nifMessage | nifIcon | nifTip,
		uCallbackMessage: wmTrayIcon,
		hIcon:            tray.icon,
	}
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	copyUTF16(nid.szTip[:], tip)
	r, _, _ := procShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	return r != 0
}

// loadTrayIcon takes the app's own icon at the size the notification area
// wants. A missing icon is not fatal: Windows draws a blank square, which is
// still clickable and still the way back.
func loadTrayIcon() uintptr {
	path, ok := iconPath()
	if !ok {
		return 0
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0
	}
	h, _, _ := procLoadImageW.Call(0, uintptr(unsafe.Pointer(p)), imageIcon,
		sysMetric(smCXSmIcon), sysMetric(smCYSmIcon), imgFromFile)
	return h
}

// trayWndProc handles the messages this file is about and passes everything
// else straight through, because the web view depends on nearly all of them.
func trayWndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch {
	case msg == wmClose && tray.live:
		hideToTray()
		return 0

	case msg == wmTrayIcon:
		switch lparam {
		case wmLButtonUp, wmLButtonDblClk:
			showFromTray()
		case wmRButtonUp:
			trayMenu()
		}
		return 0

	case msg == wmTrayTip:
		trayWaiting(int(wparam))
		return 0

	case taskbarCreated != 0 && msg == uintptr(taskbarCreated):
		// Explorer came back, and took every icon with it when it went.
		if tray.live {
			addTrayIcon(trayTipText())
		}

	case msg == wmDestroy:
		removeTray()
	}
	return callOld(hwnd, msg, wparam, lparam)
}

func callOld(hwnd, msg, wparam, lparam uintptr) uintptr {
	if tray.oldProc == 0 {
		return 0
	}
	r, _, _ := procCallWindowProc.Call(tray.oldProc, hwnd, msg, wparam, lparam)
	return r
}

func hideToTray() {
	var wp windowPlacement
	wp.length = uint32(unsafe.Sizeof(wp))
	if r, _, _ := procGetWindowPlace.Call(tray.hwnd, uintptr(unsafe.Pointer(&wp))); r != 0 {
		tray.restoreTo = uintptr(wp.showCmd)
	}
	procShowWindow.Call(tray.hwnd, swHide)
	tray.hidden = true

	if !tray.explained {
		tray.explained = true
		// Where the icon actually is, not where you would expect it.
		//
		// Windows 11 puts a new notification icon behind the overflow chevron by
		// default and there is deliberately no way to ask for it to be promoted.
		// So the one moment somebody is looking for the app is the moment to say
		// where it went, and how to stop having to look.
		balloon("Still running, and so are the agents. The icon is under the ^ " +
			"in the taskbar — drag it out to keep it visible. Click it to come " +
			"back, or right-click for Quit.")
	}
}

func showFromTray() {
	cmd := tray.restoreTo
	if cmd != swShowMaximized {
		cmd = swRestore // covers hidden, normal and minimized alike
	}
	procShowWindow.Call(tray.hwnd, cmd)
	procSetForegroundWindow.Call(tray.hwnd)
	tray.hidden = false
	StopAttention()
}

// trayWaiting records how many agents want a person and says so on the icon.
//
// The balloon is on the rising edge only, and only while the window is hidden:
// with the window on screen the card, the caption and the taskbar flash have
// all already said it.
func trayWaiting(n int) {
	was := tray.waiting
	tray.waiting = n
	if !tray.live {
		return
	}
	setTrayTip(trayTipText())
	if tray.hidden && n > was {
		balloon(waitingLine(n))
	}
}

func trayTipText() string {
	if tray.waiting > 0 {
		return "Go AI Team — " + waitingLine(tray.waiting)
	}
	return "Go AI Team"
}

func waitingLine(n int) string {
	if n == 1 {
		return "an agent is waiting for an answer"
	}
	return itoa(n) + " agents are waiting for an answer"
}

// itoa, rather than strconv, keeps this file to the Win32 imports. n is a count
// of running agents; it is never negative and never large.
func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 && i > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// trayMenu shows the icon's menu.
//
// SetForegroundWindow before and a posted WM_NULL after are both required:
// without them the menu will not go away when you click somewhere else, which
// is a documented quirk of TrackPopupMenu older than most of this codebase.
func trayMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	appendItem(menu, mfString, menuOpen, "Open Go AI Team")
	appendItem(menu, mfSeparator, 0, "")
	appendItem(menu, mfString, menuQuit, "Quit — stops every agent")

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(tray.hwnd)

	cmd, _, _ := procTrackPopupMenu.Call(menu, tpmRightButton|tpmReturnCmd,
		uintptr(pt.X), uintptr(pt.Y), 0, tray.hwnd, 0)
	procPostMessageW.Call(tray.hwnd, wmNull, 0, 0)

	trayCommand(cmd)
}

// trayCommand acts on a choice from the menu. Separate from putting the menu on
// screen so the deciding half can be tested without a mouse — TrackPopupMenu is
// Windows' code and does not need testing; "Quit actually quits" does.
func trayCommand(cmd uintptr) {
	switch cmd {
	case menuOpen:
		showFromTray()
	case menuQuit:
		// The window comes back before it goes. Its geometry is sampled while it
		// is visible, and quitting straight from the tray would otherwise save
		// the position of something nobody could see.
		showFromTray()
		removeTray()
		if tray.quit != nil {
			tray.quit()
		}
	}
}

func appendItem(menu, flags, id uintptr, text string) {
	var p *uint16
	if text != "" {
		var err error
		if p, err = windows.UTF16PtrFromString(text); err != nil {
			return
		}
	}
	procAppendMenuW.Call(menu, flags, id, uintptr(unsafe.Pointer(p)))
}

func balloon(text string) {
	if !tray.live || time.Since(tray.lastNote) < balloonEvery {
		return
	}
	tray.lastNote = time.Now()

	nid := notifyIconData{
		hWnd:        tray.hwnd,
		uID:         1,
		uFlags:      nifInfo,
		dwInfoFlags: niifInfo,
	}
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	copyUTF16(nid.szInfoTitle[:], "Go AI Team")
	copyUTF16(nid.szInfo[:], text)
	procShellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func setTrayTip(text string) {
	nid := notifyIconData{hWnd: tray.hwnd, uID: 1, uFlags: nifTip}
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	copyUTF16(nid.szTip[:], text)
	procShellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

// copyUTF16 writes a string into a fixed Win32 buffer, always leaving the
// terminating zero the API expects.
func copyUTF16(dst []uint16, s string) {
	src, err := windows.UTF16FromString(s)
	if err != nil {
		return
	}
	if len(src) > len(dst) {
		src = src[:len(dst)]
		src[len(src)-1] = 0
	}
	copy(dst, src)
}

// SetWaiting tells the notification area how many agents want a person.
//
// Called from the goroutine watching sessions, so it does not touch the tray
// directly: the work is posted to the window's own thread, which is the only
// one allowed to speak for it.
func SetWaiting(n int) {
	liveMu.Lock()
	h := liveHWND
	liveMu.Unlock()
	if h == 0 {
		return
	}
	procPostMessageW.Call(h, wmTrayTip, uintptr(n), 0)
}

// windowIsVisible reports whether the window is on screen, which is not the
// same as existing: the geometry sampler must not record the placement of a
// window that is sitting in the tray.
func windowIsVisible(hwnd windows.HWND) bool {
	v, _, _ := procIsWindowVisible.Call(uintptr(hwnd))
	return v != 0
}
