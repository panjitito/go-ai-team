package server

import (
	"path"
	"strings"
)

// Path handling that does not change meaning with the server's operating
// system.
//
// `path/filepath` is separator-aware, and on Linux the separator is `/` alone.
// That is correct for a path the process built itself and wrong for one that
// arrived over HTTP: `filepath.Base` reading `..\..\etc\passwd` on Linux
// returns the whole string, because it sees one element with backslashes in the
// name of it. Three checks in this package were written on Windows, where
// filepath understands both, and quietly stopped meaning what they say when
// the same code ran on Linux. The CI matrix found all three on its first run.
//
// Nothing here is a substitute for resolving a path and proving where it
// landed. It is the cheap textual pass in front of that, and its whole job is
// to give the same answer everywhere.

// anySeparator is a path separator on some platform somebody's browser might be
// running on, which is the only question these functions get asked.
func anySeparator(r rune) bool { return r == '/' || r == '\\' }

// baseName is the last element of a path, splitting on either separator.
//
// filepath.Base is right for a path this process owns and wrong for a filename
// a browser sent, which may carry the separators of a different machine.
func baseName(p string) string {
	if i := strings.LastIndexFunc(p, anySeparator); i >= 0 {
		return p[i+1:]
	}
	return p
}

// toSlash normalises backslashes to forward slashes on every platform.
//
// filepath.ToSlash only does this on Windows. A path from a request is checked
// before it is joined onto anything, and that check has to read `..\secret` as
// a traversal on Linux too, rather than as a peculiar filename that happens to
// contain the word secret.
func toSlash(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// pathKey is a directory's identity for comparison: one separator, no trailing
// one, folded to lower case.
//
// Used where one side of the comparison did not come from this process. A
// conversation's working directory is read out of a transcript and is spelled
// the way the machine that wrote it spells paths, while the project it should
// match was typed in here. filepath.Clean answers that question differently on
// Windows and on Linux, so `C:\Projects\api\` matched a stored project on one
// and not on the other. path.Clean always uses `/` and gives the same answer
// on both.
//
// Lower case because this is mostly Windows, where the same directory arrives
// spelled several ways. Two projects whose paths differ only in case are the
// same project on Windows and, on Linux, a collision nobody has ever hit.
func pathKey(p string) string {
	p = strings.TrimSpace(toSlash(p))
	if p == "" {
		return ""
	}
	p = path.Clean(p)
	if p == "." {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(p, "/"))
}
