package session

import (
	"net/url"
	"strings"
)

// Which API an agent is actually talking to.
//
// An account in this app is a signed-in config directory, and binding an agent
// to one is how you say "this work goes on that subscription". ANTHROPIC_BASE_URL
// overrides that completely: the CLI stops using the OAuth credentials in the
// config directory and bills whatever key sits beside the URL. The account badge
// then names an account that is not paying for anything.
//
// That is a fine thing to want — it is the whole of bring-your-own-key — and a
// bad thing to be unable to see. The variable can arrive two ways: set on the
// agent, on purpose, which is the feature; or inherited from the shell Go AI
// Team was launched from, which is usually somebody who exported it for an
// afternoon months ago and is the case worth flagging.
//
// So the endpoint is recorded on the session and shown next to the account.
// Inheritance is left working, because a company that routes everything through
// one gateway has exported it deliberately and stripping it would break every
// agent they run. Visible beats silent either way.
//
// No key is read here, ever. Only the host of the URL is kept, and the value of
// ANTHROPIC_AUTH_TOKEN or ANTHROPIC_API_KEY is never looked at, logged or
// returned.

// Endpoint describes where a session's requests go, for display only.
type Endpoint struct {
	// Host is the API's host, without scheme, path or credentials. Empty means
	// Anthropic, reached with the account's own sign-in.
	Host string `json:"host,omitempty"`

	// Source is "agent" when the agent set it, "inherited" when it came from
	// the environment Go AI Team was started in. Empty when there is none.
	Source string `json:"source,omitempty"`
}

// BYOK reports whether this session is billed somewhere other than the account
// it is bound to.
func (e Endpoint) BYOK() bool { return e.Host != "" }

// endpointOf works out where a spawn will send its requests.
//
// agentEnv is the agent's own environment, which wins because it is appended
// last to the child's environment and os/exec keeps the last of a repeated key.
// inherited is what survived cleanEnv.
func endpointOf(agentEnv map[string]string, inherited []string) Endpoint {
	if v, ok := lookupFold(agentEnv, "ANTHROPIC_BASE_URL"); ok {
		if h := hostOf(v); h != "" {
			return Endpoint{Host: h, Source: "agent"}
		}
		// An agent that sets the variable to nothing is deliberately turning
		// off an inherited one, and goes back to its account's own endpoint.
		return Endpoint{}
	}
	for _, kv := range inherited {
		k, v, ok := strings.Cut(kv, "=")
		if ok && strings.EqualFold(k, "ANTHROPIC_BASE_URL") {
			if h := hostOf(v); h != "" {
				return Endpoint{Host: h, Source: "inherited"}
			}
		}
	}
	return Endpoint{}
}

// lookupFold reads a map with case-insensitive keys, because Windows
// environment names are not case-sensitive and people type them either way.
func lookupFold(m map[string]string, key string) (string, bool) {
	if v, ok := m[key]; ok {
		return v, true
	}
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return "", false
}

// hostOf reduces a base URL to the host somebody would recognise.
//
// Deliberately lossy. The path is dropped because /anthropic and /api/anthropic
// say nothing a person needs, and any userinfo in the URL is dropped because it
// would be a credential and this string is shown on screen and sent to the API.
func hostOf(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		// Not a URL at all. Keep the first path-free token so a typo is still
		// visible as a typo rather than vanishing.
		if i := strings.IndexAny(raw, "/?#"); i >= 0 {
			raw = raw[:i]
		}
		if strings.ContainsAny(raw, "@ ") {
			return ""
		}
		return raw
	}
	return u.Host
}
