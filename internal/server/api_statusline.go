package server

import (
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/panjitito/go-ai-team/internal/claudefs"
	"github.com/panjitito/go-ai-team/internal/session"
)

// Why the strip above the composer is empty, and what to do about it.
//
// The five-hour and weekly figures are read off the terminal, and they get
// there only if the account has a status-line command configured. Two accounts
// out of three on the machine this was written for had none, so the strip was
// blank forever with nothing on screen saying so. It looked like the app
// failing rather than a setting that was never made.
//
// A blank strip has two causes and they want different answers, so the API
// distinguishes them: no command configured, which stays blank until somebody
// adds one, and nothing scraped yet, which fixes itself.

// statusLineNote explains an empty status line, and says whether installing one
// would fill it.
func (s *Server) statusLineNote(line session.StatusLine, accountDir string) (string, bool) {
	// Anything at all on screen needs no explanation.
	if line.HasFiveHour || line.HasWeekly || line.HasContext || line.HasCost {
		return "", false
	}
	if accountDir == "" {
		return "", false
	}
	if _, ok := claudefs.StatusLineOf(accountDir); ok {
		// Configured, and nothing read yet. It arrives on its own.
		return "Waiting for the first status line from this session.", false
	}
	return "This account has no status-line command, so the CLI prints no usage figures and there is nothing to read. " +
		"Nowhere else has them: a transcript carries token counts and no rate limits.", true
}

// selfPath is the binary to put in a status-line command.
//
// os.Executable rather than os.Args[0], which is whatever the launcher typed
// and may be a bare name resolved off PATH. The setting outlives the shell that
// started this process, so it has to be a path that still works without one.
func selfPath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", errors.New("cannot work out where this program lives, so there is no command to install")
	}
	return p, nil
}

type statusLineReq struct {
	// Replace overwrites a status line somebody else configured. Off by
	// default: a command already there is somebody's own setup.
	Replace bool `json:"replace"`
}

// GET /api/accounts/{id}/statusline
//
// What is configured for this account, and whether it is ours.
func (s *Server) getAccountStatusLine(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.Account(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	sl, ok := claudefs.StatusLineOf(a.Dir)
	would := ""
	if exe, err := selfPath(); err == nil {
		would = statusLineCommandFor(exe)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": ok,
		"ours":       ok && claudefs.IsOwnStatusLine(sl),
		// The command is shown so somebody can see what would be replaced. It
		// is a path they wrote, not a credential.
		"command": sl.Command,
		// And what would be written, so agreeing to it is agreeing to
		// something visible rather than to a description of it.
		"wouldInstall": would,
	})
}

// PUT /api/accounts/{id}/statusline
//
// Points the account's status line at this binary. Opt-in, per account, and
// undone by the DELETE below.
func (s *Server) putAccountStatusLine(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.Account(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	var req statusLineReq
	if r.ContentLength > 0 {
		if err := decode(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	exe, err := selfPath()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := claudefs.InstallStatusLine(a.Dir, exe, req.Replace); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sl, _ := claudefs.StatusLineOf(a.Dir)
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": true,
		"ours":       true,
		"command":    sl.Command,
		// Said plainly, because it is the surprising part: a session already
		// running does not pick this up.
		"note": "Agents already running on " + a.Name + " keep the status line they started with. Restart one to see the figures.",
	})
}

// DELETE /api/accounts/{id}/statusline
//
// Takes ours back out, and refuses to remove one it did not install.
func (s *Server) deleteAccountStatusLine(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.Account(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	if err := claudefs.RemoveStatusLine(a.Dir); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": false, "ours": false})
}

// statusLineCommandFor is the command this app would install, for showing in
// the UI before anybody agrees to it. Exported through the API rather than
// built in the browser, because only this process knows where it lives.
func statusLineCommandFor(exe string) string {
	if strings.ContainsAny(exe, " \t") {
		return `"` + exe + `" statusline`
	}
	return exe + " statusline"
}
