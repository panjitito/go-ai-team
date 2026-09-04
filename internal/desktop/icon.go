package desktop

import (
	"crypto/sha256"
	_ "embed"
	"os"
	"path/filepath"
	"sync"
)

// The application icon.
//
// It is embedded rather than shipped alongside, so the app stays one file, and
// written out on first use because the Win32 call that loads an icon at a
// specific size wants a path. The copy in the state directory is also what the
// desktop shortcut points at, so the launcher and the window agree on what the
// app looks like.

//go:embed icon.ico
var iconICO []byte

var (
	iconOnce sync.Once
	iconAt   string
	iconOK   bool
)

// IconBytes is the raw .ico, for anything that wants to serve or copy it.
func IconBytes() []byte { return iconICO }

// EnsureIcon writes the icon into dir and returns its path. Rewritten only when
// the contents differ, so a running window's file is not churned on every start.
func EnsureIcon(dir string) (string, bool) {
	iconOnce.Do(func() {
		if dir == "" || len(iconICO) == 0 {
			return
		}
		path := filepath.Join(dir, "icon.ico")
		if cur, err := os.ReadFile(path); err == nil && sameBytes(cur, iconICO) {
			iconAt, iconOK = path, true
			return
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return
		}
		if err := os.WriteFile(path, iconICO, 0o644); err != nil {
			return
		}
		iconAt, iconOK = path, true
	})
	return iconAt, iconOK
}

func sameBytes(a, b []byte) bool {
	return len(a) == len(b) && sha256.Sum256(a) == sha256.Sum256(b)
}

// iconPath returns the written icon, if EnsureIcon has produced one.
func iconPath() (string, bool) { return iconAt, iconOK }
