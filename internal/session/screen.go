package session

import (
	"strconv"
	"strings"
)

// Reconstructing what is actually on the screen.
//
// A small terminal, and only as much of one as reading a prompt needs: a grid of
// cells, absolute and relative cursor movement, the erase sequences, and
// scrolling. No attributes, no wrapping, no alternate buffer, no scrollback.
// Those are all real and this would be wrong about them; nothing here depends on
// them.
//
// Why a grid and not string handling. A numbered menu can be read from flat
// text because the numbers delimit it. A menu without numbers cannot:
//
//	Restore the code … to the point before…  Create ./one.txt  one.txt +1
//	Create ./two.txt  two.txt +1  ❯ (current)  Enter to continue · Esc to cancel
//
// is a single line once the escape sequences are dropped, with no way to tell an
// option from its own subtitle. The sequences say where every piece went, and
// grouping on that gives the rows back.
//
// String concatenation per row is not enough either, and that was tried first: a
// TUI rewrites a row in pieces, at different columns, many times per second, and
// appending them produces a row holding four seconds of spinner frames. Cells
// are what the terminal itself keeps, so cells are what this keeps.
//
// A useful side effect: a cursor-forward sequence leaves the cell it skips
// alone, exactly as a terminal does, so words no longer arrive with holes in
// them. The older parsers work around that; this does not have to.

const (
	screenCols = 400
	screenRows = 200
)

// ScreenRow is one line of the current frame.
type ScreenRow struct {
	// Row is the terminal line, counting from 1 as the escape sequences do.
	Row int
	// Text is the line with trailing blanks removed.
	Text string
}

type screen struct {
	cells [screenRows][screenCols]rune
	row   int // 0-based
	col   int
	// height is the terminal's real height, which is what decides when output
	// scrolls. Without it every line ever written lands on the row it names and
	// old text shows through the frame that replaced it.
	height int
	// maxRow is how far down anything has been written, so an empty grid is not
	// scanned to the bottom.
	maxRow int
}

func newScreen(height int) *screen {
	if height <= 0 || height > screenRows {
		height = defaultHeight
	}
	s := &screen{height: height}
	s.clearAll()
	return s
}

// defaultHeight is what a session is spawned with when nothing says otherwise.
const defaultHeight = 32

// scroll moves everything up one line, as a terminal does when output runs off
// the bottom. This is why the frame on screen is the frame this renders: text
// written before the last screenful has genuinely gone.
func (s *screen) scroll() {
	copy(s.cells[0:], s.cells[1:s.height])
	s.clearRow(s.height-1, 0, screenCols)
	if s.maxRow > 0 {
		s.maxRow--
	}
}

func (s *screen) clearAll() {
	for r := 0; r < screenRows; r++ {
		s.clearRow(r, 0, screenCols)
	}
	s.maxRow = 0
}

func (s *screen) clearRow(r, from, to int) {
	if r < 0 || r >= screenRows {
		return
	}
	for c := max(0, from); c < min(to, screenCols); c++ {
		s.cells[r][c] = ' '
	}
}

func (s *screen) put(ch rune) {
	if s.row >= 0 && s.row < screenRows && s.col >= 0 && s.col < screenCols {
		s.cells[s.row][s.col] = ch
		if s.row > s.maxRow {
			s.maxRow = s.row
		}
	}
	s.col++
}

// Screen renders the terminal tail at the default height.
func Screen(tail string) []ScreenRow { return ScreenSize(tail, defaultHeight) }

// ScreenSize renders the tail as a terminal of the given height and returns its
// non-blank rows in order.
func ScreenSize(tail string, height int) []ScreenRow {
	s := newScreen(height)
	s.feed(tail)

	var out []ScreenRow
	for r := 0; r < s.height; r++ {
		text := strings.TrimRight(string(s.cells[r][:]), " ")
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, ScreenRow{Row: r + 1, Text: text})
	}
	return out
}

func (s *screen) feed(in string) {
	rs := []rune(in)
	for i := 0; i < len(rs); {
		c := rs[i]
		if c == 0x1b {
			i += s.escape(rs[i:])
			continue
		}
		switch c {
		case '\r':
			s.col = 0
		case '\n':
			s.col = 0
			if s.row >= s.height-1 {
				s.scroll()
			} else {
				s.row++
			}
		case '\b':
			if s.col > 0 {
				s.col--
			}
		case '\t':
			s.col = (s.col/8 + 1) * 8
		default:
			if c >= 0x20 {
				s.put(c)
			}
		}
		i++
	}
}

// escape consumes one escape sequence and returns how many runes it took.
func (s *screen) escape(rs []rune) int {
	if len(rs) < 2 {
		return len(rs)
	}
	switch rs[1] {
	case ']':
		// OSC, terminated by BEL or ST.
		for i := 2; i < len(rs); i++ {
			if rs[i] == 0x07 {
				return i + 1
			}
			if rs[i] == 0x1b && i+1 < len(rs) && rs[i+1] == '\\' {
				return i + 2
			}
		}
		return len(rs)
	case '[':
		// CSI: parameters, then a final byte.
		i := 2
		for i < len(rs) && (rs[i] == ';' || rs[i] == '?' || (rs[i] >= '0' && rs[i] <= '9')) {
			i++
		}
		if i >= len(rs) {
			return len(rs)
		}
		s.csi(string(rs[2:i]), rs[i])
		return i + 1
	default:
		// Two-byte sequences, and charset selections which take one more.
		if rs[1] == '(' || rs[1] == ')' {
			return min(3, len(rs))
		}
		return 2
	}
}

func (s *screen) csi(params string, final rune) {
	if strings.HasPrefix(params, "?") {
		// Private modes: cursor visibility, bracketed paste, and the like.
		return
	}
	p := parseParams(params)
	at := func(i, def int) int {
		if i < len(p) && p[i] > 0 {
			return p[i]
		}
		return def
	}

	switch final {
	case 'H', 'f': // absolute position, 1-based
		s.row = at(0, 1) - 1
		s.col = at(1, 1) - 1
	case 'A':
		s.row -= at(0, 1)
	case 'B':
		s.row += at(0, 1)
	case 'C':
		// Forward. The cells skipped keep what they already held, which is the
		// whole reason this is a grid.
		s.col += at(0, 1)
	case 'D':
		s.col -= at(0, 1)
	case 'G':
		s.col = at(0, 1) - 1
	case 'd':
		s.row = at(0, 1) - 1
	case 'K': // erase in line
		switch at(0, 0) {
		case 1:
			s.clearRow(s.row, 0, s.col+1)
		case 2:
			s.clearRow(s.row, 0, screenCols)
		default:
			s.clearRow(s.row, s.col, screenCols)
		}
	case 'J': // erase in display
		switch at(0, 0) {
		case 1:
			for r := 0; r < s.row; r++ {
				s.clearRow(r, 0, screenCols)
			}
			s.clearRow(s.row, 0, s.col+1)
		case 2, 3:
			s.clearAll()
			s.row, s.col = 0, 0
		default:
			s.clearRow(s.row, s.col, screenCols)
			for r := s.row + 1; r < screenRows; r++ {
				s.clearRow(r, 0, screenCols)
			}
		}
	case 'X': // erase characters, in place
		s.clearRow(s.row, s.col, s.col+at(0, 1))
	}

	s.row = clamp(s.row, 0, screenRows-1)
	s.col = clamp(s.col, 0, screenCols-1)
	if s.row > s.maxRow {
		s.maxRow = s.row
	}
}

func parseParams(s string) []int {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ";")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, _ := strconv.Atoi(p)
		out[i] = n
	}
	return out
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }
