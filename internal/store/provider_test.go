package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// Binding an account is the whole app in one function: everything else assumes
// that a process started with this environment reads that account's credentials
// and nobody else's.
func TestAccountEnvBindsOneAccount(t *testing.T) {
	dir := filepath.Join("C:", "profiles", "work")

	simple := map[Provider]string{
		ProviderClaude: "CLAUDE_CONFIG_DIR",
		ProviderCodex:  "CODEX_HOME",
		ProviderGrok:   "GROK_HOME",
		ProviderCursor: "CURSOR_CONFIG_DIR",
	}
	for p, key := range simple {
		env := p.AccountEnv(dir)
		if len(env) != 1 {
			t.Errorf("%s set %d variables, want 1: %v", p, len(env), env)
		}
		if env[key] != dir {
			t.Errorf("%s set %s=%q, want %q", p, key, env[key], dir)
		}
	}

	// Nothing to bind to is not a reason to invent a binding.
	for _, p := range Providers {
		if env := p.AccountEnv(""); len(env) != 0 {
			t.Errorf("%s invented an environment for no directory: %v", p, env)
		}
	}
}

// Gemini is the one that is not a single variable, and getting it wrong is
// silent: the second account overwrites the first in the OS keychain and both
// still appear to work.
func TestAccountEnvGemini(t *testing.T) {
	// The CLI's own layout: the account directory is <home>/.gemini, so the
	// home is its parent. The CLI appends ".gemini" itself.
	cfg := filepath.Join("C:", "Users", "dev", ".gemini")
	env := ProviderGemini.AccountEnv(cfg)
	if got, want := env["GEMINI_CLI_HOME"], filepath.Join("C:", "Users", "dev"); got != want {
		t.Errorf("GEMINI_CLI_HOME = %q, want the parent %q", got, want)
	}
	if env["GEMINI_FORCE_FILE_STORAGE"] != "true" {
		t.Errorf("the token would go to the machine-wide keychain slot: %v", env)
	}
	// Never the variable the documentation talks about, which the runtime does
	// not read.
	if _, ok := env["GEMINI_CONFIG_DIR"]; ok {
		t.Errorf("set GEMINI_CONFIG_DIR, which gemini-cli ignores: %v", env)
	}

	// A managed profile is a directory somebody chose, not the CLI's layout, so
	// it becomes the home and the config lands inside it. Either way, two
	// accounts never share a directory.
	a := ProviderGemini.AccountEnv(filepath.Join("C:", "profiles", "acc_1"))
	b := ProviderGemini.AccountEnv(filepath.Join("C:", "profiles", "acc_2"))
	if a["GEMINI_CLI_HOME"] == b["GEMINI_CLI_HOME"] {
		t.Errorf("two accounts resolved to one home: %q", a["GEMINI_CLI_HOME"])
	}
	if a["GEMINI_CLI_HOME"] != filepath.Join("C:", "profiles", "acc_1") {
		t.Errorf("a chosen directory should be the home itself, got %q", a["GEMINI_CLI_HOME"])
	}

	// A trailing separator is the same directory.
	withSep := ProviderGemini.AccountEnv(filepath.Join("C:", "Users", "dev", ".gemini") + string(filepath.Separator))
	if withSep["GEMINI_CLI_HOME"] != env["GEMINI_CLI_HOME"] {
		t.Errorf("a trailing separator changed the home: %q vs %q",
			withSep["GEMINI_CLI_HOME"], env["GEMINI_CLI_HOME"])
	}
}

// AccountVars is what the spawn filter strips from an inherited environment.
// A variable AccountEnv can set and this does not list is one that survives
// from the launching shell and quietly binds an agent to the wrong account.
func TestAccountVarsCoversEveryBinding(t *testing.T) {
	listed := map[string]bool{}
	for _, v := range AccountVars() {
		listed[strings.ToUpper(v)] = true
	}
	for _, p := range Providers {
		for k := range p.AccountEnv(filepath.Join("C:", "x")) {
			if !listed[strings.ToUpper(k)] {
				t.Errorf("%s sets %s and AccountVars does not list it, so an inherited one would survive", p, k)
			}
		}
	}
}

// Every provider needs a binding variable and an executable, or an account for
// it cannot be launched at all.
func TestEveryProviderIsComplete(t *testing.T) {
	seenVar := map[string]Provider{}
	for _, p := range Providers {
		if p.EnvVar() == "" {
			t.Errorf("%s has no account variable", p)
		}
		if p.Bin() == "" {
			t.Errorf("%s has no executable", p)
		}
		if prev, ok := seenVar[p.EnvVar()]; ok {
			t.Errorf("%s and %s share %s, so binding one rebinds the other", p, prev, p.EnvVar())
		}
		seenVar[p.EnvVar()] = p
	}
	// The default arm of both switches belongs to Claude, so an unknown value
	// must not silently become a Claude account.
	if len(Providers) != 5 {
		t.Errorf("Providers has %d entries; update this test deliberately", len(Providers))
	}
}
