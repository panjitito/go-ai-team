package catalog

// Bring your own key.
//
// Claude Code talks to whatever ANTHROPIC_BASE_URL points at, and several
// providers serve Anthropic's Messages API so that it can point at them. All
// the machinery for this already existed here: an agent carries its own
// environment, the vault stores a value nothing can read back, and the spawn
// resolves {{secret:NAME}} on the way out. What was missing was knowing the
// four strings, which are not guessable and are documented on four different
// websites.
//
// So this is a list, not a feature. Choosing an endpoint fills the form in and
// every field stays editable, because these details change and a list compiled
// into a binary goes stale. Docs is the link to check when it does.
//
// Nothing here is a credential. The key is named, never stored: an endpoint
// says which variable carries it and the value comes from the vault at spawn
// time.

// Endpoint is one Anthropic-compatible API, and what it takes to reach it.
type Endpoint struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Descr string `json:"descr"`

	// BaseURL goes in ANTHROPIC_BASE_URL. Empty for the entry that means "keep
	// using the account you signed in with".
	BaseURL string `json:"baseUrl,omitempty"`

	// KeyVar is the variable the key belongs in. ANTHROPIC_AUTH_TOKEN is sent
	// as a bearer token and ANTHROPIC_API_KEY as an x-api-key header, and
	// providers differ about which one they want.
	KeyVar string `json:"keyVar,omitempty"`

	// Env is everything else the provider's own instructions set: model
	// mappings, and in one case a variable that has to be explicitly empty.
	Env map[string]string `json:"env,omitempty"`

	// Docs is where these values came from and where to check them.
	Docs string `json:"docs,omitempty"`

	// Note is what somebody needs to know before picking this one.
	Note string `json:"note,omitempty"`

	// Custom marks the entry that fills in nothing and expects the form to be
	// typed into.
	Custom bool `json:"custom,omitempty"`
}

// Endpoints is the list offered in the agent editor.
//
// The model names are the ones each provider's documentation gives today and
// are the first thing here that will be wrong. They are suggestions in a form,
// not behaviour: nothing in this app reads them back.
var Endpoints = []Endpoint{
	{
		ID:    "anthropic",
		Name:  "Anthropic (the account you signed in with)",
		Descr: "Your subscription or API account. No key to add.",
		Note:  "The default. Everything else on this list bills a different provider by the token.",
	},
	{
		ID:      "deepseek",
		Name:    "DeepSeek",
		Descr:   "deepseek-v4-pro and deepseek-v4-flash, through DeepSeek's Anthropic endpoint.",
		BaseURL: "https://api.deepseek.com/anthropic",
		KeyVar:  "ANTHROPIC_AUTH_TOKEN",
		Env: map[string]string{
			"ANTHROPIC_MODEL":                "deepseek-v4-pro",
			"ANTHROPIC_DEFAULT_OPUS_MODEL":   "deepseek-v4-pro",
			"ANTHROPIC_DEFAULT_SONNET_MODEL": "deepseek-v4-pro",
			"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "deepseek-v4-flash",
		},
		Docs: "https://api-docs.deepseek.com/quick_start/agent_integrations/claude_code/",
	},
	{
		ID:      "zai",
		Name:    "Z.AI — GLM",
		Descr:   "The GLM coding plan, a flat monthly subscription rather than metered tokens.",
		BaseURL: "https://api.z.ai/api/anthropic",
		KeyVar:  "ANTHROPIC_AUTH_TOKEN",
		Env:     map[string]string{"API_TIMEOUT_MS": "3000000"},
		Docs:    "https://docs.z.ai/devpack/tool/claude",
		Note:    "The long timeout is from Z.AI's own instructions. GLM thinks for a while before it answers.",
	},
	{
		ID:      "moonshot",
		Name:    "Moonshot — Kimi",
		Descr:   "Kimi, through Moonshot's Anthropic endpoint.",
		BaseURL: "https://api.moonshot.ai/anthropic",
		KeyVar:  "ANTHROPIC_AUTH_TOKEN",
		Docs:    "https://platform.kimi.ai/docs/guide/claude-code-kimi",
		Note:    "api.moonshot.ai, not .cn. The .cn host is the mainland China platform and a key from one will not work on the other.",
	},
	{
		ID:      "openrouter",
		Name:    "OpenRouter",
		Descr:   "One key, several hundred models, including ones nobody else fronts with an Anthropic API.",
		BaseURL: "https://openrouter.ai/api",
		KeyVar:  "ANTHROPIC_AUTH_TOKEN",
		Env: map[string]string{
			// OpenRouter's own instructions, and not a mistake. A non-empty
			// ANTHROPIC_API_KEY takes precedence over the bearer token and the
			// requests go out unauthenticated against OpenRouter.
			"ANTHROPIC_API_KEY":                          "",
			"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY": "1",
		},
		Docs: "https://openrouter.ai/docs/cookbook/coding-agents/claude-code-integration",
		Note: "Pick the model on OpenRouter's side, or with --model in the agent's extra flags.",
	},
	{
		ID:      "gateway",
		Name:    "A gateway you run (LiteLLM and the like)",
		Descr:   "Anything with no Anthropic API of its own: Gemini, a local Ollama, a model behind your own proxy.",
		BaseURL: "http://127.0.0.1:4000",
		KeyVar:  "ANTHROPIC_AUTH_TOKEN",
		Docs:    "https://docs.litellm.ai/docs/anthropic_completion",
		Note: "Google publishes no Anthropic-compatible endpoint for Gemini, so a gateway is the way to reach it. " +
			"The gateway has to serve /v1/messages and /v1/messages/count_tokens and pass the anthropic-version and anthropic-beta headers through.",
	},
	{
		ID:     "custom",
		Name:   "Something else",
		Descr:  "Type the URL in.",
		KeyVar: "ANTHROPIC_AUTH_TOKEN",
		Custom: true,
	},
}

// EndpointByID finds one, and reports whether it exists.
func EndpointByID(id string) (Endpoint, bool) {
	for _, e := range Endpoints {
		if e.ID == id {
			return e, true
		}
	}
	return Endpoint{}, false
}
