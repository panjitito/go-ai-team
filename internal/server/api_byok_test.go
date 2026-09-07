package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/panjitito/go-ai-team/internal/catalog"
	"github.com/panjitito/go-ai-team/internal/store"
)

// The endpoint list is reference data and has to be usable without reading the
// source: an entry that sends requests somewhere else is no use without the URL,
// the name of the variable its key belongs in, and a link to where those came
// from, because they change and this list will go stale.
func TestEndpointCatalogIsComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range catalog.Endpoints {
		if e.ID == "" || e.Name == "" {
			t.Errorf("an entry has no id or name: %+v", e)
		}
		if seen[e.ID] {
			t.Errorf("duplicate id %q", e.ID)
		}
		seen[e.ID] = true

		if e.ID == "anthropic" || e.Custom {
			continue
		}
		if e.BaseURL == "" {
			t.Errorf("%s has no base URL, which is the one thing it exists to supply", e.ID)
		}
		if e.KeyVar == "" {
			t.Errorf("%s does not say which variable carries the key", e.ID)
		}
		if e.Docs == "" {
			t.Errorf("%s has no docs link, so nobody can check it when it goes stale", e.ID)
		}
		if !strings.HasPrefix(e.BaseURL, "https://") && !strings.HasPrefix(e.BaseURL, "http://127.0.0.1") &&
			!strings.HasPrefix(e.BaseURL, "http://localhost") {
			t.Errorf("%s sends a key over %q; only a loopback address may be plain http", e.ID, e.BaseURL)
		}
	}

	// The one entry that must exist by name, because it is the default and the
	// form checks for it.
	if !seen["anthropic"] {
		t.Error("there is no entry for the account the user already signed in with")
	}
	if _, ok := catalog.EndpointByID("nope"); ok {
		t.Error("EndpointByID invented an entry")
	}
}

// No value, ever. The list is compiled in and must stay free of anything that
// could be a credential, including in a URL's userinfo.
func TestEndpointCatalogHoldsNoSecrets(t *testing.T) {
	for _, e := range catalog.Endpoints {
		if strings.Contains(e.BaseURL, "@") {
			t.Errorf("%s has userinfo in its base URL: %q", e.ID, e.BaseURL)
		}
		for k, v := range e.Env {
			if strings.Contains(strings.ToUpper(k), "KEY") || strings.Contains(strings.ToUpper(k), "TOKEN") {
				// The one legitimate case is OpenRouter's deliberately empty
				// ANTHROPIC_API_KEY. A value here would be somebody's key.
				if v != "" {
					t.Errorf("%s sets %s to a value in a file that ships to everyone", e.ID, k)
				}
			}
		}
	}
}

func TestListEndpointsServesTheCatalog(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	s.listEndpoints(w, httptest.NewRequest(http.MethodGet, "/api/endpoints", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var out []catalog.Endpoint
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unreadable: %v", err)
	}
	if len(out) != len(catalog.Endpoints) {
		t.Errorf("served %d entries, have %d", len(out), len(catalog.Endpoints))
	}
	if strings.TrimSpace(w.Body.String()) == "null" {
		t.Error("answered null; a list endpoint always returns []")
	}
}

// An agent's environment has to survive the round trip through the API, because
// that is where the endpoint lives. A PATCH that dropped it would quietly move
// an agent back onto the account's own endpoint.
func TestAgentEnvSurvivesTheAPI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	p := &store.Project{Name: "proj", Path: t.TempDir()}
	if err := st.AddProject(p); err != nil {
		t.Fatal(err)
	}
	s := &Server{st: st}

	post := func(path string, body any, h http.HandlerFunc) *httptest.ResponseRecorder {
		t.Helper()
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(b)))
		h(w, r)
		return w
	}

	env := map[string]string{
		"ANTHROPIC_BASE_URL":   "https://api.deepseek.com/anthropic",
		"ANTHROPIC_AUTH_TOKEN": "{{secret:DS_KEY}}",
		"MY_OWN_SETTING":       "keep me",
	}
	w := post("/api/agents", map[string]any{
		"projectId": p.ID, "name": "Checkout", "provider": "claude", "env": env,
	}, s.createAgent)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status %d (%s)", w.Code, w.Body.String())
	}
	var created store.Agent
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Env["ANTHROPIC_AUTH_TOKEN"] != "{{secret:DS_KEY}}" {
		t.Fatalf("the stored agent lost its key reference: %v", created.Env)
	}

	// A PATCH that only renames must not touch the environment.
	b, _ := json.Marshal(map[string]any{"name": "Checkout v2"})
	pw := httptest.NewRecorder()
	pr := httptest.NewRequest(http.MethodPatch, "/api/agents/"+created.ID, strings.NewReader(string(b)))
	pr.SetPathValue("id", created.ID)
	s.patchAgent(pw, pr)
	if pw.Code != http.StatusOK {
		t.Fatalf("patch: status %d (%s)", pw.Code, pw.Body.String())
	}
	after, err := st.Agent(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Env["ANTHROPIC_BASE_URL"] != env["ANTHROPIC_BASE_URL"] {
		t.Errorf("a rename dropped the endpoint: %v", after.Env)
	}

	// And one that sends an empty environment does clear it, because that is
	// how the form says "back to the account you signed in with".
	b2, _ := json.Marshal(map[string]any{"env": map[string]string{}})
	pw2 := httptest.NewRecorder()
	pr2 := httptest.NewRequest(http.MethodPatch, "/api/agents/"+created.ID, strings.NewReader(string(b2)))
	pr2.SetPathValue("id", created.ID)
	s.patchAgent(pw2, pr2)
	off, err := st.Agent(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(off.Env) != 0 {
		t.Errorf("clearing the environment left %v", off.Env)
	}
}
