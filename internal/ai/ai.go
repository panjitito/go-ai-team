// Package ai runs short, one-shot helper prompts through the agent CLI the user
// already pays for.
//
// This is the deliberate difference from the tools we are competing with. They
// route features like commit-message generation, agent suggestions and feedback
// clustering through their own backend and then meter them: a handful of commit
// messages a month on the free tier, a few hundred on the paid one, and an
// invitation to attach your own OpenAI key once the allowance runs out.
//
// There is no reason for that. The user is already signed in to a CLI with a
// subscription. Every one of those features is a small prompt with a short
// answer, so it runs headless against that CLI, on the account the cascade
// picks, at no extra cost and with no key to add. Nothing leaves the machine
// except the request the CLI itself would have made.
//
// The cost of that choice is latency — a CLI cold start is seconds, not
// milliseconds — so every call here is explicitly a background action with a
// timeout, never something the UI blocks on.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/panjitito/go-ai-team/internal/store"
)

// ErrNoAccount is returned when nothing usable is signed in.
var ErrNoAccount = errors.New("no signed-in account available for helper calls")

// Runner executes helper prompts.
type Runner struct {
	st *store.Store
	// resolveDir maps an account id to its config directory, and is supplied by
	// the caller so this package does not depend on the accounts manager.
	resolveDir func(accountID string) (dir, name string, usable bool)
	// firstUsable returns any usable account, for when none is pinned.
	firstUsable func() (id, dir, name string, ok bool)
	// cleanEnv filters the environment exactly as an agent spawn does.
	cleanEnv func([]string, store.Provider) []string
}

// New builds a Runner.
func New(
	st *store.Store,
	resolveDir func(string) (string, string, bool),
	firstUsable func() (string, string, string, bool),
	cleanEnv func([]string, store.Provider) []string,
) *Runner {
	return &Runner{st: st, resolveDir: resolveDir, firstUsable: firstUsable, cleanEnv: cleanEnv}
}

// Available reports whether a helper call could run right now.
func (r *Runner) Available() bool {
	_, _, _, ok := r.pick()
	return ok
}

// pick chooses the account for helper calls: the one configured in settings if
// it is usable, otherwise any usable account.
func (r *Runner) pick() (id, dir, name string, ok bool) {
	if want := r.st.Settings().AIAccountID; want != "" {
		if d, n, usable := r.resolveDir(want); usable {
			return want, d, n, true
		}
	}
	return r.firstUsable()
}

// Opts tunes one call.
type Opts struct {
	// Model defaults to the settings value, then to haiku: none of these
	// helpers needs a flagship, and a cheap model keeps the user's quota for
	// the work that matters.
	Model string
	// Timeout bounds the call. A helper that has not answered in time is
	// abandoned rather than left to hold the UI.
	Timeout time.Duration
	// Dir is the working directory. Some prompts want repository context.
	Dir string
	// System is prepended as an extra system prompt.
	System string
}

const defaultModel = "haiku"

// Run sends one prompt and returns the plain-text answer.
func (r *Runner) Run(ctx context.Context, prompt string, o Opts) (string, error) {
	_, dir, _, ok := r.pick()
	if !ok {
		return "", ErrNoAccount
	}

	model := o.Model
	if model == "" {
		model = r.st.Settings().AIModel
	}
	if model == "" {
		model = defaultModel
	}
	timeout := o.Timeout
	if timeout == 0 {
		timeout = 90 * time.Second
	}

	bin := r.st.Settings().ClaudeBin
	if bin == "" {
		bin = store.ProviderClaude.Bin()
	}
	resolved, err := exec.LookPath(bin)
	if err != nil {
		return "", fmt.Errorf("cannot find %q on PATH: %w", bin, err)
	}

	// -p is headless print mode: one prompt in, one answer out, no TUI.
	args := []string{"-p", prompt, "--model", model}
	if o.System != "" {
		args = append(args, "--append-system-prompt", o.System)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, resolved, args...)
	if o.Dir != "" {
		if st, err := os.Stat(o.Dir); err == nil && st.IsDir() {
			cmd.Dir = o.Dir
		}
	}
	env := r.cleanEnv(os.Environ(), store.ProviderClaude)
	if dir != "" {
		env = append(env, store.ProviderClaude.EnvVar()+"="+dir)
	}
	// A helper call must never be treated as a resumable session or write a
	// transcript that pollutes the token meter of a real agent.
	env = append(env, "GO_AI_TEAM=1", "GO_AI_TEAM_HELPER=1")
	cmd.Env = env

	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("helper call timed out after %s", timeout)
		}
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		if len(msg) > 400 {
			msg = msg[:400] + "…"
		}
		return "", fmt.Errorf("helper call failed: %s", msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// RunJSON asks for a JSON answer and decodes it into v.
//
// Models like to wrap JSON in prose or a fenced block however firmly you ask, so
// the response is salvaged rather than trusted: the first balanced JSON value is
// extracted before decoding. That turns the single most common failure of this
// pattern into a non-event.
func (r *Runner) RunJSON(ctx context.Context, prompt string, v any, o Opts) error {
	o.System = strings.TrimSpace(o.System + "\nReply with JSON only. No prose, no code fence, no commentary.")
	raw, err := r.Run(ctx, prompt, o)
	if err != nil {
		return err
	}
	body, ok := extractJSON(raw)
	if !ok {
		return fmt.Errorf("no JSON found in the reply: %.200s", raw)
	}
	if err := json.Unmarshal([]byte(body), v); err != nil {
		return fmt.Errorf("reply was not valid JSON: %w", err)
	}
	return nil
}

// extractJSON finds the first balanced JSON object or array in s, ignoring
// braces that appear inside strings.
func extractJSON(s string) (string, bool) {
	start := -1
	var open, close byte
	for i := 0; i < len(s); i++ {
		if s[i] == '{' {
			start, open, close = i, '{', '}'
			break
		}
		if s[i] == '[' {
			start, open, close = i, '[', ']'
			break
		}
	}
	if start < 0 {
		return "", false
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case c == '\\' && inStr:
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
			// nothing
		case c == open:
			depth++
		case c == close:
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}
