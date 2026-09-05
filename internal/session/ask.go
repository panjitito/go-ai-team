package session

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Answering the CLI's questions without leaving the conversation.
//
// Claude Code asks for permission constantly — to write a file, to run a
// command — and it asks in its own interface, drawn straight to the terminal:
//
//	Do you want to create hello.txt?
//	❯ 1. Yes
//	  2. Yes, and switch to accept edits … for this session (shift+tab)
//	  3. No
//	Esc to cancel · Tab to amend
//
// None of that reaches the transcript, so the conversation view could only say
// "it is waiting for an answer in the terminal" and send you to another tab to
// press a key. For the one interaction you perform more than any other, that is
// the wrong answer.
//
// So the box is read off the terminal and offered as buttons. Picking one writes
// that digit into the pty, which is exactly what pressing the key does: the CLI
// is answered by the person, through its own interface, and nothing reaches
// around it.

// Ask is a question the CLI is currently asking.
type Ask struct {
	Question string      `json:"question"`
	Options  []AskOption `json:"options"`
	// Steps is the tab strip a multi-question picker draws above its question —
	// "☐ Colour ☐ Size ✔ Submit" — which says there is more than one question
	// coming and which one this is. Empty for a single question.
	Steps string `json:"steps,omitempty"`
	// Cancel reports whether Esc will dismiss it, which the box says itself.
	Cancel bool `json:"cancel"`
}

// AskOption is one numbered choice.
type AskOption struct {
	Number int    `json:"number"`
	Label  string `json:"label"`
	// Description is the explanatory line a picker prints under an option —
	// "A warm, vibrant colour" beneath "Red". It has its own row on the screen,
	// which is what makes it separable at all; read from the flattened bytes the
	// two ran together into one unreadable string.
	Description string `json:"description,omitempty"`
	// Selected is the option the CLI's own cursor is on, so the UI can show
	// what pressing Enter in the terminal would do.
	Selected bool `json:"selected"`
}

var (
	// A numbered option. The dot is optional because the selected row is drawn
	// with its own attributes and flattening the frame can swallow it — "❯ 1 Yes"
	// alongside "2. Yes, and …" is a real capture, not a malformed one.
	askOption = regexp.MustCompile(`(?:^|[\s❯])\s*(\d{1,2})[.)]?\s+(\S)`)

	// The footer, which is also where the last option's label stops.
	//
	// The s flag is load-bearing. Without it "." stops at a newline, and since
	// the real frame puts blank lines between "No" and the footer and another
	// after it, the anchor at the end could never be reached — so the last
	// button came out reading "No Esc to cancel · Tab to amend". Captured
	// fixtures did not show it, because there the footer happened to be the last
	// thing in the buffer.
	// Different boxes word their footer differently — "Esc to cancel · Tab to
	// amend" on a permission prompt, "Enter to select · Tab/Arrow keys to
	// navigate · Esc to cancel" on a question picker — and a phrase this list
	// misses ends up glued onto the last option's label, which is where the
	// person is reading.
	askFooter = regexp.MustCompile(`(?is)\s*(?:Esc to cancel|Esc to exit|Tab to amend|` +
		`Enter to select|Enter to confirm|Enter to continue|Tab/Arrow|Arrow keys|` +
		`keys to navigate)\b.*$`)

	// A space the frame left in front of its punctuation.
	askGap = regexp.MustCompile(`\s+([?:!])`)
)

// ParseAsk reads a live numbered question out of the tail of a terminal.
//
// Not just permission prompts. Every choice Claude Code offers takes this one
// shape — the theme picker and the login-method picker a new user meets before
// anything else, the folder-trust question, the model list — and none of them
// reach the transcript, so the conversation view showed nothing whatsoever while
// the CLI sat waiting. One parser covers the lot.
//
// The second return is false when there is no question on screen *now*. That
// distinction is the whole difficulty: a box answered a minute ago is still
// sitting in the scrollback, word for word, and reporting it would leave the
// conversation permanently claiming an idle agent wants something.
//
// The tell is the ❯ cursor. While a box is up the cursor marks one of its
// options, so the last ❯ in the buffer sits inside the run of numbers; once the
// box is gone the composer is drawn back and takes the last ❯ for itself,
// underneath its own horizontal rule, followed by whatever you have typed or by
// nothing. Those two are far enough apart to tell without knowing how long ago
// anything happened.
func ParseAsk(tail string) (Ask, bool) {
	s := screenText(tail)

	cur := strings.LastIndex(s, "❯")
	if cur < 0 || !cursorOnAnOption(s, cur) {
		return Ask{}, false
	}

	opts, from, to := parseOptions(s, cur)
	if len(opts) < 2 {
		// One button is not a question. A half-drawn frame reads like this, and
		// so does a line of prose that happens to begin "1.".
		return Ask{}, false
	}
	// The cursor belongs to this box, not to something drawn after it. Measured
	// rather than allowed a few characters of slack: the padding between the
	// cursor and the first option is however wide the picker drew it, and only
	// looked like one space because the flatten used to collapse it.
	if cur > to || strings.TrimSpace(strings.TrimPrefix(s[min(cur, from):from], "❯")) != "" {
		return Ask{}, false
	}

	return Ask{
		Question: repairQuestion(s, questionBefore(s, from)),
		Options:  opts,
		Steps:    AskSteps(s[max(0, from-300):from]),
		Cancel:   strings.Contains(strings.ToLower(s[from:]), "esc to cancel"),
	}, true
}

// repairQuestion fills in a character a partial repaint swallowed.
//
// The box is drawn once in full and then patched, and a patch that moves the
// cursor over a cell it is not rewriting means that character never reaches us:
// "notify-1788592053.txt" arrives as "notify-1788592053. xt". Harmless for the
// buttons, which are read from the live frame either way, but this text is the
// heading on the panel and the body of the notification, and a filename with a
// hole in it reads as a bug.
//
// The full render is still further up the buffer. What arrives is always a
// *subsequence* of what is really there — characters go missing, none appear —
// so an earlier occurrence that contains this one as a subsequence is the same
// sentence, drawn more completely. Bounded to a few characters longer, because
// beyond that it stops being a repaint of the same thing.
// Only the permission prompts, which are the ones with a filename or a command
// in them and a fixed opening to anchor on. The other boxes state their question
// in prose, where a missing letter is not worth the risk of substituting a
// neighbouring sentence for it.
const askOpen = "Do you want"

// A lost character arrives as a space, not as nothing: the cursor moved over the
// cell and the flattening puts a space wherever it moved. So the damaged text is
// the same sentence with gaps in it, and comparing with the spaces taken out is
// what makes the two comparable.
func repairQuestion(s, q string) string {
	if !strings.HasPrefix(q, askOpen) {
		return q
	}
	want := despace(q)
	best, bestLen := q, len(want)
	for _, cand := range questionRuns(s) {
		got := despace(cand)
		if len(got) <= bestLen || len(got) > len(want)+repairSlack {
			continue
		}
		if isSubsequence(want, got) {
			best, bestLen = cand, len(got)
		}
	}
	return best
}

// screenText is the frame as it currently looks, one row per line.
//
// Not the flattened byte tail, and the difference is the whole reason answering
// a question from the conversation works at all. A TUI repaints only the cells
// that changed: press Down in a picker and the bytes that arrive are the cursor
// moving and nothing else, so the recent tail holds ". 6. 7. 8." where the
// options used to be. Parsed from the tail, the box appears to vanish the
// instant it is touched — which is exactly what it looked like, and why the
// first attempt at answering reported "the question went away" while the
// terminal plainly still showed it.
//
// Rendering the frame keeps what was drawn earlier, because that is what is
// still on the screen.
func screenText(tail string) string {
	rows := Screen(tail)
	parts := make([]string, 0, len(rows))
	for _, r := range rows {
		parts = append(parts, r.Text)
	}
	return strings.Join(parts, "\n")
}

// splitDescription separates an option from the note printed under it.
//
// Exact, because the screen is rendered by rows: the option is one row and its
// description is the next. Read from the flattened bytes the two arrived as
// "Red A warm, vibrant colour", with nothing to say where one ended.
//
// A row that merely wrapped is left joined. A description is a separate row the
// picker chose to print, not the tail of a sentence that ran out of width, and
// what tells them apart is whether the first row finishes a thought.
func splitDescription(s string) (label, desc string) {
	i := strings.IndexByte(s, '\n')
	if i < 0 {
		return s, ""
	}
	head := strings.TrimSpace(collapse(s[:i]))
	rest := strings.TrimSpace(collapse(s[i+1:]))
	if head == "" || rest == "" {
		return strings.TrimSpace(collapse(s)), ""
	}
	if len(head) > 60 && !strings.HasSuffix(head, ".") {
		return head + " " + rest, ""
	}
	return head, rest
}

// cleanFurniture removes the parts of a frame that are decoration rather than
// what is being asked.
//
// Two kinds. Box rules, which are the widest thing on screen and say nothing.
// And the tab strip a multi-question picker draws above its question —
// "← ☐ Colour ☐ Size ✔ Submit →" — which is genuinely useful, but as a caption
// telling you there are two questions and where you are, not as the first
// forty characters of the question itself.
//
// Also drops replacement characters. The terminal is read as a byte tail, so it
// can begin in the middle of a rune, and half a box-drawing character is not
// something to show anybody.
func cleanFurniture(s string) string {
	s = askTabStrip.ReplaceAllString(s, " ")
	s = askRules.ReplaceAllString(s, " ")
	return strings.Map(func(r rune) rune {
		if r == utf8.RuneError {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, ""))
}

// AskSteps returns the tab strip a multi-question picker draws, if there is one,
// so the panel can say "two questions, this is the first".
func AskSteps(s string) string {
	m := askTabStrip.FindString(s)
	if m == "" {
		return ""
	}
	return strings.TrimSpace(collapse(strings.Trim(strings.TrimSpace(m), "←→")))
}

var (
	// Four or more, so a hyphenated word or an arrow is never mistaken for one.
	askRules = regexp.MustCompile(`[─━═╌╍┄┅]{4,}`)
	// From the left arrow to the right one, which is how the strip is drawn.
	askTabStrip = regexp.MustCompile(`←[^←→
]{0,160}→`)
)

func despace(s string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, s)
}

// repairSlack is how much longer a candidate may be. A repaint loses a character
// or two; beyond that it stops being the same sentence drawn again.
const repairSlack = 4

// questionRuns returns every rendering of a question in the buffer, each cut at
// its own question mark.
func questionRuns(s string) []string {
	var out []string
	for i := 0; ; {
		j := strings.Index(s[i:], askOpen)
		if j < 0 {
			return out
		}
		j += i
		i = j + len(askOpen)

		end := j + questionMax
		if end > len(s) {
			end = len(s)
		}
		run := s[j:end]
		// A question ends at its mark; anything else means this rendering was
		// cut off, and a truncated one is no use as a repair.
		k := strings.IndexAny(run, "?\n❯")
		if k < 0 || run[k] != '?' {
			continue
		}
		out = append(out, askGap.ReplaceAllString(strings.TrimSpace(collapse(run[:k+1])), "$1"))
	}
}

// questionMax is how far past the opening to look for the question mark.
const questionMax = 300

// cursorOnAnOption rejects the composer, which is on screen permanently and
// carries a ❯ of its own.
//
// Two things separate them. The composer's cursor is followed by the text you
// are typing, or by nothing; an option's is followed by its number. And the
// composer sits directly under a horizontal rule, which no option does.
func cursorOnAnOption(s string, cur int) bool {
	rest := strings.TrimLeft(s[cur+len("❯"):], " \t ")
	if rest == "" || rest[0] < '0' || rest[0] > '9' {
		return false
	}
	before := strings.TrimRight(s[max(0, cur-12):cur], " \t\n ")
	return !strings.HasSuffix(before, "─")
}

// questionBefore takes the text in front of the first option.
//
// A permission prompt states its question in one recognisable sentence, so it is
// anchored on. Everything else — "Select login method:", "Choose the text style
// that looks best with your terminal" — has no fixed opening, and the honest
// thing there is to show the last stretch of what the CLI wrote rather than to
// invent a heading for it.
func questionBefore(s string, at int) string {
	// One row, because this is a rendered screen and a question occupies a row.
	//
	// It used to take a fixed window of characters back from the options and hunt
	// for the start of a sentence inside it, which is how a box rule and the tab
	// strip above the question ended up as the first forty characters of it.
	// Walking back a line at a time and stopping at the first that says something
	// is simpler and right by construction.
	var picked []string
	lines := strings.Split(s[:at], "\n")
	for i := len(lines) - 1; i >= 0 && len(picked) < 2; i-- {
		t := strings.TrimSpace(collapse(cleanFurniture(lines[i])))
		// A row holding only the cursor is not the question, and neither is an
		// empty one — the cursor sits on its own row above the options and was
		// winning every time.
		t = strings.TrimSpace(strings.Trim(t, " ❯›»"))
		if t == "" {
			continue
		}
		picked = append([]string{t}, picked...)
		// A question ends in a question mark or a colon. Once one is found there
		// is no reason to keep walking up into whatever was printed before it.
		if strings.HasSuffix(t, "?") || strings.HasSuffix(t, ":") {
			break
		}
	}
	w := strings.Join(picked, " ")
	// A permission prompt states its question part-way along a row, after
	// whatever else the CLI last drew there.
	if i := strings.LastIndex(w, askOpen); i >= 0 {
		w = w[i:]
	}
	q := strings.TrimRight(w, " ❯\t")
	// "hello.txt ?" — the flattening leaves a gap wherever the frame moved the
	// cursor, and a space before the question mark is the one that shows.
	q = askGap.ReplaceAllString(strings.TrimSpace(q), "$1")
	if q == "" {
		return "It is waiting for you to choose"
	}
	return q
}

// parseOptions pulls "1. Yes", "2. …", "3. No" out of the last box in the
// buffer, and reports where that box's numbers begin and end so the caller can
// take the question from in front of it and check the cursor is inside it.
//
// The numbers must run 1, 2, 3 with nothing missing. Labels contain digits of
// their own — "don't ask again for: git init" — and requiring the sequence is
// what stops one of those being read as the next option. A number 1 always
// starts a fresh run, so an older box further up the scrollback is discarded
// rather than merged into this one.
func parseOptions(s string, cursorAt int) ([]AskOption, int, int) {
	// Every run of sequential numbers in the buffer, not just the last one. The
	// theme picker is followed on screen by a worked example whose lines happen
	// to be numbered 1, 2, 3 — a perfectly good run, and the wrong one. The
	// cursor says which run is the menu.
	var runs [][2][]int // numAt, labelAt
	for _, m := range askOption.FindAllStringSubmatchIndex(s, -1) {
		n, err := strconv.Atoi(s[m[2]:m[3]])
		if err != nil {
			continue
		}
		switch {
		case n == 1:
			runs = append(runs, [2][]int{{m[2]}, {m[4]}})
		case len(runs) > 0 && n == len(runs[len(runs)-1][0])+1:
			last := &runs[len(runs)-1]
			last[0] = append(last[0], m[2])
			last[1] = append(last[1], m[4])
		}
	}
	if len(runs) == 0 {
		return nil, len(s), len(s)
	}

	// A run ends where the next one begins; the last ends with the buffer.
	pick, end := -1, len(s)
	for i, r := range runs {
		stop := len(s)
		if i+1 < len(runs) {
			stop = runs[i+1][0][0]
		}
		if cursorAt >= r[0][0]-4 && cursorAt <= stop {
			pick, end = i, stop
		}
	}
	if pick < 0 {
		return nil, len(s), len(s)
	}
	numAt, labelAt := runs[pick][0], runs[pick][1]

	opts := make([]AskOption, len(numAt))
	for x := range numAt {
		stop := end
		if x+1 < len(numAt) {
			stop = numAt[x+1]
		}
		label := askFooter.ReplaceAllString(s[labelAt[x]:stop], "")
		head, desc := splitDescription(label)
		tidy := func(v string) string {
			return strings.TrimSpace(strings.TrimRight(collapse(v), "╌─❯✔ \t\n"))
		}
		opts[x] = AskOption{
			Number: x + 1,
			// A box rule closes the menu and the last label runs into it; the
			// cursor and the current-setting tick sit at the end of a row.
			Label:       tidy(head),
			Description: tidy(desc),
			// The cursor is drawn immediately in front of the row it has
			// selected: after the previous option's text, before this one's.
			Selected: cursorAt >= 0 && cursorAt < numAt[x] &&
				(x == 0 || cursorAt > numAt[x-1]),
		}
	}
	return opts, numAt[0], end
}

// flattenFrame turns raw terminal bytes into readable text.
//
// Unlike stripANSI, which deletes escape sequences, this replaces each with a
// space. A TUI positions text by moving the cursor rather than by writing
// spaces, so deleting the moves runs neighbouring words together — "gat-pr" and
// "be-work" become one word — and the labels stop being readable. A space in
// place of every move keeps them apart, at the cost of some extra whitespace,
// which is then collapsed.
func flattenFrame(s string) string {
	s = frameEsc.ReplaceAllString(s, " ")
	s = strings.NewReplacer("\r", "\n", " ", " ").Replace(s)
	return collapse(s)
}

var (
	frameEsc  = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07]*\x07|\x1b[=>()][0-9A-Za-z]?|[\x00-\x08\x0b\x0c\x0e-\x1f]`)
	manySpace = regexp.MustCompile(`[ \t]{2,}`)
)

func collapse(s string) string { return manySpace.ReplaceAllString(s, " ") }

// AskOf reads the question one session is asking, if any.
func (m *Manager) AskOf(id string) (Ask, bool) {
	s, ok := m.Get(id)
	if !ok {
		return Ask{}, false
	}
	return ParseAsk(string(s.ring.Tail(askTail)))
}

// askTail is how much of the terminal to read. The longest of these boxes —
// four options, each a sentence — runs well past a kilobyte once the escape
// sequences that draw it are counted.
const askTail = 12 << 10

// Answer picks one of the numbered options.
//
// By moving the cursor and pressing Enter, not by typing the number — and that
// distinction is the whole of this function.
//
// Typing the digit was the first implementation, and it works on a permission
// prompt: pressing "1" there really does answer it, proved by the file appearing
// on disk. It does nothing at all on the picker AskUserQuestion draws, whose
// footer says "Enter to select · Tab/Arrow keys to navigate". The numbers there
// are labels, not shortcuts. So the panel showed five clickable options over a
// box that ignored every one of them, which is worse than not offering them.
//
// Arrow keys and Enter work on both, because both are the same kind of widget.
// And they can be checked: after moving, the cursor's own position is read back
// off the screen, and Enter is only sent once it is on the row that was asked
// for. Nothing here presses Enter hopefully.
func (m *Manager) Answer(id string, n int) error {
	ask, ok := m.AskOf(id)
	if !ok {
		return fmt.Errorf("there is no question on screen any more — it was answered or cancelled")
	}
	if !hasOption(ask, n) {
		return fmt.Errorf("option %d is not one of the choices being offered", n)
	}

	for attempt := 0; attempt < answerTries; attempt++ {
		at, want := selectedNumber(ask), n
		if at == 0 {
			return fmt.Errorf("cannot tell which option is highlighted, so it is not safe to press Enter")
		}
		if at == want {
			// On the row asked for. Only now.
			return m.SendKeys(id, "\r")
		}
		key := "\x1b[B" // down
		steps := want - at
		if steps < 0 {
			key, steps = "\x1b[A", -steps // up
		}
		for i := 0; i < steps; i++ {
			if err := m.SendKeys(id, key); err != nil {
				return err
			}
			time.Sleep(answerStep)
		}
		// Read back, with patience. The box repaints after every keypress, and a
		// read that lands mid-repaint sees no cursor and no options — which is
		// not the question having gone away, it is the question being redrawn.
		// Treating the first failed read as fatal made answering fail outright
		// perhaps half the time.
		ask, ok = m.askSettled(id)
		if !ok {
			return fmt.Errorf("the question went away while it was being answered")
		}
		if !hasOption(ask, n) {
			return fmt.Errorf("the question changed while it was being answered")
		}
	}
	return fmt.Errorf("could not move the highlight onto option %d — answer it in the terminal", n)
}

func hasOption(a Ask, n int) bool {
	for _, o := range a.Options {
		if o.Number == n {
			return true
		}
	}
	return false
}

// selectedNumber is the option the cursor is on, or 0 when that cannot be told.
func selectedNumber(a Ask) int {
	for _, o := range a.Options {
		if o.Selected {
			return o.Number
		}
	}
	return 0
}

// askSettled reads the question back once the frame has stopped moving.
func (m *Manager) askSettled(id string) (Ask, bool) {
	deadline := time.Now().Add(answerSettle * 6)
	for {
		time.Sleep(answerSettle)
		if a, ok := m.AskOf(id); ok {
			return a, true
		}
		if time.Now().After(deadline) {
			return Ask{}, false
		}
	}
}

const (
	// answerTries bounds the move-and-check loop. Two passes is enough for a
	// repaint that lands mid-read; more than that and something is wrong.
	answerTries = 3
	// answerStep paces the arrow keys so the TUI redraws between them.
	answerStep = 45 * time.Millisecond
	// answerSettle is how long to let the frame settle before reading it back.
	answerSettle = 260 * time.Millisecond
)

// refreshAsk notices when a session starts, or stops, waiting on a person.
//
// This is the whole point of running several agents at once: they do not finish
// together, and the one that has stopped to ask you something is invisible until
// you happen to look at it. "Waiting" was already a status, but it means two
// entirely different things — waiting for the model, and waiting for you — and
// only one of them is worth walking back to the desk for.
//
// The tail is read outside the session lock. It has its own, and taking them in
// the wrong order here would deadlock against the reader that is filling it.
func (m *Manager) refreshAsk(s *Session) {
	ask, asking := ParseAsk(string(s.ring.Tail(askTail)))

	s.mu.Lock()
	was := s.needsYou
	s.needsYou = asking
	s.question = ""
	if asking {
		s.question = ask.Question
	}
	q := s.question
	s.mu.Unlock()

	if asking == was {
		return
	}
	// Public takes the same lock, so it is called once the lock is back down.
	pub := s.Public()
	if asking {
		m.emit(Event{
			Type: "session.needs-you", SessionID: s.ID, AgentID: s.AgentID,
			Message: q, Payload: pub,
		})
		return
	}
	// The answered case matters too: a notification that cannot be cleared is
	// one you learn to ignore.
	m.emit(Event{Type: "session.answered", SessionID: s.ID, AgentID: s.AgentID, Payload: pub})
}

// Interrupt stops what the agent is doing without ending the session.
//
// Escape is what the CLI's own footer tells you to press, and it is the missing
// half of Stop: Stop kills the process and loses the conversation, which is far
// more than you want when an agent has simply gone off in the wrong direction.
func (m *Manager) Interrupt(id string) error {
	if _, ok := m.Get(id); !ok {
		return fmt.Errorf("that session is not running")
	}
	return m.SendKeys(id, "\x1b")
}
