package server

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every script in the UI shares one global scope.
//
// Two files declaring the same top-level function is not an error in a browser:
// the one that loads last silently replaces the other, and the caller keeps
// calling what it thinks is its own. A helper in attach.js was quietly replaced
// by an identically named one in panels.js this way, and nothing anywhere said
// so. This finds the next one.
func TestNoDuplicateTopLevelJSNames(t *testing.T) {
	// Only column-zero declarations: anything indented is inside a scope.
	decl := regexp.MustCompile(`(?m)^(?:function|const|let|var)\s+([A-Za-z_$][\w$]*)`)

	seen := map[string][]string{}
	err := fs.WalkDir(webFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".js") {
			return err
		}
		// Vendored libraries are not ours and are loaded deliberately.
		if strings.Contains(path, "vendor/") {
			return nil
		}
		b, err := fs.ReadFile(webFS, path)
		if err != nil {
			return err
		}
		for _, m := range decl.FindAllStringSubmatch(string(b), -1) {
			seen[m[1]] = append(seen[m[1]], path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var clashes []string
	for name, files := range seen {
		if len(files) > 1 {
			clashes = append(clashes, fmt.Sprintf("%s declared in %s", name, strings.Join(files, ", ")))
		}
	}
	sort.Strings(clashes)
	for _, c := range clashes {
		t.Errorf("duplicate top-level name: %s", c)
	}
}

// The scripts the page loads must all exist, and every one we ship must be
// loaded. A file that is never included is dead weight that still looks live.
func TestEveryScriptIsLoaded(t *testing.T) {
	idx, err := fs.ReadFile(webFS, "web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	tag := regexp.MustCompile(`<script src="([^"]+)"`)
	loaded := map[string]bool{}
	for _, m := range tag.FindAllStringSubmatch(string(idx), -1) {
		loaded[m[1]] = true
		if _, err := fs.ReadFile(webFS, "web/"+m[1]); err != nil {
			t.Errorf("index.html loads %q, which is not shipped: %v", m[1], err)
		}
	}

	_ = fs.WalkDir(webFS, "web", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".js") {
			return err
		}
		rel := strings.TrimPrefix(path, "web/")
		if strings.HasPrefix(rel, "vendor/") {
			return nil
		}
		if !loaded[rel] {
			t.Errorf("%s is shipped but never loaded by index.html", rel)
		}
		return nil
	})
}
