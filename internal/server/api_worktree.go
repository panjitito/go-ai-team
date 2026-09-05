package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/uniair/go-ai-team/internal/gitx"
	"github.com/uniair/go-ai-team/internal/store"
)

// Giving an agent a checkout of its own.
//
// Running several agents on one repository is the thing this app is for, and it
// is also the thing that goes wrong: they edit the same files, and one's
// half-finished change silently becomes another's starting point. Nothing warns
// you — the second agent simply reads a file mid-edit and reasons about it.
//
// A worktree is git's own answer. Each agent gets its own directory and its own
// branch off the same history, works without interference, and the results are
// merged deliberately instead of by accident.
//
// The trees live in the app's state directory, not inside the repository. Inside
// it they would appear in the file browser, in `git status`, and in every other
// agent's view of the project — and a worktree is not part of the work.

// WorktreeRoot is where an agent's checkouts are kept.
func WorktreeRoot(state string) string { return filepath.Join(state, "worktrees") }

// worktreePath is where one agent's checkout goes. Keyed on the ids rather than
// the names, so renaming an agent does not strand its work somewhere else.
func worktreePath(state, projectID, agentID string) string {
	return filepath.Join(WorktreeRoot(state), projectID, agentID)
}

// branchUnsafe matches everything git will not accept in a branch name.
var branchUnsafe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// branchFor names an agent's branch after the agent, because the name is read
// by a person deciding what to merge.
func branchFor(a *store.Agent) string {
	slug := strings.Trim(branchUnsafe.ReplaceAllString(strings.ToLower(a.Name), "-"), "-.")
	if slug == "" {
		slug = "agent"
	}
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-.")
	}
	// The id keeps two agents called "Backend" apart, and keeps the branch
	// stable when one is renamed.
	short := a.ID
	if i := strings.LastIndex(short, "_"); i >= 0 && i+1 < len(short) {
		short = short[i+1:]
	}
	if len(short) > 6 {
		short = short[:6]
	}
	return "agent/" + slug + "-" + short
}

// agentWorkdir decides where an agent runs.
//
// The project directory unless the agent asked for a tree of its own, and the
// project directory anyway if it is not a git repository — a worktree of nothing
// is not a thing, and refusing to start would be a worse answer than starting
// where it always did.
func (s *Server) agentWorkdir(ctx context.Context, a *store.Agent, p *store.Project) (string, string, error) {
	if !a.Worktree {
		return p.Path, "", nil
	}
	if !gitx.IsRepo(p.Path) {
		return p.Path, "", nil
	}
	path := worktreePath(s.st.RootDir(), p.ID, a.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", err
	}
	wt, err := gitx.AddWorktree(ctx, p.Path, path, branchFor(a))
	if err != nil {
		return "", "", fmt.Errorf("could not give %s a worktree: %w", a.Name, err)
	}
	return wt.Path, wt.Branch, nil
}

// listWorktrees reports the checkouts of one project, with the agent each
// belongs to.
func (s *Server) listWorktrees(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.Project(r.URL.Query().Get("projectId"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	if !gitx.IsRepo(p.Path) {
		writeJSON(w, http.StatusOK, []worktreeView{})
		return
	}
	list, err := gitx.ListWorktrees(r.Context(), p.Path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	// Which tree belongs to which agent, so the UI can say so rather than
	// showing a column of paths.
	owner := map[string]*store.Agent{}
	for _, a := range s.st.Agents() {
		if a.ProjectID != p.ID {
			continue
		}
		owner[strings.ToLower(worktreePath(s.st.RootDir(), p.ID, a.ID))] = a
	}

	out := make([]worktreeView, 0, len(list))
	for _, wt := range list {
		v := worktreeView{Path: wt.Path, Branch: wt.Branch, Head: wt.Head, Main: wt.Main}
		if a := owner[strings.ToLower(filepath.Clean(wt.Path))]; a != nil {
			v.AgentID, v.AgentName = a.ID, a.Name
		}
		if _, live := s.sm.SessionForAgent(v.AgentID); live && v.AgentID != "" {
			v.Running = true
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

type worktreeView struct {
	Path      string `json:"path"`
	Branch    string `json:"branch"`
	Head      string `json:"head"`
	Main      bool   `json:"main"`
	AgentID   string `json:"agentId,omitempty"`
	AgentName string `json:"agentName,omitempty"`
	Running   bool   `json:"running"`
}

type removeWorktreeReq struct {
	ProjectID string `json:"projectId"`
	Path      string `json:"path"`
	// Force throws away uncommitted work, and is only ever set by a person who
	// has been told that is what it does.
	Force bool `json:"force"`
}

// removeWorktree deletes one checkout.
func (s *Server) removeWorktree(w http.ResponseWriter, r *http.Request) {
	var req removeWorktreeReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p, err := s.st.Project(req.ProjectID)
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}

	// Only trees this app made, and never the project itself. A path from a
	// request is not permission to run `git worktree remove` on anything on the
	// disk.
	clean := filepath.Clean(req.Path)
	if !insideDir(WorktreeRoot(s.st.RootDir()), clean) {
		writeErr(w, http.StatusBadRequest,
			fmt.Errorf("that is not a worktree this app created, so it is not ours to remove"))
		return
	}
	// A running agent is still using it.
	for _, a := range s.st.Agents() {
		if strings.EqualFold(worktreePath(s.st.RootDir(), p.ID, a.ID), clean) {
			if _, live := s.sm.SessionForAgent(a.ID); live {
				writeErr(w, http.StatusConflict,
					fmt.Errorf("%s is still running in that worktree — stop it first", a.Name))
				return
			}
		}
	}

	if err := gitx.RemoveWorktree(r.Context(), p.Path, clean, req.Force); err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// insideDir reports whether path is within root, after resolving both. Symlinks
// on both sides, because comparing an unresolved path against a resolved root is
// how a guard like this gets walked around.
func insideDir(root, path string) bool {
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		r = filepath.Clean(root)
	}
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		p = filepath.Clean(path)
	}
	rel, err := filepath.Rel(r, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
