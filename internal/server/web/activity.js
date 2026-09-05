/* What happened while you were not looking.
 *
 * Everything else in this app answers "where do things stand". This answers
 * "what happened", which is the question you have when you come back to a room
 * full of agents that have been running for six hours: which one asked
 * something at two in the morning, which account ran out and handed its work
 * over, which session died and how long it had been dead.
 *
 * The log lives on the server, so it covers the hours when nobody had the page
 * open — closing the window is not the same as nothing happening.
 */
'use strict';

const ACT = { scope: 'project', timer: null, rows: [] };

async function viewActivity(main) {
  const p = projectById(S.selectedProject);
  const scoped = ACT.scope === 'project' && p;

  main.append(viewHeader(p, 'Activity',
    el('div', { class: 'seg' },
      el('button', {
        class: 'btn sm' + (scoped ? ' primary' : ''),
        onclick: () => { ACT.scope = 'project'; renderMain(); },
      }, 'This project'),
      el('button', {
        class: 'btn sm' + (scoped ? '' : ' primary'),
        onclick: () => { ACT.scope = 'all'; renderMain(); },
      }, 'Everything')),
    el('button', { class: 'btn sm', onclick: () => renderMain() }, 'Refresh')));

  const q = scoped ? '?projectId=' + encodeURIComponent(p.id) : '';
  const rows = (await tryApi('/activity' + q)) || [];
  ACT.rows = rows;

  const wrap = el('div', { class: 'act-wrap' });
  if (!rows.length) {
    wrap.append(el('div', { class: 'empty' },
      el('h3', { text: 'Nothing yet' }),
      el('p', { text: scoped
        ? 'Nothing has happened in this project since the app started. Try Everything.'
        : 'The log starts when the app does, and nothing has happened yet.' })));
    main.append(wrap);
    return;
  }

  let day = '';
  for (const a of rows) {
    const when = new Date(a.at);
    const d = dayLabel(when);
    if (d !== day) {
      day = d;
      wrap.append(el('div', { class: 'act-day', text: d }));
    }
    wrap.append(actRow(a, when, scoped));
  }
  main.append(wrap);
}

function actRow(a, when, scoped) {
  const sess = a.sessionId && sessionById(a.sessionId);
  const row = el('div', {
    class: 'act-row' + (a.level === 'warn' ? ' warn' : a.level === 'bad' ? ' bad' : '') +
      (sess ? ' go' : ''),
    // A row for a session that has since gone is still worth reading; it just
    // has nowhere to take you.
    onclick: sess ? () => openTerm(a.sessionId) : null,
    title: sess ? 'Open this agent' : '',
  },
    el('span', { class: 'act-time mono', text: hhmm(when) }),
    el('span', { class: 'act-dot' }),
    el('span', { class: 'act-who', text: a.agentName || '—' }),
    !scoped && a.projectName
      ? el('span', { class: 'act-proj', text: a.projectName }) : null,
    el('span', { class: 'act-text', text: a.text }));
  return row;
}

function hhmm(d) {
  return String(d.getHours()).padStart(2, '0') + ':' + String(d.getMinutes()).padStart(2, '0');
}

// dayLabel says Today and Yesterday by name, because a date on a log written
// four hours ago reads as older than it is.
function dayLabel(d) {
  const midnight = x => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const days = Math.round((midnight(new Date()) - midnight(d)) / 86400000);
  if (days === 0) return 'Today';
  if (days === 1) return 'Yesterday';
  return d.toLocaleDateString(undefined, { weekday: 'long', day: 'numeric', month: 'short' });
}

// refreshActivitySoon repaints the view when something happens, at most every
// few seconds.
//
// Events arrive constantly — a busy agent emits a token counter every poll —
// and refetching on each one would be a request every second for a list that
// changes every few minutes.
function refreshActivitySoon() {
  if (S.view !== 'activity' || ACT.timer) return;
  ACT.timer = setTimeout(() => {
    ACT.timer = null;
    if (S.view === 'activity') renderMain();
  }, 2500);
}
