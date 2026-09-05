/* MCP servers, across every account.
 *
 * The CLI manages these one config directory at a time. With several accounts
 * that is the wrong shape: the question is which of them can reach your
 * database, and the answer is usually "only the first one". `.claude.json` is
 * not part of the shared user layer — it cannot be, it also holds session
 * history and per-project state — so a newly signed-in account has none of your
 * servers, nothing says so, and the agent simply reports that it cannot connect.
 *
 * Values are never shown. A server's env commonly holds a password, so this
 * lists which variables it needs and never what is in them; the copy moves the
 * file's own values across without them passing through here at all.
 */
'use strict';

async function openMCP() {
  const body = el('div', { id: 'mcpBody' }, el('div', { class: 'hint', text: 'Loading…' }));
  modal('MCP servers', body, [['Close', 'btn', closeModal]], true);
  await paintMCP();
}

async function paintMCP() {
  const body = $('#mcpBody');
  if (!body) return;
  let rows = [];
  try {
    rows = await api('/mcp') || [];
  } catch (e) {
    body.innerHTML = '';
    body.append(el('div', { class: 'hint', text: e.message }));
    return;
  }

  body.innerHTML = '';
  body.append(el('div', { class: 'hint', text:
    'Each account has its own set. Sharing your user layer does not cover these — the file they live in also ' +
    'holds session history, which is what keeps accounts separate — so a newly signed-in account starts with none.' }));

  if (!rows.length) {
    body.append(el('div', { class: 'empty', style: 'padding:26px' },
      el('h3', { text: 'No accounts yet' })));
    return;
  }

  const withServers = rows.filter(r => r.servers.length);
  for (const acct of rows) {
    const head = el('div', { class: 'mcp-head' },
      el('strong', { text: acct.name }),
      el('span', { class: 'pill' }, `${acct.servers.length} server${acct.servers.length === 1 ? '' : 's'}`),
      acct.busy ? el('span', { class: 'pill warn', title: 'Its configuration is not written while it is running' }, 'running') : null,
      el('span', { style: 'flex:1' }),
      // Copying into an account only makes sense from one that has some.
      withServers.length && !acct.busy
        ? el('button', { class: 'btn sm', onclick: () => copyInto(acct, withServers) }, 'Copy from…')
        : null);
    body.append(head);

    if (acct.error) {
      body.append(el('div', { class: 'hint', text: acct.error }));
      continue;
    }
    if (!acct.servers.length) {
      body.append(el('div', { class: 'hint', text: 'None. Agents on this account have no MCP tools.' }));
      continue;
    }

    const table = el('table', { class: 'kv mcp-table' });
    for (const s of acct.servers) {
      table.append(el('tr', {},
        el('td', {}, el('span', { class: 'mono', text: s.name })),
        el('td', {}, el('span', { class: 'pill' }, s.type)),
        el('td', { class: 'mcp-cmd mono', title: s.url || [s.command].concat(s.args || []).join(' ') },
          s.url || [s.command].concat(s.args || []).join(' ')),
        el('td', {}, (s.envKeys || []).length
          ? el('span', {
              class: 'pill' + (s.hasSecrets ? ' warn' : ''),
              title: 'Environment it is given: ' + (s.envKeys || []).join(', ') +
                (s.hasSecrets ? '\nOne of these looks like a credential. Its value is never shown here.' : ''),
            }, `${s.envKeys.length} env`)
          : null)));
    }
    body.append(table);
  }
}

// copyInto asks which servers to bring over, and from where.
function copyInto(target, sources) {
  const others = sources.filter(s => s.accountId !== target.accountId);
  if (!others.length) {
    toast('No other account has any to copy', 'bad');
    return;
  }

  const pick = el('select', { id: 'mcpFrom' },
    others.map(o => el('option', { value: o.accountId }, `${o.name} — ${o.servers.length}`)));
  const list = el('div', { id: 'mcpNames', class: 'mcp-pick' });

  const fill = () => {
    const from = others.find(o => o.accountId === pick.value) || others[0];
    list.innerHTML = '';
    for (const s of from.servers) {
      const already = target.servers.some(t => t.name === s.name);
      list.append(el('label', { class: 'switch' },
        el('input', {
          type: 'checkbox', value: s.name,
          checked: already ? null : 'checked',
          disabled: already ? 'disabled' : null,
        }),
        s.name + (already ? ' — already here' : (s.hasSecrets ? ' — carries a credential' : ''))));
    }
  };
  pick.addEventListener('change', fill);
  fill();

  modal(`Copy MCP servers into ${target.name}`, el('div', {},
    el('label', { text: 'From' }), pick,
    el('label', { text: 'Servers' }), list,
    el('div', { class: 'hint', text:
      'The definition is copied with its environment, values included, so the server actually works on the ' +
      'other account. Nothing is overwritten: a name already there is left alone.' })),
    [
      ['Cancel', 'btn', () => { closeModal(); openMCP(); }],
      ['Copy', 'btn primary', async () => {
        const names = [...list.querySelectorAll('input:checked')].map(i => i.value);
        if (!names.length) return toast('Pick at least one', 'bad');
        try {
          const r = await api('/mcp/copy', {
            method: 'POST',
            body: { from: pick.value, to: target.accountId, names },
          });
          const n = (r.copied || []).length;
          toast(n ? `Copied ${n} server${n === 1 ? '' : 's'}` : 'Nothing to copy', n ? 'ok' : 'bad');
        } catch (e) {
          toast(e.message, 'bad');
        }
        closeModal();
        openMCP();
      }],
    ]);
}
