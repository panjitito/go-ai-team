/* Go AI Team — modal panels: libraries, automation, environment, roles, voice.
 *
 * These are the things you set up occasionally and then use constantly, so they
 * live in panels rather than taking a slot in the main navigation.
 */
'use strict';

// ---------------------------------------------------------------- prompts

async function openPrompts() {
  const prompts = await tryApi('/prompts');
  const body = el('div', {});

  body.append(el('p', { class: 'hint', style: 'margin-top:0' },
    'Save the briefs that worked. A prompt can pull in another with {{prompt:name}}, ' +
    'so your stack, conventions and review checklist live in one place instead of being ' +
    'copied into every prompt and drifting apart.'));

  const folders = {};
  for (const p of prompts) (folders[p.folder || 'General'] ||= []).push(p);

  for (const [folder, list] of Object.entries(folders)) {
    body.append(el('div', { class: 'side-head', text: folder }));
    for (const p of list) {
      body.append(el('div', { class: 'acct-row' },
        el('div', { class: 'info' },
          el('div', {}, el('strong', { text: p.name }),
            p.personal ? el('span', { class: 'src-badge', text: '  · personal' }) : null),
          el('div', { class: 'dirpath', text: firstLineOf(p.body, 90) }),
          p.uses ? el('div', { class: 'hint', text: `used ${p.uses}×` }) : null),
        el('div', { style: 'display:flex;gap:5px' },
          S.openSession ? el('button', {
            class: 'btn sm primary', title: 'Send it to the terminal on screen',
            onclick: async () => {
              await tryApi(`/prompts/${p.id}/send`, { method: 'POST', body: { sessionId: S.openSession } });
              closeModal();
              toast('Sent', 'ok');
            },
          }, 'Send') : null,
          el('button', { class: 'btn sm', onclick: () => editPrompt(p) }, 'Edit'))));
    }
  }
  if (!prompts.length) body.append(el('div', { class: 'hint', style: 'padding:12px', text: 'Nothing saved yet.' }));

  modal('Prompt library', body, [
    ['Close', 'btn', closeModal],
    ['+ New prompt', 'btn primary', () => editPrompt(null)],
  ], true);
}

function editPrompt(p) {
  const body = el('div', {},
    el('label', { text: 'Name' }),
    el('input', { type: 'text', id: 'pmName', value: p ? p.name : '' }),
    el('label', { text: 'Folder' }),
    el('input', { type: 'text', id: 'pmFolder', value: p ? (p.folder || '') : '', placeholder: 'General' }),
    el('label', { text: 'Body' }),
    el('textarea', { id: 'pmBody', rows: '12', value: p ? p.body : '' }),
    el('div', { class: 'hint', text: 'Reference another prompt with {{prompt:name}}. It is expanded when the prompt is sent, so the agent receives one complete brief.' }),
    el('label', { class: 'switch', style: 'margin-top:12px' },
      el('input', { type: 'checkbox', id: 'pmPersonal', checked: p && p.personal ? 'checked' : null }),
      el('span', { text: 'Personal — keep out of a team export' })));

  const buttons = [['Cancel', 'btn', () => { closeModal(); openPrompts(); }]];
  if (p) {
    buttons.push(['Preview resolved', 'btn', async () => {
      const r = await tryApi(`/prompts/${p.id}/resolve`, { method: 'POST' });
      modal('Resolved: ' + r.name,
        el('pre', { class: 'wrap mono', text: r.body }),
        [['Back', 'btn', () => { closeModal(); openPrompts(); }]], true);
    }]);
    buttons.push(['Delete', 'btn danger', async () => {
      closeModal();
      await tryApi(`/prompts/${p.id}`, { method: 'DELETE' });
      openPrompts();
    }]);
  }
  buttons.push(['Save', 'btn primary', async () => {
    const payload = {
      name: $('#pmName').value.trim(),
      folder: $('#pmFolder').value.trim(),
      body: $('#pmBody').value,
      personal: $('#pmPersonal').checked,
    };
    if (!payload.name || !payload.body.trim()) return toast('Name and body are both needed', 'bad');
    closeModal();
    if (p) await tryApi(`/prompts/${p.id}`, { method: 'PATCH', body: payload });
    else await tryApi('/prompts', { method: 'POST', body: payload });
    openPrompts();
  }]);

  modal(p ? 'Edit prompt' : 'New prompt', body, buttons, true);
}

function firstLineOf(s, n) {
  const line = (s || '').split('\n')[0].trim();
  return line.length > n ? line.slice(0, n) + '…' : line;
}

// ---------------------------------------------------------------- skills

async function openSkills() {
  const skills = await tryApi('/skills');
  const body = el('div', {});
  body.append(el('p', { class: 'hint', style: 'margin-top:0' },
    'A skill is a procedure in SKILL.md form: how your team does a particular job. ' +
    'Export one and it works in any runtime that reads that format.'));

  for (const k of skills) {
    body.append(el('div', { class: 'acct-row' },
      el('div', { class: 'info' },
        el('div', {}, el('strong', { text: k.name })),
        el('div', { class: 'dirpath', text: k.description || firstLineOf(k.body, 90) }),
        (k.triggers || []).length
          ? el('div', { style: 'margin-top:4px;display:flex;gap:4px;flex-wrap:wrap' },
              k.triggers.map(t => el('span', { class: 'pill' }, t)))
          : null),
      el('div', { style: 'display:flex;gap:5px' },
        el('a', { class: 'btn sm', href: `/api/skills/${k.id}/export` }, 'Export'),
        el('button', { class: 'btn sm', onclick: () => editSkill(k) }, 'Edit'))));
  }
  if (!skills.length) body.append(el('div', { class: 'hint', style: 'padding:12px', text: 'Nothing saved yet.' }));

  modal('Skills library', body, [
    ['Close', 'btn', closeModal],
    ['+ New skill', 'btn primary', () => editSkill(null)],
  ], true);
}

function editSkill(k) {
  const body = el('div', {},
    el('label', { text: 'Name' }),
    el('input', { type: 'text', id: 'skName', value: k ? k.name : '' }),
    el('label', { text: 'One-line description' }),
    el('input', { type: 'text', id: 'skDesc', value: k ? (k.description || '') : '' }),
    el('label', { text: 'Triggers' }),
    el('input', {
      type: 'text', id: 'skTrig',
      value: k ? (k.triggers || []).join(', ') : '',
      placeholder: 'migration, schema change',
    }),
    el('div', { class: 'hint', text: 'Comma separated. These are the words that should make this procedure the relevant one.' }),
    el('label', { text: 'Body' }),
    el('textarea', { id: 'skBody', rows: '12', value: k ? k.body : '' }));

  const buttons = [['Cancel', 'btn', () => { closeModal(); openSkills(); }]];
  if (k) buttons.push(['Delete', 'btn danger', async () => {
    closeModal();
    await tryApi(`/skills/${k.id}`, { method: 'DELETE' });
    openSkills();
  }]);
  buttons.push(['Save', 'btn primary', async () => {
    const payload = {
      name: $('#skName').value.trim(),
      description: $('#skDesc').value.trim(),
      body: $('#skBody').value,
      triggers: $('#skTrig').value.split(',').map(s => s.trim()).filter(Boolean),
    };
    if (!payload.name || !payload.body.trim()) return toast('Name and body are both needed', 'bad');
    closeModal();
    if (k) await tryApi(`/skills/${k.id}`, { method: 'PATCH', body: payload });
    else await tryApi('/skills', { method: 'POST', body: payload });
    openSkills();
  }]);

  modal(k ? 'Edit skill' : 'New skill', body, buttons, true);
}

// ---------------------------------------------------------------- memory

async function openMemory() {
  const p = projectById(S.selectedProject);
  if (!p) return toast('Pick a project first', 'bad');
  const mems = await tryApi('/memory?projectId=' + encodeURIComponent(p.id));

  const body = el('div', {});
  body.append(el('p', { class: 'hint', style: 'margin-top:0' },
    'What your agents learned about this codebase, and it survives the session. ' +
    'Every agent on the project reads the same set through memory_read, so a pitfall ' +
    'found once is not rediscovered five times.'));

  const kinds = {};
  for (const m of mems) (kinds[m.kind] ||= []).push(m);

  for (const [kind, list] of Object.entries(kinds)) {
    body.append(el('div', { class: 'side-head', text: kind }));
    for (const m of list) {
      body.append(el('div', { class: 'acct-row' },
        el('div', { class: 'info' },
          el('div', {}, el('strong', { text: m.title })),
          el('pre', { class: 'wrap', style: 'margin:4px 0;font-size:12px', text: m.body }),
          m.author ? el('div', { class: 'hint', text: 'recorded by ' + m.author }) : null),
        el('button', {
          class: 'btn danger sm',
          onclick: async () => {
            await tryApi(`/memory/${m.id}`, { method: 'DELETE' });
            closeModal(); openMemory();
          },
        }, '🗑')));
    }
  }
  if (!mems.length) {
    body.append(el('div', { class: 'hint', style: 'padding:12px' },
      'Nothing recorded yet. Agents write here themselves with the memory_save tool once the MCP bridge is wired into a project.'));
  }

  modal('Project memory', body, [
    ['Close', 'btn', closeModal],
    ['+ Add an entry', 'btn primary', () => addMemory(p)],
  ], true);
}

function addMemory(p) {
  const body = el('div', {},
    el('label', { text: 'Kind' }),
    el('select', { id: 'mmKind' },
      ['architecture', 'decisions', 'pitfalls', 'conventions', 'features']
        .map(k => el('option', { value: k }, k))),
    el('label', { text: 'Title' }),
    el('input', { type: 'text', id: 'mmTitle' }),
    el('label', { text: 'The fact, and why it matters' }),
    el('textarea', { id: 'mmBody', rows: '6' }));
  modal('Add to memory', body, [
    ['Cancel', 'btn', () => { closeModal(); openMemory(); }],
    ['Save', 'btn primary', async () => {
      const payload = {
        projectId: p.id, kind: $('#mmKind').value,
        title: $('#mmTitle').value.trim(), body: $('#mmBody').value, author: 'you',
      };
      if (!payload.title || !payload.body.trim()) return toast('Title and body are both needed', 'bad');
      closeModal();
      await tryApi('/memory', { method: 'POST', body: payload });
      openMemory();
    }],
  ]);
}

// ---------------------------------------------------------------- automation

async function openAutomation() {
  const [scheds, hooks] = await Promise.all([tryApi('/schedules'), tryApi('/webhooks')]);
  const body = el('div', {});

  body.append(el('div', { class: 'tabs' },
    el('div', { class: 'tab active', id: 'atS', onclick: () => swapAuto('s') }, 'Schedules'),
    el('div', { class: 'tab', id: 'atW', onclick: () => swapAuto('w') }, 'Webhooks')));

  // --- schedules ---
  const sPane = el('div', { id: 'autoS' });
  sPane.append(el('p', { class: 'hint', style: 'margin-top:0' },
    'Run an agent on a cadence, with no cron expression to write. A window missed while ' +
    'the app was closed is caught up on the next launch rather than skipped, and with a ' +
    'second account signed in a quota wall no longer ends an overnight run.'));
  for (const sc of scheds) {
    sPane.append(el('div', { class: 'acct-row' },
      el('div', { class: 'info' },
        el('div', {}, el('strong', { text: sc.name }),
          !sc.enabled ? el('span', { class: 'src-badge', text: '  · paused' }) : null),
        el('div', { class: 'dirpath', text: firstLineOf(sc.prompt, 80) }),
        el('div', { style: 'margin-top:5px;display:flex;gap:6px;flex-wrap:wrap' },
          el('span', { class: 'pill' }, sc.cadence),
          sc.nextRun && sc.enabled
            ? el('span', { class: 'pill' }, 'next ' + new Date(sc.nextRun).toLocaleString()) : null,
          sc.runCount ? el('span', { class: 'pill' }, `${sc.runCount} runs`) : null,
          sc.lastErr ? el('span', { class: 'pill bad', title: sc.lastErr }, 'last run failed') : null)),
      el('div', { style: 'display:flex;flex-direction:column;gap:5px' },
        el('button', {
          class: 'btn sm', onclick: async () => {
            const r = await tryApi(`/schedules/${sc.id}/run`, { method: 'POST' });
            closeModal();
            await loadAll();
            if (r.sessionId) openTerm(r.sessionId);
          },
        }, 'Run now'),
        el('div', { style: 'display:flex;gap:4px' },
          el('button', {
            class: 'btn ghost sm', onclick: async () => {
              await tryApi(`/schedules/${sc.id}`, { method: 'PATCH', body: { enabled: !sc.enabled } });
              closeModal(); openAutomation();
            },
          }, sc.enabled ? '⏸' : '▶'),
          el('button', {
            class: 'btn danger sm', onclick: async () => {
              await tryApi(`/schedules/${sc.id}`, { method: 'DELETE' });
              closeModal(); openAutomation();
            },
          }, '🗑')))));
  }
  if (!scheds.length) sPane.append(el('div', { class: 'hint', style: 'padding:12px', text: 'No schedules yet.' }));
  sPane.append(el('button', { class: 'btn primary', style: 'margin-top:12px', onclick: newSchedule }, '+ New schedule'));

  // --- webhooks ---
  const wPane = el('div', { id: 'autoW', style: 'display:none' });
  wPane.append(el('p', { class: 'hint', style: 'margin-top:0' },
    'Start an agent when something actually happens instead of on a clock. Each trigger gets ' +
    'a URL and a signing secret to paste into GitHub, GitLab, Linear or anything that can POST ' +
    'JSON. The signature is verified, a filter decides what deserves a run, and a burst limit ' +
    'stops a noisy service starting twenty agents.'));
  for (const wh of hooks) {
    const full = location.origin + wh.url;
    wPane.append(el('div', { class: 'acct-row' },
      el('div', { class: 'info' },
        el('div', {}, el('strong', { text: wh.name }),
          !wh.enabled ? el('span', { class: 'src-badge', text: '  · paused' }) : null),
        el('div', {
          class: 'dirpath', style: 'cursor:pointer', title: 'Click to copy',
          onclick: () => { navigator.clipboard.writeText(full); toast('URL copied'); },
        }, full),
        el('div', {
          class: 'dirpath', style: 'cursor:pointer', title: 'Click to copy the signing secret',
          onclick: () => { navigator.clipboard.writeText(wh.secret); toast('Secret copied'); },
        }, 'secret: ' + wh.secret.slice(0, 10) + '…'),
        el('div', { style: 'margin-top:5px;display:flex;gap:6px;flex-wrap:wrap' },
          wh.filter ? el('span', { class: 'pill mono' }, wh.filter) : null,
          el('span', { class: 'pill' }, `${wh.burstLimit}/hr`),
          wh.hits ? el('span', { class: 'pill' }, `${wh.runs}/${wh.hits} ran`) : null,
          wh.lastErr ? el('span', { class: 'pill bad', title: wh.lastErr }, 'see last error') : null)),
      el('div', { style: 'display:flex;flex-direction:column;gap:5px' },
        el('button', {
          class: 'btn sm', title: 'Send a signed test delivery through the whole pipeline',
          onclick: async () => {
            const r = await tryApi(`/webhooks/${wh.id}/test`, { method: 'POST' });
            toast(r.reason, r.accepted ? 'ok' : 'bad');
            if (r.sessionId) { closeModal(); await loadAll(); openTerm(r.sessionId); }
          },
        }, 'Test'),
        el('div', { style: 'display:flex;gap:4px' },
          el('button', {
            class: 'btn ghost sm', onclick: async () => {
              await tryApi(`/webhooks/${wh.id}`, { method: 'PATCH', body: { enabled: !wh.enabled } });
              closeModal(); openAutomation();
            },
          }, wh.enabled ? '⏸' : '▶'),
          el('button', {
            class: 'btn danger sm', onclick: async () => {
              await tryApi(`/webhooks/${wh.id}`, { method: 'DELETE' });
              closeModal(); openAutomation();
            },
          }, '🗑')))));
  }
  if (!hooks.length) wPane.append(el('div', { class: 'hint', style: 'padding:12px', text: 'No triggers yet.' }));
  wPane.append(el('button', { class: 'btn primary', style: 'margin-top:12px', onclick: newWebhook }, '+ New trigger'));

  body.append(sPane, wPane);
  modal('Automation', body, [['Close', 'btn', closeModal]], true);
}

function swapAuto(which) {
  $('#atS').classList.toggle('active', which === 's');
  $('#atW').classList.toggle('active', which === 'w');
  $('#autoS').style.display = which === 's' ? '' : 'none';
  $('#autoW').style.display = which === 'w' ? '' : 'none';
}

function newSchedule() {
  const p = projectById(S.selectedProject);
  if (!p) return toast('Pick a project first', 'bad');
  const body = el('div', {},
    el('label', { text: 'Name' }),
    el('input', { type: 'text', id: 'sName', placeholder: 'Review open pull requests' }),
    el('label', { text: 'What the agent should do' }),
    el('textarea', { id: 'sPrompt', rows: '4', placeholder: 'Review any open pull request and comment on anything that would break in production.' }),
    el('label', { text: 'Agent' }),
    el('select', { id: 'sAgent' },
      el('option', { value: '' }, '— any agent on this project —'),
      agentsOf(p.id).map(a => el('option', { value: a.id }, a.name))),
    el('label', { text: 'How often' }),
    el('select', { id: 'sEvery', onchange: syncSchedFields },
      el('option', { value: 'minutes' }, 'every N minutes'),
      el('option', { value: 'hourly' }, 'hourly'),
      el('option', { value: 'daily', selected: 'selected' }, 'daily'),
      el('option', { value: 'weekly' }, 'weekly'),
      el('option', { value: 'monthly' }, 'monthly')),
    el('div', { id: 'schedFields', style: 'display:flex;gap:8px;margin-top:8px' }));

  modal('New schedule', body, [
    ['Cancel', 'btn', () => { closeModal(); openAutomation(); }],
    ['Create', 'btn primary', async () => {
      const payload = {
        projectId: p.id,
        name: $('#sName').value.trim(),
        prompt: $('#sPrompt').value.trim(),
        agentId: $('#sAgent').value,
        every: $('#sEvery').value,
        enabled: true,
        n: intVal('#sN', 15), minute: intVal('#sMin', 0),
        hour: intVal('#sHour', 8), wday: intVal('#sWday', 1), mday: intVal('#sMday', 1),
      };
      if (!payload.name || !payload.prompt) return toast('Name and prompt are both needed', 'bad');
      closeModal();
      await tryApi('/schedules', { method: 'POST', body: payload });
      openAutomation();
    }],
  ]);
  syncSchedFields();
}

function intVal(sel, dflt) {
  const e = $(sel);
  if (!e) return dflt;
  const n = parseInt(e.value, 10);
  return isNaN(n) ? dflt : n;
}

// syncSchedFields shows only the inputs the chosen cadence actually uses, so a
// weekly schedule does not ask for a day of the month.
function syncSchedFields() {
  const host = $('#schedFields');
  if (!host) return;
  const every = $('#sEvery').value;
  host.innerHTML = '';
  const num = (id, label, val, min, max) => el('div', { style: 'flex:1' },
    el('label', { text: label, style: 'margin-top:0' }),
    el('input', { type: 'number', id, value: String(val), min: String(min), max: String(max) }));

  if (every === 'minutes') host.append(num('sN', 'every N minutes', 15, 1, 1440));
  if (every === 'hourly') host.append(num('sMin', 'at minute', 0, 0, 59));
  if (every === 'daily' || every === 'weekly' || every === 'monthly') {
    host.append(num('sHour', 'hour', 8, 0, 23), num('sMin', 'minute', 0, 0, 59));
  }
  if (every === 'weekly') {
    host.append(el('div', { style: 'flex:1' },
      el('label', { text: 'weekday', style: 'margin-top:0' }),
      el('select', { id: 'sWday' },
        ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']
          .map((d, i) => el('option', { value: String(i), selected: i === 1 ? 'selected' : null }, d)))));
  }
  if (every === 'monthly') host.append(num('sMday', 'day of month', 1, 1, 31));
}

function newWebhook() {
  const p = projectById(S.selectedProject);
  if (!p) return toast('Pick a project first', 'bad');
  const body = el('div', {},
    el('label', { text: 'Name' }),
    el('input', { type: 'text', id: 'wName', placeholder: 'New pull requests' }),
    el('label', { text: 'What the agent should do' }),
    el('textarea', {
      id: 'wPrompt', rows: '4',
      value: 'Review pull request #{{event.number}}: {{event.pull_request.title}}',
    }),
    el('div', { class: 'hint', text: 'Reference any field of the payload with {{event.path.to.field}}. A name that does not exist stays visible in the prompt rather than going blank, so a typo is obvious immediately.' }),
    el('label', { text: 'Agent' }),
    el('select', { id: 'wAgent' },
      el('option', { value: '' }, '— any agent on this project —'),
      agentsOf(p.id).map(a => el('option', { value: a.id }, a.name))),
    el('label', { text: 'Only run when' }),
    el('input', { type: 'text', id: 'wFilter', value: 'event.action == "opened"' }),
    el('div', { class: 'hint', text: 'One comparison, optionally joined with && or ||. Supports ==, !=, contains, startswith, endswith and exists. Leave empty to run on every delivery.' }),
    el('label', { text: 'Most runs per hour' }),
    el('input', { type: 'number', id: 'wBurst', value: '10', min: '1', max: '200' }));

  modal('New webhook trigger', body, [
    ['Cancel', 'btn', () => { closeModal(); openAutomation(); }],
    ['Create', 'btn primary', async () => {
      const payload = {
        projectId: p.id,
        name: $('#wName').value.trim(),
        prompt: $('#wPrompt').value.trim(),
        agentId: $('#wAgent').value,
        filter: $('#wFilter').value.trim(),
        burstLimit: intVal('#wBurst', 10),
        enabled: true,
      };
      if (!payload.name || !payload.prompt) return toast('Name and prompt are both needed', 'bad');
      closeModal();
      await tryApi('/webhooks', { method: 'POST', body: payload });
      openAutomation();
    }],
  ], true);
}

// ---------------------------------------------------------------- environment

async function openEnvironment() {
  const [sec, dbs, hosts] = await Promise.all([
    tryApi('/secrets'), tryApi('/dbconns'), tryApi('/sshhosts'),
  ]);
  const body = el('div', {});
  body.append(el('div', { class: 'tabs' },
    el('div', { class: 'tab active', id: 'evS', onclick: () => swapEnv('s') }, 'Secrets'),
    el('div', { class: 'tab', id: 'evD', onclick: () => swapEnv('d') }, 'Databases'),
    el('div', { class: 'tab', id: 'evH', onclick: () => swapEnv('h') }, 'SSH hosts')));

  // --- secrets ---
  const sPane = el('div', { id: 'envS' });
  if (!sec.available) {
    sPane.append(el('div', { class: 'pill bad', style: 'display:block;padding:10px' }, sec.reason));
  } else {
    sPane.append(el('p', { class: 'hint', style: 'margin-top:0' },
      'Encrypted through your operating system, bound to this user account. Reference an entry ' +
      'as {{secret:NAME}} in a command or a connection and it is resolved at launch, in this ' +
      'process. There is deliberately no way to read a value back — not through the interface, ' +
      'not through the API, and not through an agent.'));
    for (const x of sec.secrets) {
      sPane.append(el('div', { class: 'acct-row' },
        el('div', { class: 'info' },
          el('div', {}, el('strong', { class: 'mono', text: x.name })),
          x.note ? el('div', { class: 'dirpath', text: x.note }) : null,
          el('div', { class: 'hint', text: '•••••••••••• stored' })),
        el('button', {
          class: 'btn danger sm', onclick: async () => {
            if (!confirm(`Delete the secret ${x.name}?`)) return;
            await tryApi(`/secrets/${encodeURIComponent(x.name)}`, { method: 'DELETE' });
            closeModal(); openEnvironment();
          },
        }, '🗑')));
    }
    if (!sec.secrets.length) sPane.append(el('div', { class: 'hint', style: 'padding:12px', text: 'The vault is empty.' }));
    sPane.append(el('button', { class: 'btn primary', style: 'margin-top:12px', onclick: newSecret }, '+ Add a secret'));
  }

  // --- databases ---
  const dPane = el('div', { id: 'envD', style: 'display:none' });
  dPane.append(el('p', { class: 'hint', style: 'margin-top:0' },
    'Read-only unless you say otherwise. Agents query by naming a connection, so the password ' +
    'never reaches a prompt or a transcript. One statement per call, stacked statements refused, ' +
    'results capped.'));
  for (const c of dbs) {
    dPane.append(el('div', { class: 'acct-row' },
      el('div', { class: 'info' },
        el('div', {}, el('strong', { text: c.name })),
        el('div', { class: 'dirpath', text: `${c.driver}://${c.user}@${c.host}:${c.port}/${c.dbName}` }),
        el('div', { style: 'margin-top:5px;display:flex;gap:6px;flex-wrap:wrap' },
          c.writable ? el('span', { class: 'pill warn' }, 'writable') : el('span', { class: 'pill ok' }, 'read-only'),
          c.production ? el('span', { class: 'pill bad' }, 'production') : null,
          c.tunnelHostId ? el('span', { class: 'pill' }, 'via ssh') : null,
          c.secretRef ? el('span', { class: 'pill mono' }, c.secretRef) : null)),
      el('div', { style: 'display:flex;gap:5px' },
        el('button', { class: 'btn sm', onclick: () => openQuery(c) }, 'Query'),
        el('button', {
          class: 'btn danger sm', onclick: async () => {
            await tryApi(`/dbconns/${c.id}`, { method: 'DELETE' });
            closeModal(); openEnvironment();
          },
        }, '🗑'))));
  }
  if (!dbs.length) dPane.append(el('div', { class: 'hint', style: 'padding:12px', text: 'No connections saved.' }));
  dPane.append(el('button', { class: 'btn primary', style: 'margin-top:12px', onclick: () => newDBConn(sec) }, '+ Add a connection'));

  // --- ssh ---
  const hPane = el('div', { id: 'envH', style: 'display:none' });
  hPane.append(el('p', { class: 'hint', style: 'margin-top:0' },
    'Save a server once and open a terminal on it in one click, or tunnel a private database ' +
    'through it. Terminals use your own ssh client, so your config, agent and jump hosts all ' +
    'apply. A host key is pinned the first time it is seen and a later change is refused.'));
  for (const h of hosts) {
    hPane.append(el('div', { class: 'acct-row' },
      el('div', { class: 'info' },
        el('div', {}, el('strong', { text: h.name })),
        el('div', { class: 'dirpath', text: `${h.user}@${h.host}:${h.port}` }),
        el('div', { style: 'margin-top:5px;display:flex;gap:6px' },
          h.keyPath ? el('span', { class: 'pill mono' }, 'key') : null,
          h.secretRef ? el('span', { class: 'pill mono' }, h.secretRef) : null)),
      el('div', { style: 'display:flex;gap:5px' },
        el('button', {
          class: 'btn sm', onclick: async () => {
            const r = await tryApi(`/sshhosts/${h.id}/test`, { method: 'POST' });
            toast(r.output || r.status, 'ok');
          },
        }, 'Test'),
        el('button', {
          class: 'btn sm primary', onclick: async () => {
            const sess = await tryApi(`/sshhosts/${h.id}/shell`, { method: 'POST', body: { cols: 120, rows: 32 } });
            closeModal();
            patchSession(sess);
            openTerm(sess.id);
          },
        }, 'Shell'),
        el('button', {
          class: 'btn danger sm', onclick: async () => {
            await tryApi(`/sshhosts/${h.id}`, { method: 'DELETE' });
            closeModal(); openEnvironment();
          },
        }, '🗑'))));
  }
  if (!hosts.length) hPane.append(el('div', { class: 'hint', style: 'padding:12px', text: 'No hosts saved.' }));
  hPane.append(el('button', { class: 'btn primary', style: 'margin-top:12px', onclick: () => newSSHHost(sec) }, '+ Add a host'));

  body.append(sPane, dPane, hPane);
  modal('Environment', body, [['Close', 'btn', closeModal]], true);
}

function swapEnv(which) {
  for (const [id, key] of [['evS', 's'], ['evD', 'd'], ['evH', 'h']]) {
    $('#' + id).classList.toggle('active', which === key);
  }
  $('#envS').style.display = which === 's' ? '' : 'none';
  $('#envD').style.display = which === 'd' ? '' : 'none';
  $('#envH').style.display = which === 'h' ? '' : 'none';
}

function openSSH() { openEnvironment(); setTimeout(() => swapEnv('h'), 0); }

function newSecret() {
  const body = el('div', {},
    el('label', { text: 'Name' }),
    el('input', { type: 'text', id: 'seName', placeholder: 'STRIPE_SECRET_KEY' }),
    el('label', { text: 'Value' }),
    el('input', { type: 'password', id: 'seVal', autocomplete: 'new-password' }),
    el('label', { text: 'Note' }),
    el('input', { type: 'text', id: 'seNote', placeholder: 'what this is for' }),
    el('div', { class: 'hint', text: 'The value is encrypted immediately and cannot be read back through this interface. Only the name is ever returned.' }));
  modal('Add a secret', body, [
    ['Cancel', 'btn', () => { closeModal(); openEnvironment(); }],
    ['Store', 'btn primary', async () => {
      const payload = {
        name: $('#seName').value.trim(),
        value: $('#seVal').value,
        note: $('#seNote').value.trim(),
      };
      if (!payload.name || !payload.value) return toast('Name and value are both needed', 'bad');
      closeModal();
      await tryApi('/secrets', { method: 'PUT', body: payload });
      toast('Stored', 'ok');
      openEnvironment();
    }],
  ]);
}

function newDBConn(sec) {
  const names = (sec.secrets || []).map(s => s.name);
  const body = el('div', {},
    el('label', { text: 'Name' }),
    el('input', { type: 'text', id: 'dbName', placeholder: 'staging' }),
    el('label', { text: 'Driver' }),
    el('select', { id: 'dbDriver' },
      el('option', { value: 'postgres' }, 'PostgreSQL'),
      el('option', { value: 'mysql' }, 'MySQL'),
      el('option', { value: 'mongodb' }, 'MongoDB (saved and tunnelled; queries not implemented yet)')),
    el('div', { style: 'display:flex;gap:8px' },
      el('div', { style: 'flex:2' }, el('label', { text: 'Host' }), el('input', { type: 'text', id: 'dbHost' })),
      el('div', { style: 'flex:1' }, el('label', { text: 'Port' }), el('input', { type: 'number', id: 'dbPort', value: '5432' }))),
    el('div', { style: 'display:flex;gap:8px' },
      el('div', { style: 'flex:1' }, el('label', { text: 'User' }), el('input', { type: 'text', id: 'dbUser' })),
      el('div', { style: 'flex:1' }, el('label', { text: 'Database' }), el('input', { type: 'text', id: 'dbDb' }))),
    el('label', { text: 'Password, from the vault' }),
    el('select', { id: 'dbSecret' },
      el('option', { value: '' }, '— none —'),
      names.map(n => el('option', { value: n }, n))),
    el('div', { class: 'hint', text: 'Add the password as a secret first; connections store only its name.' }),
    el('label', { text: 'Reach it through an SSH host' }),
    el('select', { id: 'dbTunnel' },
      el('option', { value: '' }, '— direct —'),
      S.hosts.map(h => el('option', { value: h.id }, h.name))),
    el('label', { class: 'switch', style: 'margin-top:12px' },
      el('input', { type: 'checkbox', id: 'dbTLS' }), el('span', { text: 'Require TLS' })),
    el('label', { class: 'switch' },
      el('input', { type: 'checkbox', id: 'dbWrite' }), el('span', { text: 'Allow writes (still needs a confirmation each time)' })),
    el('label', { class: 'switch' },
      el('input', { type: 'checkbox', id: 'dbProd' }), el('span', { text: 'This is production' })));

  modal('Add a database connection', body, [
    ['Cancel', 'btn', () => { closeModal(); openEnvironment(); }],
    ['Save', 'btn primary', async () => {
      const payload = {
        name: $('#dbName').value.trim(),
        driver: $('#dbDriver').value,
        host: $('#dbHost').value.trim(),
        port: intVal('#dbPort', 5432),
        user: $('#dbUser').value.trim(),
        dbName: $('#dbDb').value.trim(),
        secretRef: $('#dbSecret').value,
        tunnelHostId: $('#dbTunnel').value,
        tls: $('#dbTLS').checked,
        writable: $('#dbWrite').checked,
        production: $('#dbProd').checked,
      };
      if (!payload.name || !payload.host) return toast('Name and host are both needed', 'bad');
      closeModal();
      await tryApi('/dbconns', { method: 'POST', body: payload });
      openEnvironment();
    }],
  ], true);
}

function openQuery(c) {
  const body = el('div', {},
    el('div', { class: 'hint', style: 'margin-top:0' },
      `${c.name} — ${c.writable ? 'writable' : 'read-only'}${c.production ? ', production' : ''}`),
    el('label', { text: 'One statement' }),
    el('textarea', { id: 'qSQL', rows: '4', value: 'SELECT 1' }),
    el('div', { id: 'qOut' }));
  modal('Query ' + c.name, body, [
    ['Close', 'btn', closeModal],
    ['Run', 'btn primary', async () => {
      const out = $('#qOut');
      out.innerHTML = '';
      out.append(el('div', { class: 'hint', text: 'Running…' }));
      try {
        const r = await api(`/dbconns/${c.id}/query`, {
          method: 'POST', body: { sql: $('#qSQL').value, confirm: false },
        });
        out.innerHTML = '';
        out.append(el('pre', { class: 'mono wrap', style: 'font-size:11px', text: r.table }));
      } catch (e) {
        out.innerHTML = '';
        out.append(el('div', { class: 'pill bad', style: 'display:block;padding:8px' }, e.message));
      }
    }],
  ], true);
}

function newSSHHost(sec) {
  const names = (sec.secrets || []).map(s => s.name);
  const body = el('div', {},
    el('label', { text: 'Name' }),
    el('input', { type: 'text', id: 'shName', placeholder: 'prod-vps' }),
    el('div', { style: 'display:flex;gap:8px' },
      el('div', { style: 'flex:2' }, el('label', { text: 'Host' }), el('input', { type: 'text', id: 'shHost' })),
      el('div', { style: 'flex:1' }, el('label', { text: 'Port' }), el('input', { type: 'number', id: 'shPort', value: '22' }))),
    el('label', { text: 'User' }),
    el('input', { type: 'text', id: 'shUser', placeholder: 'deploy' }),
    el('label', { text: 'Private key' }),
    el('input', { type: 'text', id: 'shKey', placeholder: '~/.ssh/id_ed25519' }),
    el('label', { text: 'Password or passphrase, from the vault' }),
    el('select', { id: 'shSecret' },
      el('option', { value: '' }, '— none —'),
      names.map(n => el('option', { value: n }, n))));
  modal('Add an SSH host', body, [
    ['Cancel', 'btn', () => { closeModal(); openEnvironment(); }],
    ['Save', 'btn primary', async () => {
      const payload = {
        name: $('#shName').value.trim(),
        host: $('#shHost').value.trim(),
        port: intVal('#shPort', 22),
        user: $('#shUser').value.trim(),
        keyPath: $('#shKey').value.trim(),
        secretRef: $('#shSecret').value,
      };
      if (!payload.name || !payload.host) return toast('Name and host are both needed', 'bad');
      closeModal();
      await tryApi('/sshhosts', { method: 'POST', body: payload });
      openEnvironment();
    }],
  ]);
}

// ---------------------------------------------------------------- roles

async function openRoles() {
  const roles = await tryApi('/roles');
  S.roles = roles;
  const body = el('div', {});

  body.append(el('div', { style: 'display:flex;gap:8px;margin-bottom:12px' },
    el('input', { type: 'text', id: 'roleTask', placeholder: 'describe a task and get the right role…', style: 'flex:1' }),
    el('button', {
      class: 'btn', onclick: async () => {
        const task = $('#roleTask').value.trim();
        if (!task) return;
        const btn = event.target;
        btn.disabled = true; btn.textContent = 'thinking…';
        try {
          const r = await api('/roles/suggest', { method: 'POST', body: { task } });
          const out = $('#roleSuggest');
          out.innerHTML = '';
          for (const s of r.suggestions) {
            out.append(el('div', { class: 'comment' },
              el('div', {}, el('strong', { text: s.role }),
                s.model ? el('span', { class: 'pill' }, s.model) : null),
              el('div', { class: 'hint', text: s.reason })));
          }
        } catch (e) { toast(e.message, 'bad'); }
        btn.disabled = false; btn.textContent = 'Suggest';
      },
    }, 'Suggest')),
    el('div', { id: 'roleSuggest' }));

  for (const r of roles) {
    body.append(el('div', { class: 'acct-row' },
      el('div', { class: 'avatar', style: `background:${r.color};width:28px;height:28px;font-size:11px` },
        initials(r.name)),
      el('div', { class: 'info' },
        el('div', {}, el('strong', { text: r.name }),
          r.builtin ? null : el('span', { class: 'src-badge', text: '  · imported' })),
        el('div', { class: 'dirpath', text: r.tagline || '' }),
        el('div', { style: 'margin-top:5px;display:flex;gap:5px;flex-wrap:wrap' },
          r.model ? el('span', { class: 'pill' }, r.model) : null,
          (r.skills || []).slice(0, 4).map(s => el('span', { class: 'pill' }, s)))),
      el('div', { style: 'display:flex;gap:5px' },
        el('button', {
          class: 'btn sm', onclick: () => modal(r.name,
            el('pre', { class: 'wrap', style: 'font-size:12px', text: r.prompt }),
            [['Back', 'btn', () => { closeModal(); openRoles(); }]], true),
        }, 'Prompt'),
        r.builtin ? null : el('button', {
          class: 'btn danger sm', onclick: async () => {
            await tryApi(`/roles/${r.id}`, { method: 'DELETE' });
            closeModal(); openRoles();
          },
        }, '🗑'))));
  }

  modal(`Roles (${roles.length})`, body, [
    ['Close', 'btn', closeModal],
    ['Import a role', 'btn primary', importRole],
  ], true);
}

function importRole() {
  const body = el('div', {},
    el('p', { class: 'hint', style: 'margin-top:0' },
      'Paste a Claude Code subagent file — frontmatter plus the prompt — or the JSON form. ' +
      'That is the same format the large open agent catalogues publish, so importing one takes no converter.'),
    el('label', { text: 'Filename' }),
    el('input', { type: 'text', id: 'riName', placeholder: 'rust-expert.md' }),
    el('label', { text: 'Contents' }),
    el('textarea', {
      id: 'riBody', rows: '14',
      placeholder: '---\nname: Rust Expert\ndescription: Ownership and lifetimes\nmodel: sonnet\n---\n\nYou are a Rust specialist…',
    }));
  modal('Import a role', body, [
    ['Cancel', 'btn', () => { closeModal(); openRoles(); }],
    ['Import', 'btn primary', async () => {
      const payload = { filename: $('#riName').value.trim(), body: $('#riBody').value };
      if (!payload.filename || !payload.body.trim()) return toast('Both fields are needed', 'bad');
      closeModal();
      const r = await tryApi('/roles/import', { method: 'POST', body: payload });
      toast('Imported ' + r.name, 'ok');
      openRoles();
    }],
  ], true);
}

// ---------------------------------------------------------------- process guard

async function openGuard() {
  const rep = await tryApi('/guard');
  const body = el('div', {});

  body.append(el('p', { class: 'hint', style: 'margin-top:0' },
    'Every tool call an agent makes spawns a real process. Most finish in seconds; some never ' +
    'do, and those hold memory at zero percent CPU until the machine starts swapping. A process ' +
    'is only flagged when it is large, old AND idle at once, so a real build is never mistaken ' +
    'for a stuck one. Nothing is ended unless you ask.'));

  if (!rep.supported) {
    body.append(el('div', { class: 'pill bad', style: 'display:block;padding:10px' },
      rep.unsupported || 'the process table could not be read on this machine'));
  } else if (!(rep.procs || []).length) {
    body.append(el('div', { class: 'hint', style: 'padding:12px', text: 'No child processes right now.' }));
  } else {
    if (rep.suspects) {
      body.append(el('div', { class: 'pill bad', style: 'display:block;padding:10px;margin-bottom:10px' },
        `${rep.suspects} stuck process${rep.suspects > 1 ? 'es' : ''} holding ${fmtBytes(rep.heldBytes)}`));
    }
    for (const p of rep.procs) {
      body.append(el('div', { class: 'acct-row' },
        el('div', { class: 'info' },
          el('div', {},
            el('strong', { class: 'mono', text: p.name }),
            el('span', { class: 'dim mono', text: '  pid ' + p.pid })),
          p.cmdLine ? el('div', { class: 'dirpath', text: p.cmdLine }) : null,
          el('div', { style: 'margin-top:5px;display:flex;gap:6px;flex-wrap:wrap' },
            el('span', { class: 'pill mono' }, fmtBytes(p.memoryBytes)),
            el('span', { class: 'pill mono' }, p.cpuPct.toFixed(1) + '% cpu'),
            el('span', { class: 'pill mono' }, fmtDur(p.ageSecs)),
            p.agentName ? el('span', { class: 'pill' }, p.agentName) : null,
            p.orphaned ? el('span', { class: 'pill warn' }, 'orphaned') : null),
          p.suspect ? el('div', { class: 'hint', style: 'color:#ffb4b4', text: p.reason }) : null),
        p.suspect ? el('button', {
          class: 'btn danger sm', onclick: async () => {
            await tryApi('/guard/kill', { method: 'POST', body: { pid: p.pid } });
            toast('Ended, memory returned', 'ok');
            closeModal(); openGuard();
          },
        }, 'End it') : null));
    }
  }
  modal('Process guard', body, [['Close', 'btn', closeModal], ['Rescan', 'btn', () => { closeModal(); openGuard(); }]], true);
}

function fmtBytes(b) {
  if (!b) return '0 B';
  const u = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0, v = b;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return v.toFixed(v < 10 && i > 0 ? 1 : 0) + ' ' + u[i];
}

// ---------------------------------------------------------------- voice

/* Voice runs entirely in the browser, on the Web Speech API that is already
 * there. That is deliberately different from the tools we are competing with,
 * which route audio through their own backend and meter it by the hour: this
 * costs nothing, works offline for synthesis, and no audio leaves the machine.
 * The trade-off is honest — recognition quality is the browser's, not a
 * dedicated model's — and Chrome does send audio to Google for transcription,
 * which is worth knowing rather than hiding.
 */
const Voice = {
  rec: null,
  listening: false,

  supported() {
    return !!(window.SpeechRecognition || window.webkitSpeechRecognition);
  },
  speakSupported() { return 'speechSynthesis' in window; },

  dictate(onText) {
    if (!this.supported()) return toast('This browser has no speech recognition', 'bad');
    if (this.listening) return this.stop();
    const R = window.SpeechRecognition || window.webkitSpeechRecognition;
    const rec = new R();
    rec.continuous = true;
    rec.interimResults = true;
    rec.lang = navigator.language || 'en-US';
    let finalText = '';
    rec.onresult = e => {
      let interim = '';
      for (let i = e.resultIndex; i < e.results.length; i++) {
        const t = e.results[i][0].transcript;
        if (e.results[i].isFinal) finalText += t;
        else interim += t;
      }
      onText(finalText + interim, false);
    };
    rec.onend = () => {
      this.listening = false;
      onText(finalText, true);
      renderVoiceButton();
    };
    rec.onerror = ev => {
      this.listening = false;
      renderVoiceButton();
      if (ev.error !== 'aborted') toast('Dictation error: ' + ev.error, 'bad');
    };
    this.rec = rec;
    this.listening = true;
    rec.start();
    renderVoiceButton();
  },

  stop() {
    if (this.rec) { try { this.rec.stop(); } catch {} }
    this.listening = false;
    renderVoiceButton();
  },

  speak(text, done) {
    if (!this.speakSupported()) { toast('This browser cannot speak', 'bad'); return; }
    speechSynthesis.cancel();
    // Strip what does not read aloud well: code fences, tables, bare paths.
    const clean = String(text)
      .replace(/```[\s\S]*?```/g, ' (code omitted) ')
      .replace(/`[^`]*`/g, ' ')
      .replace(/^\s*\|.*\|\s*$/gm, ' ')
      .replace(/[A-Za-z]:\\[^\s]+/g, ' a file path ')
      .replace(/\/[\w./-]{8,}/g, ' a file path ')
      .replace(/[*_#>]/g, ' ')
      .replace(/\s+/g, ' ')
      .trim();
    if (!clean) { toast('Nothing readable to speak'); return; }
    const u = new SpeechSynthesisUtterance(clean);
    u.lang = navigator.language || 'en-US';
    u.onend = () => { S.speaking = false; if (done) done(); renderVoiceButton(); };
    S.speaking = true;
    speechSynthesis.speak(u);
    renderVoiceButton();
  },

  hush() {
    if (this.speakSupported()) speechSynthesis.cancel();
    S.speaking = false;
    renderVoiceButton();
  },
};

function renderVoiceButton() {
  const b = $('#voiceBtn');
  if (!b) return;
  b.classList.toggle('primary', Voice.listening);
  b.textContent = Voice.listening ? '● listening' : '🎤';
  const h = $('#hushBtn');
  if (h) h.style.display = S.speaking ? '' : 'none';
}

// readAloud speaks a session's recent output, optionally condensing it first so
// a long changelog is answerable in ten seconds rather than forty minutes.
async function readAloud(sessionId, condense) {
  try {
    const res = await fetch(`/api/sessions/${sessionId}/scrollback`);
    let text = await res.text();
    // Strip ANSI so the escape codes are not read out as gibberish.
    text = text.replace(/\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07]*\x07/g, '');
    text = text.split('\n').slice(-120).join('\n');
    if (!text.trim()) return toast('Nothing on screen to read');
    if (condense) {
      toast('Writing a summary first…');
      const r = await api('/ai/summarise', { method: 'POST', body: { text, seconds: 30 } });
      Voice.speak(r.summary);
      return;
    }
    Voice.speak(text);
  } catch (e) { toast(e.message, 'bad'); }
}
