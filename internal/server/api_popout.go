package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"

	"github.com/panjitito/go-ai-team/internal/browser"
	"github.com/panjitito/go-ai-team/internal/desktop"
)

/* Opening an agent, or a project, in a window of its own.

   One window is the wrong shape for the way this is used. An agent runs for
   half an hour and you want to watch it while working in another, which on a
   desk with two monitors means two windows rather than two tabs in one.

   The client asks for a *target* — this session, this project — and never for a
   URL. The server composes the address from the one it is actually listening
   on. That is not ceremony: a handler that opens a native window on whatever
   address it is handed is a handler that will one day be handed somebody else's
   address, and the Host header is not something to trust with a window.
*/

type windowReq struct {
	SessionID string `json:"sessionId"`
	ProjectID string `json:"projectId"`
	// Title is what to put on the window. Cosmetic, and clipped.
	Title string `json:"title"`
}

type windowResp struct {
	// Opened says a real window is on screen. When it is false the client is
	// expected to open the URL itself — a second tab is a poor substitute for a
	// second window, but it is a great deal better than a button that does
	// nothing on a phone.
	Opened bool   `json:"opened"`
	URL    string `json:"url"`
	Reason string `json:"reason,omitempty"`
}

// POST /api/windows
func (s *Server) openWindow(w http.ResponseWriter, r *http.Request) {
	var req windowReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	target, err := s.windowURL(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	// Only for whoever is at the machine. A phone asking would otherwise open a
	// window on a desk it cannot see, which is a surprise rather than a feature.
	if !isLoopbackAddr(r.RemoteAddr) {
		writeJSON(w, http.StatusOK, windowResp{
			URL:    target,
			Reason: "windows open on the machine running the app; this opens a tab instead",
		})
		return
	}

	title := req.Title
	if title == "" {
		title = "Go AI Team"
	}
	if len(title) > 80 {
		title = title[:80]
	}

	if err := s.openNativeWindow(target, title); err != nil {
		// Not an error the caller has to handle: the client opens a tab, which
		// is the same page. Saying why keeps that from looking like a fault.
		writeJSON(w, http.StatusOK, windowResp{URL: target, Reason: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, windowResp{Opened: true, URL: target})
}

// openNativeWindow opens the window the same way the app opened its first one.
func (s *Server) openNativeWindow(target, title string) error {
	if s.windowMode == browser.ModeDesktop {
		return desktop.OpenWindow(target, title, filepath.Join(s.st.RootDir(), "window"))
	}
	// Started in a browser, so a second window is another browser window on the
	// app's own profile — chromeless, and not a tab in whatever else is open.
	if s.windowMode == browser.ModeNone {
		return errors.New("this instance was started without a UI to open windows from")
	}
	_, err := browser.Open(browser.Opts{
		URL:        target,
		ProfileDir: filepath.Join(s.st.RootDir(), "browser"),
		Mode:       browser.ModeApp,
	})
	return err
}

// windowURL turns a target into an address on this server.
func (s *Server) windowURL(req windowReq) (string, error) {
	base := s.baseURL
	if base == "" {
		return "", errors.New("this instance does not know its own address")
	}
	q := url.Values{"popout": {"1"}}

	switch {
	case req.SessionID != "":
		if _, ok := s.sm.Get(req.SessionID); !ok {
			return "", fmt.Errorf("that session is not running")
		}
		q.Set("session", req.SessionID)
	case req.ProjectID != "":
		p, err := s.st.Project(req.ProjectID)
		if err != nil {
			return "", err
		}
		q.Set("project", p.ID)
	default:
		return "", errors.New("say which session or project to open")
	}
	return base + "/?" + q.Encode(), nil
}

// SetWindowing tells the server its own address and how the app's first window
// was opened, which is everything a pop-out needs to be opened the same way.
//
// Set after construction because main only settles the mode once it knows
// whether a native window is available on this machine, and that check has to
// happen before anything is shown.
func (s *Server) SetWindowing(baseURL string, mode browser.Mode) {
	s.baseURL = baseURL
	s.windowMode = mode
}
