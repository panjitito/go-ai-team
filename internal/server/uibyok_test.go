//go:build uitest

package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Pointing an agent at another provider, through the form a person uses.
//
// The parts of this that could go wrong are all in the round trip. An agent's
// environment is a free-form map, the form owns only some of its keys, and the
// value of the key variable is a reference into the vault rather than a key.
// Three things have to hold: the preset fills in what the provider's own
// instructions say, everything the form does not own survives being saved, and
// no key is ever asked for or displayed here.
func TestUIByokForm(t *testing.T) {
	const port = 7841
	startServer(t, port)

	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	if ready := c.evalString(t, `
new Promise(async resolve => {
  for (let i = 0; i < 80; i++) {
    if (typeof endpointFields === 'function' && typeof readEndpoint === 'function') {
      return resolve('ready');
    }
    await new Promise(r => setTimeout(r, 250));
  }
  resolve('timeout');
})`); ready != "ready" {
		t.Fatalf("the UI never finished loading: %s", ready)
	}

	out := c.evalString(t, `
new Promise(async resolve => {
  const cat = await endpointCatalog();
  const ids = cat.map(e => e.id);

  // Every entry that sends requests elsewhere needs the two things a person
  // cannot guess, and a link to where they came from.
  const incomplete = cat.filter(e => e.id !== 'anthropic' && e.id !== 'custom')
    .filter(e => !e.baseUrl || !e.keyVar || !e.docs)
    .map(e => e.id);

  // --- reading an agent's environment back into the form ------------------
  const read = {
    plain:      readEndpoint({}, cat),
    deepseek:   readEndpoint({
      ANTHROPIC_BASE_URL: 'https://api.deepseek.com/anthropic',
      ANTHROPIC_AUTH_TOKEN: '{{secret:DS_KEY}}',
    }, cat),
    trailing:   readEndpoint({ ANTHROPIC_BASE_URL: 'https://api.deepseek.com/anthropic/' }, cat),
    unknownUrl: readEndpoint({ ANTHROPIC_BASE_URL: 'https://something.internal' }, cat),
  };

  // --- the form itself ----------------------------------------------------
  // An agent already on a provider, carrying a variable of its own that the
  // form knows nothing about.
  const agent = {
    name: 'Checkout',
    env: {
      ANTHROPIC_BASE_URL: 'https://api.z.ai/api/anthropic',
      ANTHROPIC_AUTH_TOKEN: '{{secret:ZAI_KEY}}',
      API_TIMEOUT_MS: '3000000',
      MY_OWN_SETTING: 'keep me',
    },
  };
  const vault = { available: true, reason: '', names: ['ZAI_KEY', 'DS_KEY'] };

  const host = document.createElement('div');
  host.append(endpointFields(agent, cat, vault));
  document.body.append(host);

  const sel = document.querySelector('#aEndpoint');
  const opened = {
    provider: sel.value,
    url: document.querySelector('#aEndpointUrl').value,
    secret: document.querySelector('#aEndpointSecret').value,
    keyRowShown: document.querySelector('#aEndpointKeyRow').style.display !== 'none',
  };

  // Saving it unchanged must not lose the variable the form does not own, and
  // must still store a reference rather than anything resembling a key.
  const savedSame = readEndpointFields(cat);

  // Switch provider: the URL follows, because the old one belonged to the
  // entry being left.
  sel.value = 'deepseek';
  sel.dispatchEvent(new Event('change'));
  const afterSwitch = {
    url: document.querySelector('#aEndpointUrl').value,
    noteMentionsDeepseek: document.querySelector('#aEndpointNote').textContent.toLowerCase().includes('deepseek'),
  };
  document.querySelector('#aEndpointSecret').value = 'DS_KEY';
  const savedDeepseek = readEndpointFields(cat);

  // A URL somebody typed themselves is theirs and must survive a repaint.
  document.querySelector('#aEndpointUrl').value = 'https://my.gateway.internal';
  sel.dispatchEvent(new Event('change'));
  const editedSurvives = document.querySelector('#aEndpointUrl').value;

  // Back to Anthropic: the endpoint variables go, the private one stays.
  sel.value = 'anthropic';
  sel.dispatchEvent(new Event('change'));
  const savedOff = readEndpointFields(cat);
  const offHidesRows = document.querySelector('#aEndpointUrlRow').style.display === 'none';

  // A provider with no secret chosen is refused rather than saved broken.
  sel.value = 'moonshot';
  sel.dispatchEvent(new Event('change'));
  document.querySelector('#aEndpointSecret').value = '';
  const refused = readEndpointFields(cat);

  host.remove();
  resolve(JSON.stringify({
    ids, incomplete, read, opened, savedSame, afterSwitch,
    editedSurvives, savedOff, offHidesRows, refused,
  }));
})`)

	t.Logf("byok: %s", out)

	var got struct {
		IDs        []string `json:"ids"`
		Incomplete []string `json:"incomplete"`
		Read       struct {
			Plain      struct{ ID, URL, Secret string } `json:"plain"`
			Deepseek   struct{ ID, URL, Secret string } `json:"deepseek"`
			Trailing   struct{ ID, URL, Secret string } `json:"trailing"`
			UnknownURL struct{ ID, URL, Secret string } `json:"unknownUrl"`
		} `json:"read"`
		Opened struct {
			Provider    string `json:"provider"`
			URL         string `json:"url"`
			Secret      string `json:"secret"`
			KeyRowShown bool   `json:"keyRowShown"`
		} `json:"opened"`
		SavedSame   map[string]string `json:"savedSame"`
		AfterSwitch struct {
			URL                  string `json:"url"`
			NoteMentionsDeepseek bool   `json:"noteMentionsDeepseek"`
		} `json:"afterSwitch"`
		EditedSurvives string            `json:"editedSurvives"`
		SavedOff       map[string]string `json:"savedOff"`
		OffHidesRows   bool              `json:"offHidesRows"`
		Refused        map[string]string `json:"refused"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unreadable result: %v", err)
	}

	// The catalogue is complete enough to be worth having.
	for _, want := range []string{"anthropic", "deepseek", "zai", "moonshot", "openrouter", "gateway", "custom"} {
		if !containsStr(got.IDs, want) {
			t.Errorf("the endpoint list has no %q: %v", want, got.IDs)
		}
	}
	if len(got.Incomplete) > 0 {
		t.Errorf("these entries are missing a base URL, a key variable or a docs link: %v", got.Incomplete)
	}

	// Reading an environment back.
	if got.Read.Plain.ID != "anthropic" {
		t.Errorf("an agent with no environment read as %q, want anthropic", got.Read.Plain.ID)
	}
	if got.Read.Deepseek.ID != "deepseek" || got.Read.Deepseek.Secret != "DS_KEY" {
		t.Errorf("deepseek read back as %+v", got.Read.Deepseek)
	}
	if got.Read.Trailing.ID != "deepseek" {
		t.Errorf("a trailing slash made the same URL unrecognisable: %+v", got.Read.Trailing)
	}
	if got.Read.UnknownURL.ID != "custom" {
		t.Errorf("a URL nobody publishes read as %q, want custom", got.Read.UnknownURL.ID)
	}

	// Opening an agent that is already on a provider.
	if got.Opened.Provider != "zai" || got.Opened.Secret != "ZAI_KEY" {
		t.Errorf("the form opened on %+v, want zai with ZAI_KEY selected", got.Opened)
	}
	if !got.Opened.KeyRowShown {
		t.Error("the key row was hidden for an agent that has one")
	}

	// Saving unchanged.
	if got.SavedSame["MY_OWN_SETTING"] != "keep me" {
		t.Errorf("a variable the form does not own was lost: %v", got.SavedSame)
	}
	if got.SavedSame["ANTHROPIC_AUTH_TOKEN"] != "{{secret:ZAI_KEY}}" {
		t.Errorf("the key is not a vault reference: %q", got.SavedSame["ANTHROPIC_AUTH_TOKEN"])
	}

	// Switching provider.
	if got.AfterSwitch.URL != "https://api.deepseek.com/anthropic" {
		t.Errorf("switching provider left the URL at %q", got.AfterSwitch.URL)
	}
	if !got.AfterSwitch.NoteMentionsDeepseek {
		t.Error("the hint did not change with the provider")
	}
	if got.EditedSurvives != "https://my.gateway.internal" {
		t.Errorf("a hand-typed URL was overwritten with %q", got.EditedSurvives)
	}

	// Turning it off.
	if _, ok := got.SavedOff["ANTHROPIC_BASE_URL"]; ok {
		t.Errorf("going back to Anthropic left the endpoint set: %v", got.SavedOff)
	}
	if _, ok := got.SavedOff["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Errorf("going back to Anthropic left a key reference behind: %v", got.SavedOff)
	}
	if got.SavedOff["MY_OWN_SETTING"] != "keep me" {
		t.Errorf("going back to Anthropic dropped an unrelated variable: %v", got.SavedOff)
	}
	if !got.OffHidesRows {
		t.Error("the URL row is still on screen for an agent using its own account")
	}

	// Refusing an incomplete one.
	if got.Refused["error"] == "" {
		t.Errorf("a provider with no key chosen saved anyway: %v", got.Refused)
	}

	// Nothing anywhere in this exchange may look like a key.
	if strings.Contains(out, "sk-") {
		t.Error("something key-shaped appeared in the endpoint form")
	}
}

func containsStr(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
