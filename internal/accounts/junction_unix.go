//go:build !windows

package accounts

import (
	"fmt"
	"os"
)

// junction only exists on Windows; elsewhere os.Symlink already succeeded or
// genuinely failed, so there is nothing to fall back to.
func junction(from, to string) error {
	return fmt.Errorf("symlink %s -> %s failed and junctions are Windows-only", to, from)
}

// isWindowsDirLink is always false off Windows; os.Lstat reports symlinks
// correctly here.
func isWindowsDirLink(path string, fi os.FileInfo) bool { return false }
