package session

import (
	"strings"

	"github.com/panjitito/go-ai-team/internal/store"
)

// Environment hygiene for spawned agents.
//
// Go AI Team is often launched from inside a terminal that is itself a Claude
// Code session. That parent session exports a set of variables describing
// itself — its session id, its IPC socket and token, a marker saying "you are a
// child of me". Inheriting any of them is wrong, and one of them is quietly
// destructive: CLAUDE_CODE_CHILD_SESSION makes the CLI skip writing a
// transcript, which silently kills the token meter, because there is then no
// JSONL file to read.
//
// Every agent we start is a top-level session of its own, so the parent's
// identity is stripped and transcript persistence is asked for explicitly.

// sessionScopedEnv are variables that describe one particular running Claude
// Code session. None of them is ever valid to hand to a different session.
var sessionScopedEnv = []string{
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
}

// sessionScopedFragments catch variables of the same family that we have not
// seen yet: anything naming a session or an IPC channel belongs to the parent.
// Deliberately narrower than stripping the whole CLAUDE_CODE_ prefix, which
// would also throw away legitimate user settings such as an output-token cap.
var sessionScopedFragments = []string{
	"_SESSION_ID",
	"_MESSAGING_",
	"_CHILD_SESSION",
}

// isSessionScoped reports whether an environment key belongs to a parent agent
// session rather than to the user's own configuration.
func isSessionScoped(key string) bool {
	up := strings.ToUpper(key)
	for _, k := range sessionScopedEnv {
		if up == k {
			return true
		}
	}
	if strings.HasPrefix(up, "CLAUDE") {
		for _, frag := range sessionScopedFragments {
			if strings.Contains(up, frag) {
				return true
			}
		}
	}
	return false
}

// accountVars is the set of every variable any provider uses to bind an
// account. Taken from the store rather than listed again here, so adding a
// provider cannot leave a variable behind that the filter does not know about.
// Gemini needs two of them, which is why this is a list and not one name per
// provider: see store.Provider.AccountEnv.
func accountVars() map[string]bool {
	vars := store.AccountVars()
	m := make(map[string]bool, len(vars))
	for _, v := range vars {
		m[strings.ToUpper(v)] = true
	}
	return m
}

// cleanEnv filters an environment slice, dropping every provider's account
// variable and every parent-session marker. Returning the kept entries lets the
// caller append its own bindings afterwards.
//
// All account variables go, not just the target provider's. Go AI Team owns the
// question of which account a process runs on, and a stale binding inherited
// from the launching shell is exactly the "which account am I on?" confusion
// this whole app exists to remove — including for a tool the agent shells out to
// itself. A user who genuinely wants one set can do it per agent, explicitly.
func cleanEnv(env []string, p store.Provider) []string {
	accounts := accountVars()
	out := make([]string, 0, len(env)+8)
	for _, kv := range env {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			out = append(out, kv)
			continue
		}
		if accounts[strings.ToUpper(key)] || isSessionScoped(key) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// CleanEnv is the exported form, used by the AI helper runner so a one-shot
// helper call is filtered exactly like a real agent spawn.
func CleanEnv(env []string, p store.Provider) []string { return cleanEnv(env, p) }
