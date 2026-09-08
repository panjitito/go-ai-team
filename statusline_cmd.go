package main

import (
	"os"

	"github.com/panjitito/go-ai-team/internal/claudefs"
)

// runStatusLine is `go-ai-team statusline`, the status-line command the app can
// install into an account so the numbers it displays actually exist.
//
// Claude Code writes a JSON payload to a status-line program's stdin and puts
// whatever it prints along the bottom of the terminal. That line is the only
// place the five-hour and weekly figures appear: a transcript has token counts
// and no rate limits, so with nothing printing them there is nothing to read.
//
// It exits zero whatever happens. This runs inside the CLI's render loop
// several times a second, and a status-line command that fails loudly writes
// its complaint across the screen it was meant to decorate.
func runStatusLine() {
	_ = claudefs.RunStatusLine(os.Stdin, os.Stdout)
}
