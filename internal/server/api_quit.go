package server

import (
	"net/http"
	"time"
)

/* Stopping the app from the app.

   For a long time the only way out was Ctrl-C in the console, which was fine
   while there was always a console. There is not any more — a desktop app does
   not leave a black box behind its window — and something had to take its
   place before the console could go anywhere.

   So: the window's close button, the notification icon's menu, and this. All
   three end up in the same place, which is main's single shutdown: record what
   was running for the next launch, stop every session, close the listener.

   Guarded exactly like everything else. Someone who can reach this endpoint can
   already drive every terminal it would be stopping, so quitting is the least
   of what the token protects. */

// OnQuit registers what to do when the UI asks the app to stop. main owns the
// shutdown; the server only passes the request along.
func (s *Server) OnQuit(f func()) {
	s.quitMu.Lock()
	s.onQuit = f
	s.quitMu.Unlock()
}

// POST /api/quit
func (s *Server) quit(w http.ResponseWriter, r *http.Request) {
	s.quitMu.Lock()
	f := s.onQuit
	s.quitMu.Unlock()

	if f == nil {
		writeJSON(w, http.StatusOK, map[string]string{
			"status": "this build has no way to stop itself; use Ctrl-C in the terminal",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "stopping"})

	// Answer first, stop second. Shutting down inside the handler closes the
	// listener under the reply, and the page then reports a network error for
	// something that worked perfectly.
	go func() {
		time.Sleep(200 * time.Millisecond)
		f()
	}()
}
