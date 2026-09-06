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

// The box is drawn once in full and then patched, and a patch that moves the
// cursor over a cell it is not rewriting loses that character: a real capture
// gave "notify-1788592053. xt" for a file called notify-1788592053.txt. The full
// render is still further up the buffer, and what arrives is always a
// subsequence of what is really there, so it can be found again.
func TestParseAskRepairsAHoleInTheQuestion(t *testing.T) {
	const buf = "Do you want to create notify-1788592053.txt?\n" +
		" 1. Yes\n 2. No\n" +
		// The repaint, with the hole, and the live cursor.
		"Do you want to create notify-1788592053. xt? ❯ 1. Yes 2. No Esc to cancel"
	ask, ok := ParseAsk(buf)
	if !ok {
		t.Fatal("not parsed")
	}
	if ask.Question != "Do you want to create notify-1788592053.txt?" {
		t.Errorf("question = %q, want the undamaged rendering", ask.Question)
	}
}

// Repair must never substitute a different question. A longer sentence that
// merely happens to contain this one as a subsequence is out of reach: renders
// of the same box differ by a character or two, not by a clause.
func TestParseAskRepairKeepsItsOwnQuestion(t *testing.T) {
	const buf = "Do you want to create a-very-different-and-much-longer-name.txt?\n 1. Yes\n 2. No\n" +
		"Do you want to create a.txt? ❯ 1. Yes 2. No Esc to cancel"
	ask, ok := ParseAsk(buf)
	if !ok {
		t.Fatal("not parsed")
	}
	if ask.Question != "Do you want to create a.txt?" {
		t.Errorf("question = %q, want the one actually on screen", ask.Question)
	}
}

// Nothing to repair against is the normal case, and must not change anything.
func TestParseAskRepairIsANoOpWithoutAnEarlierRender(t *testing.T) {
	ask, ok := ParseAsk("Do you want to create only-once.txt? ❯ 1. Yes 2. No Esc to cancel")
	if !ok {
		t.Fatal("not parsed")
	}
	if ask.Question != "Do you want to create only-once.txt?" {
		t.Errorf("question = %q", ask.Question)
	}
}

// The picker AskUserQuestion draws, captured from a real agent asking two
// questions at once. Not a permission prompt: it has a tab strip above it, a
// description beside each option, and a footer worded differently — every one of
// which leaked into what the panel showed before.
func TestParseAskQuestionPicker(t *testing.T) {
	ask, ok := ParseAsk(fixture(t, "ask_question.bin"))
	if !ok {
		t.Fatal("the question picker was not recognised")
	}
	// The second of the two questions, because that is the frame this terminal
	// was left on. Reading the byte tail instead used to answer with the *first*
	// question, which had already been answered — a picker repaints in place, so
	// the older one is still in the bytes and is not on the screen.
	if ask.Question != "What is your preferred size?" {
		t.Errorf("question = %q — either furniture is leaking in, or this is the stale frame", ask.Question)
	}
	// The strip says how many questions there are and which is answered.
	if !strings.Contains(ask.Steps, "Colour") || !strings.Contains(ask.Steps, "Submit") {
		t.Errorf("steps = %q", ask.Steps)
	}
	if len(ask.Options) != 4 {
		t.Fatalf("got %d options, want 4: %+v", len(ask.Options), ask.Options)
	}
	if !strings.HasPrefix(ask.Options[0].Label, "Small") || !ask.Options[0].Selected {
		t.Errorf("option 1 = %+v", ask.Options[0])
	}
	// This footer is worded "Enter to select · Tab/Arrow keys to navigate · Esc
	// to cancel", and the last option used to swallow all of it.
	if last := ask.Options[3].Label; last != "Chat about this" {
		t.Errorf("last option = %q, want just its own text", last)
	}
}

// A byte tail can begin in the middle of a rune, and the cut used to advance one
// byte past a three-byte box rule — leaving two of its bytes at the front of
// every question a picker asked.
func TestParseAskNoBrokenRunes(t *testing.T) {
	ask, ok := ParseAsk(fixture(t, "ask_question.bin"))
	if !ok {
		t.Fatal("not parsed")
	}
	for _, r := range ask.Question {
		if r == 0xFFFD {
			t.Fatalf("the question carries a broken character: %q", ask.Question)
		}
	}
	if strings.ContainsAny(ask.Question, "─━═╌") {
		t.Errorf("box drawing left in the question: %q", ask.Question)
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

// The folder-trust question, which is the first thing every new project meets.
//
// Captured from a real agent started in a directory the CLI had not seen. It is
// drawn with no numbers at all — just an arrow and two lines — so the numbered
// parser rejected it at the first test and the app reported the agent as
// "waiting" with nothing to click. It sat there for as long as anybody left it.
func TestParseAskTrustFolder(t *testing.T) {
	raw := fixture(t, "ask_trust.bin")
	ask, ok := ParseAsk(raw)
	if !ok {
		t.Fatal("the folder-trust question was not recognised")
	}
	if len(ask.Options) != 2 {
		t.Fatalf("got %d options, want 2: %+v", len(ask.Options), ask.Options)
	}
	if ask.Options[0].Label != "No, exit" {
		t.Errorf("option 1 = %q", ask.Options[0].Label)
	}
	if ask.Options[1].Label != "Yes, I trust this folder" {
		t.Errorf("option 2 = %q", ask.Options[1].Label)
	}
	// The CLI's own cursor is on the first row, and the panel has to say so:
	// the dangerous one is selected by default here.
	if !ask.Options[0].Selected || ask.Options[1].Selected {
		t.Errorf("selection = %+v, want the first", ask.Options)
	}
	// Numbered from one even though nothing on screen is numbered, because
	// Answer counts rows to move the cursor by.
	if ask.Options[0].Number != 1 || ask.Options[1].Number != 2 {
		t.Errorf("numbers = %d, %d", ask.Options[0].Number, ask.Options[1].Number)
	}
	if !strings.Contains(strings.ToLower(ask.Question), "trust") {
		t.Errorf("question = %q", ask.Question)
	}
	if !ask.Cancel {
		t.Error("the box says Esc to cancel and this did not notice")
	}
}

// The composer draws a ❯ too. Mistaking a half-typed message for a question
// would put buttons on screen over an agent that is waiting for the person to
// finish their sentence.
func TestParseArrowIgnoresTheComposer(t *testing.T) {
	for _, name := range []string{"empty prompt", "typed prompt", "no footer"} {
		var s string
		switch name {
		case "empty prompt":
			s = "some output\n\n❯ \n  \n"
		case "typed prompt":
			// What the composer looks like with two lines in it, borders and all.
			s = "│ ❯ write the migration │\n│   and then run it      │\n" +
				"  Enter to confirm\n"
		case "no footer":
			// The shape of a menu, without the thing that makes it one.
			s = "Pick one:\n❯ alpha\n  beta\n\nsomething else entirely\n"
		}
		if ask, ok := ParseAsk(s); ok {
			t.Errorf("%s: read a question that is not there: %+v", name, ask)
		}
	}
}

// Answering moves the arrow, and the box has to still be a box afterwards.
//
// The parser looked only downwards at first, on the reasoning that the arrow
// starts on the first option. It does, and then Answer presses Down: the arrow
// landed on the last row, no siblings were found beneath it, and the box
// stopped being recognised half way through being answered. Every time, on the
// only prompt it was written for.
func TestParseArrowFollowsTheMovingCursor(t *testing.T) {
	const box = " Is this a project you trust?\n" +
		" ❯ No, exit\n" +
		"   Yes, I trust this folder\n" +
		" Enter to confirm · Esc to cancel\n"
	// The same screen after one press of Down.
	const moved = " Is this a project you trust?\n" +
		"   No, exit\n" +
		" ❯ Yes, I trust this folder\n" +
		" Enter to confirm · Esc to cancel\n"

	for _, c := range []struct {
		name string
		s    string
		want int
	}{{"cursor on the first", box, 1}, {"cursor on the last", moved, 2}} {
		ask, ok := ParseAsk(c.s)
		if !ok {
			t.Fatalf("%s: the box was not recognised", c.name)
		}
		if len(ask.Options) != 2 {
			t.Fatalf("%s: %d options, want 2: %+v", c.name, len(ask.Options), ask.Options)
		}
		if ask.Options[0].Label != "No, exit" || ask.Options[1].Label != "Yes, I trust this folder" {
			t.Errorf("%s: labels = %q, %q", c.name, ask.Options[0].Label, ask.Options[1].Label)
		}
		if got := selectedNumber(ask); got != c.want {
			t.Errorf("%s: selected %d, want %d", c.name, got, c.want)
		}
	}
}
