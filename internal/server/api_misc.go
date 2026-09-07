package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/panjitito/go-ai-team/internal/claudefs"
	"github.com/panjitito/go-ai-team/internal/gitx"
	"github.com/panjitito/go-ai-team/internal/session"
	"github.com/panjitito/go-ai-team/internal/store"
)

// Review, catalogue, layout, messaging and the AI helpers.

func (s *Server) routeMisc(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/git/status", s.gitStatus)
	mux.HandleFunc("GET /api/git/diff", s.gitDiff)
	mux.HandleFunc("POST /api/git/stage", s.gitStage)
	mux.HandleFunc("POST /api/git/unstage", s.gitUnstage)
	mux.HandleFunc("POST /api/git/commit", s.gitCommit)
	mux.HandleFunc("GET /api/git/log", s.gitLog)
	mux.HandleFunc("POST /api/git/message", s.gitMessage)
	mux.HandleFunc("POST /api/git/init", s.gitInit)
	mux.HandleFunc("POST /api/git/branch", s.gitBranch)

	mux.HandleFunc("GET /api/roles", s.listRoles)
	mux.HandleFunc("POST /api/roles/import", s.importRole)
	mux.HandleFunc("DELETE /api/roles/{id}", s.deleteRole)
	mux.HandleFunc("POST /api/roles/suggest", s.suggestRole)

	mux.HandleFunc("GET /api/panes", s.getPanes)
	mux.HandleFunc("PUT /api/panes", s.putPanes)

	mux.HandleFunc("GET /api/messages", s.listMessages)
	mux.HandleFunc("POST /api/messages", s.createMessage)
	mux.HandleFunc("DELETE /api/messages/{id}", s.deleteMessage)

	mux.HandleFunc("POST /api/ai/model", s.aiPickModel)
	mux.HandleFunc("POST /api/ai/summarise", s.aiSummarise)
	mux.HandleFunc("POST /api/ai/title", s.aiTitle)
	mux.HandleFunc("GET /api/ai/status", s.aiStatus)

	mux.HandleFunc("GET /api/stats", s.projectStats)
	mux.HandleFunc("GET /api/agents/{id}/files", s.agentFiles)
	mux.HandleFunc("POST /api/agents/{id}/morph", s.morphAgent)
	mux.HandleFunc("POST /api/agents/{id}/fork", s.forkAgent)
	mux.HandleFunc("POST /api/restore", s.doRestore)
}

// randomHex makes an unguessable token.
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// A predictable token would undermine the only gate a webhook has, so
		// failing loudly beats carrying on with weak randomness.
		panic("go-ai-team: no source of randomness: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// readLimited reads a request body with a hard cap, so an oversized delivery
// cannot exhaust memory.
func readLimited(r *http.Request, max int64) ([]byte, error) {
	defer r.Body.Close()
	b, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("payload larger than %d bytes", max)
	}
	return b, nil
}

// projectDir resolves the project id in a query string to a directory.
func (s *Server) projectDir(r *http.Request) (string, *store.Project, error) {
	pid := r.URL.Query().Get("projectId")
	if pid == "" {
		return "", nil, errors.New("projectId is required")
	}
	p, err := s.st.Project(pid)
	if err != nil {
		return "", nil, err
	}
	return p.Path, p, nil
}

// ---------- git ----------

// gitDir is the repository a project's git operations run in.
//
// Not always the project directory. A project kept as a working folder — the
// checkout in it, beside notes, credentials and a scratch script — is not a
// repository itself, and every git feature in the app was answering about the
// folder rather than about the code in it: "this project is not a git
// repository" on a checkout with a hundred commits, and an empty review pane.
//
// gitx.FindRepo settles it, and refuses to guess when there is more than one.
func (s *Server) gitDir(r *http.Request) (repo string, dir string, p *store.Project, err error) {
	dir, p, err = s.projectDir(r)
	if err != nil {
		return "", "", nil, err
	}
	repo, _ = gitx.FindRepo(dir)
	return repo, dir, p, nil
}

// repoPath works out which file a caller means, and where git can see it.
//
// The two callers name a file differently. The rail lists what an agent wrote,
// relative to the project; the review pane lists what git said, relative to the
// repository. While those are the same directory the difference never shows —
// with the repository one level down they are not the same, so both readings
// are tried and the one that exists on disk wins. A file that exists as
// neither is left as the project reading, because a deleted file still has a
// diff and is exactly what somebody wants to see.
//
// The empty string for `why` means git can answer. Anything else is the reason
// it cannot, in words meant for the person reading.
func repoPath(project, repo, path string) (rel string, why string) {
	if strings.TrimSpace(path) == "" {
		return "", "" // the whole tree
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if filepath.IsAbs(clean) {
		// Already absolute: only useful if it is inside the repository.
		if rel, err := filepath.Rel(repo, clean); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel), ""
		}
		return "", "That file is outside the repository, so there is no diff for it."
	}

	fromProject := filepath.Join(project, clean)
	fromRepo := filepath.Join(repo, clean)
	pick := fromProject
	if _, err := os.Stat(fromProject); err != nil {
		if _, err := os.Stat(fromRepo); err == nil {
			pick = fromRepo
		}
	}

	rel, err := filepath.Rel(repo, pick)
	if err != nil || strings.HasPrefix(rel, "..") {
		// Inside the project and outside the repository: the notes and scripts
		// that live beside a checkout. Saying so is more use than an empty pane.
		return "", "This file is in the project but outside the repository, so git has nothing to say about it."
	}
	return filepath.ToSlash(rel), ""
}

// repoOf is gitDir for the handlers that take the project in a JSON body rather
// than a query string. Same rule: the repository the project's work belongs to,
// which is not always the project directory.
func (s *Server) repoOf(projectID string) (repo string, p *store.Project, err error) {
	p, err = s.st.Project(projectID)
	if err != nil {
		return "", nil, err
	}
	repo, ok := gitx.FindRepo(p.Path)
	if !ok {
		return "", p, fmt.Errorf("no git repository in %s, or more than one — "+
			"point the project at the checkout itself", p.Path)
	}
	return repo, p, nil
}

// repoOrPath is the repository for a project, or the project directory when
// there is none — for the things that want somewhere to write either way.
func repoOrPath(p *store.Project) string {
	if repo, ok := gitx.FindRepo(p.Path); ok {
		return repo
	}
	return p.Path
}

// gitStatus returns the working tree plus per-agent attribution, so five agents
// writing to one repository can still be reviewed one agent at a time.
func (s *Server) gitStatus(w http.ResponseWriter, r *http.Request) {
	repo, projectDir, p, err := s.gitDir(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if repo == "" {
		writeJSON(w, http.StatusOK, map[string]any{"isRepo": false, "path": projectDir})
		return
	}
	dir := repo
	st, err := gitx.GetStatus(r.Context(), dir)
	if err != nil {
		if errors.Is(err, gitx.ErrNotRepo) {
			writeJSON(w, http.StatusOK, map[string]any{"isRepo": false, "path": dir})
			return
		}
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	// Attribute each changed path to whichever agent's transcript wrote it.
	owner := map[string]string{}
	ownerName := map[string]string{}
	for _, sess := range s.sm.Sessions() {
		ps := sess.Public()
		if ps.Kind != session.KindAgent || ps.ProjectID != p.ID || ps.ClaudeSessionID == "" {
			continue
		}
		tp, ok := claudefs.FindTranscript(ps.AccountDir, ps.CWD, ps.ClaudeSessionID)
		if !ok {
			continue
		}
		touched, err := claudefs.RelativeTouched(tp, dir)
		if err != nil {
			continue
		}
		name := ps.AgentID
		if a, err := s.st.Agent(ps.AgentID); err == nil {
			name = a.Name
		}
		for _, t := range touched {
			owner[t.Path] = ps.AgentID
			ownerName[t.Path] = name
		}
	}
	for i := range st.Files {
		key := filepath.ToSlash(st.Files[i].Path)
		if id, ok := owner[key]; ok {
			st.Files[i].AgentID = id
			st.Files[i].AgentName = ownerName[key]
		}
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) gitDiff(w http.ResponseWriter, r *http.Request) {
	repo, dir, _, err := s.gitDir(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if repo == "" {
		// X-Diff-Kind, because the body here is a sentence and the body of a
		// successful call is a unified diff, and the caller was telling them
		// apart by whether it was empty.
		//
		// It is not empty: this explanation went straight into the diff parser,
		// which found no hunks in it and said "No textual diff (binary, or no
		// change)". So a project with no repository — which is most of a new
		// one — reported every file the agent had just rewritten as unchanged.
		// The reason was right there in the response and was thrown away.
		diffMessage(w, "This project is not a git repository, so there is no diff to show.")
		return
	}
	rel, why := repoPath(dir, repo, r.URL.Query().Get("path"))
	if why != "" {
		diffMessage(w, why)
		return
	}
	staged := r.URL.Query().Get("staged") == "1"
	out, err := gitx.Diff(r.Context(), repo, rel, staged)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Diff-Kind", "diff")
	_, _ = w.Write([]byte(out))
}

// diffMessage answers the diff endpoint with prose rather than a diff, labelled
// so the caller does not have to guess which it got.
func diffMessage(w http.ResponseWriter, why string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Diff-Kind", "message")
	_, _ = w.Write([]byte(why))
}

type pathsReq struct {
	ProjectID string   `json:"projectId"`
	Paths     []string `json:"paths"`
	All       bool     `json:"all"`
}

func (s *Server) gitStage(w http.ResponseWriter, r *http.Request) {
	var req pathsReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	repo, _, err := s.repoOf(req.ProjectID)
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	if req.All {
		err = gitx.StageAll(r.Context(), repo)
	} else {
		err = gitx.Stage(r.Context(), repo, req.Paths)
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "staged"})
}

func (s *Server) gitUnstage(w http.ResponseWriter, r *http.Request) {
	var req pathsReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	repo, _, err := s.repoOf(req.ProjectID)
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	if err := gitx.Unstage(r.Context(), repo, req.Paths); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unstaged"})
}

type commitReqBody struct {
	ProjectID string `json:"projectId"`
	Message   string `json:"message"`
	StageAll  bool   `json:"stageAll"`
	// AgentID attaches that agent's conversation to the commit.
	AgentID string `json:"agentId"`
}

// gitCommit records the staged changes, optionally attaching the agent
// conversation behind them so the reasoning survives the terminal closing.
func (s *Server) gitCommit(w http.ResponseWriter, r *http.Request) {
	var req commitReqBody
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	repo, p, err := s.repoOf(req.ProjectID)
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	if req.StageAll {
		if err := gitx.StageAll(r.Context(), repo); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}

	message := req.Message
	if ctxFile, ok := s.writeCommitContext(p, req.AgentID); ok {
		// A local file rather than an external gist: the reasoning stays on the
		// user's machine and in their repository history, with nothing uploaded
		// anywhere and no account needed.
		message += "\n\nAgent-Conversation: " + ctxFile
	}

	hash, err := gitx.Commit(r.Context(), repo, message)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"hash": hash})
}

// writeCommitContext saves an agent's conversation next to the repository and
// returns a repo-relative path to reference from the commit message.
//
// Secrets are stripped before anything is written, because a transcript can
// legitimately contain a token an agent was shown.
func (s *Server) writeCommitContext(p *store.Project, agentID string) (string, bool) {
	if !s.st.Settings().GistCommitContext || agentID == "" {
		return "", false
	}
	sess, ok := s.sm.SessionForAgent(agentID)
	if !ok {
		return "", false
	}
	ps := sess.Public()
	if ps.ClaudeSessionID == "" {
		return "", false
	}
	tp, ok := claudefs.FindTranscript(ps.AccountDir, ps.CWD, ps.ClaudeSessionID)
	if !ok {
		return "", false
	}
	turns, err := claudefs.Conversation(tp, 60)
	if err != nil || len(turns) == 0 {
		return "", false
	}

	agentName := agentID
	if a, err := s.st.Agent(agentID); err == nil {
		agentName = a.Name
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Agent conversation behind this commit\n\n")
	fmt.Fprintf(&b, "- Agent: %s\n- Account: %s\n- Session: %s\n- Recorded: %s\n\n",
		agentName, ps.AccountName, ps.ClaudeSessionID, time.Now().Format(time.RFC3339))
	for _, t := range turns {
		who := "You"
		if t.Role == "assistant" {
			who = agentName
		}
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", who, redactSecrets(t.Text))
	}

	dir := filepath.Join(repoOrPath(p), ".goaiteam", "commit-context")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false
	}
	name := time.Now().Format("20060102-150405") + "-" + safeFilename(agentName) + ".md"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644); err != nil {
		return "", false
	}
	return ".goaiteam/commit-context/" + name, true
}

// redactSecrets removes anything that looks like a credential from text about
// to be written into a repository.
func redactSecrets(s string) string {
	patterns := []string{
		"sk-ant-", "sk-", "ghp_", "gho_", "github_pat_", "xoxb-", "xoxp-",
		"AKIA", "ASIA", "AIza", "eyJhbGciOi",
	}
	for _, p := range patterns {
		for {
			i := strings.Index(s, p)
			if i < 0 {
				break
			}
			end := i + len(p)
			for end < len(s) && (isTokenChar(s[end])) {
				end++
			}
			if end-i < len(p)+8 {
				// Too short to be a real credential; leave it and move past.
				s = s[:i] + strings.ToUpper(p[:1]) + s[i+1:]
				continue
			}
			s = s[:i] + "[redacted credential]" + s[end:]
		}
	}
	return s
}

func isTokenChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '_' || c == '-' || c == '.'
}

func (s *Server) gitLog(w http.ResponseWriter, r *http.Request) {
	dir, _, _, err := s.gitDir(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// A project with no repository is a normal state, not an error — the same
	// answer git/status gives. Returning a raw git failure here would make every
	// caller special-case it, and the first version did exactly that.
	if dir == "" {
		writeJSON(w, http.StatusOK, []gitx.Log{})
		return
	}
	logs, err := gitx.Recent(r.Context(), dir, 30)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if logs == nil {
		// An empty repository has no commits yet; an empty list is the honest
		// answer and keeps the response type stable.
		logs = []gitx.Log{}
	}
	writeJSON(w, http.StatusOK, orEmpty(logs))
}

type gitMessageReq struct {
	ProjectID string `json:"projectId"`
	Draft     string `json:"draft"`
}

// gitMessage writes a commit message from the real staged diff, on the user's
// own subscription rather than a metered service.
func (s *Server) gitMessage(w http.ResponseWriter, r *http.Request) {
	var req gitMessageReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	repo, _, err := s.repoOf(req.ProjectID)
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	diff, err := gitx.StagedDiff(r.Context(), repo)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(diff) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("nothing is staged, so there is no diff to describe"))
		return
	}
	if !s.ai.Available() {
		writeErr(w, http.StatusServiceUnavailable, errors.New("this needs a signed-in account"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	msg, err := s.ai.CommitMessage(ctx, repo, diff, req.Draft, s.st.Settings().CommitFormat)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}

func (s *Server) gitInit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectID string `json:"projectId"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p, err := s.st.Project(req.ProjectID)
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	// Refused rather than nested. A folder that already holds a checkout does
	// not want a second repository wrapped around it, and somebody clicking
	// "initialise" here has misread the situation rather than asked for that.
	if found, ok := gitx.FindRepo(p.Path); ok {
		writeErr(w, http.StatusBadRequest, fmt.Errorf(
			"there is already a repository at %s; this project uses that one", found))
		return
	}
	if err := gitx.Init(r.Context(), p.Path); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "initialised"})
}

func (s *Server) gitBranch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectID string `json:"projectId"`
		Name      string `json:"name"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	repo, _, err := s.repoOf(req.ProjectID)
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	if err := gitx.CreateBranch(r.Context(), repo, req.Name); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"branch": req.Name})
}

// ---------- roles ----------

func (s *Server) listRoles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, orEmpty(s.cat.All()))
}

func (s *Server) importRole(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Filename string `json:"filename"`
		Body     string `json:"body"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	role, err := s.cat.Import(req.Filename, []byte(req.Body))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, role)
}

func (s *Server) deleteRole(w http.ResponseWriter, r *http.Request) {
	if err := s.cat.Delete(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) suggestRole(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Task string `json:"task"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !s.ai.Available() {
		writeErr(w, http.StatusServiceUnavailable, errors.New("suggestions need a signed-in account"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	out, err := s.ai.SuggestAgent(ctx, req.Task, s.cat.All())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": out})
}

// ---------- panes ----------

func (s *Server) getPanes(w http.ResponseWriter, r *http.Request) {
	g, ok := s.st.PaneGroup(r.URL.Query().Get("projectId"))
	if !ok {
		writeJSON(w, http.StatusOK, store.PaneGroup{Orientation: "cols"})
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) putPanes(w http.ResponseWriter, r *http.Request) {
	var g store.PaneGroup
	if err := decode(r, &g); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if g.ProjectID == "" {
		writeErr(w, http.StatusBadRequest, errors.New("projectId is required"))
		return
	}
	if g.Orientation != "rows" {
		g.Orientation = "cols"
	}
	if err := s.st.SavePaneGroup(g); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

// ---------- agent messaging ----------

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	if aid := r.URL.Query().Get("agentId"); aid != "" {
		writeJSON(w, http.StatusOK, orEmpty(s.st.Inbox(aid)))
		return
	}
	writeJSON(w, http.StatusOK, orEmpty(s.st.Messages()))
}

func (s *Server) createMessage(w http.ResponseWriter, r *http.Request) {
	var m store.AgentMessage
	if err := decode(r, &m); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	m.ID = ""
	to, err := s.st.Agent(m.ToID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("no such recipient agent"))
		return
	}
	m.ProjectID = to.ProjectID
	if m.FromName == "" {
		m.FromName = "you"
	}
	if err := s.st.AddMessage(&m); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// If the recipient is live, deliver it into its terminal now; otherwise it
	// waits in the inbox and arrives when the agent next checks.
	if sess, ok := s.sm.SessionForAgent(m.ToID); ok {
		body := fmt.Sprintf("Message from %s — %s\n\n%s", m.FromName, m.Subject, m.Body)
		if err := s.sm.SendPrompt(sess.ID, body); err == nil {
			_, _ = s.st.UpdateMessage(m.ID, func(x *store.AgentMessage) {
				x.State = "delivered"
				x.Delivered = time.Now()
			})
		}
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) deleteMessage(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DeleteMessage(r.PathValue("id")); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------- ai helpers ----------

func (s *Server) aiStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"available": s.ai.Available(),
		"note": "Helper features run headless against your own signed-in CLI, " +
			"so they are not metered and there is no key to add.",
	})
}

func (s *Server) aiPickModel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !s.ai.Available() {
		writeErr(w, http.StatusServiceUnavailable, errors.New("this needs a signed-in account"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	pick, err := s.ai.PickModel(ctx, req.Prompt)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, pick)
}

func (s *Server) aiSummarise(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text    string `json:"text"`
		Seconds int    `json:"seconds"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !s.ai.Available() {
		writeErr(w, http.StatusServiceUnavailable, errors.New("this needs a signed-in account"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	out, err := s.ai.Summarise(ctx, req.Text, req.Seconds)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"summary": out})
}

func (s *Server) aiTitle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !s.ai.Available() {
		// A title is a convenience, so falling back to the first few words is
		// better than an error the UI has to explain.
		writeJSON(w, http.StatusOK, map[string]string{"title": firstLine(req.Text, 40)})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	out, err := s.ai.Title(ctx, req.Text)
	if err != nil || out == "" {
		writeJSON(w, http.StatusOK, map[string]string{"title": firstLine(req.Text, 40)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"title": out})
}

// ---------- stats ----------

// projectStats turns raw agent work into figures for billing, retros and cost
// control.
func (s *Server) projectStats(w http.ResponseWriter, r *http.Request) {
	pid := r.URL.Query().Get("projectId")
	type agentRow struct {
		AgentID   string  `json:"agentId"`
		Name      string  `json:"name"`
		Account   string  `json:"account"`
		Tokens    int64   `json:"tokens"`
		CacheRate float64 `json:"cacheRate"`
		Messages  int     `json:"messages"`
		ToolUses  int     `json:"toolUses"`
		Minutes   int     `json:"minutes"`
		Switches  int     `json:"switches"`
	}
	var rows []agentRow
	var total int64
	byAccount := map[string]int64{}

	for _, sess := range s.sm.Sessions() {
		p := sess.Public()
		if p.Kind != session.KindAgent {
			continue
		}
		if pid != "" && p.ProjectID != pid {
			continue
		}
		name := p.AgentID
		if a, err := s.st.Agent(p.AgentID); err == nil {
			name = a.Name
		}
		mins := int(time.Since(p.StartedAt).Minutes())
		if !p.EndedAt.IsZero() {
			mins = int(p.EndedAt.Sub(p.StartedAt).Minutes())
		}
		rows = append(rows, agentRow{
			AgentID: p.AgentID, Name: name, Account: p.AccountName,
			Tokens: p.TotalTokens, CacheRate: p.CacheHitRate,
			Messages: p.Tokens.Messages, ToolUses: p.Tokens.ToolUses,
			Minutes: mins, Switches: p.SwitchCount,
		})
		total += p.TotalTokens
		byAccount[p.AccountName] += p.TotalTokens
	}

	counts := map[string]int{}
	if pid != "" {
		counts = s.boardCounts(pid)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"agents":      rows,
		"totalTokens": total,
		"byAccount":   byAccount,
		"board":       counts,
		"note": "Token figures come from the transcripts on this machine. Work done elsewhere, " +
			"or on the web, is not counted here, so this is a local reading and not an invoice.",
	})
}

// agentFiles lists what one agent changed, read back from its own transcript.
func (s *Server) agentFiles(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.Agent(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	sess, ok := s.sm.SessionForAgent(a.ID)
	if !ok {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	ps := sess.Public()
	if ps.ClaudeSessionID == "" {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	tp, found := claudefs.FindTranscript(ps.AccountDir, ps.CWD, ps.ClaudeSessionID)
	if !found {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	p, _ := s.st.Project(a.ProjectID)
	dir := ps.CWD
	if p != nil {
		dir = p.Path
	}
	touched, err := claudefs.RelativeTouched(tp, dir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, orEmpty(touched))
}

// ---------- morph and fork ----------

type morphReq struct {
	RoleID string `json:"roleId"`
	// FreshEyes tells the agent to re-read rather than trust its own summary,
	// which is what you want before a critical review.
	FreshEyes bool `json:"freshEyes"`
}

// morphAgent changes a running agent's role in place, keeping its conversation.
//
// The role change is delivered as an instruction rather than by restarting the
// CLI, because restarting is exactly what loses the context this feature exists
// to preserve.
func (s *Server) morphAgent(w http.ResponseWriter, r *http.Request) {
	var req morphReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	a, err := s.st.Agent(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	role, ok := s.cat.Get(req.RoleID)
	if !ok {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("no role %q", req.RoleID))
		return
	}
	sess, live := s.sm.SessionForAgent(a.ID)
	if !live {
		// Not running: just change the saved role, which takes effect on start.
		_, _ = s.st.UpdateAgent(a.ID, func(x *store.Agent) {
			x.Role = role.Name
			x.Color = role.Color
			if role.Model != "" {
				x.Model = role.Model
			}
		})
		writeJSON(w, http.StatusOK, map[string]string{"status": "role changed for the next launch"})
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "You are now acting as: %s.\n\n%s\n\n", role.Name, role.Prompt)
	if req.FreshEyes {
		b.WriteString("Approach this with fresh eyes: re-read the relevant code yourself rather than " +
			"relying on what you concluded earlier in this conversation.\n\n")
	}
	b.WriteString("Keep everything you have learned so far in this session. " +
		"Acknowledge the new role in one line, then continue.")

	if err := s.sm.SendPrompt(sess.ID, b.String()); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	_, _ = s.st.UpdateAgent(a.ID, func(x *store.Agent) {
		x.Role = role.Name
		x.Color = role.Color
	})
	s.sm.Notify("agent.morphed", fmt.Sprintf("%s is now acting as %s", a.Name, role.Name),
		map[string]string{"agentId": a.ID, "role": role.Name})
	writeJSON(w, http.StatusOK, map[string]string{"status": "morphed", "role": role.Name})
}

// forkAgent creates a twin that already has the parent's conversation.
func (s *Server) forkAgent(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.Agent(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	p, err := s.st.Project(a.ProjectID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	twin := &store.Agent{
		ProjectID: a.ProjectID,
		Name:      a.Name + " (fork)",
		Role:      a.Role,
		Color:     a.Color,
		Provider:  a.Provider,
		AccountID: a.AccountID,
		Model:     a.Model,
		ExtraArgs: a.ExtraArgs,
		Env:       a.Env,
	}
	if err := s.st.AddAgent(twin); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	// Resume the same Claude session id in the twin. Claude's own --resume is
	// what makes the fork carry the real conversation rather than a summary of
	// it; a provider without that support simply starts fresh, and the response
	// says which happened.
	resume := ""
	if sess, ok := s.sm.SessionForAgent(a.ID); ok {
		resume = sess.Public().ClaudeSessionID
	}

	twinEnv, err := s.agentEnv(twin)
	if err != nil {
		_ = s.st.DeleteAgent(twin.ID)
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	ns, err := s.sm.Spawn(session.SpawnOpts{
		Kind:      session.KindAgent,
		AgentID:   twin.ID,
		ProjectID: a.ProjectID,
		Provider:  a.Provider,
		CWD:       p.Path,
		Args:      s.sm.AgentArgs(twin),
		Env:       twinEnv,
		ResumeID:  resume,
		Cols:      120, Rows: 36,
	})
	if err != nil {
		_ = s.st.DeleteAgent(twin.ID)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	mode := "fresh session"
	if resume != "" {
		mode = "resumed with the parent's conversation"
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"agent": twin, "session": ns.Public(), "mode": mode,
	})
}

// ---------- restore ----------

// doRestore reopens what was running when the app last closed.
func (s *Server) doRestore(w http.ResponseWriter, r *http.Request) {
	entries := s.st.TakeRestore()
	if len(entries) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"restored": 0})
		return
	}
	restored, failed := 0, 0
	for _, e := range entries {
		switch e.Kind {
		case "agent":
			a, err := s.st.Agent(e.AgentID)
			if err != nil {
				failed++
				continue
			}
			p, err := s.st.Project(a.ProjectID)
			if err != nil {
				failed++
				continue
			}
			if _, ok := s.sm.SessionForAgent(a.ID); ok {
				continue
			}
			// One agent whose secret has gone missing does not fail the whole
			// restore. The others still come back and this one is counted.
			env, err := s.agentEnv(a)
			if err != nil {
				log.Printf("restore %s: %v", a.Name, err)
				failed++
				continue
			}
			if _, err := s.sm.Spawn(session.SpawnOpts{
				Kind: session.KindAgent, AgentID: a.ID, ProjectID: a.ProjectID,
				Provider: a.Provider, CWD: p.Path, Args: s.sm.AgentArgs(a),
				Env: env, ResumeID: e.SessionID, Cols: 120, Rows: 36,
			}); err != nil {
				failed++
				continue
			}
			restored++
		case "command":
			c, err := s.st.Command(e.CommandID)
			if err != nil {
				failed++
				continue
			}
			p, err := s.st.Project(c.ProjectID)
			if err != nil {
				failed++
				continue
			}
			env, _ := s.expandSecrets(c.Env)
			if _, err := s.sm.SpawnCommand(c, p.Path, env, 120, 32); err != nil {
				failed++
				continue
			}
			restored++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"restored": restored, "failed": failed})
}

// SaveRestoreState records what is running, for the next launch.
func (s *Server) SaveRestoreState() {
	if !s.st.Settings().RestoreOnLaunch {
		return
	}
	var entries []store.RestoreEntry
	for _, sess := range s.sm.Sessions() {
		p := sess.Public()
		if p.Status == session.StatusExited || p.Status == session.StatusError {
			continue
		}
		switch p.Kind {
		case session.KindAgent:
			entries = append(entries, store.RestoreEntry{
				Kind: "agent", AgentID: p.AgentID, ProjectID: p.ProjectID,
				SessionID: p.ClaudeSessionID,
			})
		case session.KindCommand:
			entries = append(entries, store.RestoreEntry{
				Kind: "command", CommandID: p.CommandID, ProjectID: p.ProjectID,
			})
		}
	}
	_ = s.st.SaveRestore(entries)
}
