package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/uniair/go-ai-team/internal/store"
)

// Dev command terminals.
//
// A project's commands — the dev server, the build, the test watcher — are the
// other half of what fills a developer's terminal tabs. They run through the
// same PTY manager as agents so they get the same live output, the same
// scrollback on reconnect, and the same reachability from a phone. The only
// differences are that they are not bound to an account and their status is
// about the process rather than about a conversation.

// KindCommand marks a session that is a saved dev command rather than an agent.
const KindCommand Kind = "command"

// SpawnCommand starts a saved dev command in a PTY.
func (m *Manager) SpawnCommand(c *store.DevCommand, projectPath string, env map[string]string, cols, rows uint16) (*Session, error) {
	dir := c.Dir
	if dir == "" {
		dir = projectPath
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("working directory %q is not usable", dir)
	}
	if strings.TrimSpace(c.Command) == "" {
		return nil, fmt.Errorf("this command is empty")
	}

	shell, args := shellFor(c.Command)
	merged := map[string]string{}
	for k, v := range c.Env {
		merged[k] = v
	}
	for k, v := range env {
		merged[k] = v
	}

	return m.spawnRaw(spawnRawOpts{
		Kind:      KindCommand,
		Label:     c.Name,
		CommandID: c.ID,
		ProjectID: c.ProjectID,
		Bin:       shell,
		Args:      args,
		CWD:       dir,
		Env:       merged,
		Cols:      cols,
		Rows:      rows,
	})
}

// shellFor wraps a command line in the platform's shell so that pipes,
// redirects and && behave the way the user typed them.
func shellFor(cmdline string) (string, []string) {
	if runtime.GOOS == "windows" {
		// cmd is used rather than PowerShell because a saved command is far
		// more likely to be POSIX-ish npm/go/make syntax, which cmd passes
		// through unchanged, while PowerShell would reinterpret the operators.
		return "cmd", []string{"/c", cmdline}
	}
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	return sh, []string{"-lc", cmdline}
}

// RunCommandOnce runs a saved command to completion and returns its output.
// This is what the MCP command_run tool calls, so an agent can build or test
// without needing to know the incantation.
func (m *Manager) RunCommandOnce(ctx context.Context, c *store.DevCommand, projectPath string, env map[string]string) (string, error) {
	dir := c.Dir
	if dir == "" {
		dir = projectPath
	}
	shell, args := shellFor(c.Command)

	// A build can legitimately take minutes, but an agent waiting forever on a
	// dev server that never exits is a hang, so this is bounded.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, shell, args...)
	cmd.Dir = dir
	e := cleanEnv(os.Environ(), store.ProviderClaude)
	for k, v := range c.Env {
		e = append(e, k+"="+v)
	}
	for k, v := range env {
		e = append(e, k+"="+v)
	}
	cmd.Env = e

	out, err := cmd.CombinedOutput()
	text := string(out)
	// Cap the output: an agent does not need a hundred thousand lines of build
	// log in its context, and the tail is where the failure is.
	const cap = 20000
	if len(text) > cap {
		text = "… output truncated, showing the last " + fmt.Sprint(cap) + " characters …\n" + text[len(text)-cap:]
	}
	if err != nil {
		if ctx.Err() != nil {
			return text, fmt.Errorf("command timed out after 10 minutes")
		}
		return text, fmt.Errorf("command exited with an error: %w", err)
	}
	return text, nil
}

// LaunchTask starts an agent on a project with a first prompt.
//
// It is the automation entry point: schedules, webhooks and a task dragged into
// In Progress all arrive here. If the agent already has a live session the
// prompt is delivered into it rather than starting a second one, because two
// agents editing the same files is how a scheduled task quietly corrupts a
// working tree.
func (m *Manager) LaunchTask(projectID, agentID, prompt, origin string) (string, error) {
	p, err := m.st.Project(projectID)
	if err != nil {
		return "", fmt.Errorf("project %s no longer exists", projectID)
	}

	var agent *store.Agent
	if agentID != "" {
		agent, err = m.st.Agent(agentID)
		if err != nil {
			return "", fmt.Errorf("agent %s no longer exists", agentID)
		}
	} else {
		// No agent pinned: use any agent on the project, or refuse rather than
		// inventing one, because an unattended run should not silently create
		// state the user did not ask for.
		for _, a := range m.st.Agents() {
			if a.ProjectID == projectID {
				agent = a
				break
			}
		}
		if agent == nil {
			return "", fmt.Errorf("project %q has no agents to run this on", p.Name)
		}
	}

	banner := fmt.Sprintf("\r\n\x1b[38;5;208m[Go AI Team]\x1b[0m started by %s\r\n", origin)

	if sess, ok := m.SessionForAgent(agent.ID); ok {
		m.note(sess, "a new task arrived from "+origin+"; queued into this session")
		go m.deliver(sess.ID, prompt)
		return sess.ID, nil
	}

	sess, err := m.Spawn(SpawnOpts{
		Kind:      KindAgent,
		AgentID:   agent.ID,
		ProjectID: projectID,
		Provider:  agent.Provider,
		CWD:       p.Path,
		Args:      m.AgentArgs(agent),
		Env:       agent.Env,
		Cols:      120,
		Rows:      36,
	})
	if err != nil {
		return "", err
	}

	sess.mu.Lock()
	_, _ = sess.ring.Write([]byte(banner))
	sess.mu.Unlock()

	go m.deliver(sess.ID, prompt)
	return sess.ID, nil
}

// deliver sends an automation prompt once the CLI is listening. The readiness
// wait lives in SendPrompt so the composer and automation share one answer to
// "is it safe to type yet".
func (m *Manager) deliver(sessionID, prompt string) {
	if strings.TrimSpace(prompt) == "" {
		return
	}
	_ = m.SendPrompt(sessionID, prompt)
}

// Notify implements the automation Notifier so schedules and webhooks report
// through the same event stream the UI already listens to.
func (m *Manager) Notify(kind, message string, payload any) {
	m.emit(Event{Type: kind, Message: message, Payload: payload})
}
