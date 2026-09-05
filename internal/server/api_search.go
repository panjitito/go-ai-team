package server

import (
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/uniair/go-ai-team/internal/claudefs"
	"github.com/uniair/go-ai-team/internal/store"
)

// searchHit is one match, dressed for the UI: the account and agent named
// rather than pointed at by a directory path or an id.
type searchHit struct {
	claudefs.Hit
	Account string `json:"account"`
	// ProjectName is the directory the conversation ran in, named.
	ProjectName string `json:"projectName,omitempty"`
	// LiveSession is the app's own session id when that conversation is still
	// open in a running agent, so the result can lead somewhere. Empty means the
	// conversation is history.
	LiveSession string `json:"liveSession,omitempty"`
	AgentName   string `json:"agentName,omitempty"`
}

type searchResponse struct {
	Hits      []searchHit `json:"hits"`
	Files     int         `json:"files"`
	Total     int         `json:"total"`
	Bytes     int64       `json:"bytes"`
	Truncated bool        `json:"truncated"`
	Millis    int64       `json:"millis"`
}

// GET /api/search?q=…&projectId=…&limit=…
//
// Scoped to a project by default, because that is both the useful question and
// the fast one: the largest project on the machine this was written for is
// 264 MB and scans in under a second, while everything at once is 2.4 GB.
// Without a projectId it searches every conversation in every account, newest
// first, and stops when it runs out of budget rather than out of transcripts.
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, searchResponse{Hits: []searchHit{}})
		return
	}

	cwd := ""
	if pid := r.URL.Query().Get("projectId"); pid != "" {
		p, err := s.st.Project(pid)
		if err != nil {
			writeErr(w, statusFor(err), err)
			return
		}
		cwd = p.Path
	}

	limit := 120
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	started := time.Now()
	res := claudefs.Search(claudefs.SearchOpts{
		Dirs:  s.accs.Dirs(store.ProviderClaude),
		CWD:   cwd,
		Query: q,
		Limit: limit,
	})

	out := searchResponse{
		Hits:      make([]searchHit, 0, len(res.Hits)),
		Files:     res.Files,
		Total:     res.Total,
		Bytes:     res.Bytes,
		Truncated: res.Truncated,
		Millis:    time.Since(started).Milliseconds(),
	}

	// A conversation that is still open should lead to the agent that has it,
	// which is the difference between reading history and going back to work.
	live := map[string]string{}
	agents := map[string]string{}
	for _, sess := range s.sm.Sessions() {
		pub := sess.Public()
		if pub.ClaudeSessionID == "" {
			continue
		}
		live[pub.ClaudeSessionID] = pub.ID
		if a, err := s.st.Agent(pub.AgentID); err == nil && a != nil {
			agents[pub.ClaudeSessionID] = a.Name
		}
	}

	names := map[string]string{}
	for _, a := range s.st.AccountsFor(store.ProviderClaude) {
		names[a.Dir] = a.Name
	}

	// Which project a hit came from, when the search was not confined to one.
	// Keyed on the working directory the record carries, so a conversation from
	// a folder the app has never been told about still gets a name.
	projects := map[string]string{}
	for _, p := range s.st.Projects() {
		projects[strings.ToLower(filepath.Clean(p.Path))] = p.Name
	}

	for _, h := range res.Hits {
		out.Hits = append(out.Hits, searchHit{
			Hit:         h,
			Account:     names[h.Dir],
			ProjectName: projectLabel(projects, h.CWD),
			LiveSession: live[h.SessionID],
			AgentName:   agents[h.SessionID],
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// projectLabel names the directory a conversation ran in.
//
// A project the app knows gets the name it was given. One it does not — an old
// conversation from a folder nobody added, or one added and since removed —
// gets the folder's own name, which is still what a person calls it.
//
// Matched case-insensitively because this is Windows and the same directory
// arrives spelled both ways.
func projectLabel(known map[string]string, cwd string) string {
	if cwd == "" {
		return ""
	}
	if n, ok := known[strings.ToLower(filepath.Clean(cwd))]; ok {
		return n
	}
	return filepath.Base(filepath.Clean(cwd))
}
