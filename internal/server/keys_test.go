package server

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A written-down list of shortcuts goes stale the first time somebody adds one.
//
// So this reads the handlers instead: every document-level binding of a
// modifier plus a letter, taken out of the JavaScript, has to appear in
// keys.js. It cannot check that the descriptions are true — nothing can — but
// it can stop the list quietly becoming a lie about which keys exist.
func TestKeyboardSheetListsEveryShortcut(t *testing.T) {
	sheet, err := os.ReadFile("web/keys.js")
	if err != nil {
		t.Fatal(err)
	}

	files, err := os.ReadDir("web")
	if err != nil {
		t.Fatal(err)
	}

	// The shape every one of these bindings has:
	//   (e.ctrlKey || e.metaKey) … e.key.toLowerCase() === 'b'
	//   e.key.toLowerCase() !== 'f'  (the early return form)
	ctrl := regexp.MustCompile(`e\.key\.toLowerCase\(\)\s*[!=]==\s*'([a-z])'`)

	found := map[string]string{}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".js") || f.Name() == "keys.js" {
			continue
		}
		src, err := os.ReadFile("web/" + f.Name())
		if err != nil {
			t.Fatal(err)
		}
		text := string(src)
		// Only the bindings that are on the document: a key handled inside one
		// widget belongs to that widget, not to a global list.
		if !strings.Contains(text, "document.addEventListener('keydown'") {
			continue
		}
		for _, m := range ctrl.FindAllStringSubmatch(text, -1) {
			found[strings.ToUpper(m[1])] = f.Name()
		}
	}

	if len(found) == 0 {
		t.Fatal("found no shortcuts at all; this test has stopped reading the handlers")
	}

	var missing []string
	for key, file := range found {
		// The sheet writes them as "Ctrl K", "Ctrl Shift F".
		if !strings.Contains(string(sheet), " "+key+"'") && !strings.Contains(string(sheet), " "+key+" ") {
			missing = append(missing, key+" (bound in "+file+")")
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("bound but not on the keyboard sheet: %s\nadd them to KEYS in web/keys.js",
			strings.Join(missing, ", "))
	}
}
