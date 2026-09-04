package store

import "time"

// Provider identifies an agent CLI family. v1 focuses on Claude Code but the
// account model is provider-shaped from the start so Codex/Grok/Cursor drop in
// without a redesign: each of them isolates an account behind one env var.
type Provider string

const (
	ProviderClaude Provider = "claude"
	ProviderCodex  Provider = "codex"
	ProviderGrok   Provider = "grok"
	ProviderCursor Provider = "cursor"
)

// EnvVar is the environment variable that rebinds a provider's active account.
func (p Provider) EnvVar() string {
	switch p {
	case ProviderCodex:
		return "CODEX_HOME"
	case ProviderGrok:
		return "GROK_HOME"
	case ProviderCursor:
		return "CURSOR_CONFIG_DIR"
	default:
		return "CLAUDE_CONFIG_DIR"
	}
}

// Bin is the default executable name for the provider.
func (p Provider) Bin() string {
	switch p {
	case ProviderCodex:
		return "codex"
	case ProviderGrok:
		return "grok"
	case ProviderCursor:
		return "cursor-agent"
	default:
		return "claude"
	}
}

// Account is one signed-in CLI account, materialised as a config directory.
// Everything that identifies the account — OAuth credentials, sessions,
// transcripts, per-account settings — lives inside Dir and is never copied
// between accounts.
type Account struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Provider Provider `json:"provider"`
	Dir      string   `json:"dir"`
	Color    string   `json:"color"`

	// Managed is true when Go AI Team created the directory under
	// ~/.goaiteam/profiles/. A false value means the user pointed the account
	// at an existing directory (a CCS instance, ~/.claude, anything).
	Managed bool `json:"managed"`

	// ExcludeFromFallback keeps an account out of the auto-switch pool while
	// still allowing agents explicitly bound to it to run. This is how you stop
	// an employer's subscription from silently paying for personal work.
	ExcludeFromFallback bool `json:"excludeFromFallback"`

	// BenchedUntil marks an account whose quota is exhausted. Zero means usable.
	BenchedUntil time.Time `json:"benchedUntil,omitempty"`
	BenchReason  string    `json:"benchReason,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
}

// Benched reports whether the account is currently sidelined for quota.
func (a *Account) Benched() bool {
	return !a.BenchedUntil.IsZero() && time.Now().Before(a.BenchedUntil)
}

// Folder groups projects in the sidebar and can pin an account for everything
// filed under it, sub-folders included.
type Folder struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ParentID  string `json:"parentId,omitempty"`
	AccountID string `json:"accountId,omitempty"`
}

// Project is a working directory agents are launched in.
type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	FolderID  string    `json:"folderId,omitempty"`
	AccountID string    `json:"accountId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// Agent is a saved, reusable seat in a project: a role, a provider, a model and
// optionally its own account override.
type Agent struct {
	ID        string   `json:"id"`
	ProjectID string   `json:"projectId"`
	Name      string   `json:"name"`
	Role      string   `json:"role"`
	Color     string   `json:"color"`
	Provider  Provider `json:"provider"`

	// AccountID overrides the project pin for this agent only.
	AccountID string `json:"accountId,omitempty"`

	Model     string            `json:"model,omitempty"`
	ExtraArgs string            `json:"extraArgs,omitempty"`
	Env       map[string]string `json:"env,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
}

// Settings holds machine-wide preferences.
type Settings struct {
	// DefaultAccountID is the global default, used when neither the agent, the
	// project nor any parent folder pins one.
	DefaultAccountID string `json:"defaultAccountId,omitempty"`

	// ShareUserLayer links the user's own slash commands, skills, agents,
	// CLAUDE.md, hooks and plugins into every managed profile, so a fresh
	// account does not start with an empty user layer. Credentials, sessions
	// and transcripts always stay isolated.
	ShareUserLayer bool `json:"shareUserLayer"`

	// AutoSwitch hands a quota-exhausted agent to another signed-in account.
	AutoSwitch bool `json:"autoSwitch"`

	// ClaudeBin overrides the executable used for the claude provider.
	ClaudeBin string `json:"claudeBin,omitempty"`

	Port int `json:"port,omitempty"`

	// SkipPermissions passes --dangerously-skip-permissions to Claude agents.
	SkipPermissions bool `json:"skipPermissions"`

	// RestoreOnLaunch reopens the agents and dev commands that were running
	// when the app was last closed.
	RestoreOnLaunch bool `json:"restoreOnLaunch"`

	// Window is the native window's last position and size, so the app reopens
	// where it was left instead of jumping back to the middle of the screen.
	Window *WindowBounds `json:"window,omitempty"`

	// ProcessGuard sweeps agent child processes for ones that are large, old
	// and idle on the processor, and flags them. Nothing is ever killed without
	// being asked for.
	ProcessGuard bool `json:"processGuard"`

	// ContextCanary warns when an agent stops reporting progress, which is the
	// early sign of context rot.
	ContextCanary bool `json:"contextCanary"`

	// AdaptiveModel suggests the cheapest adequate model before a prompt is
	// sent.
	AdaptiveModel bool `json:"adaptiveModel"`

	// AIAccountID is the account used for the app's own helper calls (commit
	// messages, summaries, suggestions). Empty means the cascade default.
	// These run headless against a CLI you already pay for, so they are not
	// metered by us and there is no key to add.
	AIAccountID string `json:"aiAccountId,omitempty"`

	// AIModel is the model those helper calls use. Cheap by default: none of
	// them needs a flagship.
	AIModel string `json:"aiModel,omitempty"`

	// CommitFormat is the house style applied to generated commit messages.
	CommitFormat string `json:"commitFormat,omitempty"`

	// GistCommitContext attaches each commit's agent conversation as an
	// unlisted gist and links it from the commit message.
	GistCommitContext bool `json:"gistCommitContext"`
}

// State is the whole persisted document.
type State struct {
	Version  int        `json:"version"`
	Accounts []*Account `json:"accounts"`
	Folders  []*Folder  `json:"folders"`
	Projects []*Project `json:"projects"`
	Agents   []*Agent   `json:"agents"`
	Settings Settings   `json:"settings"`

	// Work intake.
	Tasks []*Task `json:"tasks,omitempty"`
	Ideas []*Idea `json:"ideas,omitempty"`

	// Libraries.
	Prompts  []*Prompt `json:"prompts,omitempty"`
	Skills   []*Skill  `json:"skills,omitempty"`
	Memories []*Memory `json:"memories,omitempty"`

	// Automation.
	Schedules []*Schedule    `json:"schedules,omitempty"`
	Webhooks  []*Webhook     `json:"webhooks,omitempty"`
	Events    []*QueuedEvent `json:"events,omitempty"`

	// Environment.
	Commands []*DevCommand `json:"commands,omitempty"`
	SSHHosts []*SSHHost    `json:"sshHosts,omitempty"`
	DBConns  []*DBConn     `json:"dbConns,omitempty"`
	Secrets  []SecretMeta  `json:"secrets,omitempty"`

	// Layout, mail and restore.
	Panes    []PaneGroup     `json:"panes,omitempty"`
	Messages []*AgentMessage `json:"messages,omitempty"`
	Restore  []RestoreEntry  `json:"restore,omitempty"`
}

// WindowBounds is the desktop window's remembered geometry. Kept here rather
// than in a separate file so it is saved and restored with everything else.
type WindowBounds struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	W         int  `json:"w"`
	H         int  `json:"h"`
	Maximized bool `json:"maximized"`
}
