//go:build windows

package desktop

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// redirectForTest points output at path and puts it back afterwards.
//
// Closing the file is the part that matters on Windows: redirectOutput leaves it
// open on purpose — the app writes to it for as long as it runs — and a temp
// directory cannot be removed while anything still holds a handle to a file in
// it, so a test that forgets fails in its own cleanup.
func redirectForTest(t *testing.T, path string) {
	t.Helper()
	outWas, errWas, logWas := os.Stdout, os.Stderr, log.Writer()
	redirectOutput(path)
	t.Cleanup(func() {
		if f := os.Stdout; f != outWas {
			_ = f.Sync()
			_ = f.Close()
		}
		os.Stdout, os.Stderr = outWas, errWas
		log.SetOutput(logWas)
	})
}

// Output has to have somewhere to go before the console is taken away.
//
// FreeConsole invalidates the standard handles, so anything written afterwards
// is lost — and "could not open a browser" is exactly the message somebody needs
// when the app started and they cannot see it. This is the half of the change
// that can be tested without a console to release.
func TestRedirectOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log", "app.log")
	redirectForTest(t, path)

	fmt.Println("a line from fmt")
	log.Println("a line from log")
	fmt.Fprintln(os.Stderr, "a line from stderr")
	_ = os.Stdout.Sync()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no log file was made: %v", err)
	}
	got := string(b)
	for _, want := range []string{
		"console released", // says why the window went, for whoever finds this
		"a line from fmt",
		"a line from log",
		"a line from stderr",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the log is missing %q:\n%s", want, got)
		}
	}
	// Read with `type` and Notepad as often as with anything else, and Windows
	// PowerShell treats a file as the ANSI codepage unless told otherwise.
	for _, r := range got {
		if r > 127 {
			t.Errorf("non-ASCII %q in the log; it would arrive as mojibake", r)
			break
		}
	}
}

// A log that grows without bound is a second problem, not a solution to the
// first one. A run that finds it oversized starts it again.
func TestRedirectOutputCapsTheLog(t *testing.T) {
	dir := t.TempDir()

	big := filepath.Join(dir, "big.log")
	if err := os.WriteFile(big, make([]byte, logKeep+1), 0o644); err != nil {
		t.Fatal(err)
	}
	redirectForTest(t, big)
	_ = os.Stdout.Sync()
	if fi, err := os.Stat(big); err != nil {
		t.Fatal(err)
	} else if fi.Size() > 4096 {
		t.Errorf("an oversized log was appended to rather than restarted (%d bytes)", fi.Size())
	}

	// A short one is kept, because the run before this one is often the
	// interesting one.
	small := filepath.Join(dir, "small.log")
	if err := os.WriteFile(small, []byte("from the run before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	redirectForTest(t, small)
	_ = os.Stdout.Sync()
	b, _ := os.ReadFile(small)
	if !strings.Contains(string(b), "from the run before") {
		t.Error("the previous run's log was thrown away")
	}
}

// Nothing to write to is survivable; refusing to start over it is not.
func TestRedirectOutputSurvivesABadPath(t *testing.T) {
	was := os.Stdout

	redirectOutput("")
	if os.Stdout != was {
		t.Error("an empty path still swapped stdout for something")
	}

	// A file where a directory has to be.
	blocked := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	redirectOutput(filepath.Join(blocked, "app.log"))
	if os.Stdout != was {
		t.Error("a path that cannot be created still swapped stdout")
	}
}
