/* Go AI Team — the views beyond the agent grid.
 *
 * Each view is a function that renders into the main pane. They share the state
 * and helpers from app.js rather than owning any of their own, so a change that
 * arrives over the event socket repaints whichever view happens to be open
 * without each one needing its own subscription.
 */
'use strict';

// ---------------------------------------------------------------- board

async function viewBoard(main) {
  const p = projectById(S.selectedProject);
  if (!p) return main.append(el('div', { class: 'empty', text: 'Pick a project first.' }));

  const tasks = await tryApi('/tasks?projectId=' + encodeURIComponent(p.id));
  S.tasks = tasks || [];

  main.append(viewHeader(p, 'Board',
    el('button', { class: 'btn sm primary', onclick: () => newTask(p) }, '+ Task'),
    el('button', { class: 'btn sm', onclick: () => openRadar() }, 'Idea Radar')));

  const cols = [
    ['backlog', 'Backlog'], ['todo', 'To do'], ['in_progress', 'In progress'],
    ['review', 'Review'], ['done', 'Done'],
  ];
  const board = el('div', { class: 'board' });

  for (const [key, label] of cols) {
    const items = S.tasks.filter(t => t.status === key);
    const col = el('div', {
      class: 'board-col', 'data-status': key,
      ondragover: e => { e.preventDefault(); col.classList.add('drop'); },
      ondragleave: () => col.classList.remove('drop'),
      ondrop: async e => {
        e.preventDefault();
        col.classList.remove('drop');
        const id = e.dataTransfer.getData('text/plain');
        if (!id) return;
        await moveTask(id, key);
      },
    });
    col.append(el('div', { class: 'board-head' },
      el('span', { text: label }),
      el('span', { class: 'count', text: String(items.length) })));

    // in_progress is the column that starts an agent, so say so where the
    // user is about to drop something.
    if (key === 'in_progress') {
      col.append(el('div', { class: 'hint', style: 'padding:0 4px 6px' },
        'Dropping a task here starts an agent on it.'));
    }
    for (const t of items) col.append(taskCard(t));
    if (!items.length) col.append(el('div', { class: 'board-empty', text: '—' }));
    board.append(col);
  }
  main.append(board);
}

function taskCard(t) {
  const agent = agentById(t.agentId);
  const sess = t.sessionId ? sessionById(t.sessionId) : null;
  const card = el('div', {
    class: 'task' + (t.priority > 0 ? ' hot' : ''),
    draggable: 'true',
    ondragstart: e => e.dataTransfer.setData('text/plain', t.id),
    onclick: () => openTask(t),
  },
    el('div', { class: 'task-title', text: t.title }),
    el('div', { class: 'task-meta' },
      t.source && t.source !== 'manual'
        ? el('span', { class: 'pill', title: 'where this came from' }, t.source) : null,
      agent ? el('span', { class: 'pill' },
        el('span', { class: 'acct-dot', style: `background:${agent.color || '#64748b'}` }), agent.name) : null,
      sess ? el('span', { class: 'pill' + (sess.status === 'waiting' ? ' bad' : '') },
        el('span', { class: 'dot ' + sess.status }), sess.status) : null,
      t.votes ? el('span', { class: 'pill' }, `▲ ${t.votes}`) : null,
      (t.comments || []).length ? el('span', { class: 'pill' }, `💬 ${t.comments.length}`) : null),
    (t.labels || []).length
      ? el('div', { class: 'task-meta' }, t.labels.map(l => el('span', { class: 'pill' }, l)))
      : null);
  return card;
}

async function moveTask(id, status) {
  try {
    const r = await api(`/tasks/${id}`, { method: 'PATCH', body: { status } });
    if (r.sessionId) {
      toast('Agent started on this task', 'ok');
      await loadAll();
      openTerm(r.sessionId);
      return;
    }
    render();
  } catch (e) { toast(e.message, 'bad'); }
}

function newTask(p) {
  const body = el('div', {},
    el('label', { text: 'Title' }),
    el('input', { type: 'text', id: 'tTitle', placeholder: 'Fix the login redirect loop' }),
    el('label', { text: 'Detail' }),
    el('textarea', { id: 'tBody', rows: '6', placeholder: 'What the agent needs to know.' }),
    el('label', { text: 'Assign to' }),
    el('select', { id: 'tAgent' },
      el('option', { value: '' }, '— any agent on this project —'),
      agentsOf(p.id).map(a => el('option', { value: a.id }, a.name))),
    el('label', { text: 'Start in' }),
    el('select', { id: 'tStatus' },
      el('option', { value: 'backlog' }, 'Backlog'),
      el('option', { value: 'todo' }, 'To do'),
      el('option', { value: 'in_progress' }, 'In progress — starts an agent now')));
  modal('New task', body, [
    ['Cancel', 'btn', closeModal],
    ['Create', 'btn primary', async () => {
      const payload = {
        projectId: p.id,
        title: $('#tTitle').value.trim(),
        body: $('#tBody').value,
        agentId: $('#tAgent').value,
        status: $('#tStatus').value,
      };
      if (!payload.title) return toast('Give it a title', 'bad');
      closeModal();
      const t = await tryApi('/tasks', { method: 'POST', body: payload });
      if (payload.status === 'in_progress') {
        const r = await tryApi(`/tasks/${t.id}/run`, { method: 'POST' });
        await loadAll();
        if (r.sessionId) return openTerm(r.sessionId);
      }
      render();
    }],
  ]);
}

function openTask(t) {
  const body = el('div', {},
    el('h3', { style: 'margin:0 0 6px', text: t.title }),
    el('div', { class: 'hint', text: `${t.status} · ${t.source || 'manual'}${t.reporter ? ' · from ' + t.reporter : ''}` }),
    t.body ? el('pre', { class: 'mono wrap', text: t.body }) : null);

  if ((t.comments || []).length) {
    body.append(el('label', { text: 'Thread' }));
    for (const c of t.comments) {
      body.append(el('div', { class: 'comment' },
        el('div', { class: 'comment-head' },
          el('strong', { text: c.author }),
          c.isAgent ? el('span', { class: 'pill' }, 'agent') : null,
          el('span', { class: 'dim', text: new Date(c.createdAt).toLocaleString() })),
        el('pre', { class: 'wrap', text: c.body })));
    }
  }

  body.append(el('label', { text: 'Add a comment' }),
    el('textarea', { id: 'tComment', rows: '3', placeholder: 'Clarify what you meant…' }));

  modal('Task', body, [
    ['Delete', 'btn danger', async () => {
      if (!confirm('Delete this task?')) return;
      closeModal();
      await tryApi(`/tasks/${t.id}`, { method: 'DELETE' });
      render();
    }],
    ['Scope it', 'btn', async () => {
      const r = await tryApi(`/tasks/${t.id}/scope`, { method: 'POST' });
      toast(r.status, 'ok');
    }],
    ['Comment', 'btn', async () => {
      const c = $('#tComment').value.trim();
      if (!c) return closeModal();
      closeModal();
      await tryApi(`/tasks/${t.id}/comment`, { method: 'POST', body: { body: c } });
      render();
    }],
    ['Run now', 'btn primary', async () => {
      closeModal();
      const r = await tryApi(`/tasks/${t.id}/run`, { method: 'POST' });
      await loadAll();
      if (r.sessionId) openTerm(r.sessionId);
    }],
  ]);
}

// ---------------------------------------------------------------- idea radar

async function openRadar() {
  const p = projectById(S.selectedProject);
  if (!p) return;
  const ideas = await tryApi('/ideas?projectId=' + encodeURIComponent(p.id));
  const body = el('div', { id: 'radarBody' });

  const paint = () => {
    body.innerHTML = '';
    body.append(el('p', { class: 'hint', style: 'margin-top:0' },
      'Throw raw feedback in — support messages, half-formed notes, bug reports. ' +
      'Sorting groups them by theme, merges duplicates and rates impact against effort, ' +
      'running on your own signed-in account rather than a metered service.'));

    body.append(el('label', { text: 'Add feedback' }),
      el('textarea', { id: 'ideaBody', rows: '3', placeholder: 'cannot find my old exports' }),
      el('div', { style: 'display:flex;gap:8px;margin-top:8px' },
        el('input', { type: 'text', id: 'ideaFrom', placeholder: 'who said it (optional)' }),
        el('button', {
          class: 'btn', onclick: async () => {
            const b = $('#ideaBody').value.trim();
            if (!b) return;
            await tryApi('/ideas', {
              method: 'POST',
              body: { projectId: p.id, body: b, reporter: $('#ideaFrom').value, source: 'manual' },
            });
            closeModal(); openRadar();
          },
        }, 'Add')));

    const themes = {};
    for (const i of ideas) (themes[i.theme || 'unsorted'] ||= []).push(i);

    for (const [theme, list] of Object.entries(themes)) {
      const first = list[0];
      body.append(el('div', { style: 'margin-top:14px' },
        el('div', { style: 'display:flex;align-items:center;gap:8px' },
          el('strong', { text: theme }),
          el('span', { class: 'pill' }, `${list.length} ${list.length === 1 ? 'person' : 'people'}`),
          theme !== 'unsorted' && first.impact
            ? el('span', { class: 'pill' }, `impact ${first.impact} · effort ${first.effort}`) : null,
          el('span', { style: 'flex:1' }),
          theme !== 'unsorted' && !first.promoted
            ? el('button', {
                class: 'btn sm primary', onclick: async () => {
                  closeModal();
                  await tryApi(`/ideas/${first.id}/promote`, { method: 'POST' });
                  toast('Promoted to a ticket', 'ok');
                  render();
                },
              }, 'Make a ticket') : null),
        list.map(i => el('div', { class: 'comment' },
          el('pre', { class: 'wrap', text: i.body }),
          el('div', { class: 'comment-head' },
            i.reporter ? el('span', { class: 'dim', text: i.reporter }) : null,
            i.promoted ? el('span', { class: 'pill ok' }, 'ticketed') : null,
            el('button', {
              class: 'btn ghost sm', onclick: async () => {
                await tryApi(`/ideas/${i.id}`, { method: 'DELETE' });
                closeModal(); openRadar();
              },
            }, '✕'))))));
    }
    if (!ideas.length) body.append(el('div', { class: 'hint', style: 'padding:12px', text: 'Nothing yet.' }));
  };
  paint();

  modal('Idea Radar', body, [
    ['Close', 'btn', closeModal],
    ['Sort into themes', 'btn primary', async () => {
      toast('Sorting… this reads every note, so it takes a moment');
      try {
        const r = await api('/ideas/cluster?projectId=' + encodeURIComponent(p.id), { method: 'POST' });
        closeModal();
        toast(`Found ${r.clusters.length} themes`, 'ok');
        openRadar();
      } catch (e) { toast(e.message, 'bad'); }
    }],
  ], true);
}

// ---------------------------------------------------------------- review

async function viewReview(main) {
  const p = projectById(S.selectedProject);
  if (!p) return main.append(el('div', { class: 'empty', text: 'Pick a project first.' }));

  const st = await tryApi('/git/status?projectId=' + encodeURIComponent(p.id));

  main.append(viewHeader(p, 'Review',
    st.isRepo ? el('span', { class: 'pill' }, st.branch || 'detached') : null,
    st.isRepo && (st.ahead || st.behind)
      ? el('span', { class: 'pill' }, `↑${st.ahead} ↓${st.behind}`) : null));

  if (!st.isRepo) {
    return main.append(el('div', { class: 'empty' },
      el('h3', { text: 'Not a git repository' }),
      el('p', { text: 'Review shows what your agents changed, file by file, filtered by which agent changed it. That needs a repository.' }),
      el('button', {
        class: 'btn primary', onclick: async () => {
          await tryApi('/git/init', { method: 'POST', body: { projectId: p.id } });
          render();
        },
      }, 'git init here')));
  }

  // Filter by agent: the whole point when several are writing at once.
  const agents = [...new Set(st.files.map(f => f.agentId).filter(Boolean))];
  const bar = el('div', { class: 'review-bar' },
    el('button', {
      class: 'btn sm' + (!S.reviewAgent ? ' primary' : ''),
      onclick: () => { S.reviewAgent = null; render(); },
    }, `All changes (${st.files.length})`),
    agents.map(id => {
      const n = st.files.filter(f => f.agentId === id).length;
      const a = agentById(id);
      return el('button', {
        class: 'btn sm' + (S.reviewAgent === id ? ' primary' : ''),
        onclick: () => { S.reviewAgent = id; render(); },
      },
        el('span', { class: 'acct-dot', style: `background:${a ? a.color : '#64748b'}` }),
        `${a ? a.name : id} (${n})`);
    }),
    el('span', { style: 'flex:1' }),
    el('button', { class: 'btn sm', onclick: () => openCommit(p, st) }, 'Commit…'));
  main.append(bar);

  const files = S.reviewAgent ? st.files.filter(f => f.agentId === S.reviewAgent) : st.files;
  const split = el('div', { class: 'review' });
  const list = el('div', { class: 'review-list' });
  const pane = el('div', { class: 'review-diff' },
    el('div', { class: 'hint', style: 'padding:14px', text: 'Pick a file to see its diff.' }));

  if (!files.length) {
    list.append(el('div', { class: 'hint', style: 'padding:12px', text: 'No changes.' }));
  }
  for (const f of files) {
    const row = el('div', {
      class: 'review-row',
      onclick: async () => {
        for (const r of list.querySelectorAll('.review-row')) r.classList.remove('active');
        row.classList.add('active');
        pane.innerHTML = '';
        pane.append(el('div', { class: 'hint', style: 'padding:14px', text: 'Loading…' }));
        try {
          const res = await fetch(`/api/git/diff?projectId=${encodeURIComponent(p.id)}&path=${encodeURIComponent(f.path)}${f.staged ? '&staged=1' : ''}`);
          const text = await res.text();
          pane.innerHTML = '';
          pane.append(renderDiff(text));
        } catch (e) {
          pane.innerHTML = '';
          pane.append(el('div', { class: 'hint', style: 'padding:14px', text: e.message }));
        }
      },
    },
      el('span', { class: 'code', text: f.code.trim() || '??' }),
      el('span', { class: 'name', text: f.path, title: f.path }),
      f.agentName ? el('span', { class: 'pill', title: 'changed by this agent' }, f.agentName) : null,
      el('span', { class: 'nums' },
        f.insertions ? el('span', { class: 'plus', text: '+' + f.insertions }) : null,
        f.deletions ? el('span', { class: 'minus', text: '−' + f.deletions }) : null));
    list.append(row);
  }
  split.append(list, pane);
  main.append(split);
}

// renderDiff colours a unified diff without a syntax library: the line prefix
// is all the information needed, and a real highlighter would be far more code
// than the value it adds here.
function renderDiff(text) {
  const box = el('div', { class: 'diff' });
  if (!text.trim()) {
    box.append(el('div', { class: 'hint', style: 'padding:14px', text: 'No textual diff (binary, or no change).' }));
    return box;
  }
  for (const line of text.split('\n')) {
    let cls = 'ctx';
    if (line.startsWith('+++') || line.startsWith('---')) cls = 'meta';
    else if (line.startsWith('@@')) cls = 'hunk';
    else if (line.startsWith('diff ') || line.startsWith('index ')) cls = 'meta';
    else if (line.startsWith('+')) cls = 'add';
    else if (line.startsWith('-')) cls = 'del';
    box.append(el('div', { class: 'dl ' + cls, text: line || ' ' }));
  }
  return box;
}

function openCommit(p, st) {
  const agents = [...new Set(st.files.map(f => f.agentId).filter(Boolean))];
  const body = el('div', {},
    el('div', { class: 'hint', style: 'margin-top:0' },
      `${st.staged} staged, ${st.unstaged} unstaged, ${st.untracked} untracked.`),
    el('label', { class: 'switch', style: 'margin-top:10px' },
      el('input', { type: 'checkbox', id: 'cStageAll', checked: 'checked' }),
      el('span', { text: 'Stage everything first' })),
    el('label', { text: 'Message' }),
    el('textarea', { id: 'cMsg', rows: '4', placeholder: 'feat(auth): add refresh token rotation' }),
    el('div', { style: 'display:flex;gap:8px;margin-top:8px' },
      el('button', {
        class: 'btn sm', onclick: async () => {
          const btn = event.target;
          btn.disabled = true;
          btn.textContent = 'reading the diff…';
          try {
            if ($('#cStageAll').checked) {
              await api('/git/stage', { method: 'POST', body: { projectId: p.id, all: true } });
            }
            const r = await api('/git/message', {
              method: 'POST', body: { projectId: p.id, draft: $('#cMsg').value },
            });
            $('#cMsg').value = r.message;
          } catch (e) { toast(e.message, 'bad'); }
          btn.disabled = false;
          btn.textContent = '✨ Write it from the diff';
        },
      }, '✨ Write it from the diff')),
    agents.length ? el('div', {},
      el('label', { text: 'Attach the conversation behind this change' }),
      el('select', { id: 'cAgent' },
        el('option', { value: '' }, '— do not attach —'),
        agents.map(id => el('option', { value: id }, (agentById(id) || {}).name || id))),
      el('div', { class: 'hint', text: 'Writes the agent’s conversation to .goaiteam/commit-context/ in this repo and links it from the commit message, with credentials stripped. Nothing is uploaded anywhere.' })) : null);

  modal('Commit', body, [
    ['Cancel', 'btn', closeModal],
    ['Commit', 'btn primary', async () => {
      const msg = $('#cMsg').value.trim();
      if (!msg) return toast('Write a message first', 'bad');
      const payload = {
        projectId: p.id, message: msg,
        stageAll: $('#cStageAll').checked,
        agentId: $('#cAgent') ? $('#cAgent').value : '',
      };
      closeModal();
      const r = await tryApi('/git/commit', { method: 'POST', body: payload });
      toast('Committed ' + r.hash.slice(0, 8), 'ok');
      render();
    }],
  ]);
}

// ---------------------------------------------------------------- terminals

async function viewTerminals(main) {
  const p = projectById(S.selectedProject);
  if (!p) return main.append(el('div', { class: 'empty', text: 'Pick a project first.' }));

  const cmds = await tryApi('/commands?projectId=' + encodeURIComponent(p.id));

  main.append(viewHeader(p, 'Terminals',
    el('button', { class: 'btn sm primary', onclick: () => newCommand(p) }, '+ Command'),
    el('button', { class: 'btn sm', onclick: () => openSSH() }, 'SSH hosts')));

  const grid = el('div', { class: 'grid' });
  if (!cmds.length) {
    grid.append(el('div', { class: 'empty', style: 'grid-column:1/-1' },
      el('h3', { text: 'No saved commands' }),
      el('p', { text: 'Save the commands you retype every day — the dev server, the build, the test watcher — and they become one click, with live output you can also reach from your phone.' }),
      el('button', { class: 'btn primary', onclick: () => newCommand(p) }, 'Save the first one')));
  }
  for (const c of cmds) {
    const sess = S.sessions.find(s => s.commandId === c.id && s.status !== 'exited' && s.status !== 'error');
    grid.append(el('div', {
      class: 'card' + (sess ? ' running' : ''),
      onclick: () => sess ? openTerm(sess.id) : startCommand(c),
    },
      el('div', { class: 'card-top' },
        el('div', { class: 'avatar', style: 'background:#334155' }, '›_'),
        el('div', { style: 'flex:1;min-width:0' },
          el('h3', { text: c.name }),
          el('div', { class: 'role mono', text: c.command })),
        el('button', {
          class: 'btn ghost sm',
          onclick: e => { e.stopPropagation(); editCommand(c); },
        }, '···')),
      el('div', { class: 'card-meta' },
        sess ? el('span', { class: 'pill' }, el('span', { class: 'dot ' + sess.status }), sess.status)
             : el('span', { class: 'pill' }, 'stopped'),
        c.dir ? el('span', { class: 'pill mono', title: c.dir }, 'custom dir') : null,
        Object.keys(c.env || {}).length ? el('span', { class: 'pill' }, `${Object.keys(c.env).length} env`) : null),
      !sess ? el('div', { style: 'margin-top:10px' },
        el('button', {
          class: 'btn sm primary',
          onclick: e => { e.stopPropagation(); startCommand(c); },
        }, '▶ Run')) : null));
  }
  main.append(grid);
}

async function startCommand(c) {
  try {
    const sess = await api(`/commands/${c.id}/start`, { method: 'POST', body: { cols: 120, rows: 32 } });
    patchSession(sess);
    openTerm(sess.id);
  } catch (e) { toast(e.message, 'bad'); }
}

function commandForm(c) {
  const envText = Object.entries((c && c.env) || {}).map(([k, v]) => `${k}=${v}`).join('\n');
  return el('div', {},
    el('label', { text: 'Name' }),
    el('input', { type: 'text', id: 'cmName', value: c ? c.name : '', placeholder: 'dev server' }),
    el('label', { text: 'Command' }),
    el('input', { type: 'text', id: 'cmCmd', value: c ? c.command : '', placeholder: 'npm run dev' }),
    el('label', { text: 'Directory' }),
    el('input', { type: 'text', id: 'cmDir', value: c ? (c.dir || '') : '', placeholder: 'defaults to the project root' }),
    el('label', { text: 'Environment' }),
    el('textarea', { id: 'cmEnv', rows: '4', value: envText, placeholder: 'PORT=3000\nDB_PASS={{secret:DB_PASS}}' }),
    el('div', { class: 'hint', text: 'One KEY=value per line. Reference a vault entry with {{secret:NAME}} and it is resolved at launch — the value never appears here, in the API, or in an agent’s context.' }));
}

function readCommandForm() {
  const env = {};
  for (const line of $('#cmEnv').value.split('\n')) {
    const [k, ...rest] = line.split('=');
    if (k && k.trim()) env[k.trim()] = rest.join('=');
  }
  return {
    name: $('#cmName').value.trim(),
    command: $('#cmCmd').value.trim(),
    dir: $('#cmDir').value.trim(),
    env,
  };
}

function newCommand(p) {
  modal('Save a command', commandForm(null), [
    ['Cancel', 'btn', closeModal],
    ['Save', 'btn primary', async () => {
      const f = readCommandForm();
      if (!f.name || !f.command) return toast('Name and command are both needed', 'bad');
      closeModal();
      await tryApi('/commands', { method: 'POST', body: { ...f, projectId: p.id } });
      render();
    }],
  ]);
}

function editCommand(c) {
  modal('Command', commandForm(c), [
    ['Delete', 'btn danger', async () => {
      closeModal();
      await tryApi(`/commands/${c.id}`, { method: 'DELETE' });
      render();
    }],
    ['Save', 'btn primary', async () => {
      const f = readCommandForm();
      closeModal();
      await tryApi(`/commands/${c.id}`, { method: 'PATCH', body: f });
      render();
    }],
  ]);
}

// ---------------------------------------------------------------- split view

async function viewSplit(main) {
  const p = projectById(S.selectedProject);
  if (!p) return main.append(el('div', { class: 'empty', text: 'Pick a project first.' }));

  const g = await tryApi('/panes?projectId=' + encodeURIComponent(p.id));
  S.panes = g;

  const live = agentsOf(p.id).map(a => ({ a, s: liveSession(a.id) })).filter(x => x.s);
  const chosen = (g.agentIds || []).filter(id => live.some(x => x.a.id === id));
  const shown = chosen.length ? chosen : live.slice(0, 2).map(x => x.a.id);

  main.append(viewHeader(p, 'Split view',
    el('button', {
      class: 'btn sm', title: 'Flip between columns and stacked rows',
      onclick: async () => {
        await tryApi('/panes', {
          method: 'PUT',
          body: { projectId: p.id, orientation: g.orientation === 'cols' ? 'rows' : 'cols', agentIds: shown, pinned: g.pinned || [] },
        });
        render();
      },
    }, g.orientation === 'rows' ? '▤ rows' : '▥ columns'),
    el('button', { class: 'btn sm', onclick: () => choosePanes(p, live, shown, g) }, 'Choose panes')));

  if (!live.length) {
    return main.append(el('div', { class: 'empty' },
      el('h3', { text: 'Nothing running to tile' }),
      el('p', { text: 'Split view puts several running agents on screen at once, each keeping its role colour, its account dot and its live status. Start two agents and come back.' }),
      el('button', { class: 'btn primary', onclick: () => { S.view = 'grid'; render(); } }, 'Back to agents')));
  }

  const wrap = el('div', { class: 'split ' + (g.orientation === 'rows' ? 'rows' : 'cols') });
  for (const id of shown) {
    const item = live.find(x => x.a.id === id);
    if (!item) continue;
    wrap.append(splitPane(item.a, item.s, p, g));
  }
  main.append(wrap);

  // Each pane gets its own xterm. They are tracked so a re-render disposes them
  // instead of leaking a websocket per repaint.
  disposePanes();
  for (const id of shown) {
    const item = live.find(x => x.a.id === id);
    if (item) mountPaneTerm(item.s.id);
  }
}

function splitPane(agent, sess, project, g) {
  const pinned = (g.pinned || []).includes(agent.id);
  return el('div', { class: 'pane' + (pinned ? ' pinned' : '') },
    el('div', { class: 'pane-head', style: `border-color:${agent.color || '#334155'}` },
      el('span', { class: 'acct-dot', style: `background:${sess.accountColor || '#3a4250'}`, title: sess.accountName }),
      el('strong', { text: agent.name }),
      el('span', { class: 'pill' }, el('span', { class: 'dot ' + sess.status }), sess.status),
      sess.totalTokens ? el('span', { class: 'pill mono' }, fmtNum(sess.totalTokens)) : null,
      el('span', { style: 'flex:1' }),
      el('button', {
        class: 'btn ghost sm', title: pinned ? 'Unpin this pane' : 'Pin this pane so it keeps its slot',
        onclick: async () => {
          const set = new Set(g.pinned || []);
          pinned ? set.delete(agent.id) : set.add(agent.id);
          await tryApi('/panes', {
            method: 'PUT',
            body: { projectId: project.id, orientation: g.orientation, agentIds: g.agentIds || [], pinned: [...set] },
          });
          render();
        },
      }, pinned ? '📌' : '📍'),
      el('button', {
        class: 'btn ghost sm', title: 'Open this one full width',
        onclick: () => openTerm(sess.id),
      }, '⤢')),
    el('div', { class: 'pane-term', id: 'pane-' + sess.id }));
}

// mountPaneTerm attaches a small xterm to one pane.
function mountPaneTerm(sessionId) {
  const host = $('#pane-' + sessionId);
  if (!host) return;
  const term = new Terminal({
    cursorBlink: false, fontSize: 11, scrollback: 2000,
    fontFamily: 'ui-monospace, Consolas, monospace',
    theme: { background: '#07090b', foreground: '#cfd6e0', cursor: '#ff8a3d' },
  });
  const fit = new FitAddon.FitAddon();
  term.loadAddon(fit);
  term.open(host);
  try { fit.fit(); } catch {}

  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const ws = new WebSocket(`${proto}://${location.host}/ws/pty?session=${encodeURIComponent(sessionId)}`);
  ws.binaryType = 'arraybuffer';
  const dec = new TextDecoder();
  const enc = new TextEncoder();
  ws.onmessage = ev => {
    if (typeof ev.data === 'string') return;
    term.write(dec.decode(new Uint8Array(ev.data)));
  };
  // Panes are interactive, not read-only: being able to answer a permission
  // prompt without leaving the split is most of the value.
  term.onData(d => { if (ws.readyState === 1) ws.send(enc.encode(d)); });

  const ro = new ResizeObserver(() => {
    try {
      fit.fit();
      if (ws.readyState === 1) ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }));
    } catch {}
  });
  ro.observe(host);
  S.paneTerms.push({ term, ws, ro });
}

function disposePanes() {
  for (const p of S.paneTerms) {
    try { p.ws.close(); } catch {}
    try { p.ro.disconnect(); } catch {}
    try { p.term.dispose(); } catch {}
  }
  S.paneTerms = [];
}

function choosePanes(p, live, shown, g) {
  const body = el('div', {},
    el('p', { class: 'hint', style: 'margin-top:0' },
      'Tick the agents to tile. A pinned pane keeps its slot while the others cycle.'));
  for (const { a, s } of live) {
    body.append(el('label', { class: 'switch', style: 'margin:8px 0' },
      el('input', { type: 'checkbox', 'data-id': a.id, checked: shown.includes(a.id) ? 'checked' : null }),
      el('span', { class: 'acct-dot', style: `background:${s.accountColor}` }),
      el('span', { text: `${a.name} — ${s.accountName}` })));
  }
  modal('Panes', body, [
    ['Cancel', 'btn', closeModal],
    ['Apply', 'btn primary', async () => {
      const ids = [...document.querySelectorAll('#overlay input[data-id]')]
        .filter(i => i.checked).map(i => i.getAttribute('data-id'));
      closeModal();
      await tryApi('/panes', {
        method: 'PUT',
        body: { projectId: p.id, orientation: g.orientation, agentIds: ids, pinned: g.pinned || [] },
      });
      render();
    }],
  ]);
}

// ---------------------------------------------------------------- stats

async function viewStats(main) {
  const p = projectById(S.selectedProject);
  const q = p ? '?projectId=' + encodeURIComponent(p.id) : '';
  const d = await tryApi('/stats' + q);

  main.append(viewHeader(p, 'Statistics'));

  const wrap = el('div', { style: 'padding:16px;overflow-y:auto' });

  wrap.append(el('div', { class: 'tiles' },
    tile('Total tokens', fmtNum(d.totalTokens), 'this run, from local transcripts'),
    tile('Live agents', String((d.agents || []).length), 'sessions being measured'),
    tile('Accounts in use', String(Object.keys(d.byAccount || {}).length), 'spending right now')));

  if (Object.keys(d.byAccount || {}).length) {
    wrap.append(el('label', { text: 'By account' }));
    const max = Math.max(...Object.values(d.byAccount), 1);
    for (const [name, n] of Object.entries(d.byAccount)) {
      const acct = S.accounts.find(a => a.name === name);
      wrap.append(el('div', { style: 'margin:6px 0' },
        el('div', { style: 'display:flex;gap:8px;font-size:12px' },
          el('span', { class: 'acct-dot', style: `background:${acct ? acct.color : '#3a4250'}` }),
          el('span', { text: name || 'system default' }),
          el('span', { style: 'flex:1' }),
          el('span', { class: 'mono dim', text: fmtNum(n) })),
        el('div', { class: 'bar' }, el('i', { style: `width:${n / max * 100}%` }))));
    }
  }

  if ((d.agents || []).length) {
    wrap.append(el('label', { text: 'Per agent' }));
    const t = el('table', { class: 'kv wide' },
      el('tr', {},
        el('th', { text: 'Agent' }), el('th', { text: 'Account' }),
        el('th', { text: 'Tokens' }), el('th', { text: 'Cache' }),
        el('th', { text: 'Msgs' }), el('th', { text: 'Tools' }),
        el('th', { text: 'Min' }), el('th', { text: '↻' })));
    for (const a of d.agents) {
      t.append(el('tr', {},
        el('td', { text: a.name }),
        el('td', { text: a.account || '—' }),
        el('td', { class: 'mono', text: fmtNum(a.tokens) }),
        el('td', { class: 'mono', text: a.cacheRate.toFixed(0) + '%' }),
        el('td', { class: 'mono', text: String(a.messages) }),
        el('td', { class: 'mono', text: String(a.toolUses) }),
        el('td', { class: 'mono', text: String(a.minutes) }),
        el('td', { class: 'mono', text: a.switches ? String(a.switches) : '' })));
    }
    wrap.append(t);
  }

  if (d.board && Object.keys(d.board).length) {
    wrap.append(el('label', { text: 'Board' }));
    wrap.append(el('div', { style: 'display:flex;gap:8px;flex-wrap:wrap' },
      Object.entries(d.board).map(([k, v]) => el('span', { class: 'pill' }, `${k.replace('_', ' ')}: ${v}`))));
  }

  wrap.append(el('div', { class: 'hint', style: 'margin-top:16px', text: d.note }));
  main.append(wrap);
}

function tile(label, value, sub) {
  return el('div', { class: 'tile' },
    el('div', { class: 'tile-v', text: value }),
    el('div', { class: 'tile-l', text: label }),
    sub ? el('div', { class: 'tile-s', text: sub }) : null);
}

// ---------------------------------------------------------------- shared header

function viewHeader(project, title, ...extra) {
  return el('div', { class: 'main-head' },
    el('div', {},
      el('h1', { text: project ? project.name : 'All projects' }),
      el('div', { class: 'path', text: title })),
    el('span', { class: 'spacer' }),
    ...extra.filter(Boolean));
}
