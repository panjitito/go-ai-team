package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/uniair/go-ai-team/internal/mcp"
	"github.com/uniair/go-ai-team/internal/store"
)

// Board, ideas, prompts, skills and memory: the work-intake half of the app.

func (s *Server) routeWork(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tasks", s.listTasks)
	mux.HandleFunc("POST /api/tasks", s.createTask)
	mux.HandleFunc("PATCH /api/tasks/{id}", s.patchTask)
	mux.HandleFunc("DELETE /api/tasks/{id}", s.deleteTask)
	mux.HandleFunc("POST /api/tasks/{id}/run", s.runTask)
	mux.HandleFunc("POST /api/tasks/{id}/scope", s.scopeTask)
	mux.HandleFunc("POST /api/tasks/{id}/comment", s.commentTask)
	mux.HandleFunc("POST /api/tasks/{id}/vote", s.voteTask)

	mux.HandleFunc("GET /api/ideas", s.listIdeas)
	mux.HandleFunc("POST /api/ideas", s.createIdea)
	mux.HandleFunc("DELETE /api/ideas/{id}", s.deleteIdea)
	mux.HandleFunc("POST /api/ideas/cluster", s.clusterIdeas)
	mux.HandleFunc("POST /api/ideas/{id}/promote", s.promoteIdea)

	mux.HandleFunc("GET /api/prompts", s.listPrompts)
	mux.HandleFunc("POST /api/prompts", s.createPrompt)
	mux.HandleFunc("PATCH /api/prompts/{id}", s.patchPrompt)
	mux.HandleFunc("DELETE /api/prompts/{id}", s.deletePrompt)
	mux.HandleFunc("POST /api/prompts/{id}/resolve", s.resolvePrompt)
	mux.HandleFunc("POST /api/prompts/{id}/send", s.sendPrompt)

	mux.HandleFunc("GET /api/skills", s.listSkills)
	mux.HandleFunc("POST /api/skills", s.createSkill)
	mux.HandleFunc("PATCH /api/skills/{id}", s.patchSkill)
	mux.HandleFunc("DELETE /api/skills/{id}", s.deleteSkill)
	mux.HandleFunc("GET /api/skills/{id}/export", s.exportSkill)

	mux.HandleFunc("GET /api/memory", s.listMemory)
	mux.HandleFunc("POST /api/memory", s.createMemory)
	mux.HandleFunc("PATCH /api/memory/{id}", s.patchMemory)
	mux.HandleFunc("DELETE /api/memory/{id}", s.deleteMemory)
}

// ---------- tasks ----------

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	pid := r.URL.Query().Get("projectId")
	if pid == "" {
		writeJSON(w, http.StatusOK, orEmpty(s.st.Tasks()))
		return
	}
	writeJSON(w, http.StatusOK, orEmpty(s.st.TasksFor(pid)))
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var t store.Task
	if err := decode(r, &t); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	t.ID = ""
	if strings.TrimSpace(t.Title) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("a task needs a title"))
		return
	}
	if _, err := s.st.Project(t.ProjectID); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("a task needs an existing projectId"))
		return
	}
	if t.Source == "" {
		t.Source = "manual"
	}
	if err := s.st.AddTask(&t); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.sm.Notify("task.created", "Task added: "+t.Title, t)
	writeJSON(w, http.StatusCreated, t)
}

type patchTaskReq struct {
	Title    *string   `json:"title"`
	Body     *string   `json:"body"`
	Status   *string   `json:"status"`
	Order    *int      `json:"order"`
	AgentID  *string   `json:"agentId"`
	Priority *int      `json:"priority"`
	Labels   *[]string `json:"labels"`
}

// patchTask moves or edits a task. Moving one into in_progress is the trigger
// that starts an agent, which is the whole point of the board: the column is
// the instruction.
func (s *Server) patchTask(w http.ResponseWriter, r *http.Request) {
	var req patchTaskReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	before, err := s.st.Task(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	wasStatus := before.Status

	t, err := s.st.UpdateTask(before.ID, func(t *store.Task) {
		if req.Title != nil {
			t.Title = *req.Title
		}
		if req.Body != nil {
			t.Body = *req.Body
		}
		if req.Status != nil {
			t.Status = store.TaskStatus(*req.Status)
		}
		if req.Order != nil {
			t.Order = *req.Order
		}
		if req.AgentID != nil {
			t.AgentID = *req.AgentID
		}
		if req.Priority != nil {
			t.Priority = *req.Priority
		}
		if req.Labels != nil {
			t.Labels = *req.Labels
		}
	})
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}

	started := ""
	if wasStatus != store.TaskInProgress && t.Status == store.TaskInProgress {
		sid, lerr := s.startTaskAgent(t)
		if lerr != nil {
			// The move still stands: the user asked for it, and refusing it
			// would leave the board disagreeing with what they see. The reason
			// is surfaced instead.
			s.sm.Notify("task.failed",
				fmt.Sprintf("Moved %q to In Progress, but no agent could start: %v", t.Title, lerr), nil)
		} else {
			started = sid
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": t, "sessionId": started})
}

// startTaskAgent launches an agent on a task and records the session on it.
func (s *Server) startTaskAgent(t *store.Task) (string, error) {
	prompt := t.Title
	if strings.TrimSpace(t.Body) != "" {
		prompt += "\n\n" + t.Body
	}
	// Give the agent the ticket id so it can report back through MCP.
	prompt += fmt.Sprintf("\n\n(Backlog task %s. When the work is done and verified, "+
		"move it to review with backlog_update and say what you changed.)", t.ID)

	sid, err := s.sm.LaunchTask(t.ProjectID, t.AgentID, prompt, "backlog task")
	if err != nil {
		return "", err
	}
	_, _ = s.st.UpdateTask(t.ID, func(x *store.Task) { x.SessionID = sid })
	return sid, nil
}

func (s *Server) runTask(w http.ResponseWriter, r *http.Request) {
	t, err := s.st.Task(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	sid, err := s.startTaskAgent(t)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	_, _ = s.st.UpdateTask(t.ID, func(x *store.Task) { x.Status = store.TaskInProgress })
	writeJSON(w, http.StatusOK, map[string]string{"sessionId": sid})
}

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DeleteTask(r.PathValue("id")); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// scopeTask spawns a PM pass over the real codebase before any code is written,
// so a fuzzy request becomes something a developer could start on.
func (s *Server) scopeTask(w http.ResponseWriter, r *http.Request) {
	t, err := s.st.Task(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	p, err := s.st.Project(t.ProjectID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !s.ai.Available() {
		writeErr(w, http.StatusServiceUnavailable,
			errors.New("scoping needs a signed-in account; sign one in and try again"))
		return
	}
	// Answer immediately and do the work in the background: reading a codebase
	// takes minutes, and scoping deliberately does not start the task or notify
	// anyone, so there is nothing for the caller to wait on.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()
		brief, err := s.ai.ScopeTicket(ctx, p.Path, t.Title, t.Body)
		if err != nil {
			s.sm.Notify("task.scoped", "Scoping failed for "+t.Title+": "+err.Error(), nil)
			return
		}
		_, _ = s.st.UpdateTask(t.ID, func(x *store.Task) {
			x.Comments = append(x.Comments, store.Comment{
				ID: store.NewID("cmt"), Author: "Product Manager", IsAgent: true,
				Body: brief, CreatedAt: time.Now(),
			})
		})
		s.sm.Notify("task.scoped", "Scoped: "+t.Title, map[string]string{"taskId": t.ID})
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{
		"status": "scoping started; the brief will appear on the ticket",
	})
}

type commentReq struct {
	Author string `json:"author"`
	Body   string `json:"body"`
}

func (s *Server) commentTask(w http.ResponseWriter, r *http.Request) {
	var req commentReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("an empty comment"))
		return
	}
	author := req.Author
	if author == "" {
		author = "you"
	}
	t, err := s.st.UpdateTask(r.PathValue("id"), func(t *store.Task) {
		t.Comments = append(t.Comments, store.Comment{
			ID: store.NewID("cmt"), Author: author, Body: req.Body, CreatedAt: time.Now(),
		})
	})
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) voteTask(w http.ResponseWriter, r *http.Request) {
	t, err := s.st.UpdateTask(r.PathValue("id"), func(t *store.Task) { t.Votes++ })
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// ---------- ideas ----------

func (s *Server) listIdeas(w http.ResponseWriter, r *http.Request) {
	pid := r.URL.Query().Get("projectId")
	if pid == "" {
		writeJSON(w, http.StatusOK, orEmpty(s.st.Ideas()))
		return
	}
	writeJSON(w, http.StatusOK, orEmpty(s.st.IdeasFor(pid)))
}

func (s *Server) createIdea(w http.ResponseWriter, r *http.Request) {
	var i store.Idea
	if err := decode(r, &i); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	i.ID = ""
	if strings.TrimSpace(i.Body) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("an idea needs a body"))
		return
	}
	if err := s.st.AddIdea(&i); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, i)
}

func (s *Server) deleteIdea(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DeleteIdea(r.PathValue("id")); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// clusterIdeas is the Idea Radar: raw feedback in, themes out, deduplicated and
// rated. It runs on the user's own subscription rather than a metered service.
func (s *Server) clusterIdeas(w http.ResponseWriter, r *http.Request) {
	pid := r.URL.Query().Get("projectId")
	ideas := s.st.IdeasFor(pid)
	if len(ideas) == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("no ideas on this project to sort"))
		return
	}
	if !s.ai.Available() {
		writeErr(w, http.StatusServiceUnavailable, errors.New("clustering needs a signed-in account"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	clusters, err := s.ai.ClusterIdeas(ctx, ideas)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	// Write the theme and rating back onto each idea so the matrix view has
	// something to plot without re-running the analysis.
	for _, c := range clusters {
		for _, id := range c.IDs {
			_, _ = s.st.UpdateIdea(id, func(i *store.Idea) {
				i.Theme = c.Theme
				i.Impact = c.Impact
				i.Effort = c.Effort
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"clusters": clusters})
}

// promoteIdea turns a ripe idea into a board ticket.
func (s *Server) promoteIdea(w http.ResponseWriter, r *http.Request) {
	i, err := s.st.Idea(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	if i.Promoted != "" {
		writeErr(w, http.StatusBadRequest, errors.New("this idea is already a ticket"))
		return
	}
	title := i.Theme
	if title == "" {
		title = firstLine(i.Body, 80)
	}
	t := &store.Task{
		ProjectID: i.ProjectID, Title: title, Body: i.Body,
		Status: store.TaskBacklog, Source: "radar", Reporter: i.Reporter,
		Votes: i.Votes,
	}
	if err := s.st.AddTask(t); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	_, _ = s.st.UpdateIdea(i.ID, func(x *store.Idea) { x.Promoted = t.ID })
	writeJSON(w, http.StatusCreated, t)
}

func firstLine(s string, max int) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// ---------- prompts ----------

func (s *Server) listPrompts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, orEmpty(s.st.Prompts()))
}

func (s *Server) createPrompt(w http.ResponseWriter, r *http.Request) {
	var p store.Prompt
	if err := decode(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p.ID = ""
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Body) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("a prompt needs a name and a body"))
		return
	}
	if _, exists := s.st.PromptByName(p.Name); exists {
		writeErr(w, http.StatusConflict, fmt.Errorf("a prompt named %q already exists", p.Name))
		return
	}
	if err := s.st.AddPrompt(&p); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

type patchPromptReq struct {
	Name     *string `json:"name"`
	Body     *string `json:"body"`
	Folder   *string `json:"folder"`
	Personal *bool   `json:"personal"`
}

func (s *Server) patchPrompt(w http.ResponseWriter, r *http.Request) {
	var req patchPromptReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p, err := s.st.UpdatePrompt(r.PathValue("id"), func(p *store.Prompt) {
		if req.Name != nil {
			p.Name = *req.Name
		}
		if req.Body != nil {
			p.Body = *req.Body
		}
		if req.Folder != nil {
			p.Folder = *req.Folder
		}
		if req.Personal != nil {
			p.Personal = *req.Personal
		}
	})
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) deletePrompt(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DeletePrompt(r.PathValue("id")); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// resolvePrompt previews a prompt with its {{prompt:name}} references expanded,
// so the user can see the whole brief the agent will receive.
func (s *Server) resolvePrompt(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.Prompt(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"name": p.Name,
		"body": mcp.ResolveChain(s.st, p.Body, 0),
	})
}

type sendPromptReq struct {
	SessionID string `json:"sessionId"`
}

func (s *Server) sendPrompt(w http.ResponseWriter, r *http.Request) {
	var req sendPromptReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p, err := s.st.Prompt(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	body := mcp.ResolveChain(s.st, p.Body, 0)
	if err := s.sm.SendPrompt(req.SessionID, body); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	_, _ = s.st.UpdatePrompt(p.ID, func(x *store.Prompt) { x.Uses++ })
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

// ---------- skills ----------

func (s *Server) listSkills(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, orEmpty(s.st.Skills()))
}

func (s *Server) createSkill(w http.ResponseWriter, r *http.Request) {
	var k store.Skill
	if err := decode(r, &k); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	k.ID = ""
	if strings.TrimSpace(k.Name) == "" || strings.TrimSpace(k.Body) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("a skill needs a name and a body"))
		return
	}
	if err := s.st.AddSkill(&k); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, k)
}

type patchSkillReq struct {
	Name        *string   `json:"name"`
	Description *string   `json:"description"`
	Body        *string   `json:"body"`
	Triggers    *[]string `json:"triggers"`
}

func (s *Server) patchSkill(w http.ResponseWriter, r *http.Request) {
	var req patchSkillReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	k, err := s.st.UpdateSkill(r.PathValue("id"), func(k *store.Skill) {
		if req.Name != nil {
			k.Name = *req.Name
		}
		if req.Description != nil {
			k.Description = *req.Description
		}
		if req.Body != nil {
			k.Body = *req.Body
		}
		if req.Triggers != nil {
			k.Triggers = *req.Triggers
		}
	})
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, k)
}

func (s *Server) deleteSkill(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DeleteSkill(r.PathValue("id")); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// exportSkill renders a skill as a SKILL.md file, which is the portable form
// every runtime reads.
func (s *Server) exportSkill(w http.ResponseWriter, r *http.Request) {
	k, err := s.st.Skill(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", k.Name)
	if k.Description != "" {
		fmt.Fprintf(&b, "description: %s\n", k.Description)
	}
	b.WriteString("---\n\n")
	b.WriteString(k.Body)
	if len(k.Triggers) > 0 {
		fmt.Fprintf(&b, "\n\n<!-- triggers: %s -->\n", strings.Join(k.Triggers, ", "))
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", safeFilename(k.Name)+".SKILL.md"))
	_, _ = w.Write([]byte(b.String()))
}

// safeFilename strips anything that could escape a directory or upset a shell.
func safeFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "skill"
	}
	return out
}

// ---------- memory ----------

func (s *Server) listMemory(w http.ResponseWriter, r *http.Request) {
	pid := r.URL.Query().Get("projectId")
	if pid == "" {
		writeJSON(w, http.StatusOK, orEmpty(s.st.Memories()))
		return
	}
	writeJSON(w, http.StatusOK, orEmpty(s.st.MemoriesFor(pid)))
}

func (s *Server) createMemory(w http.ResponseWriter, r *http.Request) {
	var m store.Memory
	if err := decode(r, &m); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	m.ID = ""
	if strings.TrimSpace(m.Title) == "" || strings.TrimSpace(m.Body) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("a memory needs a title and a body"))
		return
	}
	if err := s.st.AddMemory(&m); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

type patchMemoryReq struct {
	Title *string `json:"title"`
	Body  *string `json:"body"`
	Kind  *string `json:"kind"`
}

func (s *Server) patchMemory(w http.ResponseWriter, r *http.Request) {
	var req patchMemoryReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	m, err := s.st.UpdateMemory(r.PathValue("id"), func(m *store.Memory) {
		if req.Title != nil {
			m.Title = *req.Title
		}
		if req.Body != nil {
			m.Body = *req.Body
		}
		if req.Kind != nil {
			m.Kind = store.MemoryKind(*req.Kind)
		}
	})
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) deleteMemory(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DeleteMemory(r.PathValue("id")); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------- board summary ----------

// boardCounts is used by the sidebar to show progress without fetching every
// task.
func (s *Server) boardCounts(projectID string) map[string]int {
	out := map[string]int{}
	for _, c := range store.BoardColumns {
		out[string(c)] = 0
	}
	for _, t := range s.st.TasksFor(projectID) {
		out[string(t.Status)]++
	}
	return out
}
