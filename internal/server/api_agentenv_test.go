package server

import (
	"strings"
	"testing"

	"github.com/uniair/go-ai-team/internal/secrets"
	"github.com/uniair/go-ai-team/internal/store"
)

// An agent's `{{secret:NAME}}` references have to be resolved before it starts.
//
// Dev terminals have done this since the vault was built and agents never did,
// so an agent configured with ANTHROPIC_AUTH_TOKEN={{secret:NAME}} started with
// that twenty-six character string as its key and failed to authenticate
// against a provider that was set up correctly. The vault exists to keep a key
// off the screen and out of the API, and pointing an agent at another endpoint
// is the case people most want it for.
func TestAgentEnvResolvesSecrets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	v, err := secrets.Open(home)
	if err != nil {
		t.Skipf("no encryption provider on this machine: %v", err)
	}
	if err := v.Set("DEEPSEEK_KEY", "sk-real-value"); err != nil {
		t.Fatal(err)
	}
	s := &Server{vault: v}

	a := &store.Agent{Name: "Checkout", Env: map[string]string{
		"ANTHROPIC_BASE_URL":   "https://api.deepseek.com/anthropic",
		"ANTHROPIC_AUTH_TOKEN": "{{secret:DEEPSEEK_KEY}}",
	}}

	got, err := s.agentEnv(a)
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if got["ANTHROPIC_AUTH_TOKEN"] != "sk-real-value" {
		t.Errorf("token = %q, want the value from the vault", got["ANTHROPIC_AUTH_TOKEN"])
	}
	if got["ANTHROPIC_BASE_URL"] != "https://api.deepseek.com/anthropic" {
		t.Errorf("a plain value was altered: %q", got["ANTHROPIC_BASE_URL"])
	}
	// The agent on disk still holds the reference. A saved agent stores the name
	// of a secret, never a copy of it.
	if a.Env["ANTHROPIC_AUTH_TOKEN"] != "{{secret:DEEPSEEK_KEY}}" {
		t.Errorf("the stored agent was rewritten to %q", a.Env["ANTHROPIC_AUTH_TOKEN"])
	}
}

// A name the vault does not know stops the agent rather than starting it with a
// broken credential, which fails later and further from the cause.
func TestAgentEnvRefusesAnUnknownSecret(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	v, err := secrets.Open(home)
	if err != nil {
		t.Skipf("no encryption provider on this machine: %v", err)
	}
	s := &Server{vault: v}

	a := &store.Agent{Name: "Checkout", Env: map[string]string{
		"ANTHROPIC_AUTH_TOKEN": "{{secret:NOT_THERE}}",
	}}
	got, err := s.agentEnv(a)
	if err == nil {
		t.Fatalf("started with %v instead of refusing", got)
	}
	if !strings.Contains(err.Error(), "NOT_THERE") {
		t.Errorf("the error does not name the missing secret: %v", err)
	}
}

// An agent with no environment of its own is the common case and must not go
// anywhere near the vault.
func TestAgentEnvWithNothingToResolve(t *testing.T) {
	s := &Server{} // no vault at all
	got, err := s.agentEnv(&store.Agent{Name: "Plain"})
	if err != nil {
		t.Fatalf("an agent with no env failed: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("invented an environment: %v", got)
	}
}
