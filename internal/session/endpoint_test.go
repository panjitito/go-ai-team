package session

import (
	"strings"
	"testing"
)

// Where a session's requests go, and where that came from.
func TestEndpointOf(t *testing.T) {
	cases := []struct {
		name      string
		agent     map[string]string
		inherited []string
		host      string
		source    string
	}{
		{
			name:      "nothing set is the account's own endpoint",
			inherited: []string{"PATH=/usr/bin", "HOME=/home/dev"},
		},
		{
			name:  "the agent's own choice",
			agent: map[string]string{"ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic"},
			host:  "api.deepseek.com", source: "agent",
		},
		{
			name:      "inherited from the shell, which is the one worth flagging",
			inherited: []string{"ANTHROPIC_BASE_URL=https://gateway.internal:8443/v1"},
			host:      "gateway.internal:8443", source: "inherited",
		},
		{
			name:      "the agent wins over the shell, as it does in the child",
			agent:     map[string]string{"ANTHROPIC_BASE_URL": "https://api.z.ai/api/anthropic"},
			inherited: []string{"ANTHROPIC_BASE_URL=https://gateway.internal"},
			host:      "api.z.ai", source: "agent",
		},
		{
			// Setting it to nothing is how an agent says "ignore the shell and
			// use my account", and it has to actually mean that.
			name:      "an agent can turn an inherited one off",
			agent:     map[string]string{"ANTHROPIC_BASE_URL": ""},
			inherited: []string{"ANTHROPIC_BASE_URL=https://gateway.internal"},
		},
		{
			name:  "a local gateway",
			agent: map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:4000"},
			host:  "127.0.0.1:4000", source: "agent",
		},
		{
			// Windows environment names are not case-sensitive and people type
			// them either way.
			name:  "the variable name is matched case-insensitively",
			agent: map[string]string{"Anthropic_Base_Url": "https://api.moonshot.ai/anthropic"},
			host:  "api.moonshot.ai", source: "agent",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := endpointOf(c.agent, c.inherited)
			if got.Host != c.host {
				t.Errorf("host = %q, want %q", got.Host, c.host)
			}
			if got.Source != c.source {
				t.Errorf("source = %q, want %q", got.Source, c.source)
			}
			if got.BYOK() != (c.host != "") {
				t.Errorf("BYOK() = %v for host %q", got.BYOK(), got.Host)
			}
		})
	}
}

// The endpoint is displayed and serialised, so anything that could be a
// credential has to be gone before it gets there. A base URL with userinfo in
// it is a password in a field that ends up on screen.
func TestEndpointNeverCarriesACredential(t *testing.T) {
	nasty := []string{
		"https://user:hunter2@api.example.com/anthropic",
		"https://sk-abcdef123456@gateway.internal",
	}
	for _, raw := range nasty {
		got := endpointOf(map[string]string{"ANTHROPIC_BASE_URL": raw}, nil)
		for _, secret := range []string{"hunter2", "sk-abcdef123456", "user:"} {
			if strings.Contains(got.Host, secret) {
				t.Errorf("endpointOf(%q).Host = %q, which carries %q", raw, got.Host, secret)
			}
		}
	}

	// And the key itself is never read, whatever it is called.
	env := map[string]string{
		"ANTHROPIC_BASE_URL":   "https://api.deepseek.com/anthropic",
		"ANTHROPIC_AUTH_TOKEN": "sk-do-not-leak-me",
		"ANTHROPIC_API_KEY":    "sk-nor-this-one",
	}
	got := endpointOf(env, nil)
	if strings.Contains(got.Host, "sk-") || strings.Contains(got.Source, "sk-") {
		t.Errorf("a key reached the endpoint: %+v", got)
	}
}

// hostOf is deliberately lossy, and has to stay readable when it is fed
// something that is not a URL at all.
func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"https://api.deepseek.com/anthropic": "api.deepseek.com",
		"https://openrouter.ai/api":          "openrouter.ai",
		"http://127.0.0.1:4000":              "127.0.0.1:4000",
		"":                                   "",
		"   ":                                "",
		// Somebody who forgot the scheme should see their typo, not silence.
		"api.deepseek.com/anthropic": "api.deepseek.com",
		"localhost:4000":             "localhost:4000",
	}
	for in, want := range cases {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
}
