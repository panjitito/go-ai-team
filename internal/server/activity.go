package server

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/panjitito/go-ai-team/internal/session"
)

/* What happened while you were not looking.

   The app is built for leaving agents running and going away, and until now
   coming back told you only where things stand: this one is waiting, that one
   died. Not that it died at 02:14 having asked a question at 02:09 that nobody
   answered, or that an account ran out of quota at midnight and the work moved
   somewhere else. The events all existed — they are what the board is drawn
   from — and every one of them was thrown away the moment it had been drawn.

   So they are kept. In memory and bounded, because this is a record of a
   session's evening rather than an audit log, and a file that grows forever is
   a different feature with different promises. */

// Activity is one thing that happened, in the words the UI shows.
type Activity struct {
	At          time.Time `json:"at"`
	Type        string    `json:"type"`
	SessionID   string    `json:"sessionId,omitempty"`
	AgentID     string    `json:"agentId,omitempty"`
	AgentName   string    `json:"agentName,omitempty"`
	ProjectID   string    `json:"projectId,omitempty"`
	ProjectName string    `json:"projectName,omitempty"`
	Text        string    `json:"text"`
	// Level is how it should read: "info", "warn" for something wanting a
	// person, "bad" for something that went wrong.
	Level string `json:"level"`
}

// activityKeep is how many entries are held.
//
// An agent working through a long evening produces a handful an hour, not a
// stream: a start, a question, a turn ending, a switch. A thousand covers a
// night across a dozen agents and costs a few hundred kilobytes.
const activityKeep = 1000

type journal struct {
	mu      sync.RWMutex
	entries []Activity
}

func (j *journal) add(a Activity) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries = append(j.entries, a)
	if over := len(j.entries) - activityKeep; over > 0 {
		// Copy down rather than reslice: reslicing keeps the whole backing
		// array alive, so a long-running app would hold every entry it ever
		// dropped.
		j.entries = append(j.entries[:0], j.entries[over:]...)
	}
}

// list returns up to n entries, newest first.
func (j *journal) list(n int) []Activity {
	j.mu.RLock()
	defer j.mu.RUnlock()
	if n <= 0 || n > len(j.entries) {
		n = len(j.entries)
	}
	out := make([]Activity, 0, n)
	for i := len(j.entries) - 1; i >= len(j.entries)-n; i-- {
		out = append(out, j.entries[i])
	}
	return out
}

// watchActivity records the events worth remembering.
//
// It subscribes like any other client, so nothing had to be added to the
// session manager to make this work, and a slow write here can never stall a
// PTY — emit drops rather than blocks.
func (s *Server) watchActivity() {
	events, _ := s.sm.Subscribe()
	for e := range events {
		if a, ok := s.describe(e); ok {
			s.log.add(a)
		}
	}
}

// describe turns an event into a line, or says it is not worth one.
func (s *Server) describe(e session.Event) (Activity, bool) {
	a := Activity{
		At:        time.Now(),
		Type:      e.Type,
		SessionID: e.SessionID,
		AgentID:   e.AgentID,
		Level:     "info",
	}

	pub, hasPub := e.Payload.(session.PublicSession)
	if hasPub {
		a.ProjectID = pub.ProjectID
		if a.SessionID == "" {
			a.SessionID = pub.ID
		}
		if a.AgentID == "" {
			a.AgentID = pub.AgentID
		}
	}

	switch e.Type {
	case "session.started":
		a.Text = "Started"
		if hasPub && pub.AccountName != "" {
			a.Text = "Started on " + pub.AccountName
		}

	case "session.needs-you":
		a.Level = "warn"
		a.Text = "Asked a question"
		if q := strings.TrimSpace(e.Message); q != "" {
			a.Text = "Asked: " + oneLine(q, 160)
		}

	case "session.answered":
		a.Text = "Answered, and carried on"

	case "session.status":
		// Only the end of a turn. The other direction is "it started drawing
		// again", which is every turn of every agent and says nothing.
		if !hasPub || pub.Status != session.StatusWaiting {
			return Activity{}, false
		}
		a.Text = "Finished a turn"

	case "session.exited":
		a.Text = "Session ended"
		if hasPub {
			switch {
			case pub.Error != "":
				a.Level = "bad"
				a.Text = "Ended with an error: " + oneLine(pub.Error, 160)
			case pub.ExitCode != 0:
				a.Level = "bad"
				a.Text = fmt.Sprintf("Ended with exit code %d", pub.ExitCode)
			}
		}

	case "session.note":
		a.Text = oneLine(e.Message, 200)

	case "switch.done":
		a.Level = "warn"
		a.Text = oneLine(e.Message, 200)

	case "switch.failed":
		a.Level = "bad"
		a.Text = oneLine(e.Message, 200)

	case "account.benched":
		a.Level = "warn"
		a.Text = oneLine(e.Message, 200)

	case "account.unbenched":
		a.Text = oneLine(e.Message, 200) + " is back in the pool"

	default:
		// session.tokens fires every few seconds for every busy agent, and
		// session.identified and session.removed are bookkeeping.
		return Activity{}, false
	}

	s.nameIt(&a)
	return a, true
}

// nameIt fills in who and where, because an id is not a thing anybody can read.
func (s *Server) nameIt(a *Activity) {
	if a.AgentID != "" {
		if ag, err := s.st.Agent(a.AgentID); err == nil && ag != nil {
			a.AgentName = ag.Name
			if a.ProjectID == "" {
				a.ProjectID = ag.ProjectID
			}
		}
	}
	if a.ProjectID != "" {
		if p, err := s.st.Project(a.ProjectID); err == nil && p != nil {
			a.ProjectName = p.Name
		}
	}
}

// oneLine flattens a message for a single row and keeps it to a readable width.
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
	if len([]rune(s)) <= max {
		return s
	}
	return string([]rune(s)[:max-1]) + "…"
}

// GET /api/activity?limit=200&projectId=…
func (s *Server) listActivity(w http.ResponseWriter, r *http.Request) {
	limit := 300
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	pid := r.URL.Query().Get("projectId")

	// Filtering after the read rather than during it: a project with one agent
	// would otherwise be capped by how much every other project had been doing.
	all := s.log.list(0)
	out := make([]Activity, 0, min(limit, len(all)))
	for _, a := range all {
		if pid != "" && a.ProjectID != pid {
			continue
		}
		out = append(out, a)
		if len(out) >= limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, out)
}
