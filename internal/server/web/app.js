/* Go AI Team — single-file UI.
 *
 * No framework and no build step on purpose: the whole app has to fit inside one
 * Go binary and stay editable by hand. State lives in one object, and render()
 * rebuilds whichever region changed.
 */
'use strict';

// ---------------------------------------------------------------- state

const S = {
  accounts: [],
  folders: [],
  projects: [],
  agents: [],
  sessions: [],
  settings: {},
  tasks: [],
  hosts: [],
  roles: [],
  panes: {},
  selectedProject: null,
  openSession: null,   // session id whose terminal is on screen
  // view is the main pane: the agent grid, one of the other tabs, or a
  // full-width terminal. 'term' is tracked separately from the tab the user
  // came from so Back returns where they were.
  view: 'grid',
  lastTab: 'grid',
  reviewAgent: null,
  chatMode: 'chat',
  // chatPref is what the person chose; chatMode is what the open session can
  // actually show. A sign-in terminal must not change the preference.
  chatPref: 'chat',
  paneTerms: [],
  speaking: false,
  ws: null,
  term: null,
  fit: null,
  termSocket: null,
};

// TABS are the main-pane views. Each renders into #main and is repainted
// whenever state changes, so a status flip arriving over the socket updates the
// board and the split view without either subscribing separately.
const TABS = [
  ['grid',      'Agents'],
  ['split',     'Split'],
  ['board',     'Board'],
  ['review',    'Review'],
  ['files',     'Files'],
  ['terminals', 'Terminals'],
  ['stats',     'Stats'],
  ['activity',  'Activity'],
];

const ROLES = [
  ['Architect',  '#3b82f6'], ['Full-Stack', '#a855f7'], ['Frontend', '#06b6d4'],
  ['Backend',    '#8b5cf6'], ['DevOps',     '#f59e0b'], ['QA',       '#ef4444'],
  ['Security',   '#10b981'], ['Docs',       '#64748b'], ['Reviewer', '#ec4899'],
];

// Aliases rather than pinned ids, so "opus" keeps meaning the current Opus
// instead of freezing an agent on whichever one was latest the day it was made.
const MODELS = [
  ['',        'Default (CLI decides)'],
  ['opus',    'Opus — deep work'],
  ['fable',   'Fable — deep work, newest'],
  ['sonnet',  'Sonnet — everyday'],
  ['haiku',   'Haiku — cheap and fast'],
];

// ---------------------------------------------------------------- helpers

const $  = (sel, root = document) => root.querySelector(sel);
const el = (tag, props = {}, ...kids) => {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k === 'class') n.className = v;
    else if (k === 'text') n.textContent = v;
    else if (k === 'html') n.innerHTML = v;
    else if (k.startsWith('on')) n.addEventListener(k.slice(2), v);
    else if (v !== null && v !== undefined && v !== false) n.setAttribute(k, v);
  }
  for (const k of kids.flat()) {
    if (k === null || k === undefined || k === false) continue;
    n.append(k.nodeType ? k : document.createTextNode(String(k)));
  }
  return n;
};

function fmtNum(n) {
  if (n === null || n === undefined) return '0';
  if (n >= 1e9) return (n / 1e9).toFixed(2) + 'B';
  if (n >= 1e6) return (n / 1e6).toFixed(2) + 'M';
  if (n >= 1e3) return (n / 1e3).toFixed(1) + 'k';
  return String(n);
}
function fmtFull(n) { return (n || 0).toLocaleString(); }
function fmtDur(secs) {
  if (!secs || secs < 0) return '—';
  const h = Math.floor(secs / 3600), m = Math.floor((secs % 3600) / 60), s = Math.floor(secs % 60);
  return h ? `${h}h ${m}m` : m ? `${m}m ${s}s` : `${s}s`;
}
function initials(name) {
  return (name || '?').split(/[\s_-]+/).filter(Boolean).slice(0, 2)
    .map(w => w[0].toUpperCase()).join('') || '?';
}

// toast says one thing, briefly. An optional onClick makes it the way to the
// thing it is about — a question that needs answering is not much use as a
// notice you then have to go and find the agent for.
function toast(msg, kind = '', onClick) {
  const t = el('div', { class: 'toast ' + kind + (onClick ? ' clickable' : ''), text: msg });
  if (onClick) {
    t.onclick = () => { t.remove(); onClick(); };
  }
  $('#toasts').append(t);
  // Bad news and a question both deserve longer than a confirmation does.
  const life = kind === 'bad' ? 7000 : kind === 'warn' ? 9000 : 3600;
  setTimeout(() => {
    t.style.transition = 'opacity .25s';
    t.style.opacity = '0';
    setTimeout(() => t.remove(), 260);
  }, life);
}

// api wraps fetch so every call reports its error the same way, in a toast,
// instead of failing silently in the console.
async function api(path, opts = {}) {
  const init = { headers: {}, ...opts };
  if (init.body && typeof init.body !== 'string') {
    init.headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(init.body);
  }
  const res = await fetch('/api' + path, init);
  const text = await res.text();
  let data = null;
  if (text) { try { data = JSON.parse(text); } catch { data = { raw: text }; } }
  if (!res.ok) {
    const msg = (data && data.error) || `${res.status} ${res.statusText}`;
    throw new Error(msg);
  }
  return data;
}
async function tryApi(path, opts) {
  try { return await api(path, opts); }
  catch (e) { toast(e.message, 'bad'); throw e; }
}

// AUTH maps the credentials' real state to what the user is told. The
// distinction between "logged out" and "no credentials" matters: logging out of
// Claude Code leaves the file behind with its tokens blanked, so a directory can
// look set up and still be unusable.
const AUTH = {
  active:      ['ok',  'signed in',  'An access token is present.'],
  refreshable: ['ok',  'signed in',  'Only a refresh token is left, but it is still valid — the CLI will mint a new one.'],
  loggedOut:   ['bad', 'logged out', 'The credentials file is there but its tokens are blank. This account was signed out, so agents on it will fail. Sign in again.'],
  missing:     ['bad', 'no sign-in', 'No credentials file in this directory yet.'],
  unreadable:  ['bad', 'unreadable', 'The credentials file could not be parsed.'],
};

function authPill(a) {
  const st = (a.auth && a.auth.state) || (a.signedIn ? 'active' : 'missing');
  const [cls, label, why] = AUTH[st] || AUTH.missing;
  return el('span', { class: 'pill ' + cls, title: why }, label);
}

// ---------------------------------------------------------------- lookups

const accountById = id => S.accounts.find(a => a.id === id);
const projectById = id => S.projects.find(p => p.id === id);
const sessionById = id => S.sessions.find(s => s.id === id);
const agentById   = id => S.agents.find(a => a.id === id);
const agentsOf    = pid => S.agents.filter(a => a.projectId === pid);
const liveSession = agentId => S.sessions.find(
  s => s.agentId === agentId && s.kind === 'agent' && s.status !== 'exited' && s.status !== 'error');

// ------------------------------------------------------- the projects panel
//
/* Collapsing it.
 *
 * The panel is 268px of a window that is mostly conversation, and on a laptop
 * that is a real fraction of the screen — but only while you are not switching
 * project, which is most of the time. There was a ☰ button for this already and
 * it was hidden above 780px, so on a desktop there was no way to reclaim the
 * space at all.
 *
 * One button, two behaviours, because the panel is two different things. Wide,
 * it is a column and collapsing means the column goes to nothing. Narrow, it is
 * already an overlay over the whole app and the button shows and hides that.
 * Deciding which from the same media query the stylesheet uses keeps the two
 * from disagreeing.
 *
 * The choice is remembered per browser rather than in the app's settings: a
 * phone and a desktop want different answers, and the settings are shared
 * between them.
 */

const NAV_KEY = 'goaiteam.nav.collapsed';
const navNarrow = () => window.matchMedia('(max-width: 780px)').matches;

function navCollapsed() {
  try {
    return localStorage.getItem(NAV_KEY) === '1';
  } catch {
    // Private browsing, or storage turned off. Not a reason to fail.
    return false;
  }
}

function applyNav() {
  const app = $('#app');
  const collapsed = navCollapsed();
  // Only the wide layout has a column to collapse. On a phone the panel is an
  // overlay and starts hidden either way, so the remembered state must not
  // leave it stuck open there.
  app.classList.toggle('nav-collapsed', collapsed && !navNarrow());
  if (navNarrow()) app.classList.remove('nav-collapsed');
  else $('#sidebar').classList.remove('open');

  const btn = $('#menuBtn');
  if (!btn) return;
  const showing = navNarrow()
    ? $('#sidebar').classList.contains('open')
    : !collapsed;
  btn.title = (showing ? 'Hide projects' : 'Show projects') + '  (Ctrl-B)';
  btn.setAttribute('aria-expanded', showing ? 'true' : 'false');
  // What the panel would have shown, now that it cannot.
  paintNavActivity();
}

function toggleNav() {
  if (navNarrow()) {
    $('#sidebar').classList.toggle('open');
    applyNav();
    return;
  }
  try {
    localStorage.setItem(NAV_KEY, navCollapsed() ? '0' : '1');
  } catch { /* nothing to remember it in; the toggle still works this session */ }
  const app = $('#app');
  app.classList.toggle('nav-collapsed');
  // Kept in step with the class actually on the element, so a browser that
  // refused to store anything still gets a correct label.
  const btn = $('#menuBtn');
  if (btn) {
    const showing = !app.classList.contains('nav-collapsed');
    btn.title = (showing ? 'Hide projects' : 'Show projects') + '  (Ctrl-B)';
    btn.setAttribute('aria-expanded', showing ? 'true' : 'false');
  }
  // The terminal refits itself: mountTerminal puts a ResizeObserver on its
  // host, and the host just changed width.
}

// ---------------------------------------------------------------- data load

async function loadAll() {
  const [accounts, folders, projects, agents, sessions, settings, hosts] = await Promise.all([
    api('/accounts'), api('/folders'), api('/projects'),
    api('/agents'), api('/sessions'), api('/settings'),
    api('/sshhosts').catch(() => []),
  ]);
  S.hosts = hosts || [];
  S.accounts = accounts || [];
  S.folders  = folders  || [];
  S.projects = projects || [];
  S.agents   = agents   || [];
  S.sessions = sessions || [];
  S.settings = settings || {};
  if (!S.selectedProject && S.projects.length) S.selectedProject = S.projects[0].id;
  if (S.selectedProject && !projectById(S.selectedProject)) {
    S.selectedProject = S.projects.length ? S.projects[0].id : null;
  }
  render();
  updateWaitingCount();
}

// ------------------------------------------------------- activity in the tree
//
/* Which projects have something happening in them.
 *
 * The tree showed a count of live agents and nothing else, so a project with
 * three agents looked the same whether all three were working, all three were
 * waiting on you, or all three had finished ten minutes ago. With the panel
 * usually showing several projects at once, that is the one question it should
 * be able to answer at a glance.
 *
 * Three resting states and one transition. Working pulses, because motion is
 * what the eye catches across a screen. Needing you is steady and bright, on
 * purpose: a thing that wants a decision should not be competing with the
 * spinner next to it. Running-but-idle is a dim dot, present but quiet. And when
 * the last agent in a project stops working, the dot flashes green once and
 * settles — the moment work finishes is a moment worth noticing, and it is
 * exactly the one nothing reported before.
 *
 * Painted in place rather than re-rendered. The tree is rebuilt on a full
 * render, but status events arrive every few seconds, and rebuilding the row
 * each time would restart every animation mid-pulse and make the panel twitch.
 */

const ACTIVITY = {
  // was maps a project to the state it was last seen in, which is the only way
  // to notice the change from working to not.
  was: {},
  timers: {},
};

// projectActivity summarises what a project's agents are doing.
function projectActivity(projectId) {
  const live = agentsOf(projectId).map(a => liveSession(a.id)).filter(Boolean);
  if (!live.length) return { state: '', live: 0 };
  if (live.some(s => s.needsYou)) return { state: 'needs', live: live.length };
  if (live.some(s => s.status === 'working' || s.status === 'starting')) {
    return { state: 'working', live: live.length };
  }
  return { state: 'idle', live: live.length };
}

const ACTIVITY_TITLE = {
  working: 'working',
  needs: 'waiting for you',
  idle: 'running, nothing in progress',
};

// paintActivity updates the indicators without touching the rest of the tree.
function paintActivity() {
  for (const p of S.projects) {
    const node = document.querySelector(`.tree-item[data-project="${CSS.escape(p.id)}"] .activity`);
    if (!node) continue;

    const { state, live } = projectActivity(p.id);
    const before = ACTIVITY.was[p.id] || '';
    ACTIVITY.was[p.id] = state;

    // The transition worth showing: something was working here and has stopped.
    // Not when it stopped to ask a question — that is not finishing, and the
    // panel is about to say so in a much louder way.
    if (before === 'working' && state !== 'working' && state !== 'needs') {
      node.classList.add('just-done');
      clearTimeout(ACTIVITY.timers[p.id]);
      ACTIVITY.timers[p.id] = setTimeout(() => {
        node.classList.remove('just-done');
        // Repainted, not just unclassed: the tick below has to go with it.
        paintActivity();
      }, 4200);
    }
    if (state === 'working' || state === 'needs') node.classList.remove('just-done');

    node.className = 'activity' + (state ? ' act-' + state : '') +
      (node.classList.contains('just-done') ? ' just-done' : '');
    // A tick when the work is finished and the agent has gone, because the
    // count is what gives the badge its shape and there is no count left — the
    // flash was otherwise a small green blob in the margin.
    node.textContent = live ? String(live)
      : (node.classList.contains('just-done') ? '✓' : '');
    node.title = live
      ? `${live} agent${live === 1 ? '' : 's'} — ${ACTIVITY_TITLE[state] || ''}`
      : '';
  }
  paintNavActivity();
}

// paintNavActivity puts the same answer on the ☰ button, because the panel this
// is drawn in can be collapsed and then none of it is visible at all.
function paintNavActivity() {
  const btn = $('#menuBtn');
  if (!btn) return;
  let worst = '';
  for (const p of S.projects) {
    const { state } = projectActivity(p.id);
    if (state === 'needs') { worst = 'needs'; break; }
    if (state === 'working') worst = 'working';
    else if (state && !worst) worst = 'idle';
  }
  // Only while the panel is collapsed. The classes come off together rather
  // than being left inert, so what the element says about itself is true.
  const show = !!worst && $('#app').classList.contains('nav-collapsed');
  btn.classList.toggle('has-activity', show);
  btn.classList.toggle('act-needs', show && worst === 'needs');
  btn.classList.toggle('act-working', show && worst === 'working');
}

// ---------------------------------------------------------------- render

function render() {
  renderTopbar();
  renderTabs();
  renderSidebar();
  renderMain();
  renderStatus();
  paintActivity();
}

function renderTopbar() {
  const signed = S.accounts.filter(a => a.signedIn).length;
  $('#acctCount').textContent = S.accounts.length ? `${signed}/${S.accounts.length}` : '';
  const dot = $('#acctHealthDot');
  dot.className = 'dot ' + (signed >= 2 ? 'done' : signed === 1 ? 'idle' : 'waiting');
  dot.title = signed >= 2
    ? `${signed} accounts signed in — auto-switch has somewhere to go`
    : signed === 1 ? 'One account signed in — add a second to survive a usage limit'
    : 'No account signed in';

  const benched = S.accounts.filter(a => a.benched);
  const warn = $('#quotaWarn');
  warn.innerHTML = '';

  // A signed-out account looks configured but cannot run anything, so say so
  // rather than letting the user find out from a red line inside a terminal.
  const husks = S.accounts.filter(a => a.auth && a.auth.state === 'loggedOut');
  if (husks.length) {
    warn.append(el('span', {
      class: 'pill bad', style: 'cursor:pointer',
      title: husks.map(h => h.name + ' (' + h.dir + ') is signed out: its tokens are blank').join('\n'),
      onclick: openAccounts,
    }, husks.length + ' signed out'));
  }
  if (benched.length) {
    warn.append(el('span', { class: 'pill bad', title: benched.map(b => b.benchReason || '').join('\n') },
      `${benched.length} account${benched.length > 1 ? 's' : ''} out of quota`));
  }
}

function renderTabs() {
  const host = $('#tabbar');
  if (!host) return;
  host.innerHTML = '';
  for (const [key, label] of TABS) {
    const active = S.view === key || (S.view === 'term' && S.lastTab === key);
    host.append(el('div', {
      class: 'tab' + (active ? ' active' : ''),
      onclick: () => { closeTerm(); S.view = key; S.lastTab = key; render(); },
    }, label));
  }
}

function renderSidebar() {
  const sb = $('#sidebar');
  sb.innerHTML = '';

  sb.append(el('div', { class: 'side-head' },
    el('span', { text: 'Projects' }),
    el('button', { class: 'btn ghost sm', title: 'New folder', onclick: newFolder }, '+ folder')));

  const rootFolders = S.folders.filter(f => !f.parentId);
  const drawProject = (p, nested) => {
    const acct = accountById(p.accountId);
    const row = el('div', {
      class: 'tree-item' + (nested ? ' nested' : '') + (S.selectedProject === p.id ? ' active' : ''),
      onclick: () => {
        S.selectedProject = p.id;
        // Keep whichever tab the user is on: switching project should not also
        // throw away the view they were working in.
        if (S.view === 'term') S.view = S.lastTab || 'grid';
        closeTerm();
        render();
        $('#sidebar').classList.remove('open');
      },
    },
      el('span', { class: 'acct-dot', style: `background:${acct ? acct.color : '#3a4250'}`,
        title: acct ? `pinned to ${acct.name}` : 'inherits the cascade' }),
      el('span', { class: 'name', text: p.name, title: p.path }),
      // Always present, even when nothing is running. Painted in place rather
      // than rebuilt, so an animation starts once and is not restarted by the
      // next status event a second later.
      el('span', { class: 'activity', 'data-for': p.id }));
    row.dataset.project = p.id;
    dndDraggable(row, 'project', p.id);
    return row;
  };

  const drawFolder = (f, nested) => {
    const acct = accountById(f.accountId);
    const row = el('div', {
      class: 'tree-item folder' + (nested ? ' nested' : ''),
      onclick: () => editFolder(f),
    },
      el('span', { class: 'acct-dot', style: `background:${acct ? acct.color : '#3a4250'}`,
        title: acct ? `folder pinned to ${acct.name}` : 'no folder pin' }),
      el('span', { class: 'name', text: f.name }),
      nested ? null : el('span', { class: 'src-badge', text: 'folder' }));
    dndDraggable(row, 'folder', f.id);
    dndTarget(row, f.id);
    return row;
  };

  for (const f of rootFolders) {
    sb.append(drawFolder(f, false));
    for (const p of S.projects.filter(p => p.folderId === f.id)) sb.append(drawProject(p, true));
    for (const sub of S.folders.filter(x => x.parentId === f.id)) {
      sb.append(drawFolder(sub, true));
      for (const p of S.projects.filter(p => p.folderId === sub.id)) sb.append(drawProject(p, true));
    }
  }
  for (const p of S.projects.filter(p => !p.folderId)) sb.append(drawProject(p, false));

  // Somewhere to drop a thing to take it back out of a folder. It only appears
  // while something is being dragged, because the rest of the time it is an
  // empty box asking to be explained.
  const root = el('div', { class: 'drop-root' }, 'Drag here to take out of a folder');
  dndTarget(root, '');
  sb.append(root);

  if (!S.projects.length) {
    sb.append(el('div', { class: 'hint', style: 'padding:10px 8px' },
      'No projects yet. Add one to start putting agents on it.'));
  }
}

function renderStatus() {
  const running = S.sessions.filter(s => s.kind === 'agent' && s.status !== 'exited' && s.status !== 'error');
  const tokens = running.reduce((n, s) => n + (s.totalTokens || 0), 0);
  $('#sessionSummary').textContent =
    `${running.length} live · ${fmtNum(tokens)} tokens this run`;
  $('#hostInfo').textContent = location.host;
}

// ---------------------------------------------------------------- main pane

function renderMain() {
  const main = $('#main');
  if (S.view === 'term' && S.openSession) return; // terminal owns the pane
  disposePanes();
  main.innerHTML = '';

  if (!S.projects.length) return main.append(emptyFirstRun());

  // Every tab but the grid renders asynchronously, because each fetches the
  // data it needs. Errors surface as a toast from tryApi.
  const async_views = { split: viewSplit, board: viewBoard, review: viewReview, files: viewFiles,
                        terminals: viewTerminals, stats: viewStats,
                        activity: viewActivity };
  if (async_views[S.view]) {
    async_views[S.view](main).catch(e => {
      main.innerHTML = '';
      main.append(el('div', { class: 'empty' },
        el('h3', { text: 'Could not load this view' }),
        el('p', { text: e.message })));
    });
    return;
  }

  const p = projectById(S.selectedProject);
  if (!p) return main.append(el('div', { class: 'empty', text: 'Pick a project on the left.' }));

  const acct = accountById(p.accountId);
  main.append(el('div', { class: 'main-head' },
    el('div', {},
      el('h1', { text: p.name }),
      el('div', { class: 'path', text: p.path })),
    el('span', { class: 'spacer' }),
    el('button', { class: 'btn sm', onclick: () => pinProjectAccount(p) },
      el('span', { class: 'acct-dot', style: `background:${acct ? acct.color : '#3a4250'}` }),
      acct ? acct.name : 'inherited'),
    el('button', { class: 'btn sm primary', onclick: () => newAgent(p) }, '+ Agent'),
    POPOUT.project === p.id ? null : el('button', {
      class: 'btn ghost sm', title: 'Open this project in its own window',
      onclick: () => popOutProject(p.id),
    }, '⧉'),
    el('button', { class: 'btn ghost sm', title: 'Project settings', onclick: () => editProject(p) }, '···')));

  const grid = el('div', { class: 'grid' });
  const list = agentsOf(p.id);
  if (!list.length) {
    grid.append(el('div', { class: 'empty', style: 'grid-column:1/-1' },
      el('h3', { text: 'No agents on this project yet' }),
      el('p', { text: 'An agent is a saved seat: a role, a model, and optionally its own account. Give one project a work account and another a personal one, and both run at the same time.' }),
      el('button', { class: 'btn primary', onclick: () => newAgent(p) }, 'Add the first agent')));
  }
  for (const a of list) grid.append(agentCard(a));
  main.append(grid);
}

function emptyFirstRun() {
  const signed = S.accounts.filter(a => a.signedIn).length;
  return el('div', { class: 'empty' },
    el('h3', { text: 'Welcome to Go AI Team' }),
    el('p', { text: 'Run every Claude Code account you own side by side, in one window. Nothing here proxies the API or touches your credentials — an account is just a config directory, and each agent is launched against the right one.' }),
    el('div', { style: 'display:flex;gap:8px;justify-content:center;flex-wrap:wrap' },
      el('button', { class: 'btn' + (signed ? '' : ' primary'), onclick: openAccounts },
        signed ? `Accounts (${signed} signed in)` : '1. Add an account'),
      el('button', { class: 'btn' + (signed ? ' primary' : ''), onclick: addProject }, '2. Add a project')));
}

function agentCard(a) {
  const sess = liveSession(a.id);
  const acct = accountById(a.accountId) || (sess && { color: sess.accountColor, name: sess.accountName });
  const res  = a.resolution || {};
  const effColor = sess ? sess.accountColor : (res.color || '#3a4250');
  const effName  = sess ? sess.accountName  : (res.name || 'system default');
  const roleColor = a.color || (ROLES.find(r => r[0] === a.role) || [, '#64748b'])[1];
  const status = sess ? sess.status : 'offline';

  // An agent stopped at a question is the one thing on this screen worth
  // crossing the room for, and "waiting" alone does not say it — that is also
  // what an agent waiting on the model looks like.
  const asking = !!(sess && sess.needsYou);

  const card = el('div', {
    class: 'card' + (sess ? ' running' : '') + (asking ? ' asking' : ''),
    onclick: () => sess ? openTerm(sess.id) : startAgent(a),
  },
    el('div', { class: 'card-top' },
      el('div', { class: 'avatar', style: `background:${roleColor}` },
        initials(a.name),
        el('span', { class: 'acct-dot', style: `background:${effColor}`,
          title: `runs on ${effName}` })),
      el('div', { style: 'flex:1;min-width:0' },
        el('h3', { text: a.name }),
        el('div', { class: 'role', text: [a.role, a.model || 'default model'].filter(Boolean).join(' · ') })),
      el('button', {
        class: 'btn ghost sm', title: 'Edit agent',
        onclick: e => { e.stopPropagation(); editAgent(a); },
      }, '···')),

    el('div', { class: 'card-meta' },
      asking
        ? el('span', { class: 'pill needs-you', title: sess.question || 'It is asking you something' },
            el('span', { class: 'dot waiting' }), 'needs you')
        : el('span', { class: 'pill' + (status === 'waiting' ? ' bad' : status === 'working' ? ' warn' : '') },
            el('span', { class: 'dot ' + status }), status),
      el('span', { class: 'pill', title: `account resolved from: ${res.source || 'n/a'}` },
        el('span', { class: 'acct-dot', style: `background:${effColor}` }), effName),
      sess && sess.branch
        ? el('span', { class: 'pill', title: 'Running in its own worktree on this branch' },
            '⎇ ' + sess.branch)
        : (!sess && a.worktree
            ? el('span', { class: 'pill', title: 'Will run in a git worktree of its own' }, '⎇ worktree')
            : null),
      sess && sess.switchCount
        ? el('span', { class: 'pill warn', title: (sess.switchLog || []).join('\n') },
            `↻ ${sess.switchCount} switch${sess.switchCount > 1 ? 'es' : ''}`)
        : null,
      sess && sess.totalTokens
        ? el('span', {
            class: 'token-badge' + (sess.totalTokens > 2e6 ? ' hot' : ''),
            title: 'Open the session monitor',
            onclick: e => { e.stopPropagation(); openUsage(sess.id); },
          }, `${fmtNum(sess.totalTokens)} tok · ${(sess.cacheHitRate || 0).toFixed(0)}% cache`)
        : null),

    !sess ? el('div', { style: 'margin-top:10px' },
      el('button', {
        class: 'btn sm primary',
        onclick: e => { e.stopPropagation(); startAgent(a); },
      }, '▶ Start')) : null);

  return card;
}

// ---------------------------------------------------------------- terminal

// The conversation view lives in chat.js. openTerm is kept as the name every
// caller already uses; it hands off to the chat-first view, which owns the
// terminal as one of its two modes. That is what removed the two stacked input
// boxes: the CLI's own prompt is only on screen in terminal mode.
function openTerm(sessionId) { openAgent(sessionId); }

function closeTerm() { leaveAgent(); }

// ---------------------------------------------------------------- actions

async function startAgent(a) {
  const cols = 120, rows = 32;
  try {
    const sess = await api(`/agents/${a.id}/start`, { method: 'POST', body: { cols, rows } });
    patchSession(sess);
    openTerm(sess.id);
  } catch (e) { toast(e.message, 'bad'); }
}

async function stopSession(id) {
  await tryApi(`/sessions/${id}/stop`, { method: 'POST' });
  toast('Session stopped');
}

// manualSwitch restarts an agent on a chosen account, carrying nothing but the
// agent's own definition. It is the deliberate version of auto-switch.
async function manualSwitch(sess) {
  const options = S.accounts.filter(a => a.provider === sess.provider && a.signedIn && a.id !== sess.accountId);
  if (!options.length) return toast('No other signed-in account for this provider', 'bad');
  const body = el('div', {},
    el('p', { class: 'hint', text: 'The agent stops and relaunches on the account you pick. Claude sessions cannot be resumed under a different account, so this starts a fresh conversation — auto-switch is the path that carries a conversation across.' }),
    el('label', { text: 'Run this agent on' }),
    el('select', { id: 'swAcct' }, options.map(a => el('option', { value: a.id }, a.name))),
    el('label', { class: 'switch', style: 'margin-top:14px' },
      el('input', { type: 'checkbox', id: 'swPersist' }),
      el('span', { text: 'Also pin this account on the agent' })));
  modal('Switch account', body, [
    ['Cancel', 'btn', closeModal],
    ['Switch', 'btn primary', async () => {
      const id = $('#swAcct').value;
      const persist = $('#swPersist').checked;
      closeModal();
      if (persist) await tryApi(`/agents/${sess.agentId}`, { method: 'PATCH', body: { accountId: id } });
      await tryApi(`/sessions/${sess.id}`, { method: 'DELETE' });
      const a = agentById(sess.agentId);
      if (!a) return loadAll();
      // A one-off run on another account: pin, start, then restore the pin.
      const before = a.accountId || '';
      if (!persist) await tryApi(`/agents/${a.id}`, { method: 'PATCH', body: { accountId: id } });
      const ns = await tryApi(`/agents/${a.id}/start`, { method: 'POST', body: { cols: 120, rows: 32 } });
      if (!persist) await tryApi(`/agents/${a.id}`, { method: 'PATCH', body: { accountId: before } });
      await loadAll();
      openTerm(ns.id);
    }],
  ]);
}

async function pinProjectAccount(p) {
  const claude = S.accounts.filter(a => a.provider === 'claude');
  const body = el('div', {},
    el('p', { class: 'hint', text: 'Every agent in this project inherits this account unless the agent overrides it. Leave it inherited to fall through to the folder pin, then the global default, then ~/.claude.' }),
    el('label', { text: 'Account for this project' }),
    el('select', { id: 'pinAcct' },
      el('option', { value: '' }, '— inherit the cascade —'),
      claude.map(a => el('option', { value: a.id, selected: p.accountId === a.id ? 'selected' : null },
        a.name + (a.signedIn ? '' : ' — signed out, agents will fail')))));
  modal(`Account for ${p.name}`, body, [
    ['Cancel', 'btn', closeModal],
    ['Save', 'btn primary', async () => {
      const v = $('#pinAcct').value;
      closeModal();
      await tryApi(`/projects/${p.id}`, { method: 'PATCH', body: { accountId: v } });
      await loadAll();
      toast('Project account updated', 'ok');
    }],
  ]);
}

// morphAgent changes a running agent's role without restarting it, so the
// conversation it has built up survives the change.
async function morphAgent(sess) {
  const roles = S.roles.length ? S.roles : await tryApi('/roles');
  S.roles = roles;
  const body = el('div', {},
    el('p', { class: 'hint', style: 'margin-top:0' },
      'The role change is delivered as an instruction into the running session, not by restarting it, ' +
      'so everything the agent has learned so far is kept.'),
    el('label', { text: 'Act as' }),
    el('select', { id: 'moRole' }, roles.map(r => el('option', { value: r.id }, r.name))),
    el('label', { class: 'switch', style: 'margin-top:12px' },
      el('input', { type: 'checkbox', id: 'moFresh' }),
      el('span', { text: 'Fresh eyes — re-read the code rather than trusting earlier conclusions' })));
  modal('Morph', body, [
    ['Cancel', 'btn', closeModal],
    ['Morph', 'btn primary', async () => {
      const payload = { roleId: $('#moRole').value, freshEyes: $('#moFresh').checked };
      closeModal();
      const r = await tryApi(`/agents/${sess.agentId}/morph`, { method: 'POST', body: payload });
      toast(r.status === 'morphed' ? `Now acting as ${r.role}` : r.status, 'ok');
      await loadAll();
    }],
  ]);
}

// forkAgent makes a twin that already has the parent's conversation.
async function forkAgent(sess) {
  const body = el('div', {},
    el('p', {}, 'Create a twin of this agent that already has its whole conversation?'),
    el('div', { class: 'hint', text: 'On Claude the twin resumes the same session, so it really does carry the conversation rather than a summary of it. Both then run independently.' }));
  modal('Fork agent', body, [
    ['Cancel', 'btn', closeModal],
    ['Fork', 'btn primary', async () => {
      closeModal();
      const r = await tryApi(`/agents/${sess.agentId}/fork`, { method: 'POST' });
      toast('Forked — ' + r.mode, 'ok');
      await loadAll();
      openTerm(r.session.id);
    }],
  ]);
}

// adaptiveCheck right-sizes the model for what is in the composer, before it is
// sent, so a translation does not run on a flagship.
async function adaptiveCheck(sessionId) {
  const box = $('#composerBox');
  if (!box || !box.value.trim()) return toast('Type the prompt first');
  toast('Reading the prompt…');
  try {
    const pick = await api('/ai/model', { method: 'POST', body: { prompt: box.value } });
    const sess = sessionById(sessionId);
    const cur = sess && sess.agentId ? (agentById(sess.agentId) || {}).model : '';
    const body = el('div', {},
      el('div', { style: 'display:flex;gap:8px;align-items:center' },
        el('span', { class: 'pill' }, 'suggested: ' + pick.model),
        cur ? el('span', { class: 'pill dim' }, 'currently: ' + cur) : null),
      el('div', { class: 'hint', style: 'margin-top:8px', text: pick.reason }));
    modal('Model for this task', body, [
      ['Keep current', 'btn', closeModal],
      ['Use ' + pick.model, 'btn primary', async () => {
        closeModal();
        if (!sess || !sess.agentId) return;
        await tryApi(`/agents/${sess.agentId}`, { method: 'PATCH', body: { model: pick.model } });
        // Claude switches model in place, so the change lands without a restart.
        await api(`/sessions/${sessionId}/input`, {
          method: 'POST', body: { data: '/model ' + pick.model, enter: true },
        });
        toast('Switched to ' + pick.model, 'ok');
        await loadAll();
      }],
    ]);
  } catch (e) { toast(e.message, 'bad'); }
}

// ---------------------------------------------------------------- modal shell

function modal(title, bodyNode, buttons = [], wide = false) {
  closeModal();
  const foot = el('div', { class: 'modal-foot' },
    buttons.map(([label, cls, fn]) => el('button', { class: cls, onclick: fn }, label)));
  const ov = el('div', { class: 'overlay', id: 'overlay' },
    el('div', { class: 'modal' + (wide ? ' wide' : ''), onclick: e => e.stopPropagation() },
      el('div', { class: 'modal-head' },
        el('h2', { text: title }),
        el('button', { class: 'btn ghost sm', onclick: closeModal }, '✕')),
      el('div', { class: 'modal-body' }, bodyNode),
      buttons.length ? foot : null));
  ov.addEventListener('click', closeModal);
  $('#modalHost').append(ov);
  document.addEventListener('keydown', escClose);
}
function escClose(e) { if (e.key === 'Escape') closeModal(); }
function closeModal() {
  const ov = $('#overlay');
  if (ov) ov.remove();
  document.removeEventListener('keydown', escClose);
}

// ---------------------------------------------------------------- projects

async function addProject() {
  let cur = null;
  const listHost = el('div', { style: 'max-height:280px;overflow-y:auto;margin-top:8px' });
  const pathInput = el('input', { type: 'text', id: 'projPath', placeholder: 'C:\\Users\\you\\code\\my-app' });

  async function browse(path) {
    try {
      const q = path ? '?path=' + encodeURIComponent(path) : '';
      const d = await api('/fs/list' + q);
      cur = d;
      pathInput.value = d.path;
      listHost.innerHTML = '';
      listHost.append(el('div', { class: 'tree-item', onclick: () => browse(d.parent) },
        el('span', { class: 'name', text: '.. up one level' })));
      for (const dir of (d.dirs || [])) {
        listHost.append(el('div', { class: 'tree-item', onclick: () => browse(dir.path) },
          el('span', { class: 'name', text: dir.name }),
          dir.isGit ? el('span', { class: 'src-badge', text: 'git' }) : null));
      }
      if (!(d.dirs || []).length) {
        listHost.append(el('div', { class: 'hint', style: 'padding:8px', text: 'No sub-folders here.' }));
      }
    } catch (e) { toast(e.message, 'bad'); }
  }

  const body = el('div', {},
    el('label', { text: 'Project folder' }),
    pathInput,
    el('div', { class: 'hint', text: 'Type a path or browse below. This becomes the working directory every agent on the project runs in.' }),
    listHost);
  modal('Add a project', body, [
    ['Cancel', 'btn', closeModal],
    ['Add project', 'btn primary', async () => {
      const path = pathInput.value.trim();
      if (!path) return toast('Pick a folder first', 'bad');
      closeModal();
      const p = await tryApi('/projects', { method: 'POST', body: { path } });
      S.selectedProject = p.id;
      await loadAll();
      toast(`Added ${p.name}`, 'ok');
    }],
  ], true);
  browse('');
}

function editProject(p) {
  const body = el('div', {},
    el('label', { text: 'Name' }),
    el('input', { type: 'text', id: 'pName', value: p.name }),
    el('label', { text: 'Folder' }),
    el('select', { id: 'pFolder' },
      el('option', { value: '' }, '— no folder —'),
      S.folders.map(f => el('option', { value: f.id, selected: p.folderId === f.id ? 'selected' : null }, f.name))),
    el('div', { class: 'hint', text: 'A folder can pin an account for every project inside it, sub-folders included.' }),
    el('label', { text: 'Path' }),
    el('input', { type: 'text', value: p.path, disabled: 'disabled' }));
  modal('Project settings', body, [
    ['Remove project', 'btn danger', async () => {
      if (!confirm(`Remove ${p.name} from Go AI Team? The folder on disk is untouched.`)) return;
      closeModal();
      await tryApi(`/projects/${p.id}`, { method: 'DELETE' });
      S.selectedProject = null;
      await loadAll();
    }],
    ['Save', 'btn primary', async () => {
      const body = { name: $('#pName').value, folderId: $('#pFolder').value };
      closeModal();
      await tryApi(`/projects/${p.id}`, { method: 'PATCH', body });
      await loadAll();
    }],
  ]);
}

async function newFolder() {
  const body = el('div', {},
    el('label', { text: 'Folder name' }),
    el('input', { type: 'text', id: 'fName', placeholder: 'Work' }),
    el('label', { text: 'Pin an account for everything inside' }),
    el('select', { id: 'fAcct' },
      el('option', { value: '' }, '— no pin —'),
      S.accounts.filter(a => a.provider === 'claude').map(a => el('option', { value: a.id }, a.name))),
    el('div', { class: 'hint', text: 'Put your work repositories in a folder pinned to the work account, and every project filed under it starts there — including ones you add later.' }));
  modal('New folder', body, [
    ['Cancel', 'btn', closeModal],
    ['Create', 'btn primary', async () => {
      const name = $('#fName').value.trim();
      if (!name) return toast('Name it first', 'bad');
      const accountId = $('#fAcct').value;
      closeModal();
      await tryApi('/folders', { method: 'POST', body: { name, accountId } });
      await loadAll();
    }],
  ]);
}

function editFolder(f) {
  const body = el('div', {},
    el('label', { text: 'Name' }),
    el('input', { type: 'text', id: 'fName', value: f.name }),
    el('label', { text: 'Pinned account' }),
    el('select', { id: 'fAcct' },
      el('option', { value: '' }, '— no pin —'),
      S.accounts.filter(a => a.provider === 'claude').map(a =>
        el('option', { value: a.id, selected: f.accountId === a.id ? 'selected' : null }, a.name))));
  modal('Folder', body, [
    ['Delete folder', 'btn danger', async () => {
      closeModal();
      await tryApi(`/folders/${f.id}`, { method: 'DELETE' });
      await loadAll();
    }],
    ['Save', 'btn primary', async () => {
      const body = { name: $('#fName').value, accountId: $('#fAcct').value };
      closeModal();
      await tryApi(`/folders/${f.id}`, { method: 'PATCH', body });
      await loadAll();
    }],
  ]);
}

// ---------------------------------------------------------------- agents

function agentForm(a, project) {
  const claude = S.accounts.filter(x => x.provider === 'claude');
  return el('div', {},
    el('label', { text: 'Name' }),
    el('input', { type: 'text', id: 'aName', value: a ? a.name : '', placeholder: 'Backend Dev' }),

    el('label', { text: 'Role' }),
    el('select', { id: 'aRole' },
      el('option', { value: '' }, '— none —'),
      ROLES.map(([r]) => el('option', { value: r, selected: a && a.role === r ? 'selected' : null }, r))),

    el('label', { text: 'Model' }),
    el('select', { id: 'aModel' },
      MODELS.map(([v, label]) => el('option', { value: v, selected: a && a.model === v ? 'selected' : null }, label))),

    el('label', { text: 'Account override' }),
    el('select', { id: 'aAcct' },
      el('option', { value: '' }, '— inherit from project / folder / default —'),
      claude.map(x => el('option', { value: x.id, selected: a && a.accountId === x.id ? 'selected' : null },
        x.name + (x.signedIn ? '' : ' — signed out, agents will fail')))),
    el('div', { class: 'hint', text: 'An override applies to this agent alone. Two agents in one project can run on two different accounts at the same time.' }),

    el('label', { text: 'Working directory' }),
    el('label', { class: 'switch' },
      el('input', { type: 'checkbox', id: 'aWorktree', checked: a && a.worktree ? 'checked' : null }),
      'Give this agent a git worktree of its own'),
    el('div', { class: 'hint', text: 'Its own checkout on its own branch, off the same history. Several agents in one repository otherwise edit the same files, and one’s half-finished change becomes another’s starting point. Merge the branch when you are happy with it. Ignored where the project is not a git repository.' }),

    el('label', { text: 'Extra CLI flags' }),
    el('input', { type: 'text', id: 'aArgs', value: a ? (a.extraArgs || '') : '',
      placeholder: '--model opus-4.8  --append-system-prompt "be terse"' }),
    el('div', { class: 'hint', text: 'Passed straight to the CLI at launch. Use this to pin a model the picker does not list.' }));
}

function readAgentForm() {
  return {
    name: $('#aName').value.trim(),
    role: $('#aRole').value,
    model: $('#aModel').value,
    accountId: $('#aAcct').value,
    extraArgs: $('#aArgs').value.trim(),
    worktree: $('#aWorktree').checked,
  };
}

function newAgent(project) {
  modal('New agent', agentForm(null, project), [
    ['Cancel', 'btn', closeModal],
    ['Create', 'btn primary', async () => {
      const f = readAgentForm();
      if (!f.name) return toast('Give the agent a name', 'bad');
      const role = ROLES.find(r => r[0] === f.role);
      closeModal();
      await tryApi('/agents', {
        method: 'POST',
        body: { ...f, projectId: project.id, provider: 'claude', color: role ? role[1] : '#64748b' },
      });
      await loadAll();
    }],
  ]);
}

function editAgent(a) {
  const sess = liveSession(a.id);
  const body = el('div', {}, agentForm(a, projectById(a.projectId)));
  if (sess) {
    body.prepend(el('div', { class: 'pill warn', style: 'margin-bottom:12px' },
      'This agent is running. An account change applies the next time it starts.'));
  }
  modal('Edit agent', body, [
    ['Delete', 'btn danger', async () => {
      if (!confirm(`Delete agent ${a.name}?`)) return;
      closeModal();
      await tryApi(`/agents/${a.id}`, { method: 'DELETE' });
      await loadAll();
    }],
    ['Save', 'btn primary', async () => {
      const f = readAgentForm();
      if (!f.name) return toast('Give the agent a name', 'bad');
      closeModal();
      await tryApi(`/agents/${a.id}`, { method: 'PATCH', body: f });
      await loadAll();
    }],
  ]);
}

// ---------------------------------------------------------------- accounts UI

async function openAccounts() {
  const body = el('div', { id: 'acctBody' });
  modal('Accounts', body, [['Done', 'btn primary', closeModal]], true);
  await paintAccounts();
}

async function paintAccounts() {
  const host = $('#acctBody');
  if (!host) return;
  S.accounts = await api('/accounts');
  const layer = await api('/accounts/userlayer');
  host.innerHTML = '';

  host.append(el('p', { class: 'hint', style: 'margin-top:0' },
    'An account is a config directory. Go AI Team sets CLAUDE_CONFIG_DIR to the right one before launching each agent — Claude Code\'s own documented mechanism — so credentials, sessions and transcripts stay isolated and nothing passes through this app.'));

  for (const a of S.accounts) {
    const signedPill = authPill(a);
    const benchPill = a.benched
      ? el('span', { class: 'pill warn', title: a.benchReason || '' },
          'out of quota until ' + new Date(a.benchedUntil).toLocaleTimeString())
      : null;
    const isDefault = S.settings.defaultAccountId === a.id;

    host.append(el('div', { class: 'acct-row' },
      el('input', {
        type: 'color', value: a.color || '#f59e0b', title: 'Account colour',
        onchange: async e => {
          await tryApi(`/accounts/${a.id}`, { method: 'PATCH', body: { color: e.target.value } });
          await loadAll(); await paintAccounts();
        },
      }),
      el('div', { class: 'info' },
        el('div', {}, el('strong', { text: a.name }),
          isDefault ? el('span', { class: 'src-badge', text: '  · global default' }) : null),
        el('div', { class: 'dirpath', text: a.dir, title: a.dir }),
        el('div', { style: 'margin-top:5px;display:flex;gap:6px;flex-wrap:wrap' },
          signedPill, benchPill,
          a.label ? el('span', { class: 'pill' }, a.label) : null,
          el('span', { class: 'pill' }, a.managed ? 'managed' : 'attached'),
          a.projects ? el('span', { class: 'pill' }, `${a.projects} project dirs`) : null)),
      el('div', { style: 'display:flex;flex-direction:column;gap:5px;align-items:stretch' },
        el('button', { class: 'btn sm' + (a.signedIn ? '' : ' primary'), onclick: () => signIn(a) },
          a.signedIn ? 'Re-sign in' : 'Sign in'),
        el('label', { class: 'switch', style: 'font-size:11px;justify-content:flex-end' },
          el('input', {
            type: 'checkbox', checked: a.excludeFromFallback ? 'checked' : null,
            onchange: async e => {
              await tryApi(`/accounts/${a.id}`, { method: 'PATCH', body: { excludeFromFallback: e.target.checked } });
              await paintAccounts();
            },
          }),
          el('span', { text: 'never a backup', title: 'Keep this account out of the auto-switch pool. Use it to stop an employer\u2019s subscription paying for personal work.' })),
        el('div', { style: 'display:flex;gap:5px' },
          !isDefault ? el('button', {
            class: 'btn ghost sm', title: 'Make this the global default',
            onclick: async () => {
              await tryApi('/settings', { method: 'PATCH', body: { defaultAccountId: a.id } });
              await loadAll(); await paintAccounts();
            },
          }, '★') : null,
          a.benched ? el('button', {
            class: 'btn ghost sm', title: 'Return to the pool now',
            onclick: async () => { await tryApi(`/accounts/${a.id}/unbench`, { method: 'POST' }); await paintAccounts(); },
          }, '↺') : null,
          el('button', {
            class: 'btn danger sm', title: 'Remove from Go AI Team',
            onclick: () => removeAccount(a),
          }, '🗑')))));
  }

  if (!S.accounts.length) {
    host.append(el('div', { class: 'hint', style: 'padding:12px;text-align:center' },
      'No accounts registered yet. Attach the one you already use, or add a fresh one and sign in.'));
  }

  // sharing switch
  host.append(el('div', { style: 'margin:16px 0 6px;padding-top:14px;border-top:1px solid var(--line)' },
    el('label', { class: 'switch' },
      el('input', {
        type: 'checkbox', checked: layer.share ? 'checked' : null,
        onchange: async e => {
          await tryApi('/settings', { method: 'PATCH', body: { shareUserLayer: e.target.checked } });
          await loadAll(); await paintAccounts();
          toast(e.target.checked ? 'Your CLI configuration is now linked into every account' : 'Shared configuration unlinked', 'ok');
        },
      }),
      el('strong', { text: 'Use my own CLI configuration in every account' })),
    el('div', { class: 'hint' },
      'A freshly signed-in account starts empty: no slash commands, no skills, no CLAUDE.md. This links yours from ' + layer.source + ' into each one, so there is a single copy of the truth. Credentials, sessions and usage stay isolated either way.'),
    el('div', { style: 'margin-top:8px;display:flex;gap:5px;flex-wrap:wrap' },
      (layer.items || []).map(i => el('span', {
        class: 'pill' + (i.exists ? ' ok' : ''), title: i.source,
      }, (i.exists ? '✓ ' : '· ') + i.name)))));

  host.append(el('div', { style: 'display:flex;gap:8px;margin-top:14px;flex-wrap:wrap' },
    el('button', { class: 'btn primary', onclick: addAccountFlow }, '+ Add an account'),
    el('button', { class: 'btn', onclick: discoverFlow }, 'Find existing accounts')));
}

function addAccountFlow() {
  const body = el('div', {},
    el('div', { class: 'tabs' },
      el('div', { class: 'tab active', id: 'tabNew', onclick: () => swap('new') }, 'New account'),
      el('div', { class: 'tab', id: 'tabExisting', onclick: () => swap('existing') }, 'Existing directory')),
    el('div', { id: 'paneNew' },
      el('label', { text: 'Name it' }),
      el('input', { type: 'text', id: 'anName', placeholder: 'Work' }),
      el('div', { class: 'hint', text: 'A directory is created under ~/.goaiteam/profiles/ and you sign in there. Nothing touches your existing ~/.claude.' })),
    el('div', { id: 'paneExisting', style: 'display:none' },
      el('label', { text: 'Name it' }),
      el('input', { type: 'text', id: 'aeName', placeholder: 'Personal' }),
      el('label', { text: 'Config directory' }),
      el('input', { type: 'text', id: 'aeDir', placeholder: '~/.claude  or  ~/.ccs/instances/work' }),
      el('div', { class: 'hint', text: 'Point at a directory that already holds credentials and it is reused as-is, with no re-login. This is how a CCS profile — or the account you are signed in to right now — comes under management.' })));

  function swap(which) {
    $('#tabNew').classList.toggle('active', which === 'new');
    $('#tabExisting').classList.toggle('active', which === 'existing');
    $('#paneNew').style.display = which === 'new' ? '' : 'none';
    $('#paneExisting').style.display = which === 'existing' ? '' : 'none';
  }

  modal('Add an account', body, [
    ['Cancel', 'btn', () => { closeModal(); openAccounts(); }],
    ['Add', 'btn primary', async () => {
      const isNew = $('#tabNew').classList.contains('active');
      const payload = isNew
        ? { name: $('#anName').value.trim(), provider: 'claude', dir: '' }
        : { name: $('#aeName').value.trim(), provider: 'claude', dir: $('#aeDir').value.trim() };
      if (!payload.name) return toast('Name the account first', 'bad');
      if (!isNew && !payload.dir) return toast('Give the directory path', 'bad');
      closeModal();
      const a = await tryApi('/accounts', { method: 'POST', body: payload });
      await loadAll();
      if (a.signedIn) { openAccounts(); toast(`${a.name} is already signed in`, 'ok'); }
      else signIn(a);
    }],
  ]);
}

async function discoverFlow() {
  const found = await tryApi('/accounts/discover');
  const body = el('div', {});
  body.append(el('p', { class: 'hint', style: 'margin-top:0' },
    'Directories on this machine that look like agent accounts. Attaching one reuses its credentials with no re-login.'));
  if (!found.length) body.append(el('div', { class: 'hint', text: 'Nothing found.' }));
  for (const d of found) {
    body.append(el('div', { class: 'acct-row' },
      el('div', { class: 'info' },
        el('div', {}, el('strong', { text: d.name })),
        el('div', { class: 'dirpath', text: d.dir }),
        el('div', { style: 'margin-top:5px;display:flex;gap:6px' },
          d.signedIn ? el('span', { class: 'pill ok' }, 'signed in') : el('span', { class: 'pill bad' }, 'signed out'),
          el('span', { class: 'pill' }, d.origin),
          d.known ? el('span', { class: 'pill warn' }, 'already added') : null)),
      d.known ? null : el('button', {
        class: 'btn sm primary',
        onclick: async e => {
          e.target.disabled = true;
          await tryApi('/accounts', { method: 'POST', body: { name: d.name, provider: d.provider, dir: d.dir } });
          await loadAll();
          closeModal(); openAccounts();
          toast(`Attached ${d.name}`, 'ok');
        },
      }, 'Attach')));
  }
  modal('Existing accounts on this machine', body,
    [['Back', 'btn', () => { closeModal(); openAccounts(); }]], true);
}

function removeAccount(a) {
  const body = el('div', {},
    el('p', {}, `Remove ${a.name} from Go AI Team?`),
    el('div', { class: 'hint', text: 'Agents pinned to it fall through to the next step of the cascade instead of breaking. By default the directory stays on disk, so you can point another profile at it later.' }),
    a.managed ? el('label', { class: 'switch', style: 'margin-top:12px' },
      el('input', { type: 'checkbox', id: 'purgeDir' }),
      el('span', { text: 'Also delete its directory and its credentials' })) : null);
  modal('Remove account', body, [
    ['Cancel', 'btn', () => { closeModal(); openAccounts(); }],
    ['Remove', 'btn danger', async () => {
      const purge = $('#purgeDir') && $('#purgeDir').checked;
      closeModal();
      const r = await tryApi(`/accounts/${a.id}${purge ? '?purge=1' : ''}`, { method: 'DELETE' });
      await loadAll();
      openAccounts();
      toast(r.message || 'Removed', 'ok');
    }],
  ]);
}

// signIn opens the embedded PTY and watches for credentials appearing on disk.
async function signIn(a) {
  const r = await tryApi(`/accounts/${a.id}/login`, { method: 'POST' });
  closeModal();
  patchSession(r.session);
  openTerm(r.session.id);
  toast(r.hint, 'ok');

  // Poll for the credentials file. The badge is meant to flip the moment the
  // file lands, without the user telling us they are done.
  const started = Date.now();
  const timer = setInterval(async () => {
    if (Date.now() - started > 5 * 60 * 1000) return clearInterval(timer);
    try {
      const list = await api('/accounts');
      const me = list.find(x => x.id === a.id);
      if (me && me.signedIn) {
        clearInterval(timer);
        S.accounts = list;
        toast(`${a.name} is signed in`, 'ok');
        renderTopbar();
      }
    } catch { clearInterval(timer); }
  }, 1500);
}

// ---------------------------------------------------------------- usage panel

async function openUsage(sessionId) {
  const d = await tryApi(`/sessions/${sessionId}/usage`);
  const s = d.session, t = s.tokens || {};
  const rate = s.cacheHitRate || 0;
  const rateCls = rate >= 70 ? 'good' : rate >= 30 ? 'mid' : 'poor';

  const body = el('div', {},
    el('div', { style: 'display:flex;align-items:center;gap:8px;margin-bottom:12px' },
      el('span', { class: 'pill' }, el('span', { class: 'dot ' + s.status }), s.status),
      el('span', { class: 'pill' },
        el('span', { class: 'acct-dot', style: `background:${s.accountColor || '#3a4250'}` }),
        s.accountName || 'system default'),
      s.switchCount ? el('span', { class: 'pill warn' }, `↻ ${s.switchCount}`) : null),

    el('label', { text: `Cache hit rate — ${rate.toFixed(1)}%` }),
    el('div', { class: 'bar ' + rateCls }, el('i', { style: `width:${Math.min(rate, 100)}%` })),
    el('div', { class: 'hint', text: 'Cache reads cost roughly a tenth of fresh input, so this is the strongest single lever on what a long session costs. Green above 70%, amber 30–70, red below.' }),

    el('table', { class: 'kv', style: 'margin-top:14px' },
      el('tr', {}, el('td', { text: 'Input tokens' }), el('td', { text: fmtFull(t.inputTokens) })),
      el('tr', {}, el('td', { text: 'Output tokens' }), el('td', { text: fmtFull(t.outputTokens) })),
      el('tr', {}, el('td', { text: 'Cache writes' }), el('td', { text: fmtFull(t.cacheWrite) })),
      el('tr', {}, el('td', { text: 'Cache reads' }), el('td', { text: fmtFull(t.cacheRead) })),
      el('tr', {}, el('td', {}, el('strong', { text: 'Total' })), el('td', {}, el('strong', { text: fmtFull(s.totalTokens) }))),
      el('tr', {}, el('td', { text: 'Session duration' }), el('td', { text: fmtDur(d.durationSecs) })),
      el('tr', {}, el('td', { text: 'Prompts / assistant turns' }), el('td', { text: `${t.userTurns || 0} / ${t.assistantTurns || 0}` })),
      el('tr', {}, el('td', { text: 'Tool uses' }), el('td', { text: fmtFull(t.toolUses) })),
      el('tr', {}, el('td', { text: 'Models routed' }), el('td', { text: (t.models || []).join(', ') || '—' })),
      el('tr', {}, el('td', { text: 'Claude session id' }),
        el('td', {}, el('span', {
          class: 'mono', style: 'cursor:pointer', title: 'Copy — use it with claude --resume',
          onclick: () => { navigator.clipboard.writeText(s.claudeSessionId || ''); toast('Session id copied'); },
        }, (s.claudeSessionId || '—').slice(0, 18) + (s.claudeSessionId ? '…' : '')))),
      el('tr', {}, el('td', { text: 'PID' }), el('td', { text: String(s.pid || '—') }))));

  if ((d.tips || []).length) {
    body.append(el('label', { text: 'Suggestions from these numbers', style: 'margin-top:16px' }));
    for (const tip of d.tips) {
      body.append(el('div', { style: 'background:var(--bg-2);border:1px solid var(--line);border-radius:8px;padding:10px;margin-bottom:8px' },
        el('div', {}, el('strong', { text: tip.title })),
        el('div', { class: 'hint', text: tip.body }),
        tip.prompt ? el('button', {
          class: 'btn sm', style: 'margin-top:8px',
          onclick: async () => {
            await tryApi(`/sessions/${sessionId}/input`, { method: 'POST', body: { data: tip.prompt, enter: true } });
            toast('Sent to the agent', 'ok');
          },
        }, 'Send this to the agent') : null));
    }
  }

  if ((s.switchLog || []).length) {
    body.append(el('label', { text: 'Account history' }));
    body.append(el('pre', { class: 'mono', style: 'font-size:11px;color:var(--fg-mute);white-space:pre-wrap;margin:0' },
      s.switchLog.join('\n')));
  }

  modal('Session monitor', body, [['Close', 'btn primary', closeModal]]);
}

// ---------------------------------------------------------------- settings

async function openSettings() {
  const st = await api('/settings');
  S.settings = st;
  const claude = S.accounts.filter(a => a.provider === 'claude');
  const body = el('div', {},
    el('label', { class: 'switch' },
      el('input', { type: 'checkbox', id: 'setAuto', checked: st.autoSwitch ? 'checked' : null }),
      el('strong', { text: 'Hand the work to another account at a usage limit' })),
    el('div', { class: 'hint', text: 'When the CLI says it is out of quota, the transcript is copied into another signed-in account and the same conversation resumes there. It needs at least two accounts signed in, and it only fires on a real limit message — never on a warning or a percentage.' }),

    el('label', { text: 'Global default account' }),
    el('select', { id: 'setDefault' },
      el('option', { value: '' }, '— none: fall through to ~/.claude —'),
      claude.map(a => el('option', { value: a.id, selected: st.defaultAccountId === a.id ? 'selected' : null }, a.name))),
    el('div', { class: 'hint', text: 'The last step before the system directory. Every project with no pin of its own starts here.' }),

    el('label', { class: 'switch', style: 'margin-top:16px' },
      el('input', { type: 'checkbox', id: 'setSkip', checked: st.skipPermissions ? 'checked' : null }),
      el('span', { text: 'Launch Claude agents with --dangerously-skip-permissions' })),
    el('div', { class: 'hint', text: 'Agents stop asking before each tool call. Convenient for a trusted repo, and exactly as risky as it sounds anywhere else.' }),

    el('label', { text: 'Path to the claude executable' }),
    el('input', { type: 'text', id: 'setBin', value: st.claudeBin || '', placeholder: 'claude (found on PATH)' }),
    el('div', { class: 'hint', text: 'Leave empty unless the CLI lives somewhere PATH does not reach.' }),

    alertsSection(),

    el('div', { class: 'hint', style: 'margin-top:18px;padding-top:12px;border-top:1px solid var(--line)' },
      'The cascade, in order: agent override → project pin → nearest folder that pins an account → global default → ~/.claude.'));

  modal('Settings', body, [
    ['Quit Go AI Team', 'btn danger', confirmQuit],
    ['Cancel', 'btn', closeModal],
    ['Save', 'btn primary', async () => {
      const payload = {
        autoSwitch: $('#setAuto').checked,
        defaultAccountId: $('#setDefault').value,
        skipPermissions: $('#setSkip').checked,
        claudeBin: $('#setBin').value.trim(),
      };
      closeModal();
      await tryApi('/settings', { method: 'PATCH', body: payload });
      await loadAll();
      toast('Settings saved', 'ok');
    }],
  ]);
}

/* Stopping the app from inside it.
 *
 * Ctrl-C in the console used to be the way out, and the console is given back
 * on startup now so that no black box sits behind the window. Something had to
 * replace it, and in a browser tab — where there is no window to close and no
 * icon in the notification area — this is the only thing there is.
 *
 * It asks first, and says what it costs. Quitting is not closing a window: it
 * ends every running agent, and an agent halfway through a turn does not come
 * back where it left off.
 */
async function confirmQuit() {
  const live = S.sessions.filter(s => s.status !== 'exited' && s.status !== 'error');
  const body = el('div', {},
    el('p', { text: live.length
      ? `This stops Go AI Team and ends ${live.length} running ` +
        (live.length === 1 ? 'session' : 'sessions') + '.'
      : 'This stops Go AI Team. Nothing is running right now.' }),
    el('div', { class: 'hint', text: 'What was running is remembered, and offered back the next time the app starts.' }));

  modal('Quit', body, [
    ['Cancel', 'btn', closeModal],
    ['Quit', 'btn danger', async () => {
      closeModal();
      toast('Stopping…', 'ok');
      try {
        await api('/quit', { method: 'POST', body: {} });
      } catch {
        // The reply can lose the race with the listener closing, and that means
        // it worked. Only a refusal is worth reporting, and there is no way to
        // tell one from the other here — so say the honest thing.
      }
      document.body.innerHTML =
        '<div class="empty" style="height:100vh"><h3>Go AI Team has stopped</h3>' +
        '<p>You can close this window.</p></div>';
    }],
  ]);
}

async function openDoctor() {
  const d = await tryApi('/doctor');
  const body = el('div', {});
  for (const c of d.checks) {
    body.append(el('div', { class: 'acct-row' },
      el('span', { class: 'dot ' + (c.ok ? 'done' : 'waiting'), style: 'width:9px;height:9px' }),
      el('div', { class: 'info' },
        el('div', {}, el('strong', { text: c.name })),
        el('div', { class: 'dirpath', text: c.detail, title: c.detail }),
        !c.ok && c.fix ? el('div', { class: 'hint', text: '→ ' + c.fix }) : null)));
  }
  body.append(el('div', { class: 'hint', style: 'margin-top:10px' },
    `${d.platform} · state in ${d.home}`));
  modal('Doctor', body, [['Close', 'btn primary', closeModal]]);
}

// ---------------------------------------------------------------- realtime

// The tab and window caption carry the count, so the answer to "is anything
// waiting on me" is visible from the taskbar without opening the app. On the
// desktop window this is the caption of the window itself.
function updateWaitingCount() {
  const n = S.sessions.filter(s => s.needsYou && s.status !== 'exited' && s.status !== 'error').length;
  document.title = n ? `(${n}) Go AI Team` : 'Go AI Team';
}

function agentName(agentId) {
  const a = agentId && agentById(agentId);
  return a ? a.name : '';
}

function patchSession(p) {
  if (!p || !p.id) return;
  const i = S.sessions.findIndex(s => s.id === p.id);
  if (i >= 0) S.sessions[i] = p; else S.sessions.push(p);
}

function connectEvents() {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const ws = new WebSocket(`${proto}://${location.host}/ws/events`);
  S.ws = ws;

  ws.onopen = () => { $('#wsState').textContent = 'live'; $('#wsState').className = 'ok'; };
  ws.onclose = () => {
    $('#wsState').textContent = 'reconnecting…';
    $('#wsState').className = 'bad';
    setTimeout(connectEvents, 1500);
  };
  ws.onmessage = ev => {
    let m; try { m = JSON.parse(ev.data); } catch { return; }

    if (m.type === 'snapshot') {
      S.sessions = m.sessions || [];
      S.accounts = m.accounts || S.accounts;
      if (S.view === 'grid') renderMain();
      refreshLiveViewSoon();
      renderTopbar(); renderStatus(); renderSidebar();
      updateWaitingCount();
      alertScan();
      paintActivity();
      return;
    }

    if (m.payload && m.payload.id) patchSession(m.payload);

    switch (m.type) {
      case 'session.removed':
        S.sessions = S.sessions.filter(s => s.id !== m.sessionId);
        if (S.openSession === m.sessionId) { closeTerm(); S.view = 'grid'; }
        break;
      case 'account.benched':
        toast(m.message, 'bad');
        api('/accounts').then(a => { S.accounts = a; renderTopbar(); paintAccounts().catch(() => {}); });
        break;
      case 'account.unbenched':
        toast(`${m.message} is back in the pool`, 'ok');
        api('/accounts').then(a => { S.accounts = a; renderTopbar(); });
        break;
      case 'switch.done':
        toast(m.message, 'ok');
        // The agent now has a new session; follow it so the terminal on screen
        // is the one still doing the work.
        if (S.view === 'term' && m.payload && m.payload.agentId) {
          const prev = S.openSession && sessionById(S.openSession);
          if (!prev || prev.agentId === m.payload.agentId) {
            closeTerm();
            loadAll().then(() => openTerm(m.payload.id));
            return;
          }
        }
        break;
      case 'switch.failed':
        toast(m.message, 'bad');
        break;
      case 'session.note':
        break;
      case 'session.needs-you':
        // Not while you are looking straight at it: the panel is already on
        // screen and a toast over the top of it is noise.
        if (S.openSession !== m.sessionId) {
          const who = agentName(m.agentId) || 'An agent';
          toast(`${who} needs you — ${m.message || 'it is asking something'}`, 'warn',
            () => openTerm(m.sessionId));
        }
        break;
    }

    updateWaitingCount();
    alertScan();

    if (S.view === 'grid') renderMain();
    if (S.view === 'activity') refreshActivitySoon();
    refreshLiveViewSoon();
    if (S.view === 'term') updateTermTokens();
    renderStatus(); renderTopbar();
    // The tree was not in this list, so its live count only changed when
    // something else forced a full render — a project could sit showing "1"
    // long after that agent had gone.
    paintActivity();
  };
}

// ---------------------------------------------------------------- boot

$('#accountsBtn').onclick = openAccounts;
$('#promptsBtn').onclick = openPrompts;
$('#skillsBtn').onclick = openSkills;
$('#memoryBtn').onclick = openMemory;
$('#autoBtn').onclick = openAutomation;
$('#envBtn').onclick = openEnvironment;
$('#mcpBtn').onclick = openMCP;
$('#rolesBtn').onclick = openRoles;
$('#guardBtn').onclick = openGuard;
$('#addProjectBtn').onclick = addProject;
$('#settingsBtn').onclick = openSettings;
$('#doctorBtn').onclick = openDoctor;
$('#menuBtn').onclick = toggleNav;
// A keyboard shortcut nobody can see is a keyboard shortcut nobody uses, so the
// button carries its own key next to it and teaches itself.
$('#gotoBtn').onclick = openPalette;

// Ctrl-B, because that is the shortcut every editor uses for this and muscle
// memory is worth more than a novel one. Not while a modifier combination means
// something else, and harmless in the composer: a textarea does nothing with
// Ctrl-B of its own.
document.addEventListener('keydown', e => {
  if ((e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey && e.key.toLowerCase() === 'b') {
    e.preventDefault();
    toggleNav();
  }
});

applyNav();
// A window dragged across the phone breakpoint changes what the button means,
// so the state is reapplied rather than left as whatever it was.
window.matchMedia('(max-width: 780px)').addEventListener('change', applyNav);

applyPopoutChrome();

loadAll().then(goToPopoutTarget).then(connectEvents).catch(e => {
  document.body.innerHTML =
    `<div class="empty"><h3>Could not reach the Go AI Team server</h3><p>${e.message}</p></div>`;
});
