package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/panjitito/go-ai-team/internal/store"
)

// Deps is what the tools need from the rest of the app. Passed in rather than
// imported so this package stays a leaf and can be tested without a running
// server.
type Deps struct {
	Store *store.Store

	// ProjectID scopes every tool to the project the agent is working in. An
	// agent must not be able to read another project's backlog or memory just
	// because both happen to live in the same app.
	ProjectID string
	// AgentID and AgentName identify the caller, for message authorship and
	// memory attribution.
	AgentID   string
	AgentName string

	// RunCommand executes a saved dev command and returns its output.
	RunCommand func(ctx context.Context, commandID string) (string, error)
	// SecretNames lists vault entries. Values are never exposed here.
	SecretNames func() ([]string, error)
	// Query runs a read-only SQL statement against a named saved connection.
	Query func(ctx context.Context, connName, sql string) (string, error)
}

// Register installs every tool onto s.
func Register(s *Server, d Deps) {
	registerBacklog(s, d)
	registerMemory(s, d)
	registerPrompts(s, d)
	registerCommands(s, d)
	registerMessaging(s, d)
	registerSecrets(s, d)
	registerDB(s, d)
}

// ---------- backlog ----------

func registerBacklog(s *Server, d Deps) {
	s.Register(Tool{
		Name: "backlog_list",
		Description: "List the tasks on this project's board. Use it to find what to work on next, " +
			"or to check whether something is already tracked before creating a duplicate.",
		InputSchema: Schema(map[string]any{
			"status": Enum("only tasks in this column", "backlog", "todo", "in_progress", "review", "done"),
		}),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct{ Status string }
			if err := Decode(raw, &a); err != nil {
				return "", err
			}
			type row struct {
				ID       string   `json:"id"`
				Title    string   `json:"title"`
				Status   string   `json:"status"`
				Priority int      `json:"priority"`
				Labels   []string `json:"labels,omitempty"`
				Body     string   `json:"body,omitempty"`
			}
			var out []row
			for _, t := range d.Store.TasksFor(d.ProjectID) {
				if a.Status != "" && string(t.Status) != a.Status {
					continue
				}
				out = append(out, row{t.ID, t.Title, string(t.Status), t.Priority, t.Labels, t.Body})
			}
			if len(out) == 0 {
				return "The board is empty.", nil
			}
			return JSONText(out)
		},
	})

	s.Register(Tool{
		Name:        "backlog_create",
		Description: "Add a task to this project's board. Use it to record follow-up work you found but were not asked to do.",
		InputSchema: Schema(map[string]any{
			"title":  Str("one line describing the work"),
			"body":   Str("optional detail"),
			"status": Enum("which column to put it in; defaults to backlog", "backlog", "todo", "in_progress", "review", "done"),
			"labels": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		}, "title"),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct {
				Title  string
				Body   string
				Status string
				Labels []string
			}
			if err := Decode(raw, &a); err != nil {
				return "", err
			}
			if strings.TrimSpace(a.Title) == "" {
				return "", fmt.Errorf("a title is required")
			}
			st := store.TaskStatus(a.Status)
			if st == "" {
				st = store.TaskBacklog
			}
			t := &store.Task{
				ProjectID: d.ProjectID, Title: a.Title, Body: a.Body,
				Status: st, Labels: a.Labels, Source: "agent",
				Reporter: d.AgentName,
			}
			if err := d.Store.AddTask(t); err != nil {
				return "", err
			}
			return fmt.Sprintf("Created task %s: %s", t.ID, t.Title), nil
		},
	})

	s.Register(Tool{
		Name: "backlog_update",
		Description: "Change a task: move it between columns, edit it, or add a comment recording what you did. " +
			"Move a task to done only when the work is actually finished and verified.",
		InputSchema: Schema(map[string]any{
			"id":      Str("the task id"),
			"status":  Enum("move it to this column", "backlog", "todo", "in_progress", "review", "done"),
			"title":   Str("new title"),
			"body":    Str("new body"),
			"comment": Str("append a note to the task's thread"),
		}, "id"),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct {
				ID, Status, Title, Body, Comment string
			}
			if err := Decode(raw, &a); err != nil {
				return "", err
			}
			cur, err := d.Store.Task(a.ID)
			if err != nil {
				return "", fmt.Errorf("no task %s", a.ID)
			}
			if cur.ProjectID != d.ProjectID {
				return "", fmt.Errorf("task %s belongs to a different project", a.ID)
			}
			t, err := d.Store.UpdateTask(a.ID, func(t *store.Task) {
				if a.Status != "" {
					t.Status = store.TaskStatus(a.Status)
				}
				if a.Title != "" {
					t.Title = a.Title
				}
				if a.Body != "" {
					t.Body = a.Body
				}
				if strings.TrimSpace(a.Comment) != "" {
					t.Comments = append(t.Comments, store.Comment{
						ID: store.NewID("cmt"), Author: d.AgentName, IsAgent: true,
						Body: a.Comment, CreatedAt: time.Now(),
					})
				}
			})
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Task %s is now %s.", t.ID, t.Status), nil
		},
	})
}

// ---------- project memory ----------

func registerMemory(s *Server, d Deps) {
	s.Register(Tool{
		Name: "memory_read",
		Description: "Read what previous agents learned about this codebase: architecture, decisions, pitfalls, " +
			"conventions. Read this before making a design choice — it will tell you what has already been " +
			"decided and what has already gone wrong.",
		InputSchema: Schema(map[string]any{
			"kind": Enum("only entries of this kind", "architecture", "decisions", "pitfalls", "conventions", "features"),
		}),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct{ Kind string }
			if err := Decode(raw, &a); err != nil {
				return "", err
			}
			var b strings.Builder
			n := 0
			for _, m := range d.Store.MemoriesFor(d.ProjectID) {
				if a.Kind != "" && string(m.Kind) != a.Kind {
					continue
				}
				n++
				fmt.Fprintf(&b, "## [%s] %s\n%s\n", m.Kind, m.Title, m.Body)
				if m.Author != "" {
					fmt.Fprintf(&b, "_recorded by %s_\n", m.Author)
				}
				b.WriteString("\n")
			}
			if n == 0 {
				return "Nothing has been recorded for this project yet.", nil
			}
			return b.String(), nil
		},
	})

	s.Register(Tool{
		Name: "memory_save",
		Description: "Record something about this codebase that the next agent should know: a decision and why, " +
			"a pitfall you hit, a convention you discovered. This survives your session and every agent on " +
			"the project reads it. Write what was not obvious, not what the code already says.",
		InputSchema: Schema(map[string]any{
			"kind":  Enum("what sort of fact this is", "architecture", "decisions", "pitfalls", "conventions", "features"),
			"title": Str("a short handle for it"),
			"body":  Str("the fact, and why it matters"),
		}, "title", "body"),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct{ Kind, Title, Body string }
			if err := Decode(raw, &a); err != nil {
				return "", err
			}
			if strings.TrimSpace(a.Title) == "" || strings.TrimSpace(a.Body) == "" {
				return "", fmt.Errorf("both a title and a body are required")
			}
			kind := store.MemoryKind(a.Kind)
			if kind == "" {
				kind = store.MemDecision
			}
			m := &store.Memory{
				ProjectID: d.ProjectID, Kind: kind, Title: a.Title,
				Body: a.Body, Author: d.AgentName,
			}
			if err := d.Store.AddMemory(m); err != nil {
				return "", err
			}
			return fmt.Sprintf("Saved to project memory under %s: %s", kind, a.Title), nil
		},
	})
}

// ---------- prompt library ----------

func registerPrompts(s *Server, d Deps) {
	s.Register(Tool{
		Name:        "prompt_list",
		Description: "List the saved prompts available in this project, with their names.",
		InputSchema: Schema(nil),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			type row struct {
				Name   string `json:"name"`
				Folder string `json:"folder,omitempty"`
			}
			var out []row
			for _, p := range d.Store.Prompts() {
				if p.ProjectID != "" && p.ProjectID != d.ProjectID {
					continue
				}
				out = append(out, row{p.Name, p.Folder})
			}
			if len(out) == 0 {
				return "No saved prompts.", nil
			}
			return JSONText(out)
		},
	})

	s.Register(Tool{
		Name:        "prompt_get",
		Description: "Fetch a saved prompt's full text by name, with any prompt references it contains resolved.",
		InputSchema: Schema(map[string]any{"name": Str("the prompt's name")}, "name"),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct{ Name string }
			if err := Decode(raw, &a); err != nil {
				return "", err
			}
			p, ok := d.Store.PromptByName(a.Name)
			if !ok {
				return "", fmt.Errorf("no prompt named %q", a.Name)
			}
			return ResolveChain(d.Store, p.Body, 0), nil
		},
	})

	s.Register(Tool{
		Name: "prompt_save",
		Description: "Save a reusable prompt so it is one click away next time. Use it when you have written " +
			"an instruction that worked well and will be needed again.",
		InputSchema: Schema(map[string]any{
			"name":   Str("a short unique name"),
			"body":   Str("the prompt text"),
			"folder": Str("optional folder to file it under"),
		}, "name", "body"),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct{ Name, Body, Folder string }
			if err := Decode(raw, &a); err != nil {
				return "", err
			}
			if strings.TrimSpace(a.Name) == "" || strings.TrimSpace(a.Body) == "" {
				return "", fmt.Errorf("both a name and a body are required")
			}
			if existing, ok := d.Store.PromptByName(a.Name); ok {
				if _, err := d.Store.UpdatePrompt(existing.ID, func(p *store.Prompt) {
					p.Body = a.Body
					if a.Folder != "" {
						p.Folder = a.Folder
					}
				}); err != nil {
					return "", err
				}
				return fmt.Sprintf("Updated the saved prompt %q.", a.Name), nil
			}
			p := &store.Prompt{Name: a.Name, Body: a.Body, Folder: a.Folder, ProjectID: d.ProjectID}
			if err := d.Store.AddPrompt(p); err != nil {
				return "", err
			}
			return fmt.Sprintf("Saved the prompt %q.", a.Name), nil
		},
	})
}

// ResolveChain expands {{prompt:name}} references so shared rules live in one
// place instead of being copied into every prompt and drifting apart.
//
// The depth cap is what makes a cycle harmless: two prompts that reference each
// other resolve a few levels deep and then stop, rather than hanging.
func ResolveChain(st *store.Store, body string, depth int) string {
	const open, close = "{{prompt:", "}}"
	if depth > 5 || !strings.Contains(body, open) {
		return body
	}
	var b strings.Builder
	rest := body
	for {
		i := strings.Index(rest, open)
		if i < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:i])
		rest = rest[i+len(open):]
		j := strings.Index(rest, close)
		if j < 0 {
			b.WriteString(open)
			b.WriteString(rest)
			break
		}
		name := strings.TrimSpace(rest[:j])
		rest = rest[j+len(close):]
		if p, ok := st.PromptByName(name); ok {
			b.WriteString(ResolveChain(st, p.Body, depth+1))
		} else {
			// Leave an unresolved reference visible; a silent blank would hide
			// a typo inside an otherwise plausible brief.
			b.WriteString(open + name + close)
		}
	}
	return b.String()
}

// ---------- dev commands ----------

func registerCommands(s *Server, d Deps) {
	s.Register(Tool{
		Name:        "command_list",
		Description: "List this project's saved dev commands (dev server, build, tests) that you can run.",
		InputSchema: Schema(nil),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			type row struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Command string `json:"command"`
			}
			var out []row
			for _, c := range d.Store.CommandsFor(d.ProjectID) {
				out = append(out, row{c.ID, c.Name, c.Command})
			}
			if len(out) == 0 {
				return "No saved commands for this project.", nil
			}
			return JSONText(out)
		},
	})

	s.Register(Tool{
		Name: "command_run",
		Description: "Run one of this project's saved dev commands and return its output. " +
			"Use it to build, test or lint without having to know the exact incantation.",
		InputSchema: Schema(map[string]any{"id": Str("the command id from command_list")}, "id"),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct{ ID string }
			if err := Decode(raw, &a); err != nil {
				return "", err
			}
			if d.RunCommand == nil {
				return "", fmt.Errorf("running commands is not available in this session")
			}
			c, err := d.Store.Command(a.ID)
			if err != nil {
				return "", fmt.Errorf("no command %s", a.ID)
			}
			if c.ProjectID != d.ProjectID {
				return "", fmt.Errorf("command %s belongs to a different project", a.ID)
			}
			return d.RunCommand(ctx, a.ID)
		},
	})
}

// ---------- agent messaging ----------

func registerMessaging(s *Server, d Deps) {
	s.Register(Tool{
		Name:        "agent_list",
		Description: "List the other saved agents on this project that you can send a message to.",
		InputSchema: Schema(nil),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			type row struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				Role string `json:"role,omitempty"`
			}
			var out []row
			for _, a := range d.Store.Agents() {
				if a.ProjectID != d.ProjectID || a.ID == d.AgentID {
					continue
				}
				out = append(out, row{a.ID, a.Name, a.Role})
			}
			if len(out) == 0 {
				return "No other agents on this project.", nil
			}
			return JSONText(out)
		},
	})

	s.Register(Tool{
		Name: "agent_send",
		Description: "Send a message to another agent on this project. It is stored on disk before delivery is " +
			"attempted, so it arrives even if the recipient is not running yet. Use it to hand work off with " +
			"context: what you did, what is left, what to watch out for.",
		InputSchema: Schema(map[string]any{
			"to":      Str("the recipient's agent id from agent_list"),
			"subject": Str("one line"),
			"body":    Str("the message"),
		}, "to", "subject", "body"),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct{ To, Subject, Body string }
			if err := Decode(raw, &a); err != nil {
				return "", err
			}
			to, err := d.Store.Agent(a.To)
			if err != nil {
				return "", fmt.Errorf("no agent %s", a.To)
			}
			if to.ProjectID != d.ProjectID {
				return "", fmt.Errorf("agent %s is on a different project", a.To)
			}
			m := &store.AgentMessage{
				ProjectID: d.ProjectID, FromID: d.AgentID, FromName: d.AgentName,
				ToID: a.To, Subject: a.Subject, Body: a.Body,
			}
			if err := d.Store.AddMessage(m); err != nil {
				return "", err
			}
			return fmt.Sprintf("Message queued for %s.", to.Name), nil
		},
	})

	s.Register(Tool{
		Name:        "agent_inbox",
		Description: "Read messages other agents sent you. Check this when you start work and after a handoff.",
		InputSchema: Schema(map[string]any{"unreadOnly": Bool("only messages you have not read yet")}),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct{ UnreadOnly bool }
			if err := Decode(raw, &a); err != nil {
				return "", err
			}
			if d.AgentID == "" {
				return "", fmt.Errorf("this session has no agent identity, so it has no inbox")
			}
			var b strings.Builder
			n := 0
			for _, m := range d.Store.Inbox(d.AgentID) {
				if a.UnreadOnly && m.State == "read" {
					continue
				}
				n++
				fmt.Fprintf(&b, "## From %s: %s\n%s\n\n", m.FromName, m.Subject, m.Body)
				// Reading is what marks it read, which is what makes the sender's
				// receipt mean something.
				_, _ = d.Store.UpdateMessage(m.ID, func(x *store.AgentMessage) {
					x.State = "read"
					x.ReadAt = time.Now()
				})
			}
			if n == 0 {
				return "No messages.", nil
			}
			return b.String(), nil
		},
	})
}

// ---------- secrets ----------

func registerSecrets(s *Server, d Deps) {
	s.Register(Tool{
		Name: "secret_list",
		Description: "List the names of secrets stored in this machine's vault. Values are never returned to you " +
			"by design — reference a secret by name in a saved dev command and it is injected at launch.",
		InputSchema: Schema(nil),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			if d.SecretNames == nil {
				return "", fmt.Errorf("the vault is not available in this session")
			}
			names, err := d.SecretNames()
			if err != nil {
				return "", err
			}
			if len(names) == 0 {
				return "The vault is empty.", nil
			}
			return "Available secret names (use {{secret:NAME}} in a command, never ask for the value):\n- " +
				strings.Join(names, "\n- "), nil
		},
	})
}

// ---------- databases ----------

func registerDB(s *Server, d Deps) {
	s.Register(Tool{
		Name:        "db_list",
		Description: "List the saved database connections you can query. Passwords are never exposed to you.",
		InputSchema: Schema(nil),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			type row struct {
				Name       string `json:"name"`
				Driver     string `json:"driver"`
				Database   string `json:"database"`
				ReadOnly   bool   `json:"readOnly"`
				Production bool   `json:"production"`
			}
			var out []row
			for _, c := range d.Store.DBConns() {
				if c.ProjectID != "" && c.ProjectID != d.ProjectID {
					continue
				}
				out = append(out, row{c.Name, c.Driver, c.DBName, !c.Writable, c.Production})
			}
			if len(out) == 0 {
				return "No saved database connections.", nil
			}
			return JSONText(out)
		},
	})

	s.Register(Tool{
		Name: "db_query",
		Description: "Run one read-only SQL statement against a saved connection, naming it rather than " +
			"supplying credentials. Results are capped. Statements that would change data are refused " +
			"unless the connection is explicitly writable.",
		InputSchema: Schema(map[string]any{
			"connection": Str("the connection name from db_list"),
			"sql":        Str("a single SQL statement"),
		}, "connection", "sql"),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct{ Connection, SQL string }
			if err := Decode(raw, &a); err != nil {
				return "", err
			}
			if d.Query == nil {
				return "", fmt.Errorf("database access is not available in this session")
			}
			return d.Query(ctx, a.Connection, a.SQL)
		},
	})
}
