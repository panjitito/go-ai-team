package claudefs

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The line this writes is read back by a regex in internal/session, so the two
// have to agree exactly. Every case here is a shape that parser looks for.
func TestStatusLineText(t *testing.T) {
	full := StatusPayload{}
	full.Workspace.CurrentDir = filepath.Join("C:", "Users", "dev", "Projects", "api")
	full.Model.DisplayName = "Opus 5"
	full.ContextWindow.UsedPercentage = ptr(42.4)
	full.Cost.TotalCostUSD = ptr(1.234)
	full.RateLimits.FiveHour.UsedPercentage = ptr(12.6)
	full.RateLimits.SevenDay.UsedPercentage = ptr(34.0)

	got := StatusLineText(full)
	for _, want := range []string{"api", "Opus 5", "ctx:42%", "$1.23", "5h:13%", "wk:34%"} {
		if !strings.Contains(got, want) {
			t.Errorf("line %q is missing %q", got, want)
		}
	}

	// A field the CLI did not send is left out rather than printed as a zero.
	// The parser tells "not shown" from "genuinely 0%" by whether the label is
	// there at all, so inventing one would report a full window as an empty one.
	var bare StatusPayload
	bare.Model.DisplayName = "Sonnet 5"
	got = StatusLineText(bare)
	for _, absent := range []string{"ctx:", "5h:", "wk:", "$"} {
		if strings.Contains(got, absent) {
			t.Errorf("line %q invented %q from a payload that had none", got, absent)
		}
	}
	if got != "Sonnet 5" {
		t.Errorf("line = %q, want just the model", got)
	}

	// Nothing at all is an empty line, not a stray space.
	if got := StatusLineText(StatusPayload{}); got != "" {
		t.Errorf("an empty payload produced %q", got)
	}

	// A real zero is printed, because 0% of the weekly allowance is a fact.
	var zero StatusPayload
	zero.RateLimits.SevenDay.UsedPercentage = ptr(0.0)
	if got := StatusLineText(zero); got != "wk:0%" {
		t.Errorf("a genuine zero rendered as %q", got)
	}

	// Percentages are clamped, since a meter cannot draw 140% and a negative
	// one would come out as a minus sign the parser does not expect.
	var odd StatusPayload
	odd.ContextWindow.UsedPercentage = ptr(140.0)
	odd.RateLimits.FiveHour.UsedPercentage = ptr(-3.0)
	got = StatusLineText(odd)
	if !strings.Contains(got, "ctx:100%") || !strings.Contains(got, "5h:0%") {
		t.Errorf("out-of-range percentages rendered as %q", got)
	}
}

// cwd is the fallback when workspace.current_dir is absent.
func TestStatusLineTextFallsBackToCWD(t *testing.T) {
	var p StatusPayload
	p.CWD = filepath.Join("C:", "work", "thing")
	if got := StatusLineText(p); got != "thing" {
		t.Errorf("got %q, want the folder name from cwd", got)
	}
}

// This runs inside the CLI's render loop several times a second. Anything it
// cannot read has to come out as silence, because its stdout goes on screen.
func TestRunStatusLineStaysQuiet(t *testing.T) {
	for _, in := range []string{"", "not json", "[1,2,3]", `{"model":`, "null"} {
		var out bytes.Buffer
		if err := RunStatusLine(strings.NewReader(in), &out); err != nil {
			t.Errorf("input %q returned an error: %v", in, err)
		}
		if out.Len() != 0 {
			t.Errorf("input %q printed %q onto the user's terminal", in, out.String())
		}
	}

	var out bytes.Buffer
	body, _ := json.Marshal(map[string]any{
		"model":       map[string]any{"display_name": "Haiku 4.5"},
		"rate_limits": map[string]any{"five_hour": map[string]any{"used_percentage": 7}},
	})
	if err := RunStatusLine(bytes.NewReader(body), &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "Haiku 4.5 5h:7%" {
		t.Errorf("printed %q", got)
	}
}

// ---------------------------------------------------------------- settings

// Installing a status line edits a file that belongs to whoever signed in with
// that account, so it has to leave everything else in it alone.
func TestInstallStatusLineKeepsTheRestOfTheFile(t *testing.T) {
	dir := t.TempDir()
	before := map[string]any{
		"model":           "opus",
		"includeCoAuthor": false,
		"permissions":     map[string]any{"allow": []string{"Bash(git:*)"}},
	}
	writeJSONFile(t, filepath.Join(dir, "settings.json"), before)

	if err := InstallStatusLine(dir, filepath.Join("C:", "Program Files", "app", "go-ai-team.exe"), false); err != nil {
		t.Fatal(err)
	}

	after := readJSONFile(t, filepath.Join(dir, "settings.json"))
	for k := range before {
		if _, ok := after[k]; !ok {
			t.Errorf("installing dropped %q from the file", k)
		}
	}
	sl, ok := StatusLineOf(dir)
	if !ok {
		t.Fatal("nothing was installed")
	}
	if !IsOwnStatusLine(sl) {
		t.Errorf("installed %q, which is not recognised as ours", sl.Command)
	}
	// A path with a space in it is the normal case on Windows and has to survive
	// as one argument.
	if !strings.HasPrefix(sl.Command, `"`) {
		t.Errorf("a path with a space was left unquoted: %q", sl.Command)
	}

	// And back out again, leaving the rest.
	if err := RemoveStatusLine(dir); err != nil {
		t.Fatal(err)
	}
	if _, ok := StatusLineOf(dir); ok {
		t.Error("removing left it configured")
	}
	after = readJSONFile(t, filepath.Join(dir, "settings.json"))
	for k := range before {
		if _, ok := after[k]; !ok {
			t.Errorf("removing dropped %q from the file", k)
		}
	}
}

// Somebody else's status line is theirs. Replacing it is a separate decision
// and has to be asked for.
func TestInstallStatusLineWillNotClobber(t *testing.T) {
	dir := t.TempDir()
	writeJSONFile(t, filepath.Join(dir, "settings.json"), map[string]any{
		"statusLine": map[string]any{"type": "command", "command": "node ~/mine.js"},
	})

	err := InstallStatusLine(dir, "go-ai-team", false)
	if err == nil {
		t.Fatal("it overwrote a status line somebody else configured")
	}
	if !strings.Contains(err.Error(), "mine.js") {
		t.Errorf("the error does not say what is in the way: %v", err)
	}
	if sl, _ := StatusLineOf(dir); sl.Command != "node ~/mine.js" {
		t.Errorf("the existing command changed to %q", sl.Command)
	}

	// Removing it is refused for the same reason.
	if err := RemoveStatusLine(dir); err == nil {
		t.Error("it removed a status line it did not install")
	}

	// Asked for explicitly, it goes in.
	if err := InstallStatusLine(dir, "go-ai-team", true); err != nil {
		t.Fatal(err)
	}
	if sl, _ := StatusLineOf(dir); !IsOwnStatusLine(sl) {
		t.Errorf("replace left %q", sl.Command)
	}
}

// A directory with no settings file yet is the common case for a managed
// profile, and has to work.
func TestInstallStatusLineWithNoFileYet(t *testing.T) {
	dir := t.TempDir()
	if err := InstallStatusLine(dir, "go-ai-team", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := StatusLineOf(dir); !ok {
		t.Error("nothing was written")
	}
	// Removing from a directory that never had one is not an error.
	if err := RemoveStatusLine(t.TempDir()); err != nil {
		t.Errorf("removing nothing failed: %v", err)
	}
}

// A settings file that is not valid JSON must stop the edit rather than be
// replaced by one containing only our key.
func TestInstallStatusLineRefusesBrokenJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InstallStatusLine(dir, "go-ai-team", false); err == nil {
		t.Fatal("it rewrote a file it could not read")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "{ this is not json" {
		t.Errorf("the file was changed to %q", string(b))
	}
}

// A directory that does not exist is a stale account, not a place to create
// one: writing there would leave a settings file with no account around it.
func TestInstallStatusLineNeedsTheDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-there")
	if err := InstallStatusLine(missing, "go-ai-team", false); err == nil {
		t.Error("it wrote into a directory that does not exist")
	}
	if err := InstallStatusLine("", "go-ai-team", false); err == nil {
		t.Error("it accepted an empty directory")
	}
}

func ptr[T any](v T) *T { return &v }

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("the file is no longer valid JSON: %v", err)
	}
	return m
}
