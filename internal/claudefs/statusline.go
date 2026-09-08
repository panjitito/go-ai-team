package claudefs

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strings"
)

// The status line, from the other end.
//
// internal/session reads these numbers off the terminal, because they exist
// nowhere else: a transcript carries token counts per message and no rate
// limits at all. The scrape works only when something is printing them, and
// what prints them is a status-line command the user has configured. On an
// account with none, the strip above the composer is permanently blank with
// nothing on screen saying why.
//
// So the app can be that command. `go-ai-team statusline` reads the payload
// Claude Code hands a status-line program on stdin and writes one line back.
// Installing it is opt-in per account and reversible, and it needs nothing that
// is not already on the machine: no Node, no shell, just the binary that is
// already running.
//
// The format is the one internal/session/statusline.go parses, because the two
// have to agree and there is no reason for a second one.

// StatusPayload is the part of Claude Code's status-line JSON this reads.
// Everything is optional: a field that is absent is left out of the line rather
// than printed as a zero, and the parser at the other end tells those apart.
type StatusPayload struct {
	Workspace struct {
		CurrentDir string `json:"current_dir"`
	} `json:"workspace"`
	CWD   string `json:"cwd"`
	Model struct {
		DisplayName string `json:"display_name"`
		ID          string `json:"id"`
	} `json:"model"`
	ContextWindow struct {
		UsedPercentage *float64 `json:"used_percentage"`
	} `json:"context_window"`
	Cost struct {
		TotalCostUSD *float64 `json:"total_cost_usd"`
	} `json:"cost"`
	RateLimits struct {
		FiveHour struct {
			UsedPercentage *float64 `json:"used_percentage"`
		} `json:"five_hour"`
		SevenDay struct {
			UsedPercentage *float64 `json:"used_percentage"`
		} `json:"seven_day"`
	} `json:"rate_limits"`
}

// StatusLineText renders one status line from the payload.
//
// Plain text, no colour. The line is read back by a regex over terminal bytes
// with the escapes stripped, so colouring it would only add work at both ends.
// Order is fixed, which keeps the line stable on screen as fields come and go.
func StatusLineText(p StatusPayload) string {
	var seg []string

	dir := p.Workspace.CurrentDir
	if dir == "" {
		dir = p.CWD
	}
	if dir != "" {
		seg = append(seg, filepath.Base(strings.TrimRight(dir, `/\`)))
	}
	if n := strings.TrimSpace(p.Model.DisplayName); n != "" {
		seg = append(seg, n)
	}
	if v := p.ContextWindow.UsedPercentage; v != nil {
		seg = append(seg, fmt.Sprintf("ctx:%d%%", pct(*v)))
	}
	if v := p.Cost.TotalCostUSD; v != nil {
		seg = append(seg, fmt.Sprintf("$%.2f", math.Max(0, *v)))
	}
	if v := p.RateLimits.FiveHour.UsedPercentage; v != nil {
		seg = append(seg, fmt.Sprintf("5h:%d%%", pct(*v)))
	}
	if v := p.RateLimits.SevenDay.UsedPercentage; v != nil {
		seg = append(seg, fmt.Sprintf("wk:%d%%", pct(*v)))
	}
	return strings.Join(seg, " ")
}

// pct rounds a percentage and clamps it. A figure outside 0..100 is a bug
// somewhere upstream and a meter cannot draw it either way.
func pct(v float64) int {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return int(math.Round(v))
}

// RunStatusLine reads a payload from in and writes the line to out.
//
// A malformed payload prints nothing and reports no error. This runs inside the
// CLI's own render loop, several times a second, and a status-line command that
// fails noisily would put its complaint on the user's screen in place of the
// line it could not draw.
func RunStatusLine(in io.Reader, out io.Writer) error {
	body, err := io.ReadAll(io.LimitReader(in, 1<<20))
	if err != nil {
		return nil
	}
	var p StatusPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil
	}
	line := StatusLineText(p)
	if line == "" {
		return nil
	}
	_, err = io.WriteString(out, line)
	return err
}
