package browser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestParseMode(t *testing.T) {
	cases := map[string]Mode{
		// The default is a native window now, not a borrowed Chrome.
		"":        ModeDesktop,
		"desktop": ModeDesktop,
		"native":  ModeDesktop,
		"app":     ModeApp,
		"APP":     ModeApp,
		" app ":   ModeApp,
		"tab":     ModeTab,
		"system":  ModeSystem,
		"default": ModeSystem,
		"none":    ModeNone,
		"off":     ModeNone,
		"false":   ModeNone,
	}
	for in, want := range cases {
		got, err := ParseMode(in)
		if err != nil {
			t.Errorf("ParseMode(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseMode(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := ParseMode("nonsense"); err == nil {
		t.Error("an unknown mode should be an error, not a silent default")
	}
}

func TestPrepareProfileSeedsPreferences(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "browser")
	if err := prepareProfile(dir); err != nil {
		t.Fatal(err)
	}

	prefs := readPrefs(t, dir)
	// exit_type is the one that earns its keep: without it Chrome shows the
	// "didn't shut down correctly / Restore pages?" bubble every time the app
	// window is closed by quitting the server rather than by the window button.
	if got := nested(prefs, "profile", "exit_type"); got != "Normal" {
		t.Errorf("profile.exit_type = %v, want Normal", got)
	}
	if got := nested(prefs, "profile", "name"); got != "Go AI Team" {
		t.Errorf("profile.name = %v", got)
	}
	if got := nested(prefs, "bookmark_bar", "show_on_all_tabs"); got != false {
		t.Errorf("the bookmark bar should be hidden in an app window, got %v", got)
	}
	if got := nested(prefs, "browser", "check_default_browser"); got != false {
		t.Errorf("check_default_browser = %v, want false", got)
	}

	// The README matters: an unexplained 100 MB Chrome profile inside a dotfolder
	// is exactly the sort of thing that gets deleted in confusion later.
	if _, err := os.Stat(filepath.Join(dir, "README.txt")); err != nil {
		t.Error("the profile directory has no README explaining what it is")
	}
}

// Running it twice must be safe, and must not throw away settings the user
// changed inside the profile.
func TestPrepareProfileIsIdempotentAndPreservesOtherKeys(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "browser")
	if err := prepareProfile(dir); err != nil {
		t.Fatal(err)
	}

	// Simulate Chrome writing its own state, including flipping exit_type the
	// way it does while running.
	prefs := readPrefs(t, dir)
	setPath(prefs, []string{"profile", "exit_type"}, "Crashed")
	setPath(prefs, []string{"extensions", "theme", "id"}, "some-theme")
	setPath(prefs, []string{"partition", "per_host_zoom_levels"}, map[string]any{"localhost": 1.2})
	writePrefs(t, dir, prefs)

	if err := prepareProfile(dir); err != nil {
		t.Fatal(err)
	}
	after := readPrefs(t, dir)

	if got := nested(after, "profile", "exit_type"); got != "Normal" {
		t.Errorf("exit_type was not reset to Normal, got %v", got)
	}
	if got := nested(after, "extensions", "theme", "id"); got != "some-theme" {
		t.Errorf("an unrelated preference was lost: theme.id = %v", got)
	}
	if got := nested(after, "partition", "per_host_zoom_levels", "localhost"); got != 1.2 {
		t.Errorf("a zoom level set inside the profile was lost: %v", got)
	}
}

func TestPrepareProfileRejectsEmptyDir(t *testing.T) {
	if err := prepareProfile(""); err == nil {
		t.Error("an empty profile directory should be refused")
	}
}

func TestSetPathCreatesNesting(t *testing.T) {
	m := map[string]any{}
	setPath(m, []string{"a", "b", "c"}, 42)
	if got := nested(m, "a", "b", "c"); got != 42 {
		t.Errorf("setPath produced %v", m)
	}
	// An existing branch must be extended, not replaced.
	setPath(m, []string{"a", "b", "d"}, "x")
	if got := nested(m, "a", "b", "c"); got != 42 {
		t.Errorf("setPath clobbered a sibling key: %v", m)
	}
}

// ---------- helpers ----------

func readPrefs(t *testing.T, dir string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "Default", "Preferences"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Preferences is not valid JSON: %v", err)
	}
	return m
}

func writePrefs(t *testing.T, dir string, m map[string]any) {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Default", "Preferences"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func nested(m map[string]any, path ...string) any {
	var cur any = m
	for _, k := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[k]
	}
	return cur
}
