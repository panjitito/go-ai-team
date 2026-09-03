package store

import "time"

// This file holds everything beyond accounts, projects and agents: the work
// intake, the libraries, the environment and the automation. Kept separate from
// types.go so the account model — the part the whole app rests on — stays easy
// to read on its own.

// ---------- work intake ----------

// TaskStatus is a column on the board.
type TaskStatus string

const (
	TaskBacklog    TaskStatus = "backlog"
	TaskTodo       TaskStatus = "todo"
	TaskInProgress TaskStatus = "in_progress"
	TaskReview     TaskStatus = "review"
	TaskDone       TaskStatus = "done"
)

// BoardColumns is the left-to-right order of the board.
var BoardColumns = []TaskStatus{TaskBacklog, TaskTodo, TaskInProgress, TaskReview, TaskDone}

// Task is one unit of work. Dragging it to in_progress is what starts an agent,
// so a task is both a note and a trigger.
type Task struct {
	ID        string     `json:"id"`
	ProjectID string     `json:"projectId"`
	Title     string     `json:"title"`
	Body      string     `json:"body,omitempty"`
	Status    TaskStatus `json:"status"`
	Order     int        `json:"order"`

	// AgentID is the agent that should pick this up, if pinned.
	AgentID string `json:"agentId,omitempty"`
	// SessionID records which run is executing it.
	SessionID string `json:"sessionId,omitempty"`

	Labels   []string `json:"labels,omitempty"`
	Priority int      `json:"priority"` // 0 normal, 1 high, -1 low

	// Source says where the ticket came from: manual, public, webhook, radar.
	Source string `json:"source,omitempty"`
	// Reporter is a free-text submitter name for public tickets.
	Reporter string `json:"reporter,omitempty"`
	Votes    int    `json:"votes"`

	// Attachments are file paths (screenshots, Figma captures) that travel with
	// the ticket, so the agent that picks it up later still sees what was asked.
	Attachments []string `json:"attachments,omitempty"`

	// Scoping holds a PM agent's mockup/clarification thread for this ticket.
	Comments []Comment `json:"comments,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	DoneAt    time.Time `json:"doneAt,omitempty"`
}

// Comment is one message on a ticket, from a person or an agent.
type Comment struct {
	ID        string    `json:"id"`
	Author    string    `json:"author"`
	IsAgent   bool      `json:"isAgent"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

// Idea is raw, unsorted feedback before it earns a ticket.
type Idea struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"projectId"`
	Body      string    `json:"body"`
	Source    string    `json:"source,omitempty"`
	Reporter  string    `json:"reporter,omitempty"`
	Theme     string    `json:"theme,omitempty"`
	Impact    int       `json:"impact"`
	Effort    int       `json:"effort"`
	Votes     int       `json:"votes"`
	Promoted  string    `json:"promoted,omitempty"` // task id, once promoted
	CreatedAt time.Time `json:"createdAt"`
}

// ---------- libraries ----------

// Prompt is a saved, reusable brief. A prompt may reference other prompts with
// {{prompt:name}}, which is resolved at send time, so shared rules live in one
// place instead of being copied and drifting.
type Prompt struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Folder    string    `json:"folder,omitempty"`
	Body      string    `json:"body"`
	ProjectID string    `json:"projectId,omitempty"` // empty = global
	Personal  bool      `json:"personal"`            // excluded from git export
	Uses      int       `json:"uses"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Skill is a modular procedure in SKILL.md form, attachable to agents or tasks
// and exportable to other runtimes.
type Skill struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Body        string    `json:"body"`
	Triggers    []string  `json:"triggers,omitempty"`
	ProjectID   string    `json:"projectId,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// MemoryKind groups what an agent learned.
type MemoryKind string

const (
	MemArchitecture MemoryKind = "architecture"
	MemDecision     MemoryKind = "decisions"
	MemPitfall      MemoryKind = "pitfalls"
	MemConvention   MemoryKind = "conventions"
	MemFeature      MemoryKind = "features"
)

// Memory is a fact an agent wrote down that outlives its session. Every agent
// on the project reads the same set, which is the point: it is the shared brain
// of the codebase rather than one conversation's context.
type Memory struct {
	ID        string     `json:"id"`
	ProjectID string     `json:"projectId"`
	Kind      MemoryKind `json:"kind"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Author    string     `json:"author,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// ---------- automation ----------

// Schedule runs an agent on a recurring cadence, with no cron expression to
// write.
type Schedule struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	Prompt    string `json:"prompt"`
	AgentID   string `json:"agentId,omitempty"`
	Role      string `json:"role,omitempty"`

	// Every is one of: minutes, hourly, daily, weekly, monthly.
	Every  string `json:"every"`
	N      int    `json:"n"`      // for "minutes": every N minutes
	Minute int    `json:"minute"` // for hourly/daily/weekly/monthly
	Hour   int    `json:"hour"`   // for daily/weekly/monthly
	Wday   int    `json:"wday"`   // 0=Sunday, for weekly
	Mday   int    `json:"mday"`   // for monthly

	Enabled  bool      `json:"enabled"`
	LastRun  time.Time `json:"lastRun,omitempty"`
	NextRun  time.Time `json:"nextRun,omitempty"`
	RunCount int       `json:"runCount"`
	LastErr  string    `json:"lastErr,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
}

// Webhook starts an agent on an outside event instead of a clock.
type Webhook struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	Prompt    string `json:"prompt"`
	AgentID   string `json:"agentId,omitempty"`

	// Token is the public path segment; Secret signs the payload.
	Token  string `json:"token"`
	Secret string `json:"secret"`

	// Filter is a simple expression on the payload, e.g. action == "opened".
	Filter string `json:"filter,omitempty"`

	// BurstLimit caps runs per hour so a noisy service cannot start twenty
	// agents at once.
	BurstLimit int `json:"burstLimit"`

	Enabled   bool      `json:"enabled"`
	Hits      int       `json:"hits"`
	Runs      int       `json:"runs"`
	LastHit   time.Time `json:"lastHit,omitempty"`
	LastErr   string    `json:"lastErr,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// QueuedEvent is a webhook delivery that arrived while nothing could run it.
type QueuedEvent struct {
	ID        string            `json:"id"`
	WebhookID string            `json:"webhookId"`
	Vars      map[string]string `json:"vars"`
	Received  time.Time         `json:"received"`
}

// ---------- environment ----------

// DevCommand is a saved per-project command: a dev server, a build, a test
// watcher. Remembered so it is one click instead of one recalled incantation.
type DevCommand struct {
	ID        string            `json:"id"`
	ProjectID string            `json:"projectId"`
	Name      string            `json:"name"`
	Command   string            `json:"command"`
	Dir       string            `json:"dir,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Autostart bool              `json:"autostart"`
	Order     int               `json:"order"`
	CreatedAt time.Time         `json:"createdAt"`
}

// SSHHost is a saved remote server an agent can run on.
type SSHHost struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	User    string `json:"user"`
	KeyPath string `json:"keyPath,omitempty"`
	// SecretRef names a secret in the vault holding the password or passphrase.
	// The value itself never lives here.
	SecretRef string    `json:"secretRef,omitempty"`
	ProjectID string    `json:"projectId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// DBConn is a saved database connection. Read-only unless explicitly made
// writable, and a production flag makes a write confirmation harder.
type DBConn struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Driver string `json:"driver"` // mysql | postgres | mongodb
	Host   string `json:"host"`
	Port   int    `json:"port"`
	User   string `json:"user"`
	DBName string `json:"dbName"`
	TLS    bool   `json:"tls"`

	SecretRef string `json:"secretRef,omitempty"`

	// Writable must be true, and the caller must confirm, before any statement
	// that changes data is allowed through.
	Writable   bool `json:"writable"`
	Production bool `json:"production"`

	// TunnelHostID routes the connection through a saved SSH host.
	TunnelHostID string `json:"tunnelHostId,omitempty"`

	ProjectID string    `json:"projectId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// SecretMeta is the non-secret half of a vault entry. Values live in the
// encrypted vault file and are never stored here, never returned to an agent
// and never sent over MCP.
type SecretMeta struct {
	Name      string    `json:"name"`
	Note      string    `json:"note,omitempty"`
	ProjectID string    `json:"projectId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ---------- layout and messaging ----------

// PaneGroup is a split-view layout, saved per project so a split survives a
// restart instead of being rebuilt every morning.
type PaneGroup struct {
	ProjectID   string   `json:"projectId"`
	Orientation string   `json:"orientation"` // "cols" | "rows"
	AgentIDs    []string `json:"agentIds"`
	Pinned      []string `json:"pinned,omitempty"`
}

// AgentMessage is mail between agents. Persisted before delivery is attempted,
// so an offline recipient, a crashed CLI or an app restart never loses one.
type AgentMessage struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"projectId"`
	FromID    string    `json:"fromId"`
	FromName  string    `json:"fromName"`
	ToID      string    `json:"toId"`
	Subject   string    `json:"subject"`
	Body      string    `json:"body"`
	State     string    `json:"state"` // queued | delivered | read
	CreatedAt time.Time `json:"createdAt"`
	Delivered time.Time `json:"delivered,omitempty"`
	ReadAt    time.Time `json:"readAt,omitempty"`
}

// RestoreEntry remembers one thing that was running at shutdown.
type RestoreEntry struct {
	Kind      string `json:"kind"` // "agent" | "command"
	AgentID   string `json:"agentId,omitempty"`
	CommandID string `json:"commandId,omitempty"`
	ProjectID string `json:"projectId"`
	SessionID string `json:"claudeSessionId,omitempty"`
}

// ---------- catalogue ----------

// RoleDef is a selectable agent role with its own system prompt.
type RoleDef struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Tagline   string   `json:"tagline"`
	Color     string   `json:"color"`
	Model     string   `json:"model"`
	Skills    []string `json:"skills,omitempty"`
	BestFor   string   `json:"bestFor,omitempty"`
	Prompt    string   `json:"prompt"`
	Division  string   `json:"division,omitempty"`
	Builtin   bool     `json:"builtin"`
	Community bool     `json:"community,omitempty"`
}
