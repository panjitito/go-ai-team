// Pointing one agent at somebody else's API.
//
// Claude Code goes wherever ANTHROPIC_BASE_URL points, and several providers
// serve Anthropic's Messages API so it can point at them. Every piece of that
// already worked here: an agent carries its own environment, the vault holds a
// value nothing can read back, and the spawn resolves {{secret:NAME}} on the
// way out. What was missing was knowing four strings per provider, which are
// not guessable and live on four different documentation sites.
//
// So this is a form that fills itself in. Choosing a provider writes its base
// URL and the extra variables its own instructions call for, and every field
// stays editable, because those details change.
//
// The key is never typed here and never leaves the vault. The agent stores
// {{secret:NAME}} — the name of a secret, not a copy of one — and the value is
// resolved in the server process on the way into the child. Nothing on this
// page has ever seen it.

// EP is the endpoint catalogue, fetched once.
let EP = null;

async function endpointCatalog() {
  if (!EP) EP = (await tryApi('/endpoints')) || [];
  return EP;
}

async function vaultNames() {
  const r = await tryApi('/secrets');
  if (!r || !r.available) return { available: false, reason: r ? r.reason : '', names: [] };
  return { available: true, reason: '', names: (r.secrets || []).map(s => s.name) };
}

// SECRET_REF matches the reference an agent stores in place of a key.
const SECRET_REF = /^\{\{secret:([^}]+)\}\}$/;

// endpointKeys are the variables this form owns. Anything else in an agent's
// environment was put there by hand and is carried through untouched — losing
// somebody's PYTHONPATH because they changed provider would be its own bug.
function endpointKeys(catalogue) {
  const keys = new Set(['ANTHROPIC_BASE_URL']);
  for (const e of catalogue) {
    if (e.keyVar) keys.add(e.keyVar);
    for (const k of Object.keys(e.env || {})) keys.add(k);
  }
  return keys;
}

// readEndpoint works out which entry an agent's environment corresponds to.
// Matched on the base URL, since that is the part that decides where requests
// go; anything set that no entry claims is "custom".
function readEndpoint(env, catalogue) {
  env = env || {};
  const url = (env.ANTHROPIC_BASE_URL || '').trim();
  if (!url) return { id: 'anthropic', url: '', secret: '' };

  let id = 'custom';
  for (const e of catalogue) {
    if (e.baseUrl && e.baseUrl.replace(/\/+$/, '') === url.replace(/\/+$/, '')) { id = e.id; break; }
  }
  // Whichever variable actually carries a reference is the key variable, which
  // is more reliable than assuming the entry's default.
  let secret = '';
  for (const [k, v] of Object.entries(env)) {
    const m = SECRET_REF.exec(String(v || ''));
    if (m && /KEY|TOKEN/i.test(k)) { secret = m[1]; break; }
  }
  return { id, url, secret };
}

// endpointFields is the block that goes into the agent form.
function endpointFields(agent, catalogue, vault) {
  const cur = readEndpoint(agent && agent.env, catalogue);
  const owned = endpointKeys(catalogue);
  // Anything the form does not own, kept aside and written back on save.
  const carried = {};
  for (const [k, v] of Object.entries((agent && agent.env) || {})) {
    if (!owned.has(k)) carried[k] = v;
  }

  const sel = el('select', { id: 'aEndpoint' },
    catalogue.map(e => el('option', { value: e.id, selected: e.id === cur.id ? 'selected' : null }, e.name)));

  const note = el('div', { class: 'hint', id: 'aEndpointNote' });
  const urlRow = el('div', { id: 'aEndpointUrlRow' },
    el('label', { text: 'Base URL' }),
    el('input', { type: 'text', id: 'aEndpointUrl', value: cur.url, placeholder: 'https://api.example.com/anthropic' }));

  const keyRow = el('div', { id: 'aEndpointKeyRow' },
    el('label', { text: 'Key, from the vault' }),
    el('select', { id: 'aEndpointSecret' },
      el('option', { value: '' }, vault.available ? '— pick a secret —' : '— the vault is unavailable —'),
      vault.names.map(n => el('option', { value: n, selected: n === cur.secret ? 'selected' : null }, n))),
    el('div', { class: 'hint' },
      vault.available
        ? 'Add keys under Environment → Secrets. The agent stores the name; the value is read in the server process at launch and never comes back through this page.'
        : 'The vault is unavailable on this machine' + (vault.reason ? ': ' + vault.reason : '.') +
          ' Without it there is nowhere to put a key that is not clear text, so this agent cannot use another provider.'));

  // A secret named in the agent but no longer in the vault. Silently showing
  // "pick a secret" would look like it had never been set, and the agent would
  // refuse to start with an error about a name that is not on screen anywhere.
  const missing = el('div', { class: 'pill bad', id: 'aEndpointMissing', style: 'display:none;margin-top:8px' });
  if (cur.secret && vault.available && !vault.names.includes(cur.secret)) {
    missing.textContent = `This agent points at a secret called ${cur.secret}, which the vault no longer has. It will refuse to start until that is fixed.`;
    missing.style.display = '';
  }

  const wrap = el('div', { id: 'aEndpointBlock' },
    el('label', { text: 'API endpoint' }), sel, note, urlRow, keyRow, missing);
  wrap._carried = carried;

  const paint = () => {
    const e = catalogue.find(x => x.id === sel.value) || catalogue[0];
    const off = e.id === 'anthropic';
    urlRow.style.display = off ? 'none' : '';
    keyRow.style.display = off ? 'none' : '';
    note.textContent = [e.descr, e.note].filter(Boolean).join(' ');
    if (e.docs) {
      note.append(' ');
      note.append(el('a', { href: e.docs, target: '_blank', rel: 'noreferrer noopener' }, 'Provider docs'));
    }
    // Filling the URL in is the point of picking a provider, but retyping over
    // somebody's edited value is not: only an empty box or a value belonging to
    // a different entry gets replaced.
    const box = urlRow.querySelector('#aEndpointUrl');
    const known = catalogue.some(x => x.baseUrl && x.baseUrl === box.value.trim());
    if (!off && e.baseUrl && (!box.value.trim() || known)) box.value = e.baseUrl;
    if (off) box.value = '';
  };
  sel.addEventListener('change', paint);
  paint();
  return wrap;
}

// readEndpointFields turns the block back into an agent environment.
function readEndpointFields(catalogue) {
  const block = document.querySelector('#aEndpointBlock');
  if (!block) return null; // the form did not include it
  const id = document.querySelector('#aEndpoint').value;
  const e = catalogue.find(x => x.id === id) || catalogue[0];
  const env = { ...(block._carried || {}) };
  if (e.id === 'anthropic') return env;

  const url = document.querySelector('#aEndpointUrl').value.trim();
  const secret = document.querySelector('#aEndpointSecret').value;
  if (!url) return { error: 'That endpoint needs a base URL.' };
  if (!secret) return { error: 'Pick the vault secret holding the key for that endpoint.' };

  env.ANTHROPIC_BASE_URL = url;
  env[e.keyVar || 'ANTHROPIC_AUTH_TOKEN'] = `{{secret:${secret}}}`;
  // The provider's own extra variables, including OpenRouter's deliberately
  // empty ANTHROPIC_API_KEY. An empty value is meaningful and must survive.
  for (const [k, v] of Object.entries(e.env || {})) env[k] = v;
  return env;
}

// endpointBadge names the API a running session is talking to, when that is not
// the account's own. Without it the account badge is a lie by omission: it names
// the account while a key beside ANTHROPIC_BASE_URL is paying for every token.
function endpointBadge(sess) {
  const ep = sess && sess.endpoint;
  if (!ep || !ep.host) return null;
  const inherited = ep.source === 'inherited';
  return el('span', {
    class: 'pill ' + (inherited ? 'warn' : ''),
    title: inherited
      ? `ANTHROPIC_BASE_URL was inherited from the shell Go AI Team was started in, so this agent is not billed to its account. Set it on the agent, or unset it in that shell, to make the choice deliberate.`
      : `This agent talks to ${ep.host} rather than to its account.`,
  }, ep.host);
}
