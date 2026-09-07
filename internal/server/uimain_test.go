//go:build uitest

package server

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The shared server on 7788.
//
// A dozen tests in this package are written against a server on that port and
// skipped when there is none, because they were built to run against the app the
// developer already had open. That was fine as a working habit and useless as a
// check: on a clean machine, and on every CI runner, nine of them skipped and
// the suite still reported ok. Two of them cover the composer and the run bar,
// which are the parts a person touches most.
//
// So one server is started here for the whole package if nothing is listening
// yet, on a home directory of its own. A server that is already there is left
// alone and used as it is — that is the developer's own app, and the handful of
// tests needing a live agent or a signed-in account can only work against it.
func TestMain(m *testing.M) {
	stop := startSharedServer()
	code := m.Run()
	stop()
	os.Exit(code)
}

// sharedPort is the port those tests look for.
const sharedPort = 7788

// startSharedServer returns a function that shuts down whatever it started,
// which is nothing at all when a server was already listening.
func startSharedServer() func() {
	if serverIsUp(sharedPort, 500*time.Millisecond) {
		return func() {}
	}
	exe := findBinary()
	if exe == "" {
		// Nothing to start. The tests that need it say so themselves, and
		// UITEST_REQUIRED turns that into a failure.
		return func() {}
	}

	home, err := os.MkdirTemp("", "goaiteam-uitest-")
	if err != nil {
		return func() {}
	}
	cmd := exec.Command(exe, "--port", fmt.Sprint(sharedPort), "--browser", "none")
	cmd.Env = append(os.Environ(), "USERPROFILE="+home, "HOME="+home)
	if err := cmd.Start(); err != nil {
		os.RemoveAll(home)
		return func() {}
	}

	stop := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		os.RemoveAll(home)
	}
	if !serverIsUp(sharedPort, 25*time.Second) {
		stop()
		return func() {}
	}
	return stop
}

// findBinary prefers the dev build, for the reason startServer gives: Windows
// locks a running executable, so the dev build is the newer one whenever
// somebody has the app open.
func findBinary() string {
	for _, c := range []string{
		"../../go-ai-team-dev.exe", "../../go-ai-team-dev",
		"../../go-ai-team.exe", "../../go-ai-team",
	} {
		if _, err := os.Stat(c); err == nil {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	return ""
}

func serverIsUp(port int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	url := fmt.Sprintf("http://127.0.0.1:%d/api/settings", port)
	for {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}
