package gitx

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Two spellings of one directory have to compare equal.
//
// This is not hypothetical tidiness. TEMP on a GitHub Actions Windows runner is
// C:\Users\RUNNER~1\AppData\Local\Temp, so every path a test builds there is
// the 8.3 alias while git prints the full name. AddWorktree compared the two as
// strings, decided a worktree it had already created did not exist, and asked
// git to create it again — which fails. The diff pane made the same mistake and
// reported files inside a repository as outside it. Both were invisible on the
// machine this was written on, where the user name is short enough to have no
// alias at all.
func TestSamePathAcrossSpellings(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	same := [][2]string{
		{sub, sub},
		{sub, filepath.Join(dir, "a", "b", ".")},
		{sub, filepath.Join(dir, "a", "x", "..", "b")},
		{sub + string(filepath.Separator), sub},
	}
	for _, pair := range same {
		if !SamePath(pair[0], pair[1]) {
			t.Errorf("SamePath(%q, %q) = false, and they are the same directory", pair[0], pair[1])
		}
	}

	if SamePath(sub, filepath.Join(dir, "a")) {
		t.Error("a directory and its parent compared equal")
	}
	if SamePath("", sub) {
		t.Error("an empty path matched a real one")
	}

	// The 8.3 case itself, on the only platform that has one. Program Files is
	// the alias every Windows install has had for thirty years, so this needs no
	// fixture and no privileges.
	if runtime.GOOS == "windows" {
		long := `C:\Program Files`
		short := `C:\PROGRA~1`
		if _, err := os.Stat(long); err != nil {
			t.Skip("no C:\\Program Files on this machine")
		}
		if _, err := os.Stat(short); err != nil {
			t.Skip("8.3 aliases are switched off on this volume")
		}
		if !SamePath(short, long) {
			t.Errorf("SamePath(%q, %q) = false; the short name is the same directory",
				short, long)
		}
		if got := RealPath(short); got != long {
			t.Errorf("RealPath(%q) = %q, want %q", short, got, long)
		}
		// And case, which Windows does not distinguish.
		if !SamePath(`c:\program files`, long) {
			t.Error("case made two spellings of one directory differ")
		}
	}
}

// A path that does not exist yet still has to come back cleaned rather than
// empty: callers compare it against something.
func TestRealPathOfSomethingNotThere(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no", "such", "place")
	if got := RealPath(missing); got != filepath.Clean(missing) {
		t.Errorf("RealPath(%q) = %q, want it cleaned", missing, got)
	}
	if got := RealPath(""); got != "" {
		t.Errorf("RealPath(\"\") = %q, want empty", got)
	}
}
