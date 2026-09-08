/* The strip above the composer: what you are running on, and what it is costing.
 *
 * All of this was already on screen — in the terminal tab, along the bottom, in
 * a line you had to switch views to read. The two numbers that actually govern a
 * day's work, how much of the five-hour window and the week's allowance has
 * gone, were the furthest from where you type.
 *
 * Model and effort are changed by sending the CLI its own slash commands, which
 * is what a person would type. Nothing here reaches around the CLI: `/model
 * sonnet` and `/effort high` are exactly what it understands, and the result
 * shows up in its own status line, which is where these numbers are read back
 * from.
 */
'use strict';

// The same list the agent settings offer, minus the "let the CLI decide" entry —
// there is nothing to decide once a session is running — and with the trailing
// description trimmed, because a menu of four words does not need sentences.
function switchableModels() {
  return MODELS
    .filter(([alias]) => alias)
    .map(([alias, label]) => [alias, label.split('—')[0].trim()]);
}

// The levels `claude --effort` accepts.
const EFFORTS = ['low', 'medium', 'high', 'xhigh', 'max'];

// The permission modes, in the order the CLI cycles them, with what each one
// actually means rather than just its name.
const MODES = [
  ['auto', 'auto', 'It decides which actions are safe and asks about the rest'],
  ['manual', 'manual', 'Asks before every action'],
  ['acceptEdits', 'accept edits', 'Edits files without asking; still asks for the rest'],
  ['plan', 'plan', 'Changes nothing — researches and writes a plan first'],
];
const MODE_LABELS = Object.fromEntries(MODES.map(([k, label]) => [k, label]));

const RUNBAR = {
  // effort is not printed in the status line, so the only honest thing to show
  // is what was last set from here.
  effort: '',
};

function runBarEl(sessionId) {
  return el('div', { class: 'runbar', id: 'runBar' },
    el('button', {
      class: 'rb-btn', id: 'rbModel', title: 'Change the model for this session',
      onclick: e => modelMenu(e.currentTarget, sessionId),
    }, '—'),
    el('button', {
      class: 'rb-btn', id: 'rbEffort', title: 'Change the effort level',
      onclick: e => effortMenu(e.currentTarget, sessionId),
    }, 'effort'),
    el('button', {
      class: 'rb-btn', id: 'rbMode',
      title: 'Permission mode — how much it asks before acting',
      onclick: e => modeMenu(e.currentTarget, sessionId),
    }, 'mode'),
    el('span', { class: 'rb-sep' }),
    // The plan. Of everything on this strip it is the one that answers "what is
    // this agent doing", which is the question you have when you look at it.
    el('button', {
      class: 'rb-btn rb-plan', id: 'rbPlan', style: 'display:none',
      title: 'The agent’s plan',
      onclick: e => planMenu(e.currentTarget),
    }),
    el('span', { class: 'rb-stat', id: 'rbCtx', title: 'Context window used' }),
    el('span', { class: 'rb-stat', id: 'rbCost', title: 'Cost of this session' }),
    // Why there is nothing to the right of here, when there is going to go on
    // being nothing. See updateRunBar.
    el('span', { class: 'rb-note', id: 'rbNote', style: 'display:none' }),
    el('span', { style: 'flex:1' }),
    meterEl('rb5h', '5h'),
    meterEl('rbWk', 'wk'));
}

// meterEl is a labelled bar. A percentage as a number is something you read; as
// a bar it is something you notice, which is the point of showing a limit.
function meterEl(id, label) {
  return el('span', { class: 'rb-meter', id, title: label === '5h'
    ? 'Five-hour window used' : 'Weekly allowance used' },
    el('span', { class: 'rb-meter-label', text: label }),
    el('span', { class: 'rb-meter-track' }, el('i')),
    el('span', { class: 'rb-meter-pct' }));
}

// updateRunBar redraws from the latest poll.
function updateRunBar(d, sess) {
  const bar = $('#runBar');
  if (!bar || !d) return;
  const line = d.line || {};

  const model = $('#rbModel');
  if (model) model.textContent = line.model || '—';

  const eff = $('#rbEffort');
  if (eff) eff.textContent = RUNBAR.effort || 'effort';

  const mode = $('#rbMode');
  if (mode) {
    mode.textContent = MODE_LABELS[line.mode] || 'mode';
    // Plan touches nothing and accept-edits touches everything without asking.
    // Both are worth noticing from across the room.
    mode.classList.toggle('mode-plan', line.mode === 'plan');
    mode.classList.toggle('mode-loose', line.mode === 'acceptEdits');
  }

  updatePlan(d.tasks || []);

  // age is how long ago the server last managed to read any of this. The values
  // are held across a scrape that finds nothing, so a number on screen can be a
  // minute old, and saying so is the difference between a stale reading and a
  // wrong one.
  const age = Number(line.ageSeconds) || 0;
  setStat('#rbCtx', line.hasContext, () => `ctx ${line.context}%`, age);
  setStat('#rbCost', line.hasCost, () => '$' + Number(line.cost).toFixed(2), age);
  setMeter('#rb5h', line.hasFiveHour, line.fiveHour, age);
  setMeter('#rbWk', line.hasWeekly, line.weekly, age);

  updateRunBarNote(d, sess);
}

// staleTitle says how old a held reading is, in words rather than seconds.
function staleTitle(base, age) {
  if (age < STALE_AFTER) return base;
  const mins = Math.round(age / 60);
  return `${base} — last read ${mins < 1 ? 'under a minute' : mins + (mins === 1 ? ' minute' : ' minutes')} ago`;
}

// Under this many seconds a held value is treated as current. The strip is
// polled every second or two, so a gap of a few seconds is an ordinary scrape
// miss rather than anything worth marking on screen.
const STALE_AFTER = 20;

function setStat(sel, has, text, age = 0) {
  const n = $(sel);
  if (!n) return;
  // A field the status line has never printed is hidden rather than shown empty
  // or as a guessed zero. One that was printed and is momentarily unreadable is
  // kept, dimmed: see holdLine in internal/session/statusline.go.
  n.style.display = has ? '' : 'none';
  if (!has) return;
  n.textContent = text();
  n.classList.toggle('rb-stale', age >= STALE_AFTER);
  n.title = staleTitle(sel === '#rbCtx' ? 'Context window used' : 'Cost of this session', age);
}

function setMeter(sel, has, pct, age = 0) {
  const n = $(sel);
  if (!n) return;
  n.style.display = has ? '' : 'none';
  if (!has) return;
  const v = Math.max(0, Math.min(100, Number(pct) || 0));
  n.querySelector('.rb-meter-track i').style.width = v + '%';
  n.querySelector('.rb-meter-pct').textContent = v + '%';
  n.classList.toggle('warn', v >= 75 && v < 90);
  n.classList.toggle('hot', v >= 90);
  n.classList.toggle('rb-stale', age >= STALE_AFTER);
  n.title = staleTitle(sel === '#rb5h' ? 'Five-hour window used' : 'Weekly allowance used', age);
}

// updateRunBarNote fills the gap where the numbers would be.
//
// An empty strip explains nothing, and the two reasons for one want different
// responses. Nothing scraped yet arrives on its own and says so. An account
// with no status-line command never will, because the CLI prints no usage
// figures without one and nothing else on disk carries them, so that one gets
// the offer to install it.
function updateRunBarNote(d, sess) {
  const n = $('#rbNote');
  if (!n) return;
  const note = d.lineNote || '';
  if (!note) {
    n.style.display = 'none';
    n.textContent = '';
    return;
  }
  n.style.display = '';
  n.textContent = '';
  n.title = note;
  n.append(el('span', { text: d.lineFixable ? 'no usage figures' : 'reading the status line…' }));
  if (d.lineFixable) {
    n.append(el('button', {
      title: note,
      onclick: () => offerStatusLine(sess),
    }, 'why'));
  }
}

// ----------------------------------------------------------------- plan

// RUNBAR.tasks is the latest plan, kept so the popover can be built on click
// rather than rebuilt on every poll under the pointer.
RUNBAR.tasks = [];

function updatePlan(tasks) {
  RUNBAR.tasks = tasks;
  const btn = $('#rbPlan');
  if (!btn) return;
  if (!tasks.length) { btn.style.display = 'none'; return; }
  btn.style.display = '';

  const done = tasks.filter(t => t.status === 'completed' || t.status === 'cancelled').length;
  const on = tasks.find(t => t.status === 'in_progress');
  const count = `${done}/${tasks.length}`;
  // The step it is on, when there is one, because that is the answer to the
  // question the plan is being consulted for.
  const label = on ? `${count} · ${on.activeForm || on.subject}` : count;

  btn.textContent = '';
  btn.append(el('span', { class: 'rb-plan-count', text: '☑' }), el('span', { text: label }));
  btn.classList.toggle('rb-plan-done', done === tasks.length);
  btn.title = tasks.map(t => taskMark(t) + ' ' + t.subject).join('\n');
}

function taskMark(t) {
  if (t.status === 'completed') return '✓';
  if (t.status === 'cancelled') return '✗';
  if (t.status === 'in_progress') return '▸';
  return '·';
}

function planMenu(anchor) {
  const items = RUNBAR.tasks.map(t => [
    taskMark(t) + '  ' + t.subject,
    () => {},
    t.status === 'in_progress',
    t.status === 'in_progress' ? t.activeForm : '',
  ]);
  if (!items.length) return;
  rbMenu(anchor, items);
  // Nothing here is a command — it is the plan, shown. Striking through what is
  // finished says so faster than reading the marks.
  const menu = document.querySelector('.rb-menu');
  if (!menu) return;
  menu.classList.add('rb-menu-plan');
  [...menu.querySelectorAll('.rb-menu-item')].forEach((n, i) => {
    const t = RUNBAR.tasks[i];
    if (t && (t.status === 'completed' || t.status === 'cancelled')) n.classList.add('is-done');
  });
}

// ---------------------------------------------------------------- menus

function rbMenu(anchor, items, footer) {
  for (const old of document.querySelectorAll('.rb-menu')) old.remove();
  const menu = el('div', { class: 'rb-menu' });
  for (const [label, onPick, active, note] of items) {
    menu.append(el('button', {
      class: 'rb-menu-item' + (active ? ' active' : ''),
      onclick: () => { menu.remove(); onPick(); },
    },
      el('span', { text: label }),
      note ? el('span', { class: 'rb-menu-note', text: note }) : null));
  }
  if (footer) menu.append(el('div', { class: 'rb-menu-foot', text: footer }));
  document.body.append(menu);

  const r = anchor.getBoundingClientRect();
  menu.style.left = Math.max(8, Math.min(r.left, window.innerWidth - menu.offsetWidth - 8)) + 'px';
  // Above the button: this sits at the bottom of the window, so a menu dropping
  // downwards would open off screen.
  menu.style.top = (r.top - menu.offsetHeight - 6) + 'px';

  const away = e => {
    if (menu.contains(e.target) || anchor.contains(e.target)) return;
    menu.remove();
    document.removeEventListener('mousedown', away);
  };
  setTimeout(() => document.addEventListener('mousedown', away), 0);
}

function modelMenu(anchor, sessionId) {
  const current = ($('#rbModel') || {}).textContent || '';
  const items = switchableModels().map(([alias, label]) => [
    label,
    () => runSlash(sessionId, '/model ' + alias, `Switching to ${label}…`),
    current.toLowerCase().startsWith(label.toLowerCase()),
  ]);
  // Said once, at the bottom, because it is true of every entry and it is not
  // what you would assume: the CLI answers /model with "set model to X and saved
  // as your default for new sessions". Picking one here is not only about this
  // session, and finding that out later would be an unpleasant surprise.
  rbMenu(anchor, items, 'Also becomes the CLI’s default for new sessions.');
}

function effortMenu(anchor, sessionId) {
  rbMenu(anchor, EFFORTS.map(lvl => [
    lvl,
    () => {
      RUNBAR.effort = lvl;
      runSlash(sessionId, '/effort ' + lvl, `Effort set to ${lvl}`);
    },
    RUNBAR.effort === lvl,
  ]));
}

// modeMenu changes the permission mode.
//
// There is no command that jumps to a mode — the CLI cycles them with shift+tab
// — so the server presses that key until the terminal says it has arrived. Which
// modes are in the cycle depends on the model, so this can land somewhere else,
// and when it does the bar says where rather than claiming success.
function modeMenu(anchor, sessionId) {
  const current = currentMode();
  rbMenu(anchor, MODES.map(([key, label, note]) => [
    label,
    async () => {
      const btn = $('#rbMode');
      if (btn) btn.textContent = '…';
      try {
        const r = await api(`/sessions/${sessionId}/mode`, { method: 'POST', body: { mode: key } });
        toast(`${MODE_LABELS[r.mode] || label} mode`, 'ok');
      } catch (e) {
        toast(e.message, 'bad');
      }
    },
    key === current,
    note,
  ]));
}

// currentMode reads what the bar is showing, which came from the terminal.
function currentMode() {
  const txt = (($('#rbMode') || {}).textContent || '').trim();
  const hit = MODES.find(([, label]) => label === txt);
  return hit ? hit[0] : '';
}

// runSlash sends the CLI one of its own commands.
//
// Its own endpoint, not the message one. A message is confirmed by the
// transcript growing, and a slash command never becomes a turn — so every model
// and effort change here waited out the full delivery budget and then reported
// "the CLI did not accept the message", over a session that had just switched
// perfectly. A red toast on every successful action.
async function runSlash(sessionId, cmd, note) {
  try {
    await api(`/sessions/${sessionId}/command`, { method: 'POST', body: { text: cmd } });
    toast(note, 'ok');
  } catch (e) {
    toast(e.message, 'bad');
  }
}

// ------------------------------------------------- the missing status line

// offerStatusLine explains an empty strip and offers to make it not empty.
//
// The five-hour and weekly figures come out of a JSON payload the CLI hands to
// whatever status-line command an account has configured. With none there is
// nothing printing them, and nowhere else to look: a transcript carries token
// counts per message and no rate limits at all.
//
// So the offer is to install one, and the command is this app's own binary with
// a subcommand that reads that payload and prints a line. Nothing extra to
// install, and it is shown before it is agreed to, because it writes to a
// settings file that belongs to the person who signed in with it.
async function offerStatusLine(sess) {
  if (!sess || !sess.accountId) {
    return toast('No account on this session to configure', 'bad');
  }
  let cur = {};
  try {
    cur = await api(`/accounts/${sess.accountId}/statusline`);
  } catch (e) {
    return toast(e.message, 'bad');
  }

  const body = el('div', {},
    el('p', { style: 'margin-top:0' },
      `Claude Code prints how much of the five-hour window and the weekly allowance you have spent only when the account has a status-line command. `,
      el('strong', { text: sess.accountName || 'This account' }), ' has ',
      cur.configured ? 'one that does not print them.' : 'none, so there is nothing for the strip above the composer to read.'),
    el('p', { class: 'hint' },
      'Those two numbers exist nowhere else. A transcript carries token counts per message and no rate limits, so without a status line there is no source at all.'),
    cur.configured && !cur.ours
      ? el('div', { class: 'pill warn', style: 'margin:10px 0' },
          'This account already has its own status line. Installing this one would replace it.')
      : null,
    cur.configured && !cur.ours
      ? el('pre', { class: 'tool-pre', text: cur.command })
      : null,
    el('label', { text: 'What would be written to settings.json' }),
    el('pre', { class: 'tool-pre', text: cur.wouldInstall || '(this program, with the statusline subcommand)' }),
    el('div', { class: 'hint' },
      'Reversible from here, and it changes nothing else in that file. Agents already running keep the status line they started with, so restart one to see the figures.'));

  const buttons = [['Close', 'btn', closeModal]];
  if (cur.ours) {
    buttons.push(['Remove it', 'btn danger', async () => {
      closeModal();
      try {
        await api(`/accounts/${sess.accountId}/statusline`, { method: 'DELETE' });
        toast('Status line removed', 'ok');
      } catch (e) { toast(e.message, 'bad'); }
    }]);
  } else {
    buttons.push([cur.configured ? 'Replace it' : 'Install it', 'btn primary', async () => {
      closeModal();
      try {
        const r = await api(`/accounts/${sess.accountId}/statusline`,
          { method: 'PUT', body: { replace: !!cur.configured } });
        toast(r.note || 'Status line installed', 'ok');
      } catch (e) { toast(e.message, 'bad'); }
    }]);
  }
  modal('Usage figures for ' + (sess.accountName || 'this account'), body, buttons);
}
