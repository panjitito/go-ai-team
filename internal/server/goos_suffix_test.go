package server

import (
	"os"
	"strings"
	"testing"
)

// No source file here may be named after a GOOS or GOARCH.
//
// Go builds a file called anything_windows.go only on Windows, with no build
// tag and no warning. api_windows.go was the pop-out window handler, meant for
// every platform; the name alone took it out of the build everywhere else and
// the package stopped compiling on Linux and macOS. It passed every check on
// the machine it was written on, which is the whole difficulty.
//
// Anything genuinely platform-specific lives in internal/desktop and says so
// with a build tag as well as a name.
func TestNoAccidentalPlatformSuffixes(t *testing.T) {
	// The suffixes that would silently narrow a file, minus none: this package
	// has no business being platform-specific at all.
	narrowing := []string{
		"_windows", "_linux", "_darwin", "_freebsd", "_openbsd", "_netbsd",
		"_plan9", "_js", "_wasip1", "_android", "_ios", "_solaris", "_aix",
		"_amd64", "_386", "_arm", "_arm64", "_riscv64", "_ppc64", "_s390x",
		"_mips", "_mips64", "_loong64", "_wasm",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		base := strings.TrimSuffix(name, ".go")
		base = strings.TrimSuffix(base, "_test")
		for _, s := range narrowing {
			if strings.HasSuffix(base, s) {
				t.Errorf("%s is built only on %s, because of its name — "+
					"rename it unless that is genuinely what you meant",
					name, strings.TrimPrefix(s, "_"))
			}
		}
	}
}
