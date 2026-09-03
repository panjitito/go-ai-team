package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ErrNotFound is returned when an id does not resolve.
var ErrNotFound = errors.New("not found")

// Store owns the persisted state and serialises every mutation. Writes go to a
// temp file and are renamed into place, so a crash mid-write never leaves a
// truncated state file behind.
type Store struct {
	mu    sync.RWMutex
	path  string
	root  string
	state State
}

// Root returns the Go AI Team home directory (~/.goaiteam).
func Root() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".goaiteam"), nil
}

// ProfilesDir is where managed account directories are created.
func (s *Store) ProfilesDir() string { return filepath.Join(s.root, "profiles") }

// RootDir returns the store's home directory.
func (s *Store) RootDir() string { return s.root }

// Open loads state from disk, creating a default document on first run.
func Open() (*Store, error) {
	root, err := Root()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "profiles"), 0o700); err != nil {
		return nil, err
	}
	s := &Store{
		root: root,
		path: filepath.Join(root, "state.json"),
		state: State{
			Version: 1,
			Settings: Settings{
				AutoSwitch:     true,
				ShareUserLayer: true,
				Port:           7777,
			},
		},
	}
	b, err := os.ReadFile(s.path)
	if err == nil {
		if err := json.Unmarshal(b, &s.state); err != nil {
			return nil, fmt.Errorf("state.json is corrupt: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if s.state.Settings.Port == 0 {
		s.state.Settings.Port = 7777
	}
	return s, nil
}

// NewID returns a prefixed random identifier.
func NewID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(&s.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Snapshot returns a deep-enough copy of the state for read-only use.
func (s *Store) Snapshot() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// Settings returns the current settings.
func (s *Store) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Settings
}

// UpdateSettings applies fn to the settings under lock and persists.
func (s *Store) UpdateSettings(fn func(*Settings)) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.state.Settings)
	return s.state.Settings, s.saveLocked()
}

// ---------- accounts ----------

// Accounts returns all accounts.
func (s *Store) Accounts() []*Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Account, len(s.state.Accounts))
	copy(out, s.state.Accounts)
	return out
}

// AccountsFor returns the accounts of one provider.
func (s *Store) AccountsFor(p Provider) []*Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Account
	for _, a := range s.state.Accounts {
		if a.Provider == p {
			out = append(out, a)
		}
	}
	return out
}

// Account looks up one account by id.
func (s *Store) Account(id string) (*Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.state.Accounts {
		if a.ID == id {
			return a, nil
		}
	}
	return nil, ErrNotFound
}

// AddAccount registers a new account and persists it.
func (s *Store) AddAccount(a *Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.ID == "" {
		a.ID = NewID("acc")
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	if a.Provider == "" {
		a.Provider = ProviderClaude
	}
	s.state.Accounts = append(s.state.Accounts, a)
	// First account of a provider becomes the global default when none is set.
	if s.state.Settings.DefaultAccountID == "" {
		s.state.Settings.DefaultAccountID = a.ID
	}
	return s.saveLocked()
}

// UpdateAccount mutates an account under lock.
func (s *Store) UpdateAccount(id string, fn func(*Account)) (*Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.state.Accounts {
		if a.ID == id {
			fn(a)
			return a, s.saveLocked()
		}
	}
	return nil, ErrNotFound
}

// DeleteAccount removes an account from the registry. The directory on disk is
// deliberately left alone so another profile can be pointed at it later, and
// every reference to it degrades to the cascade's next step rather than
// breaking.
func (s *Store) DeleteAccount(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.state.Accounts[:0]
	found := false
	for _, a := range s.state.Accounts {
		if a.ID == id {
			found = true
			continue
		}
		out = append(out, a)
	}
	if !found {
		return ErrNotFound
	}
	s.state.Accounts = out
	if s.state.Settings.DefaultAccountID == id {
		s.state.Settings.DefaultAccountID = ""
		if len(s.state.Accounts) > 0 {
			s.state.Settings.DefaultAccountID = s.state.Accounts[0].ID
		}
	}
	return s.saveLocked()
}

// ---------- folders ----------

// Folders returns all folders.
func (s *Store) Folders() []*Folder {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Folder, len(s.state.Folders))
	copy(out, s.state.Folders)
	return out
}

// AddFolder creates a folder.
func (s *Store) AddFolder(f *Folder) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f.ID == "" {
		f.ID = NewID("fld")
	}
	s.state.Folders = append(s.state.Folders, f)
	return s.saveLocked()
}

// UpdateFolder mutates a folder under lock.
func (s *Store) UpdateFolder(id string, fn func(*Folder)) (*Folder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.state.Folders {
		if f.ID == id {
			fn(f)
			return f, s.saveLocked()
		}
	}
	return nil, ErrNotFound
}

// DeleteFolder removes a folder and unparents anything filed under it.
func (s *Store) DeleteFolder(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.state.Folders[:0]
	for _, f := range s.state.Folders {
		if f.ID == id {
			continue
		}
		if f.ParentID == id {
			f.ParentID = ""
		}
		out = append(out, f)
	}
	s.state.Folders = out
	for _, p := range s.state.Projects {
		if p.FolderID == id {
			p.FolderID = ""
		}
	}
	return s.saveLocked()
}

// ---------- projects ----------

// Projects returns all projects.
func (s *Store) Projects() []*Project {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Project, len(s.state.Projects))
	copy(out, s.state.Projects)
	return out
}

// Project looks up one project.
func (s *Store) Project(id string) (*Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.state.Projects {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, ErrNotFound
}

// AddProject registers a project.
func (s *Store) AddProject(p *Project) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.ID == "" {
		p.ID = NewID("prj")
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	s.state.Projects = append(s.state.Projects, p)
	return s.saveLocked()
}

// UpdateProject mutates a project under lock.
func (s *Store) UpdateProject(id string, fn func(*Project)) (*Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.state.Projects {
		if p.ID == id {
			fn(p)
			return p, s.saveLocked()
		}
	}
	return nil, ErrNotFound
}

// DeleteProject removes a project and everything that belonged to it.
//
// The cascade matters: a project's tasks, ideas, memories, saved commands,
// schedules and triggers are meaningless without it, and leaving them behind
// means a deleted project keeps firing scheduled agents at a directory that is
// no longer listed. Anything scoped to the project goes with it.
func (s *Store) DeleteProject(id string) error {
	s.mu.Lock()
	found := false
	pout := s.state.Projects[:0]
	for _, p := range s.state.Projects {
		if p.ID == id {
			found = true
			continue
		}
		pout = append(pout, p)
	}
	s.state.Projects = pout

	aout := s.state.Agents[:0]
	for _, a := range s.state.Agents {
		if a.ProjectID != id {
			aout = append(aout, a)
		}
	}
	s.state.Agents = aout

	// Panes are keyed by project, so the saved layout goes too.
	gout := s.state.Panes[:0]
	for _, g := range s.state.Panes {
		if g.ProjectID != id {
			gout = append(gout, g)
		}
	}
	s.state.Panes = gout

	if err := s.saveLocked(); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()

	if !found {
		return ErrNotFound
	}

	// These take the lock themselves, so they run after it is released.
	_, _ = s.tasks().delWhere(func(t *Task) bool { return t.ProjectID == id })
	_, _ = s.ideas().delWhere(func(i *Idea) bool { return i.ProjectID == id })
	_, _ = s.memories().delWhere(func(m *Memory) bool { return m.ProjectID == id })
	_, _ = s.commands().delWhere(func(c *DevCommand) bool { return c.ProjectID == id })
	_, _ = s.schedules().delWhere(func(x *Schedule) bool { return x.ProjectID == id })
	_, _ = s.webhooks().delWhere(func(w *Webhook) bool { return w.ProjectID == id })
	_, _ = s.messages().delWhere(func(m *AgentMessage) bool { return m.ProjectID == id })
	// A prompt or skill scoped to the project goes; a global one stays.
	_, _ = s.prompts().delWhere(func(x *Prompt) bool { return x.ProjectID == id })
	_, _ = s.skills().delWhere(func(x *Skill) bool { return x.ProjectID == id })
	return nil
}

// ---------- agents ----------

// Agents returns all agents.
func (s *Store) Agents() []*Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Agent, len(s.state.Agents))
	copy(out, s.state.Agents)
	return out
}

// Agent looks up one agent.
func (s *Store) Agent(id string) (*Agent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.state.Agents {
		if a.ID == id {
			return a, nil
		}
	}
	return nil, ErrNotFound
}

// AddAgent registers an agent.
func (s *Store) AddAgent(a *Agent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.ID == "" {
		a.ID = NewID("agt")
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	if a.Provider == "" {
		a.Provider = ProviderClaude
	}
	s.state.Agents = append(s.state.Agents, a)
	return s.saveLocked()
}

// UpdateAgent mutates an agent under lock.
func (s *Store) UpdateAgent(id string, fn func(*Agent)) (*Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.state.Agents {
		if a.ID == id {
			fn(a)
			return a, s.saveLocked()
		}
	}
	return nil, ErrNotFound
}

// DeleteAgent removes an agent.
func (s *Store) DeleteAgent(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.state.Agents[:0]
	found := false
	for _, a := range s.state.Agents {
		if a.ID == id {
			found = true
			continue
		}
		out = append(out, a)
	}
	if !found {
		return ErrNotFound
	}
	s.state.Agents = out
	return s.saveLocked()
}

// ---------- cascade ----------

// Resolution records which account an agent will run on and why.
type Resolution struct {
	AccountID string `json:"accountId"`
	Dir       string `json:"dir"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	// Source is one of: agent, project, folder, default, system.
	Source string `json:"source"`
}

// ResolveAccount implements the cascade: agent override, project pin, nearest
// folder that pins an account, global default, then the provider's own default
// directory (~/.claude). It is the single place that answers "which account is
// this about to spend", and every caller — spawn, token meter, badges — goes
// through it so they can never disagree.
func (s *Store) ResolveAccount(agentID, projectID string, p Provider) Resolution {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.resolveLocked(agentID, projectID, p)
}

func (s *Store) resolveLocked(agentID, projectID string, p Provider) Resolution {
	pick := func(id, source string) (Resolution, bool) {
		if id == "" {
			return Resolution{}, false
		}
		for _, a := range s.state.Accounts {
			if a.ID == id && a.Provider == p {
				return Resolution{
					AccountID: a.ID, Dir: a.Dir, Name: a.Name,
					Color: a.Color, Source: source,
				}, true
			}
		}
		// A dangling reference (deleted account) falls through to the next
		// step rather than breaking the agent.
		return Resolution{}, false
	}

	var agent *Agent
	for _, a := range s.state.Agents {
		if a.ID == agentID {
			agent = a
			break
		}
	}
	if agent != nil {
		if r, ok := pick(agent.AccountID, "agent"); ok {
			return r
		}
		if projectID == "" {
			projectID = agent.ProjectID
		}
	}

	var project *Project
	for _, pr := range s.state.Projects {
		if pr.ID == projectID {
			project = pr
			break
		}
	}
	if project != nil {
		if r, ok := pick(project.AccountID, "project"); ok {
			return r
		}
		// Walk up the folder chain, nearest first.
		seen := map[string]bool{}
		fid := project.FolderID
		for fid != "" && !seen[fid] {
			seen[fid] = true
			var f *Folder
			for _, cand := range s.state.Folders {
				if cand.ID == fid {
					f = cand
					break
				}
			}
			if f == nil {
				break
			}
			if r, ok := pick(f.AccountID, "folder"); ok {
				return r
			}
			fid = f.ParentID
		}
	}

	if r, ok := pick(s.state.Settings.DefaultAccountID, "default"); ok {
		return r
	}
	return Resolution{Source: "system", Name: "System default", Dir: ""}
}
