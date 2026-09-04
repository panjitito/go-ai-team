// Package session runs agent CLIs in pseudo-terminals and keeps track of what
// each one is doing.
//
// A session is a real CLI process — claude, codex, whatever the agent is bound
// to — launched with the environment that selects its account. Go AI Team does
// not wrap or proxy the CLI; it decides the working directory, the environment
// and the arguments, then gets out of the way.
package session

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/aymanbagabas/go-pty"

	"github.com/uniair/go-ai-team/internal/accounts"
	"github.com/uniair/go-ai-team/internal/claudefs"
	"github.com/uniair/go-ai-team/internal/store"
)

// Status is an agent's live state, the thing the coloured dot reads from.
type Status string

const (
	StatusStarting Status = "starting"
	StatusIdle     Status = "idle"
	StatusWorking  Status = "working"
	StatusWaiting  Status = "waiting"
	StatusDone     Status = "done"
	StatusExited   Status = "exited"
	StatusError    Status = "error"
)

const scrollbackBytes = 256 * 1024

// errNotFound mirrors the store's error so callers can treat both the same way.
var errNotFound = store.ErrNotFound

// Kind separates real agent work from the throwaway PTY used to sign an
// account in, so a login terminal never appears as an agent on the dashboard.
type Kind string

const (
	KindAgent Kind = "agent"
	KindLogin Kind = "login"
)

// Session is one running CLI process.
type Session struct {
	ID        string         `json:"id"`
	Kind      Kind           `json:"kind"`
	AgentID   string         `json:"agentId,omitempty"`
	ProjectID string         `json:"projectId,omitempty"`
	Provider  store.Provider `json:"provider"`

	// Label names a non-agent session, such as a saved dev command.
	Label string `json:"label,omitempty"`
	// CommandID links a session back to the saved command that started it.
	CommandID string `json:"commandId,omitempty"`

	// AccountID and AccountDir are frozen at spawn time. The cascade is
	// resolved once, here, so the badge on screen always names the account the
	// process is really running on rather than what the config says today.
	AccountID    string `json:"accountId"`
	AccountDir   string `json:"accountDir"`
	AccountName  string `json:"accountName"`
	AccountColor string `json:"accountColor"`

	CWD       string    `json:"cwd"`
	Command   string    `json:"command"`
	PID       int       `json:"pid"`
	Status    Status    `json:"status"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt,omitempty"`
	ExitCode  int       `json:"exitCode"`
	Error     string    `json:"error,omitempty"`

	// ClaudeSessionID is discovered from sessions/<pid>.json once the CLI
	// writes it, and is what lets the token meter find the transcript.
	ClaudeSessionID string `json:"claudeSessionId,omitempty"`

	// AccountVerified is set once we have seen this process's own session file
	// inside the account directory we bound it to. Until then the badge shows
	// what we intended; after it, what disk confirms.
	AccountVerified bool `json:"accountVerified"`

	// SwitchCount records how many times auto-switch rescued this session.
	SwitchCount int      `json:"switchCount"`
	SwitchLog   []string `json:"switchLog,omitempty"`

	Tokens claudefs.TokenStats `json:"tokens"`

	// resume carries the Claude session id across an auto-switch relaunch.
	resumeID string

	mu sync.Mutex
	// ptyOnce guards the pseudo-terminal against being closed twice.
	//
	// This is not tidiness. On Windows a pty is a pseudoconsole, and calling
	// ClosePseudoConsole on an already-closed handle takes the whole process
	// down instantly — no panic, no error, no signal, nothing in the log. Two
	// closes were reachable together: Stop closes the pty, and the read loop
	// then sees EOF and closes it again on its way out. Pressing Stop killed
	// Go AI Team itself, and every other agent that was running with it.
	ptyOnce sync.Once

	// sendMu serialises prompt delivery. Confirming one message means pressing
	// Enter at the terminal, which must not land in the next message being typed.
	sendMu sync.Mutex

	pty     pty.Pty
	cmd     *pty.Cmd
	ring    *ring
	subs    map[chan []byte]struct{}
	closed  bool
	lastOut time.Time
	// recent holds the tail used for limit detection, kept small so scanning
	// it on every read stays cheap.
	recent      []byte
	switchAfter time.Time
	// lastTokenMove is when the transcript last grew. Together with lastOut it
	// separates "thinking" from "waiting on you".
	lastTokenMove time.Time
	// inputReady is set once the CLI has been seen to settle, after which
	// prompts are written without waiting.
	inputReady bool
}

// Manager owns every live session.
type Manager struct {
	st   *store.Store
	accs *accounts.Manager

	mu       sync.RWMutex
	sessions map[string]*Session

	// events fans out state changes to websocket subscribers.
	evMu   sync.Mutex
	evSubs map[chan Event]struct{}
}

// Event is a state change pushed to the UI.
type Event struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId,omitempty"`
	AgentID   string `json:"agentId,omitempty"`
	Message   string `json:"message,omitempty"`
	Payload   any    `json:"payload,omitempty"`
}

// NewManager returns a Manager and starts its background pollers.
func NewManager(st *store.Store, accs *accounts.Manager) *Manager {
	m := &Manager{
		st:       st,
		accs:     accs,
		sessions: map[string]*Session{},
		evSubs:   map[chan Event]struct{}{},
	}
	go m.pollLoop()
	go m.unbenchLoop()
	return m
}

// Subscribe returns a channel of events plus a cancel func.
func (m *Manager) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	m.evMu.Lock()
	m.evSubs[ch] = struct{}{}
	m.evMu.Unlock()
	return ch, func() {
		m.evMu.Lock()
		if _, ok := m.evSubs[ch]; ok {
			delete(m.evSubs, ch)
			close(ch)
		}
		m.evMu.Unlock()
	}
}

func (m *Manager) emit(e Event) {
	m.evMu.Lock()
	defer m.evMu.Unlock()
	for ch := range m.evSubs {
		select {
		case ch <- e:
		default: // a slow client never blocks the PTY reader
		}
	}
}

// Sessions returns a snapshot of every session.
func (m *Manager) Sessions() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	return out
}

// Get returns one session.
func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	return s, ok
}

// SessionForAgent returns the live session bound to an agent, if any.
func (m *Manager) SessionForAgent(agentID string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.sessions {
		if s.AgentID == agentID && s.Kind == KindAgent && !s.terminal() {
			return s, true
		}
	}
	return nil, false
}

func (s *Session) terminal() bool {
	return s.Status == StatusExited || s.Status == StatusError
}

// SpawnOpts describes a session to start.
type SpawnOpts struct {
	Kind      Kind
	AgentID   string
	ProjectID string
	Provider  store.Provider

	// AccountID forces an account, bypassing the cascade. The sign-in flow uses
	// it to launch into the exact directory being signed in to.
	AccountID string

	CWD  string
	Args []string
	Env  map[string]string

	Cols uint16
	Rows uint16

	// ResumeID resumes an existing CLI conversation instead of starting fresh.
	ResumeID string
}

// Spawn starts a CLI process in a PTY.
func (m *Manager) Spawn(o SpawnOpts) (*Session, error) {
	if o.Provider == "" {
		o.Provider = store.ProviderClaude
	}
	if o.Kind == "" {
		o.Kind = KindAgent
	}

	// Resolve the account once, now. Every later display of "which account" reads
	// this frozen answer, so the UI can never disagree with the process.
	var res store.Resolution
	if o.AccountID != "" {
		if a, err := m.st.Account(o.AccountID); err == nil {
			res = store.Resolution{
				AccountID: a.ID, Dir: a.Dir, Name: a.Name,
				Color: a.Color, Source: "explicit",
			}
		}
	}
	if res.AccountID == "" && o.Kind == KindAgent {
		res = m.st.ResolveAccount(o.AgentID, o.ProjectID, o.Provider)
	}

	bin := o.Provider.Bin()
	if o.Provider == store.ProviderClaude {
		if cb := m.st.Settings().ClaudeBin; cb != "" {
			bin = cb
		}
	}

	args := append([]string{}, o.Args...)
	if o.ResumeID != "" && o.Provider == store.ProviderClaude {
		args = append(args, "--resume", o.ResumeID)
	}
	// Let the session read the folder pasted images are written to. They are kept
	// outside the project on purpose, so without this every pasted screenshot
	// would stop on a permission prompt before the agent could look at it.
	//
	// Agents only. A sign-in terminal has nothing to read and the flow is
	// delicate enough without an extra flag in it.
	if o.Provider == store.ProviderClaude && o.Kind == KindAgent {
		if root := AttachRoot(m.st.RootDir()); root != "" {
			if err := os.MkdirAll(root, 0o700); err == nil {
				args = append(args, "--add-dir", root)
			}
		}
	}

	s, err := m.spawnRaw(spawnRawOpts{
		Kind:         o.Kind,
		AgentID:      o.AgentID,
		ProjectID:    o.ProjectID,
		Provider:     o.Provider,
		Bin:          bin,
		Args:         args,
		CWD:          o.CWD,
		Env:          o.Env,
		AccountID:    res.AccountID,
		AccountDir:   res.Dir,
		AccountName:  res.Name,
		AccountColor: res.Color,
		Cols:         o.Cols,
		Rows:         o.Rows,
	})
	if err != nil {
		return nil, err
	}
	s.resumeID = o.ResumeID
	if res.Source != "" && res.Source != "explicit" {
		s.mu.Lock()
		s.SwitchLog = append(s.SwitchLog,
			fmt.Sprintf("%s  account resolved from %s: %s",
				time.Now().Format("15:04:05"), res.Source, displayAccount(res)))
		s.mu.Unlock()
	}
	return s, nil
}

func displayAccount(r store.Resolution) string {
	if r.Name == "" {
		return "system default"
	}
	return r.Name
}

// readLoop pumps PTY output into the ring buffer, every attached websocket, and
// the quota detector.
func (m *Manager) readLoop(s *Session) {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.mu.Lock()
			_, _ = s.ring.Write(chunk)
			s.lastOut = time.Now()
			if s.Status == StatusIdle || s.Status == StatusDone || s.Status == StatusStarting {
				s.Status = StatusWorking
			}
			// Keep a bounded tail for limit detection.
			s.recent = append(s.recent, chunk...)
			if len(s.recent) > 8192 {
				s.recent = s.recent[len(s.recent)-8192:]
			}
			tail := string(s.recent)
			canSwitch := time.Now().After(s.switchAfter)
			for ch := range s.subs {
				select {
				case ch <- chunk:
				default:
				}
			}
			s.mu.Unlock()

			if canSwitch {
				if hit := DetectLimit(tail); hit.Matched {
					s.mu.Lock()
					s.recent = nil
					// Cooldown: a resumed conversation replays its own history,
					// which would otherwise re-trigger the detector instantly.
					s.switchAfter = time.Now().Add(90 * time.Second)
					s.mu.Unlock()
					go m.handleLimit(s, hit)
				}
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.mu.Lock()
				if s.Error == "" && !s.terminal() {
					s.Error = err.Error()
				}
				s.mu.Unlock()
			}
			return
		}
	}
}

// waitLoop reaps the process and records how it ended.
func (m *Manager) waitLoop(s *Session) {
	err := s.cmd.Wait()
	s.mu.Lock()
	s.EndedAt = time.Now()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			s.ExitCode = ee.ExitCode()
			s.Status = StatusExited
		} else {
			s.Status = StatusError
			s.Error = err.Error()
		}
	} else {
		s.Status = StatusExited
	}
	s.mu.Unlock()

	// The process has gone, but bytes it wrote just before exiting can still be
	// sitting in the pseudo-terminal's buffer. Closing straight away discards
	// them, and for a short command that is the entire output — `npm test`
	// printing its result and exiting would show a blank terminal about one run
	// in five. So wait for the reader to stop making progress before closing.
	drainPTY(s)
	s.closePTY()
	m.emit(Event{Type: "session.exited", SessionID: s.ID, AgentID: s.AgentID, Payload: s.Public()})
}

// drainPTY waits until the read loop has stopped producing output, or until a
// deadline. The cap matters: a child that inherited the terminal and is still
// writing must not be able to hold a finished session open indefinitely.
func drainPTY(s *Session) {
	const (
		quiet    = 120 * time.Millisecond
		deadline = 3 * time.Second
		poll     = 40 * time.Millisecond
	)
	stop := time.Now().Add(deadline)
	last := s.ring.Total()
	still := time.Now()
	for time.Now().Before(stop) {
		time.Sleep(poll)
		now := s.ring.Total()
		if now != last {
			last = now
			still = time.Now()
			continue
		}
		if time.Since(still) >= quiet {
			return
		}
	}
}

// Attach subscribes a client to a session's output and returns the scrollback so
// the terminal is never blank on connect.
func (m *Manager) Attach(id string) (scrollback []byte, out <-chan []byte, cancel func(), err error) {
	s, ok := m.Get(id)
	if !ok {
		return nil, nil, nil, store.ErrNotFound
	}
	ch := make(chan []byte, 256)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, nil, nil, errors.New("session is closed")
	}
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return s.ring.Snapshot(), ch, func() {
		s.mu.Lock()
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
		s.mu.Unlock()
	}, nil
}

// Scrollback returns the buffered terminal output for a session. It is what
// makes a reconnect show context, and it is also how you inspect an agent that
// is sitting at a prompt without attaching a terminal to it.
func (m *Manager) Scrollback(id string) ([]byte, error) {
	s, ok := m.Get(id)
	if !ok {
		return nil, store.ErrNotFound
	}
	return s.ring.Snapshot(), nil
}

// Write sends keystrokes to a session.
func (m *Manager) Write(id string, p []byte) error {
	s, ok := m.Get(id)
	if !ok {
		return store.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.terminal() {
		return errors.New("session is not running")
	}
	_, err := s.pty.Write(p)
	return err
}

// Resize applies a new terminal size.
func (m *Manager) Resize(id string, cols, rows uint16) error {
	s, ok := m.Get(id)
	if !ok {
		return store.ErrNotFound
	}
	if cols == 0 || rows == 0 {
		return nil
	}
	return s.pty.Resize(int(cols), int(rows))
}

// Stop terminates a session's process.
func (m *Manager) Stop(id string) error {
	s, ok := m.Get(id)
	if !ok {
		return store.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	return s.closePTY()
}

// closePTY closes the pseudo-terminal at most once. See ptyOnce: a second close
// is not a harmless no-op on Windows, it ends the process.
func (s *Session) closePTY() error {
	var err error
	s.ptyOnce.Do(func() { err = s.pty.Close() })
	return err
}

// Remove drops a finished session from the registry.
func (m *Manager) Remove(id string) error {
	s, ok := m.Get(id)
	if !ok {
		return store.ErrNotFound
	}
	if !s.terminal() {
		_ = m.Stop(id)
	}
	s.mu.Lock()
	s.closed = true
	for ch := range s.subs {
		delete(s.subs, ch)
		close(ch)
	}
	s.mu.Unlock()
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	m.emit(Event{Type: "session.removed", SessionID: id, AgentID: s.AgentID})
	return nil
}

// Public is the JSON-safe view of a session, taken under lock.
type PublicSession struct {
	ID              string              `json:"id"`
	Kind            Kind                `json:"kind"`
	AgentID         string              `json:"agentId,omitempty"`
	ProjectID       string              `json:"projectId,omitempty"`
	Provider        store.Provider      `json:"provider"`
	Label           string              `json:"label,omitempty"`
	CommandID       string              `json:"commandId,omitempty"`
	AccountID       string              `json:"accountId"`
	AccountName     string              `json:"accountName"`
	AccountColor    string              `json:"accountColor"`
	AccountDir      string              `json:"accountDir"`
	CWD             string              `json:"cwd"`
	Command         string              `json:"command"`
	PID             int                 `json:"pid"`
	Status          Status              `json:"status"`
	StartedAt       time.Time           `json:"startedAt"`
	EndedAt         time.Time           `json:"endedAt,omitempty"`
	ExitCode        int                 `json:"exitCode"`
	Error           string              `json:"error,omitempty"`
	ClaudeSessionID string              `json:"claudeSessionId,omitempty"`
	AccountVerified bool                `json:"accountVerified"`
	SwitchCount     int                 `json:"switchCount"`
	SwitchLog       []string            `json:"switchLog,omitempty"`
	Tokens          claudefs.TokenStats `json:"tokens"`
	TotalTokens     int64               `json:"totalTokens"`
	CacheHitRate    float64             `json:"cacheHitRate"`
	IdleSeconds     int                 `json:"idleSeconds"`
}

// Public snapshots the session for the API.
func (s *Session) Public() PublicSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	idle := 0
	if !s.lastOut.IsZero() {
		idle = int(time.Since(s.lastOut).Seconds())
	}
	logCopy := append([]string(nil), s.SwitchLog...)
	return PublicSession{
		ID: s.ID, Kind: s.Kind, AgentID: s.AgentID, ProjectID: s.ProjectID,
		Provider: s.Provider, Label: s.Label, CommandID: s.CommandID,
		AccountID: s.AccountID, AccountName: s.AccountName,
		AccountColor: s.AccountColor, AccountDir: s.AccountDir,
		CWD: s.CWD, Command: s.Command, PID: s.PID, Status: s.Status,
		StartedAt: s.StartedAt, EndedAt: s.EndedAt, ExitCode: s.ExitCode,
		Error: s.Error, ClaudeSessionID: s.ClaudeSessionID,
		AccountVerified: s.AccountVerified,
		SwitchCount:     s.SwitchCount, SwitchLog: logCopy,
		Tokens: s.Tokens, TotalTokens: s.Tokens.Total(),
		CacheHitRate: s.Tokens.CacheHitRate(), IdleSeconds: idle,
	}
}

// pollLoop refreshes token counters and session ids. It reads local files only:
// no network call, no instrumentation in the CLI, so an agent runs at full speed
// whether or not anyone is watching the meter.
func (m *Manager) pollLoop() {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for range tick.C {
		for _, s := range m.Sessions() {
			m.refresh(s)
		}
	}
}

func (m *Manager) refresh(s *Session) {
	s.mu.Lock()
	pid, dir, cwd, provider := s.PID, s.AccountDir, s.CWD, s.Provider
	sid := s.ClaudeSessionID
	s.mu.Unlock()

	// The status heuristic must run for every session, including one that has
	// no transcript yet. An agent sitting at Claude's own trust prompt has
	// produced output and then gone quiet with nothing on disk to read, and
	// "which agent is waiting on me" is precisely the question the dashboard
	// exists to answer — so this can never be gated behind finding a file.
	defer m.refreshStatus(s)

	if provider != store.ProviderClaude || pid == 0 {
		return
	}

	// Discover the CLI's own session id from its metafile. Searching every known
	// account directory keeps this correct even after an auto-switch moved the
	// session somewhere else.
	if sid == "" {
		dirs := []string{dir}
		dirs = append(dirs, m.accs.Dirs(provider)...)

		// The owning directory is knowable as soon as the session starts, from
		// the <pid>.key file, whereas the session id only appears once Claude
		// writes <pid>.json. Confirming the owner first means the account badge
		// is verified against disk long before there is a transcript to read.
		if owner, ok := claudefs.SessionOwnerDir(dirs, pid); ok {
			s.mu.Lock()
			if s.AccountDir != owner {
				s.AccountDir = owner
			}
			s.AccountVerified = true
			s.mu.Unlock()
			dir = owner
		}

		if meta := claudefs.FindSessionMeta(dirs, pid); meta != nil {
			sid = meta.SessionID
			s.mu.Lock()
			s.ClaudeSessionID = sid
			if meta.Dir != "" {
				s.AccountDir = meta.Dir
				dir = meta.Dir
			}
			s.mu.Unlock()
			m.emit(Event{Type: "session.identified", SessionID: s.ID, AgentID: s.AgentID, Payload: s.Public()})
		}
	}

	var stats claudefs.TokenStats
	var ok bool
	if sid != "" {
		if p, found := claudefs.FindTranscript(dir, cwd, sid); found {
			if st, err := claudefs.ParseTranscript(p); err == nil {
				stats, ok = st, true
			}
		}
	}
	if !ok {
		// Before a session id exists, fall back to the newest transcript in this
		// Deliberately no fallback.
		//
		// This used to guess at the newest transcript for the working directory,
		// guarded only by "modified since we started". That guard is useless
		// against a transcript that is being written right now by somebody else:
		// running an agent in a directory where another Claude session was
		// already live attributed that session's entire history to the new
		// agent — a fresh agent reported 332 million tokens and a 99% cache rate
		// it had done nothing to earn.
		//
		// Before the CLI writes its own session id there is genuinely nothing to
		// attribute, and zero is the honest answer. Somebody else's numbers are
		// worse than none.
		return
	}
	if !ok {
		return
	}

	s.mu.Lock()
	changed := stats.Total() != s.Tokens.Total()
	s.Tokens = stats
	if changed {
		s.lastTokenMove = time.Now()
	}
	s.mu.Unlock()

	if changed {
		m.emit(Event{Type: "session.tokens", SessionID: s.ID, AgentID: s.AgentID, Payload: s.Public()})
	}
}

// refreshStatus decides whether an agent is working or waiting on the user.
//
// The signal is silence: the CLI streams while it thinks and stops when it wants
// an answer. So an agent that produced output and has since been quiet, with no
// new tokens landing on disk, is waiting rather than working.
func (m *Manager) refreshStatus(s *Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal() || s.lastOut.IsZero() {
		return
	}
	quiet := time.Since(s.lastOut)
	tokensQuiet := s.lastTokenMove.IsZero() || time.Since(s.lastTokenMove) > 20*time.Second

	switch {
	case s.Status == StatusWorking && quiet > 20*time.Second && tokensQuiet:
		s.Status = StatusWaiting
	case s.Status == StatusWaiting && quiet < 5*time.Second:
		// Output resumed: it is thinking again.
		s.Status = StatusWorking
	}
}

// unbenchLoop returns quota-exhausted accounts to the pool when their window
// reopens, so nothing has to be clicked to recover.
func (m *Manager) unbenchLoop() {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for range tick.C {
		for _, a := range m.st.Accounts() {
			if a.BenchedUntil.IsZero() || a.Benched() {
				continue
			}
			_, _ = m.st.UpdateAccount(a.ID, func(x *store.Account) {
				x.BenchedUntil = time.Time{}
				x.BenchReason = ""
			})
			m.emit(Event{Type: "account.unbenched", Message: a.Name})
		}
	}
}

// LoginArgs are the arguments used to drive an in-app sign-in.
func LoginArgs(p store.Provider) []string {
	switch p {
	case store.ProviderCodex:
		return []string{"login"}
	case store.ProviderGrok:
		return []string{"login"}
	case store.ProviderCursor:
		return []string{"login"}
	default:
		// Claude Code signs in from inside an interactive session via /login,
		// so the PTY just opens the CLI and the UI tells you to type it.
		return nil
	}
}

// EnsureDir creates an account directory if it is missing.
func EnsureDir(dir string) error {
	if dir == "" {
		return nil
	}
	return os.MkdirAll(filepath.Clean(dir), 0o700)
}
