package store

import (
	"sort"
	"time"
)

// The store grew from four collections to nineteen. Writing List/Get/Add/Update/
// Delete by hand for each one produced five near-identical methods per type and
// a real risk of one of them forgetting to persist. Generics collapse that to a
// single implementation: every collection gets the same locking, the same
// atomic write, and the same not-found behaviour, so a bug fixed here is fixed
// everywhere.

// entity is anything the store can hold: it knows its own id.
type entity interface {
	getID() string
	setID(string)
}

func (t *Task) getID() string          { return t.ID }
func (t *Task) setID(s string)         { t.ID = s }
func (i *Idea) getID() string          { return i.ID }
func (i *Idea) setID(s string)         { i.ID = s }
func (p *Prompt) getID() string        { return p.ID }
func (p *Prompt) setID(s string)       { p.ID = s }
func (s *Skill) getID() string         { return s.ID }
func (s *Skill) setID(x string)        { s.ID = x }
func (m *Memory) getID() string        { return m.ID }
func (m *Memory) setID(s string)       { m.ID = s }
func (s *Schedule) getID() string      { return s.ID }
func (s *Schedule) setID(x string)     { s.ID = x }
func (w *Webhook) getID() string       { return w.ID }
func (w *Webhook) setID(s string)      { w.ID = s }
func (d *DevCommand) getID() string    { return d.ID }
func (d *DevCommand) setID(s string)   { d.ID = s }
func (h *SSHHost) getID() string       { return h.ID }
func (h *SSHHost) setID(s string)      { h.ID = s }
func (c *DBConn) getID() string        { return c.ID }
func (c *DBConn) setID(s string)       { c.ID = s }
func (m *AgentMessage) getID() string  { return m.ID }
func (m *AgentMessage) setID(s string) { m.ID = s }
func (e *QueuedEvent) getID() string   { return e.ID }
func (e *QueuedEvent) setID(s string)  { e.ID = s }

// coll is a typed view onto one slice inside State. The pick function is what
// binds a Go type to its field in the document.
type coll[T entity] struct {
	s    *Store
	pick func(*State) *[]T
	pfx  string
}

func (c coll[T]) list() []T {
	c.s.mu.RLock()
	defer c.s.mu.RUnlock()
	src := *c.pick(&c.s.state)
	out := make([]T, len(src))
	copy(out, src)
	return out
}

func (c coll[T]) get(id string) (T, error) {
	var zero T
	c.s.mu.RLock()
	defer c.s.mu.RUnlock()
	for _, v := range *c.pick(&c.s.state) {
		if v.getID() == id {
			return v, nil
		}
	}
	return zero, ErrNotFound
}

func (c coll[T]) add(v T) error {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	if v.getID() == "" {
		v.setID(NewID(c.pfx))
	}
	p := c.pick(&c.s.state)
	*p = append(*p, v)
	return c.s.saveLocked()
}

func (c coll[T]) update(id string, fn func(T)) (T, error) {
	var zero T
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	for _, v := range *c.pick(&c.s.state) {
		if v.getID() == id {
			fn(v)
			return v, c.s.saveLocked()
		}
	}
	return zero, ErrNotFound
}

func (c coll[T]) del(id string) error {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	p := c.pick(&c.s.state)
	out := (*p)[:0]
	found := false
	for _, v := range *p {
		if v.getID() == id {
			found = true
			continue
		}
		out = append(out, v)
	}
	if !found {
		return ErrNotFound
	}
	*p = out
	return c.s.saveLocked()
}

// delWhere removes everything matching a predicate and reports how many went.
func (c coll[T]) delWhere(match func(T) bool) (int, error) {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	p := c.pick(&c.s.state)
	out := (*p)[:0]
	n := 0
	for _, v := range *p {
		if match(v) {
			n++
			continue
		}
		out = append(out, v)
	}
	*p = out
	if n == 0 {
		return 0, nil
	}
	return n, c.s.saveLocked()
}

// ---------- typed collection accessors ----------

func (s *Store) tasks() coll[*Task] {
	return coll[*Task]{s, func(st *State) *[]*Task { return &st.Tasks }, "tsk"}
}
func (s *Store) ideas() coll[*Idea] {
	return coll[*Idea]{s, func(st *State) *[]*Idea { return &st.Ideas }, "ida"}
}
func (s *Store) prompts() coll[*Prompt] {
	return coll[*Prompt]{s, func(st *State) *[]*Prompt { return &st.Prompts }, "prm"}
}
func (s *Store) skills() coll[*Skill] {
	return coll[*Skill]{s, func(st *State) *[]*Skill { return &st.Skills }, "skl"}
}
func (s *Store) memories() coll[*Memory] {
	return coll[*Memory]{s, func(st *State) *[]*Memory { return &st.Memories }, "mem"}
}
func (s *Store) schedules() coll[*Schedule] {
	return coll[*Schedule]{s, func(st *State) *[]*Schedule { return &st.Schedules }, "sch"}
}
func (s *Store) webhooks() coll[*Webhook] {
	return coll[*Webhook]{s, func(st *State) *[]*Webhook { return &st.Webhooks }, "whk"}
}
func (s *Store) commands() coll[*DevCommand] {
	return coll[*DevCommand]{s, func(st *State) *[]*DevCommand { return &st.Commands }, "cmd"}
}
func (s *Store) hosts() coll[*SSHHost] {
	return coll[*SSHHost]{s, func(st *State) *[]*SSHHost { return &st.SSHHosts }, "ssh"}
}
func (s *Store) dbs() coll[*DBConn] {
	return coll[*DBConn]{s, func(st *State) *[]*DBConn { return &st.DBConns }, "dbc"}
}
func (s *Store) messages() coll[*AgentMessage] {
	return coll[*AgentMessage]{s, func(st *State) *[]*AgentMessage { return &st.Messages }, "msg"}
}
func (s *Store) events() coll[*QueuedEvent] {
	return coll[*QueuedEvent]{s, func(st *State) *[]*QueuedEvent { return &st.Events }, "evt"}
}

// ---------- tasks ----------

// Tasks returns every task, board order first.
func (s *Store) Tasks() []*Task {
	out := s.tasks().list()
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

// TasksFor returns a project's tasks in board order.
func (s *Store) TasksFor(projectID string) []*Task {
	var out []*Task
	for _, t := range s.Tasks() {
		if t.ProjectID == projectID {
			out = append(out, t)
		}
	}
	return out
}

// Task looks up one task.
func (s *Store) Task(id string) (*Task, error) { return s.tasks().get(id) }

// AddTask appends a task at the end of its column.
func (s *Store) AddTask(t *Task) error {
	now := time.Now()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	if t.Status == "" {
		t.Status = TaskBacklog
	}
	if t.Order == 0 {
		t.Order = int(now.UnixMilli())
	}
	return s.tasks().add(t)
}

// UpdateTask mutates a task and stamps it.
func (s *Store) UpdateTask(id string, fn func(*Task)) (*Task, error) {
	return s.tasks().update(id, func(t *Task) {
		fn(t)
		t.UpdatedAt = time.Now()
		if t.Status == TaskDone && t.DoneAt.IsZero() {
			t.DoneAt = time.Now()
		}
		if t.Status != TaskDone {
			t.DoneAt = time.Time{}
		}
	})
}

// DeleteTask removes a task.
func (s *Store) DeleteTask(id string) error { return s.tasks().del(id) }

// ---------- ideas ----------

// Ideas returns every idea.
func (s *Store) Ideas() []*Idea { return s.ideas().list() }

// IdeasFor returns a project's ideas.
func (s *Store) IdeasFor(projectID string) []*Idea {
	var out []*Idea
	for _, i := range s.ideas().list() {
		if i.ProjectID == projectID {
			out = append(out, i)
		}
	}
	return out
}

// Idea looks up one idea.
func (s *Store) Idea(id string) (*Idea, error) { return s.ideas().get(id) }

// AddIdea records raw feedback.
func (s *Store) AddIdea(i *Idea) error {
	if i.CreatedAt.IsZero() {
		i.CreatedAt = time.Now()
	}
	return s.ideas().add(i)
}

// UpdateIdea mutates an idea.
func (s *Store) UpdateIdea(id string, fn func(*Idea)) (*Idea, error) {
	return s.ideas().update(id, fn)
}

// DeleteIdea removes an idea.
func (s *Store) DeleteIdea(id string) error { return s.ideas().del(id) }

// ---------- prompts ----------

// Prompts returns the prompt library, newest edits first.
func (s *Store) Prompts() []*Prompt {
	out := s.prompts().list()
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Prompt looks up one prompt.
func (s *Store) Prompt(id string) (*Prompt, error) { return s.prompts().get(id) }

// PromptByName finds a prompt by exact name, which is how prompt chaining
// resolves a {{prompt:name}} reference.
func (s *Store) PromptByName(name string) (*Prompt, bool) {
	for _, p := range s.prompts().list() {
		if p.Name == name {
			return p, true
		}
	}
	return nil, false
}

// AddPrompt saves a prompt.
func (s *Store) AddPrompt(p *Prompt) error {
	now := time.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	return s.prompts().add(p)
}

// UpdatePrompt mutates a prompt.
func (s *Store) UpdatePrompt(id string, fn func(*Prompt)) (*Prompt, error) {
	return s.prompts().update(id, func(p *Prompt) { fn(p); p.UpdatedAt = time.Now() })
}

// DeletePrompt removes a prompt.
func (s *Store) DeletePrompt(id string) error { return s.prompts().del(id) }

// ---------- skills ----------

// Skills returns the skills library.
func (s *Store) Skills() []*Skill {
	out := s.skills().list()
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Skill looks up one skill.
func (s *Store) Skill(id string) (*Skill, error) { return s.skills().get(id) }

// AddSkill saves a skill.
func (s *Store) AddSkill(k *Skill) error {
	now := time.Now()
	if k.CreatedAt.IsZero() {
		k.CreatedAt = now
	}
	k.UpdatedAt = now
	return s.skills().add(k)
}

// UpdateSkill mutates a skill.
func (s *Store) UpdateSkill(id string, fn func(*Skill)) (*Skill, error) {
	return s.skills().update(id, func(k *Skill) { fn(k); k.UpdatedAt = time.Now() })
}

// DeleteSkill removes a skill.
func (s *Store) DeleteSkill(id string) error { return s.skills().del(id) }

// ---------- memory ----------

// Memories returns every memory.
func (s *Store) Memories() []*Memory { return s.memories().list() }

// MemoriesFor returns a project's memory, newest first.
func (s *Store) MemoriesFor(projectID string) []*Memory {
	var out []*Memory
	for _, m := range s.memories().list() {
		if m.ProjectID == projectID {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

// Memory looks up one entry.
func (s *Store) Memory(id string) (*Memory, error) { return s.memories().get(id) }

// AddMemory records what an agent learned.
func (s *Store) AddMemory(m *Memory) error {
	now := time.Now()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	if m.Kind == "" {
		m.Kind = MemDecision
	}
	return s.memories().add(m)
}

// UpdateMemory mutates an entry.
func (s *Store) UpdateMemory(id string, fn func(*Memory)) (*Memory, error) {
	return s.memories().update(id, func(m *Memory) { fn(m); m.UpdatedAt = time.Now() })
}

// DeleteMemory removes an entry.
func (s *Store) DeleteMemory(id string) error { return s.memories().del(id) }

// ---------- schedules ----------

// Schedules returns every schedule.
func (s *Store) Schedules() []*Schedule { return s.schedules().list() }

// Schedule looks up one schedule.
func (s *Store) Schedule(id string) (*Schedule, error) { return s.schedules().get(id) }

// AddSchedule saves a schedule.
func (s *Store) AddSchedule(x *Schedule) error {
	if x.CreatedAt.IsZero() {
		x.CreatedAt = time.Now()
	}
	return s.schedules().add(x)
}

// UpdateSchedule mutates a schedule.
func (s *Store) UpdateSchedule(id string, fn func(*Schedule)) (*Schedule, error) {
	return s.schedules().update(id, fn)
}

// DeleteSchedule removes a schedule.
func (s *Store) DeleteSchedule(id string) error { return s.schedules().del(id) }

// ---------- webhooks ----------

// Webhooks returns every trigger.
func (s *Store) Webhooks() []*Webhook { return s.webhooks().list() }

// Webhook looks up one trigger.
func (s *Store) Webhook(id string) (*Webhook, error) { return s.webhooks().get(id) }

// WebhookByToken resolves the public URL segment to a trigger.
func (s *Store) WebhookByToken(token string) (*Webhook, bool) {
	if token == "" {
		return nil, false
	}
	for _, w := range s.webhooks().list() {
		if w.Token == token {
			return w, true
		}
	}
	return nil, false
}

// AddWebhook saves a trigger.
func (s *Store) AddWebhook(w *Webhook) error {
	if w.CreatedAt.IsZero() {
		w.CreatedAt = time.Now()
	}
	return s.webhooks().add(w)
}

// UpdateWebhook mutates a trigger.
func (s *Store) UpdateWebhook(id string, fn func(*Webhook)) (*Webhook, error) {
	return s.webhooks().update(id, fn)
}

// DeleteWebhook removes a trigger.
func (s *Store) DeleteWebhook(id string) error { return s.webhooks().del(id) }

// ---------- queued events ----------

// QueuedEvents returns deliveries waiting to be replayed.
func (s *Store) QueuedEvents() []*QueuedEvent { return s.events().list() }

// AddQueuedEvent parks a delivery for replay on next launch.
func (s *Store) AddQueuedEvent(e *QueuedEvent) error {
	if e.Received.IsZero() {
		e.Received = time.Now()
	}
	return s.events().add(e)
}

// DeleteQueuedEvent drops a replayed delivery.
func (s *Store) DeleteQueuedEvent(id string) error { return s.events().del(id) }

// ---------- dev commands ----------

// Commands returns every saved command in display order.
func (s *Store) Commands() []*DevCommand {
	out := s.commands().list()
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

// CommandsFor returns a project's saved commands.
func (s *Store) CommandsFor(projectID string) []*DevCommand {
	var out []*DevCommand
	for _, c := range s.Commands() {
		if c.ProjectID == projectID {
			out = append(out, c)
		}
	}
	return out
}

// Command looks up one saved command.
func (s *Store) Command(id string) (*DevCommand, error) { return s.commands().get(id) }

// AddCommand saves a command.
func (s *Store) AddCommand(c *DevCommand) error {
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	if c.Order == 0 {
		c.Order = int(time.Now().UnixMilli())
	}
	return s.commands().add(c)
}

// UpdateCommand mutates a saved command.
func (s *Store) UpdateCommand(id string, fn func(*DevCommand)) (*DevCommand, error) {
	return s.commands().update(id, fn)
}

// DeleteCommand removes a saved command.
func (s *Store) DeleteCommand(id string) error { return s.commands().del(id) }

// ---------- ssh ----------

// SSHHosts returns every saved host.
func (s *Store) SSHHosts() []*SSHHost { return s.hosts().list() }

// SSHHost looks up one host.
func (s *Store) SSHHost(id string) (*SSHHost, error) { return s.hosts().get(id) }

// AddSSHHost saves a host.
func (s *Store) AddSSHHost(h *SSHHost) error {
	if h.CreatedAt.IsZero() {
		h.CreatedAt = time.Now()
	}
	if h.Port == 0 {
		h.Port = 22
	}
	return s.hosts().add(h)
}

// UpdateSSHHost mutates a host.
func (s *Store) UpdateSSHHost(id string, fn func(*SSHHost)) (*SSHHost, error) {
	return s.hosts().update(id, fn)
}

// DeleteSSHHost removes a host.
func (s *Store) DeleteSSHHost(id string) error { return s.hosts().del(id) }

// ---------- databases ----------

// DBConns returns every saved connection.
func (s *Store) DBConns() []*DBConn { return s.dbs().list() }

// DBConn looks up one connection.
func (s *Store) DBConn(id string) (*DBConn, error) { return s.dbs().get(id) }

// DBConnByName resolves a connection by name, which is how an agent asks for
// one over MCP without ever seeing a password.
func (s *Store) DBConnByName(name string) (*DBConn, bool) {
	for _, c := range s.dbs().list() {
		if c.Name == name {
			return c, true
		}
	}
	return nil, false
}

// AddDBConn saves a connection.
func (s *Store) AddDBConn(c *DBConn) error {
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	return s.dbs().add(c)
}

// UpdateDBConn mutates a connection.
func (s *Store) UpdateDBConn(id string, fn func(*DBConn)) (*DBConn, error) {
	return s.dbs().update(id, fn)
}

// DeleteDBConn removes a connection.
func (s *Store) DeleteDBConn(id string) error { return s.dbs().del(id) }

// ---------- agent messages ----------

// Messages returns the whole mailbox.
func (s *Store) Messages() []*AgentMessage {
	out := s.messages().list()
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Inbox returns messages addressed to one agent.
func (s *Store) Inbox(agentID string) []*AgentMessage {
	var out []*AgentMessage
	for _, m := range s.Messages() {
		if m.ToID == agentID {
			out = append(out, m)
		}
	}
	return out
}

// Message looks up one message.
func (s *Store) Message(id string) (*AgentMessage, error) { return s.messages().get(id) }

// AddMessage persists a message before delivery is even attempted, which is
// what makes an offline recipient safe.
func (s *Store) AddMessage(m *AgentMessage) error {
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	if m.State == "" {
		m.State = "queued"
	}
	return s.messages().add(m)
}

// UpdateMessage mutates a message.
func (s *Store) UpdateMessage(id string, fn func(*AgentMessage)) (*AgentMessage, error) {
	return s.messages().update(id, fn)
}

// DeleteMessage removes a message.
func (s *Store) DeleteMessage(id string) error { return s.messages().del(id) }

// ---------- pane layout ----------

// PaneGroup returns the saved split for a project.
func (s *Store) PaneGroup(projectID string) (PaneGroup, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, g := range s.state.Panes {
		if g.ProjectID == projectID {
			return g, true
		}
	}
	return PaneGroup{}, false
}

// SavePaneGroup stores a project's split layout.
func (s *Store) SavePaneGroup(g PaneGroup) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.state.Panes {
		if s.state.Panes[i].ProjectID == g.ProjectID {
			s.state.Panes[i] = g
			return s.saveLocked()
		}
	}
	s.state.Panes = append(s.state.Panes, g)
	return s.saveLocked()
}

// ---------- restore ----------

// SaveRestore records what was running, for the next launch.
func (s *Store) SaveRestore(entries []RestoreEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Restore = entries
	return s.saveLocked()
}

// TakeRestore returns the saved set and clears it, so a restore is attempted
// once rather than on every launch forever.
func (s *Store) TakeRestore() []RestoreEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.state.Restore
	s.state.Restore = nil
	_ = s.saveLocked()
	return out
}

// ---------- secrets metadata ----------

// SecretMetas returns the names and notes of vault entries. Never values.
func (s *Store) SecretMetas() []SecretMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SecretMeta, len(s.state.Secrets))
	copy(out, s.state.Secrets)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// UpsertSecretMeta records that a named secret exists.
func (s *Store) UpsertSecretMeta(m SecretMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for i := range s.state.Secrets {
		if s.state.Secrets[i].Name == m.Name {
			m.CreatedAt = s.state.Secrets[i].CreatedAt
			m.UpdatedAt = now
			s.state.Secrets[i] = m
			return s.saveLocked()
		}
	}
	m.CreatedAt = now
	m.UpdatedAt = now
	s.state.Secrets = append(s.state.Secrets, m)
	return s.saveLocked()
}

// DeleteSecretMeta forgets that a named secret existed.
func (s *Store) DeleteSecretMeta(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.state.Secrets[:0]
	for _, m := range s.state.Secrets {
		if m.Name != name {
			out = append(out, m)
		}
	}
	s.state.Secrets = out
	return s.saveLocked()
}
