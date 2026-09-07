package session

import (
	"strings"
	"testing"

	"github.com/panjitito/go-ai-team/internal/store"
)

// This is the exact environment Go AI Team sees when it is launched from a
// terminal that is itself a Claude Code session. Every one of these was observed
// leaking in a real run.
var parentSessionEnv = []string{
	"PATH=/usr/bin",
	"HOME=/home/dev",
	"CLAUDECODE=1",
	"CLAUDE_CODE_CHILD_SESSION=1",
	"CLAUDE_CODE_SESSION_ID=abc-123",
	"CLAUDE_CODE_BRIDGE_SESSION_ID=def-456",
	"CLAUDE_CODE_MESSAGING_SOCKET=/tmp/sock",
	"CLAUDE_CODE_MESSAGING_TOKEN=secret",
	"CLAUDE_CODE_ENTRYPOINT=cli",
	"CLAUDE_CODE_EXECPATH=/usr/bin/claude",
	"CLAUDE_PID=4242",
	"CLAUDE_EFFORT=max",
	"CLAUDE_CONFIG_DIR=/home/dev/.claude-other",
}

func envMap(kvs []string) map[string]string {
	m := map[string]string{}
	for _, kv := range kvs {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

func TestCleanEnv_StripsParentSessionMarkers(t *testing.T) {
	got := envMap(cleanEnv(parentSessionEnv, store.ProviderClaude))

	// CLAUDE_CODE_CHILD_SESSION is the destructive one: it turns transcript
	// writing off, and the token meter has nothing to read without a transcript.
	mustGo := []string{
		"CLAUDECODE",
		"CLAUDE_CODE_CHILD_SESSION",
		"CLAUDE_CODE_SESSION_ID",
		"CLAUDE_CODE_BRIDGE_SESSION_ID",
		"CLAUDE_CODE_MESSAGING_SOCKET",
		"CLAUDE_CODE_MESSAGING_TOKEN",
		"CLAUDE_CODE_ENTRYPOINT",
		"CLAUDE_CODE_EXECPATH",
		"CLAUDE_PID",
		"CLAUDE_EFFORT",
		"CLAUDE_CONFIG_DIR",
	}
	for _, k := range mustGo {
		if v, ok := got[k]; ok {
			t.Errorf("%s leaked into the child environment (=%q)", k, v)
		}
	}

	// Ordinary environment must survive untouched.
	for _, k := range []string{"PATH", "HOME"} {
		if _, ok := got[k]; !ok {
			t.Errorf("%s was stripped but should have been kept", k)
		}
	}
}

// No inherited account binding may survive, whichever provider is being
// spawned. A stale one is the exact confusion this app exists to remove, and it
// could bind a tool the agent shells out to onto an account nobody chose.
func TestCleanEnv_StripsEveryProvidersAccountVar(t *testing.T) {
	env := []string{
		"CLAUDE_CONFIG_DIR=/a",
		"CODEX_HOME=/b",
		"GROK_HOME=/c",
		"CURSOR_CONFIG_DIR=/d",
		"KEEP_ME=1",
	}
	for _, p := range []store.Provider{
		store.ProviderClaude, store.ProviderCodex,
		store.ProviderGrok, store.ProviderCursor,
	} {
		got := envMap(cleanEnv(env, p))
		for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "GROK_HOME", "CURSOR_CONFIG_DIR"} {
			if v, ok := got[k]; ok {
				t.Errorf("spawning %s: %s leaked (=%q)", p, k, v)
			}
		}
		if _, ok := got["KEEP_ME"]; !ok {
			t.Errorf("spawning %s: unrelated variable was stripped", p)
		}
	}
}

// Legitimate user configuration must not be collateral damage. Stripping the
// whole CLAUDE_CODE_ prefix would have taken these with it.
func TestCleanEnv_KeepsUserConfiguration(t *testing.T) {
	env := []string{
		"CLAUDE_CODE_MAX_OUTPUT_TOKENS=8192",
		"CLAUDE_CODE_USE_BEDROCK=1",
		"ANTHROPIC_BASE_URL=https://gateway.internal",
		"ANTHROPIC_API_KEY=sk-test",
		"DISABLE_TELEMETRY=1",
	}
	got := envMap(cleanEnv(env, store.ProviderClaude))
	for _, k := range []string{
		"CLAUDE_CODE_MAX_OUTPUT_TOKENS", "CLAUDE_CODE_USE_BEDROCK",
		"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "DISABLE_TELEMETRY",
	} {
		if _, ok := got[k]; !ok {
			t.Errorf("%s was stripped but is the user's own configuration", k)
		}
	}
}

func TestIsSessionScoped_FutureVariants(t *testing.T) {
	scoped := []string{
		"CLAUDE_CODE_SOMETHING_SESSION_ID",
		"CLAUDE_CODE_MESSAGING_ENDPOINT",
		"claude_code_child_session",
	}
	for _, k := range scoped {
		if !isSessionScoped(k) {
			t.Errorf("%s should be treated as session-scoped", k)
		}
	}
	notScoped := []string{
		"PATH", "ANTHROPIC_API_KEY",
		"CLAUDE_CODE_MAX_OUTPUT_TOKENS",
		// Not a Claude variable at all, despite the fragment.
		"MY_APP_SESSION_ID",
	}
	for _, k := range notScoped {
		if isSessionScoped(k) {
			t.Errorf("%s should NOT be treated as session-scoped", k)
		}
	}
}
