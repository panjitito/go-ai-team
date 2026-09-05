/* Go to anything, from the keyboard.
 *
 * The premise of this app is running a lot of agents at once, and the cost of
 * that is getting to the right one: pick the project in the tree, find the card
 * in the grid, click it. Three deliberate movements, repeated all day, and worse
 * when the panel is collapsed.
 *
 * Ctrl-K lists every agent, every project and every panel, filtered as you type.
 * Agents come first and carry their state, because "which one wanted me" is the
 * question being asked most of the time — an agent waiting on an answer sorts
 * above one that is working, which sorts above one that is idle.
 *
 * Matching is a subsequence, so "bkw" finds "Backend worker" without anyone
 * having to remember the exact words. Ranking prefers a run of letters at the
 * start of a word, which is how people actually abbreviate.
 */
'use strict';

const PALETTE = { open: false, items: [], active: 0, node: null };

function openPalette() {
  if (PALETTE.open) return;
  PALETTE.open = true;
  PALETTE.active = 0;

  const input = el('input', {
    type: 'text', id: 'palInput', autocomplete: 'off', spellcheck: 'false',
    placeholder: 'Go to an agent, a project, or a panel…',
  });
  const list = el('div', { class: 'pal-list', id: 'palList' });
  const node = el('div', { class: 'pal-host', id: 'palHost' },
    el('div', { class: 'pal' },
      el('div', { class: 'pal-input' }, input),
      list,
      el('div', { class: 'pal-foot' },
        el('span', { text: '↑↓ to move · Enter to open · Esc to close' }))));

  node.addEventListener('mousedown', e => { if (e.target === node) closePalette(); });
  document.body.append(node);
  PALETTE.node = node;

  input.addEventListener('input', () => paintPalette(input.value));
  input.addEventListener('keydown', palKeydown);
  paintPalette('');
  input.focus();
}

function closePalette() {
  if (PALETTE.node) PALETTE.node.remove();
  PALETTE.node = null;
  PALETTE.open = false;
  PALETTE.items = [];
}

function palKeydown(e) {
  if (e.key === 'Escape') { e.preventDefault(); return closePalette(); }
  if (e.key === 'ArrowDown' || (e.ctrlKey && e.key.toLowerCase() === 'n')) {
    e.preventDefault();
    PALETTE.active = Math.min(PALETTE.active + 1, PALETTE.items.length - 1);
    return paintActive();
  }
  if (e.key === 'ArrowUp' || (e.ctrlKey && e.key.toLowerCase() === 'p')) {
    e.preventDefault();
    PALETTE.active = Math.max(PALETTE.active - 1, 0);
    return paintActive();
  }
  if (e.key === 'Enter') {
    e.preventDefault();
    const it = PALETTE.items[PALETTE.active];
    if (it) { closePalette(); it.run(); }
  }
}

// paletteItems is everything worth going to.
function paletteItems() {
  const items = [];

  for (const a of S.agents) {
    const sess = liveSession(a.id);
    const proj = projectById(a.projectId);
    const state = sess
      ? (sess.needsYou ? 'needs you'
        : sess.status === 'working' ? 'working' : sess.status)
      : 'not running';
    items.push({
      kind: 'agent',
      // Rank order within a tie: the ones wanting something first.
      urgency: sess ? (sess.needsYou ? 0 : sess.status === 'working' ? 1 : 2) : 3,
      title: a.name,
      note: (proj ? proj.name : '') + ' · ' + state,
      hay: a.name + ' ' + (proj ? proj.name : '') + ' ' + (a.role || ''),
      badge: state,
      dot: sess ? (sess.needsYou ? 'waiting' : sess.status) : '',
      run: () => {
        if (sess) { openTerm(sess.id); return; }
        S.selectedProject = a.projectId;
        startAgent(a);
      },
    });
  }

  for (const p of S.projects) {
    items.push({
      kind: 'project', urgency: 4,
      title: p.name, note: p.path, hay: p.name + ' ' + p.path,
      run: () => {
        S.selectedProject = p.id;
        if (S.view === 'term') { leaveAgent(); S.view = S.lastTab || 'grid'; }
        render();
      },
    });
  }

  const panels = [
    ['Accounts', openAccounts], ['Prompts', openPrompts], ['Skills', openSkills],
    ['Memory', openMemory], ['Roles', openRoles], ['Automation', openAutomation],
    ['Environment', openEnvironment], ['MCP servers', openMCP],
    ['Process guard', openGuard], ['Doctor', openDoctor], ['Settings', openSettings],
  ];
  for (const [name, fn] of panels) {
    items.push({ kind: 'panel', urgency: 5, title: name, note: 'panel', hay: name, run: fn });
  }
  for (const [id, label] of TABS) {
    items.push({
      kind: 'view', urgency: 6, title: label, note: 'view', hay: label,
      run: () => { if (S.view === 'term') leaveAgent(); S.view = id; S.lastTab = id; render(); },
    });
  }
  return items;
}

function paintPalette(query) {
  const all = paletteItems();
  const q = query.trim().toLowerCase();

  let hits;
  if (!q) {
    hits = all.map(it => ({ it, score: 0 }));
  } else {
    hits = [];
    for (const it of all) {
      const score = fuzzyScore(it.hay.toLowerCase(), q);
      if (score >= 0) hits.push({ it, score });
    }
  }
  hits.sort((a, b) => (a.score - b.score) || (a.it.urgency - b.it.urgency) ||
    a.it.title.localeCompare(b.it.title));

  PALETTE.items = hits.slice(0, 40).map(h => h.it);

  // Whatever was typed is also a thing to look for. Somewhere near half the
  // time the answer is not "go to that agent" but "what did it say about this",
  // and the palette is where the hands already are.
  if (q) {
    PALETTE.items.push({
      kind: 'search', urgency: 9,
      title: 'Search conversations', note: '“' + query.trim() + '”',
      run: () => openFind(query.trim()),
    });
  }
  PALETTE.active = 0;

  const list = $('#palList');
  list.innerHTML = '';
  // Nothing here by that name still deserves saying — but with the offer to go
  // and look for it in what the agents actually said, which is where a word the
  // app has never heard of usually lives.
  if (!hits.length) {
    list.append(el('div', { class: 'pal-empty', text: 'Nothing matches that.' }));
  }
  PALETTE.items.forEach((it, i) => {
    list.append(el('div', {
      class: 'pal-item' + (i === PALETTE.active ? ' active' : ''),
      onmousedown: e => { e.preventDefault(); closePalette(); it.run(); },
      onmousemove: () => { if (PALETTE.active !== i) { PALETTE.active = i; paintActive(); } },
    },
      el('span', { class: 'pal-kind', text: it.kind }),
      it.dot ? el('span', { class: 'dot ' + it.dot }) : null,
      el('span', { class: 'pal-title', text: it.title }),
      el('span', { class: 'pal-note', text: it.note || '' })));
  });
}

function paintActive() {
  const rows = [...document.querySelectorAll('.pal-item')];
  rows.forEach((n, i) => n.classList.toggle('active', i === PALETTE.active));
  const on = rows[PALETTE.active];
  if (on) on.scrollIntoView({ block: 'nearest' });
}

// fuzzyScore matches a query as a subsequence and scores how well.
//
// Lower is better. A letter that begins a word costs nothing, a letter directly
// after the previous match costs little, and a letter found somewhere further
// along costs the distance — so "bkw" ranks "Backend worker" above a name that
// merely contains those letters scattered through it. Returns -1 for no match.
function fuzzyScore(hay, needle) {
  let score = 0;
  let at = 0;
  for (const ch of needle) {
    if (ch === ' ') continue;
    const i = hay.indexOf(ch, at);
    if (i < 0) return -1;
    const atWordStart = i === 0 || /[\s\-_/.]/.test(hay[i - 1]);
    score += atWordStart ? 0 : (i === at ? 1 : 2 + Math.min(i - at, 20));
    at = i + 1;
  }
  // A shorter haystack is a closer match for the same letters.
  return score + hay.length / 200;
}

// Ctrl-K, and Ctrl-P for the same reason every editor has both. Bound on the
// document in the capture phase so it works from inside the composer too, which
// is where the cursor usually is.
document.addEventListener('keydown', e => {
  if (!(e.ctrlKey || e.metaKey) || e.altKey || e.shiftKey) return;
  const k = e.key.toLowerCase();
  if (k !== 'k' && k !== 'p') return;
  e.preventDefault();
  e.stopPropagation();
  PALETTE.open ? closePalette() : openPalette();
}, true);
