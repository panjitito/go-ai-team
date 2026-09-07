// Package accounts manages provider account directories.
//
// The whole multi-account feature rests on one mechanism each agent CLI already
// documents: an environment variable that points the CLI at a different config
// directory. Claude Code reads CLAUDE_CONFIG_DIR, Codex reads CODEX_HOME, and
// so on. An account, therefore, is not a database row with a token in it — it
// is a directory. Go AI Team never reads, copies or transmits credentials; it
// decides which directory a process is launched against.
package accounts

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/panjitito/go-ai-team/internal/claudefs"
	"github.com/panjitito/go-ai-team/internal/store"
)

// Palette is the default set of account colours. The colour is what makes
// "which account is this spending" readable at a glance instead of something
// you look up: it shows as a dot on the agent card, the terminal header and the
// project tile.
var Palette = []string{
	"#f59e0b", // amber
	"#06b6d4", // cyan
	"#a855f7", // violet
	"#ef4444", // red
	"#22c55e", // green
	"#3b82f6", // blue
	"#ec4899", // pink
	"#14b8a6", // teal
}

// Manager creates and inspects account directories.
type Manager struct {
	st *store.Store
}

// New returns a Manager backed by st.
func New(st *store.Store) *Manager { return &Manager{st: st} }

// SystemDir returns the provider's own default config directory, the last step
// of the cascade.
func SystemDir(p store.Provider) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch p {
	case store.ProviderCodex:
		return filepath.Join(home, ".codex")
	case store.ProviderGrok:
		return filepath.Join(home, ".grok")
	case store.ProviderCursor:
		return filepath.Join(home, ".cursor")
	case store.ProviderGemini:
		return filepath.Join(home, ".gemini")
	default:
		return filepath.Join(home, ".claude")
	}
}

// Status describes an account's live state on disk.
type Status struct {
	*store.Account

	// SignedIn means an agent launched on this account can expect to reach the
	// API. It is derived from the credentials' contents, not from the file
	// merely existing: logging out leaves the file behind with its tokens
	// blanked, and reporting that as ready would be a lie the auto-switch
	// fallback then acts on.
	SignedIn bool                 `json:"signedIn"`
	Auth     claudefs.Credentials `json:"auth"`
	Label    string               `json:"label,omitempty"`

	Exists   bool      `json:"exists"`
	Benched  bool      `json:"benched"`
	Sessions int       `json:"sessions"`
	Projects int       `json:"projects"`
	LastUsed time.Time `json:"lastUsed,omitempty"`
}

// Status reads the on-disk state of one account.
func (m *Manager) Status(a *store.Account) Status {
	s := Status{Account: a, Benched: a.Benched()}
	if a.Dir == "" {
		return s
	}
	if st, err := os.Stat(a.Dir); err == nil && st.IsDir() {
		s.Exists = true
	}
	s.Auth = claudefs.ReadCredentials(a.Dir)
	s.SignedIn = s.Auth.State.Usable()
	s.Label = claudefs.AccountLabel(a.Dir)
	if entries, err := os.ReadDir(filepath.Join(a.Dir, "sessions")); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") {
				s.Sessions++
			}
		}
	}
	if entries, err := os.ReadDir(filepath.Join(a.Dir, "projects")); err == nil {
		s.Projects = len(entries)
		for _, e := range entries {
			if info, err := e.Info(); err == nil && info.ModTime().After(s.LastUsed) {
				s.LastUsed = info.ModTime()
			}
		}
	}
	return s
}

// Statuses returns the live state of every registered account.
func (m *Manager) Statuses() []Status {
	accs := m.st.Accounts()
	out := make([]Status, 0, len(accs))
	for _, a := range accs {
		out = append(out, m.Status(a))
	}
	return out
}

// Dirs returns every known config directory for a provider plus the provider's
// system default, which is the search space for PID lookups and token reading.
func (m *Manager) Dirs(p store.Provider) []string {
	var out []string
	seen := map[string]bool{}
	add := func(d string) {
		if d == "" || seen[d] {
			return
		}
		seen[d] = true
		out = append(out, d)
	}
	for _, a := range m.st.AccountsFor(p) {
		add(a.Dir)
	}
	add(SystemDir(p))
	return out
}

// nextColor picks the first palette entry not already in use.
func (m *Manager) nextColor() string {
	used := map[string]bool{}
	for _, a := range m.st.Accounts() {
		used[a.Color] = true
	}
	for _, c := range Palette {
		if !used[c] {
			return c
		}
	}
	return Palette[len(m.st.Accounts())%len(Palette)]
}

// CreateManaged registers a new account backed by a directory Go AI Team owns,
// under ~/.goaiteam/profiles/<id>. The directory starts empty; credentials
// arrive only when the user completes the sign-in flow inside it.
func (m *Manager) CreateManaged(name string, p store.Provider) (*store.Account, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("account name is required")
	}
	if p == "" {
		p = store.ProviderClaude
	}
	id := store.NewID("acc")
	dir := filepath.Join(m.st.ProfilesDir(), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	a := &store.Account{
		ID:       id,
		Name:     strings.TrimSpace(name),
		Provider: p,
		Dir:      dir,
		Color:    m.nextColor(),
		Managed:  true,
	}
	if err := m.st.AddAccount(a); err != nil {
		return nil, err
	}
	return a, nil
}

// Attach registers an account pointed at a directory that already exists. This
// is the path that makes existing setups keep working: point it at
// ~/.ccs/instances/work to reuse a Claude Code Switcher profile, or at ~/.claude
// itself to manage the account you are already signed in to, with no re-login.
func (m *Manager) Attach(name, dir string, p store.Provider) (*store.Account, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("account name is required")
	}
	dir = expandHome(strings.TrimSpace(dir))
	if dir == "" {
		return nil, fmt.Errorf("directory is required")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", abs, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", abs)
	}
	for _, ex := range m.st.Accounts() {
		if sameDir(ex.Dir, abs) {
			return nil, fmt.Errorf("%q already uses that directory", ex.Name)
		}
	}
	if p == "" {
		p = store.ProviderClaude
	}
	a := &store.Account{
		ID:       store.NewID("acc"),
		Name:     strings.TrimSpace(name),
		Provider: p,
		Dir:      abs,
		Color:    m.nextColor(),
		Managed:  false,
	}
	if err := m.st.AddAccount(a); err != nil {
		return nil, err
	}
	return a, nil
}

// sameDir compares two paths, case-insensitively on Windows.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ca := filepath.Clean(a)
	cb := filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(ca, cb)
	}
	return ca == cb
}

// expandHome resolves a leading ~ so the UI can accept ~/.ccs/instances/work.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p[1:], "/"), `\`))
		}
	}
	return p
}

// ExpandHome is the exported form used by the HTTP layer.
func ExpandHome(p string) string { return expandHome(p) }

// Discover looks for account directories the user already has but has not
// registered: the provider default and any CCS instances. It turns a first run
// from "set everything up" into "tick the ones you want".
type Discovered struct {
	Name     string         `json:"name"`
	Dir      string         `json:"dir"`
	Provider store.Provider `json:"provider"`
	SignedIn bool           `json:"signedIn"`
	Origin   string         `json:"origin"`
	Known    bool           `json:"known"`
}

// Discover returns candidate directories for every provider.
func (m *Manager) Discover() []Discovered {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	known := map[string]bool{}
	for _, a := range m.st.Accounts() {
		known[strings.ToLower(filepath.Clean(a.Dir))] = true
	}
	var out []Discovered
	add := func(name, dir string, p store.Provider, origin string) {
		st, err := os.Stat(dir)
		if err != nil || !st.IsDir() {
			return
		}
		out = append(out, Discovered{
			Name: name, Dir: dir, Provider: p,
			SignedIn: claudefs.SignedIn(dir),
			Origin:   origin,
			Known:    known[strings.ToLower(filepath.Clean(dir))],
		})
	}

	for _, p := range store.Providers {
		add(string(p)+" (default)", SystemDir(p), p, "provider default")
	}

	// CCS (Claude Code Switcher) keeps one profile per directory here.
	ccs := filepath.Join(home, ".ccs", "instances")
	if entries, err := os.ReadDir(ccs); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				add("ccs: "+e.Name(), filepath.Join(ccs, e.Name()), store.ProviderClaude, "CCS profile")
			}
		}
	}
	// A common hand-rolled convention: ~/.claude-work, ~/.claude-personal.
	//
	// One sibling is skipped: the directory this process was itself started
	// with. When Go AI Team is launched from a Claude Code session, offering
	// that session's own config as a managed account invites the two to write
	// the same credentials file from different processes.
	self := strings.ToLower(filepath.Clean(os.Getenv(store.ProviderClaude.EnvVar())))
	if entries, err := os.ReadDir(home); err == nil {
		for _, e := range entries {
			n := e.Name()
			if !e.IsDir() || !strings.HasPrefix(n, ".claude-") {
				continue
			}
			dir := filepath.Join(home, n)
			if self != "." && strings.ToLower(filepath.Clean(dir)) == self {
				continue
			}
			add(strings.TrimPrefix(n, "."), dir, store.ProviderClaude, "sibling directory")
		}
	}
	return out
}

// DeleteManagedDir removes a managed profile directory from disk. It refuses to
// touch a directory the user attached, because that data is not ours: deleting
// an account in the UI unregisters it, and only an explicit purge of a managed
// profile ever removes files.
func (m *Manager) DeleteManagedDir(a *store.Account) error {
	if !a.Managed {
		return fmt.Errorf("refusing to delete %s: directory was attached, not created by Go AI Team", a.Dir)
	}
	if !strings.HasPrefix(filepath.Clean(a.Dir), filepath.Clean(m.st.ProfilesDir())) {
		return fmt.Errorf("refusing to delete %s: outside the profiles directory", a.Dir)
	}
	return os.RemoveAll(a.Dir)
}
