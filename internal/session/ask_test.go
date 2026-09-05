package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures are raw pty bytes captured from a real Claude Code answering a
// real prompt — escape sequences, repaints, squashed words and all. Every one of
// the awkward cases below came out of a run rather than out of a guess:
// testdata/ask_edit.bin in particular is the tail of a terminal whose prompt has
// already been dismissed, with the whole box still sitting in the scrollback.
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseAskWrite(t *testing.T) {
	ask, ok := ParseAsk(fixture(t, "ask_write.bin"))
	if !ok {
		t.Fatal("a prompt that is on screen was not found")
	}
	if !strings.Contains(ask.Question, "create hello.txt?") {
		t.Errorf("question = %q", ask.Question)
	}
	if len(ask.Options) != 3 {
		t.Fatalf("got %d options, want 3: %+v", len(ask.Options), ask.Options)
	}
	if ask.Options[0].Label != "Yes" {
		t.Errorf("option 1 = %q, want Yes", ask.Options[0].Label)
	}
	if !ask.Options[0].Selected {
		t.Error("the cursor is on option 1 and it was not reported as selected")
	}
	if ask.Options[2].Label != "No" {
		t.Errorf("option 3 = %q, want No", ask.Options[2].Label)
	}
	if !strings.Contains(ask.Options[1].Label, "accept edits") {
		t.Errorf("option 2 = %q", ask.Options[1].Label)
	}
	// The footer belongs to the box, not to the last option.
	for _, o := range ask.Options {
		if strings.Contains(strings.ToLower(o.Label), "esc to cancel") {
			t.Errorf("option %d swallowed the footer: %q", o.Number, o.Label)
		}
	}
	// The real frame separates the last option from the footer with blank lines
	// and puts more after it. A live run found this; the fixture above did not,
	// because there the footer was the last thing in the buffer.
	spaced, ok := ParseAsk("Do you want to proceed? ❯ 1. Yes\n 2. No\n\n\n Esc to cancel · Tab to amend\n\n──────\n")
	if !ok {
		t.Fatal("a box with blank lines round its footer was not parsed")
	}
	if got := spaced.Options[1].Label; got != "No" {
		t.Errorf("last option = %q, want No", got)
	}
	if !ask.Cancel {
		t.Error("the box offers Esc to cancel and that was not reported")
	}
}

// Four options, and one of the labels contains a command with its own digits
// and punctuation. Requiring the numbers to run in sequence is what stops those
// being read as further options.
func TestParseAskBash(t *testing.T) {
	ask, ok := ParseAsk(fixture(t, "ask_bash.bin"))
	if !ok {
		t.Fatal("a prompt that is on screen was not found")
	}
	if len(ask.Options) != 4 {
		t.Fatalf("got %d options, want 4: %+v", len(ask.Options), ask.Options)
	}
	want := []string{"Yes", "", "", "No"}
	for i, w := range want {
		if w == "" {
			continue
		}
		if ask.Options[i].Label != w {
			t.Errorf("option %d = %q, want %q", i+1, ask.Options[i].Label, w)
		}
	}
	if !strings.Contains(ask.Options[1].Label, "ask again") {
		t.Errorf("option 2 = %q", ask.Options[1].Label)
	}
	if !strings.Contains(ask.Options[2].Label, "auto mode") {
		t.Errorf("option 3 = %q", ask.Options[2].Label)
	}
}

// The case that matters most. This terminal's prompt was cancelled: the box is
// still in the scrollback, word for word, and the composer is back. Reporting a
// question here would leave the conversation permanently claiming an idle agent
// is waiting for an answer — the exact failure the banner used to have.
func TestParseAskIgnoresADismissedBox(t *testing.T) {
	raw := fixture(t, "ask_edit.bin")
	if !strings.Contains(flattenFrame(raw), "Do you want") {
		t.Fatal("fixture no longer contains the stale box; it is not testing anything")
	}
	if ask, ok := ParseAsk(raw); ok {
		t.Errorf("read a question off a terminal that has none: %+v", ask)
	}
}

// The two boxes a new user meets before anything else. Neither is a permission
// prompt, neither reaches the transcript, and until this parser covered them
// the conversation view showed a blank panel over a CLI that was waiting.
func TestParseAskFirstRun(t *testing.T) {
	theme, ok := ParseAsk(fixture(t, "ask_theme.bin"))
	if !ok {
		t.Fatal("the theme picker was not recognised")
	}
	if len(theme.Options) != 7 {
		t.Errorf("got %d theme options, want 7: %+v", len(theme.Options), theme.Options)
	}
	if theme.Options[0].Label != "Auto (match terminal)" {
		t.Errorf("theme option 1 = %q", theme.Options[0].Label)
	}
	// The CLI's own cursor starts on the second row here, which is exactly why
	// the panel shows which one is selected rather than assuming the first.
	if !theme.Options[1].Selected {
		t.Errorf("selection = %+v, want option 2", theme.Options)
	}
	if !strings.Contains(theme.Question, "text style") {
		t.Errorf("theme question = %q", theme.Question)
	}

	login, ok := ParseAsk(fixture(t, "ask_login.bin"))
	if !ok {
		t.Fatal("the login-method picker was not recognised")
	}
	if len(login.Options) != 3 {
		t.Errorf("got %d login options, want 3: %+v", len(login.Options), login.Options)
	}
	if !strings.Contains(login.Question, "login method") {
		t.Errorf("login question = %q", login.Question)
	}
}

func TestParseAskNoPrompt(t *testing.T) {
	for _, s := range []string{
		"",
		"go-ai-team (main) Sonnet 5 ctx:6% $0.07 5h:33% wk:3%",
		// The composer, which is on screen permanently and is not a question.
		"────────────\n❯ \n────────────\n⏸ manual mode on",
		// Prose that merely mentions the phrase.
		"I could rename it. Do you want to do that? Let me know.\n❯ ",
	} {
		if ask, ok := ParseAsk(s); ok {
			t.Errorf("ParseAsk(%q) found %+v", s, ask)
		}
	}
}

// A single button is not a choice, and a half-drawn frame looks like one.
func TestParseAskNeedsMoreThanOneOption(t *testing.T) {
	if _, ok := ParseAsk("Do you want to proceed? ❯ 1. Yes"); ok {
		t.Error("offered a question with only one option")
	}
}

func TestParseAskSelectionFollowsTheCursor(t *testing.T) {
	const box = "Do you want to proceed? 1. Yes 2. No, and tell Claude what to do differently " +
		"❯ 3. No Esc to cancel · Tab to amend"
	ask, ok := ParseAsk(box)
	if !ok {
		t.Fatal("not parsed")
	}
	if len(ask.Options) != 3 {
		t.Fatalf("got %d options: %+v", len(ask.Options), ask.Options)
	}
	if !ask.Options[2].Selected {
		t.Errorf("selection = %+v, want option 3", ask.Options)
	}
	for _, i := range []int{0, 1} {
		if ask.Options[i].Selected {
			t.Errorf("option %d reported as selected too", i+1)
		}
	}
}

// flattenFrame exists because deleting a cursor move runs the words on either
// side of it together, and the labels stop being readable.
func TestFlattenFrameKeepsWordsApart(t *testing.T) {
	const raw = "gat-pr\x1b[1Cbe-work\x1b[3CSonnet\x1b[1C5"
	got := flattenFrame(raw)
	if strings.Contains(got, "gat-prbe-work") {
		t.Errorf("words ran together: %q", got)
	}
	if !strings.Contains(got, "Sonnet 5") {
		t.Errorf("flattenFrame(%q) = %q", raw, got)
	}
}
