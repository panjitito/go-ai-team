//go:build windows

package accounts

import (
	"fmt"
	"os"
	"os/exec"
)

// junction creates an NTFS directory junction. Windows refuses symlink creation
// to unprivileged processes unless Developer Mode is on, but a junction needs no
// privilege at all, so this is the reliable way to share a config directory.
func junction(from, to string) error {
	// /J is a directory junction; unlike /D it does not require elevation.
	cmd := exec.Command("cmd", "/c", "mklink", "/J", to, from)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mklink /J failed: %v: %s", err, out)
	}
	return nil
}

// isWindowsDirLink reports whether a path is a reparse point (junction), which
// os.Lstat does not always surface as a symlink on Windows.
func isWindowsDirLink(path string, fi os.FileInfo) bool {
	return fi.Mode()&os.ModeIrregular != 0 || fi.Mode()&os.ModeSymlink != 0
}
