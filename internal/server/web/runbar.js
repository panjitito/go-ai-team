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
    el('span', { class: 'rb-sep' }),
    el('span', { class: 'rb-stat', id: 'rbCtx', title: 'Context window used' }),
    el('span', { class: 'rb-stat', id: 'rbCost', title: 'Cost of this session' }),
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
function updateRunBar(d) {
  const bar = $('#runBar');
  if (!bar || !d) return;
  const line = d.line || {};

  const model = $('#rbModel');
  if (model) model.textContent = line.model || '—';

  const eff = $('#rbEffort');
  if (eff) eff.textContent = RUNBAR.effort || 'effort';

  setStat('#rbCtx', line.hasContext, () => `ctx ${line.context}%`);
  setStat('#rbCost', line.hasCost, () => '$' + Number(line.cost).toFixed(2));
  setMeter('#rb5h', line.hasFiveHour, line.fiveHour);
  setMeter('#rbWk', line.hasWeekly, line.weekly);
}

function setStat(sel, has, text) {
  const n = $(sel);
  if (!n) return;
  // A field the status line does not print is hidden rather than shown empty or
  // as a guessed zero.
  n.style.display = has ? '' : 'none';
  if (has) n.textContent = text();
}

function setMeter(sel, has, pct) {
  const n = $(sel);
  if (!n) return;
  n.style.display = has ? '' : 'none';
  if (!has) return;
  const v = Math.max(0, Math.min(100, Number(pct) || 0));
  n.querySelector('.rb-meter-track i').style.width = v + '%';
  n.querySelector('.rb-meter-pct').textContent = v + '%';
  n.classList.toggle('warn', v >= 75 && v < 90);
  n.classList.toggle('hot', v >= 90);
}

// ---------------------------------------------------------------- menus

function rbMenu(anchor, items) {
  for (const old of document.querySelectorAll('.rb-menu')) old.remove();
  const menu = el('div', { class: 'rb-menu' });
  for (const [label, onPick, active] of items) {
    menu.append(el('button', {
      class: 'rb-menu-item' + (active ? ' active' : ''),
      onclick: () => { menu.remove(); onPick(); },
    }, label));
  }
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
  rbMenu(anchor, switchableModels().map(([alias, label]) => [
    label,
    () => runSlash(sessionId, '/model ' + alias, `Switching to ${label}…`),
    current.toLowerCase().startsWith(label.toLowerCase()),
  ]));
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

// runSlash sends the CLI one of its own commands.
//
// Through the same delivery path as a message, so it gets the same confirmation:
// a slash command that silently failed to arrive would leave the bar showing a
// model that was never selected.
async function runSlash(sessionId, cmd, note) {
  try {
    await api(`/sessions/${sessionId}/input`, { method: 'POST', body: { data: cmd, enter: true } });
    toast(note, 'ok');
  } catch (e) {
    toast(e.message, 'bad');
  }
}
