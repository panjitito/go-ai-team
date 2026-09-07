package session

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aymanbagabas/go-pty"

	"github.com/panjitito/go-ai-team/internal/store"
)

// spawnRaw is the shared machinery behind every PTY this app opens: agents,
// dev commands, remote shells and the sign-in terminal.
//
// Factoring it out matters because the parts that are easy to get wrong — the
// ring buffer, the reader goroutine, the reaper, the subscriber fan-out — must
// behave identically for all of them. A dev server that loses its scrollback on
// reconnect is the same bug as an agent that does, and it should not be
// possible to fix one without the other.
type spawnRawOpts struct {
	Kind      Kind
	Label     string
	AgentID   string
	CommandID string
	ProjectID string
	Provider  store.Provider

	Bin  string
	Args []string
	CWD  string
	Env  map[string]string

	// AccountDir binds a provider account, for agent sessions only.
	AccountDir   string
	AccountID    string
	AccountName  string
	AccountColor string

	Cols uint16
	Rows uint16
}

func (m *Manager) spawnRaw(o spawnRawOpts) (*Session, error) {
	if o.CWD == "" {
		o.CWD, _ = os.Getwd()
	}
	if st, err := os.Stat(o.CWD); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("working directory %q is not usable", o.CWD)
	}
	resolved, err := exec.LookPath(o.Bin)
	if err != nil {
		return nil, fmt.Errorf("cannot find %q on PATH: %w", o.Bin, err)
	}

	p, err := pty.New()
	if err != nil {
		return nil, fmt.Errorf("cannot open a pseudo-terminal: %w", err)
	}
	cols, rows := o.Cols, o.Rows
	if cols == 0 {
		cols = 120
	}
	if rows == 0 {
		rows = 32
	}
	// A resize failure is not fatal: the child simply uses its default size
	// until the browser sends one.
	_ = p.Resize(int(cols), int(rows))

	provider := o.Provider
	if provider == "" {
		provider = store.ProviderClaude
	}

	s := &Session{
		ID:           store.NewID("ses"),
		Kind:         o.Kind,
		Label:        o.Label,
		AgentID:      o.AgentID,
		CommandID:    o.CommandID,
		ProjectID:    o.ProjectID,
		Provider:     provider,
		AccountID:    o.AccountID,
		AccountDir:   o.AccountDir,
		AccountName:  o.AccountName,
		AccountColor: o.AccountColor,
		CWD:          o.CWD,
		Command:      strings.Join(append([]string{o.Bin}, o.Args...), " "),
		Status:       StatusStarting,
		StartedAt:    time.Now(),
		ring:         newRing(scrollbackBytes),
		subs:         map[chan []byte]struct{}{},
		pty:          p,
	}

	cmd := p.Command(resolved, o.Args...)
	cmd.Dir = o.CWD

	env := cleanEnv(os.Environ(), provider)
	// Where this session's requests will actually go, worked out before the
	// agent's own variables are appended so the two sources stay distinguishable.
	// Display only, and never the key: see endpoint.go.
	s.Endpoint = endpointOf(o.Env, env)
	if o.AccountDir != "" {
		env = append(env, provider.EnvVar()+"="+o.AccountDir)
	}
	if o.Kind == KindAgent && provider == store.ProviderClaude {
		// See env.go: without this the CLI may skip its transcript, and the
		// token meter, session id and auto-switch handoff all read that file.
		env = append(env, "CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1")
	}
	env = append(env, "GO_AI_TEAM=1")
	if o.AccountName != "" {
		env = append(env, "GO_AI_TEAM_ACCOUNT="+o.AccountName)
	}
	for k, v := range o.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	s.cmd = cmd

	if err := cmd.Start(); err != nil {
		_ = p.Close()
		return nil, fmt.Errorf("cannot start %s: %w", o.Bin, err)
	}
	if cmd.Process != nil {
		s.PID = cmd.Process.Pid
	}
	s.Status = StatusIdle

	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()

	go m.readLoop(s)
	go m.waitLoop(s)

	m.emit(Event{Type: "session.started", SessionID: s.ID, AgentID: s.AgentID, Payload: s.Public()})
	return s, nil
}
