package server

import (
	"fmt"
	"net/http"
	"strings"
)

// Driving a running session the way its own keyboard does.
//
// Three things Claude Code does from the keyboard had no equivalent here, and
// each one is something you reach for many times an hour: answering a permission
// prompt, stopping a turn that has gone the wrong way, and changing the
// permission mode. All three go through the CLI's own interface — a digit, an
// Escape, a shift+tab — so the CLI stays the thing making the decisions and this
// is only a keyboard that happens to be in a browser.

type commandReq struct {
	Text string `json:"text"`
}

// runCommand sends the CLI one of its own slash commands.
//
// Separate from the prompt endpoint because the two are proved differently. A
// prompt is confirmed by the transcript growing; a slash command never becomes a
// turn, so that proof reports failure over a command that worked perfectly —
// which is what the run bar was doing on every model and effort change.
func (s *Server) runCommand(w http.ResponseWriter, r *http.Request) {
	var req commandReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !strings.HasPrefix(strings.TrimSpace(req.Text), "/") {
		writeErr(w, http.StatusBadRequest,
			fmt.Errorf("that is not a slash command; send ordinary text as a message instead"))
		return
	}
	if err := s.sm.SendCommand(r.PathValue("id"), strings.TrimSpace(req.Text)); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type answerReq struct {
	Option int `json:"option"`
}

// answerAsk picks one of the numbered options the CLI is offering.
func (s *Server) answerAsk(w http.ResponseWriter, r *http.Request) {
	var req answerReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.sm.Answer(r.PathValue("id"), req.Option); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// interruptSession stops the current turn and leaves the session running.
func (s *Server) interruptSession(w http.ResponseWriter, r *http.Request) {
	if err := s.sm.Interrupt(r.PathValue("id")); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type modeReq struct {
	Mode string `json:"mode"`
}

// setMode cycles the session into a permission mode.
//
// The mode it actually landed in comes back either way. Cycling can end
// somewhere else — a model with no auto mode simply does not have it in the
// cycle — and the honest thing is to say where it ended up rather than to report
// success and let the UI show a mode the session is not in.
func (s *Server) setMode(w http.ResponseWriter, r *http.Request) {
	var req modeReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	got, err := s.sm.SetMode(r.PathValue("id"), req.Mode)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": err.Error(),
			"mode":  got,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "mode": got})
}
