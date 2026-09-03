package guard

import (
	"fmt"
	"os"
)

// Kill ends one process. It is only ever called from an explicit user action:
// the sweep flags, it never terminates on its own, because a false positive
// that kills a real build would cost more than the memory it freed.
func Kill(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid")
	}
	if pid == os.Getpid() {
		return fmt.Errorf("refusing to end Go AI Team itself")
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("process %d not found: %w", pid, err)
	}
	return p.Kill()
}
