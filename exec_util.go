package main

import "os/exec"

// runDetached starts a helper process and does not wait for it. Used only to
// hand a URL to the system browser.
func runDetached(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	// Reap in the background so the child is not left a zombie.
	go func() { _ = cmd.Wait() }()
	return nil
}
