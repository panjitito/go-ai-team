/* Searching everything the agents have ever said.
 *
 * The transcripts are the record — every message, every tool call, every result
 * — and until now the only way back into any of it was to remember which
 * conversation it was and still have that session running. So "one of them
 * worked this out last week, I just don't know which" had no answer at all.
 *
 * On Enter rather than on every keystroke: the scan is a real pass over real
 * files, quick enough to wait for and much too expensive to repeat per letter.
 * The footer says how much was actually read, because "nothing found" and
 * "nothing found in the part I had time for" are different answers and the
 * second one deserves to be said out loud.
 */
'use strict';

const FIND = { open: false, node: null, q: '', scope: 'project', busy: false };

function openFind(initial) {
  if (FIND.open) {
    if (initial) { $('#findInput').value = initial; runFind(); }
    return;
  }
  FIND.open = true;

  const input = el('input', {
    type: 'text', id: 'findInput', autocomplete: 'off', spellcheck: 'false',
    placeholder: 'Search every conversation…', value: initial || '',
  });
  const list = el('div', { class: 'find-list', id: 'findList' });
  const foot = el('div', { class: 'find-foot', id: 'findFoot' });

  const node = el('div', { class: 'pal-host find-host', id: 'findHost' },
    el('div', { class: 'find' },
      el('div', { class: 'find-bar' },
        input,
        el('div', { class: 'seg' },
          el('button', { class: 'btn sm', id: 'findScopeP', onclick: () => setFindScope('project') },
            'This project'),
          el('button', { class: 'btn sm', id: 'findScopeA', onclick: () => setFindScope('all') },
            'Everywhere')),
        el('button', { class: 'btn sm primary', onclick: () => runFind() }, 'Search')),
      list, foot));

  node.addEventListener('mousedown', e => { if (e.target === node) closeFind(); });
  document.body.append(node);
  FIND.node = node;

  input.addEventListener('keydown', e => {
    if (e.key === 'Escape') { e.preventDefault(); return closeFind(); }
    if (e.key === 'Enter') { e.preventDefault(); runFind(); }
  });

  paintFindScope();
  foot.textContent = 'Type what you are looking for and press Enter.';
  input.focus();
  input.select();
  if (initial) runFind();
}

function closeFind() {
  if (FIND.node) FIND.node.remove();
  FIND.node = null;
  FIND.open = false;
}

function setFindScope(s) {
  FIND.scope = s;
  paintFindScope();
  if ($('#findInput').value.trim()) runFind();
}

function paintFindScope() {
  const p = $('#findScopeP'), a = $('#findScopeA');
  if (!p) return;
  p.classList.toggle('primary', FIND.scope === 'project');
  a.classList.toggle('primary', FIND.scope === 'all');
}

async function runFind() {
  const q = $('#findInput').value.trim();
  const list = $('#findList'), foot = $('#findFoot');
  if (!q) { list.innerHTML = ''; foot.textContent = 'Type what you are looking for and press Enter.'; return; }
  if (FIND.busy) return;

  FIND.busy = true;
  FIND.q = q;
  list.innerHTML = '';
  foot.textContent = 'Searching…';

  const proj = FIND.scope === 'project' ? projectById(S.selectedProject) : null;
  const qs = '/search?q=' + encodeURIComponent(q) +
    (proj ? '&projectId=' + encodeURIComponent(proj.id) : '');

  let d;
  try {
    d = await tryApi(qs);
  } finally {
    FIND.busy = false;
  }
  if (!FIND.open) return;      // closed while it ran
  d = d || {};
  const hits = d.hits || [];

  if (!hits.length) {
    list.append(el('div', { class: 'pal-empty', text: 'Nothing found.' }));
  }
  for (const h of hits) list.append(findRow(h, q));
  foot.textContent = findFooter(d, hits.length, proj);
}

function findFooter(d, n, proj) {
  const where = proj ? proj.name : 'every project';
  const mb = (d.bytes || 0) / 1048576;
  const size = mb >= 1 ? mb.toFixed(0) + ' MB' : Math.round((d.bytes || 0) / 1024) + ' kB';
  let s = `${n} ${n === 1 ? 'match' : 'matches'} in ${where} · read ${d.files || 0}`;
  s += ` of ${d.total || 0} conversations (${size}) in ${d.millis || 0} ms`;
  if (d.truncated) {
    s += ' · stopped early, so older conversations were not read';
  }
  return s;
}

function findRow(h, q) {
  const when = h.when ? new Date(h.when) : null;
  const who = h.agentName || (h.role === 'user' ? 'You' : 'Assistant');
  const body = el('div', { class: 'find-snip' }, ...highlight(h.snippet || '', q));

  const row = el('div', { class: 'find-item' + (h.liveSession ? ' go' : '') },
    el('div', { class: 'find-meta' },
      el('span', { class: 'pill' }, h.role || '—'),
      // Said by a subagent, which is a different claim from said by the agent:
      // most of the transcripts on disk are these, and the conversation itself
      // records only that one ran.
      h.sub ? el('span', { class: 'pill', title: h.sub }, 'subagent') : null,
      el('span', { class: 'find-who', text: who }),
      when ? el('span', { class: 'find-when mono', text: whenLabel(when) }) : null,
      // Which project, when the answer could have come from any of them. The
      // conversation records the directory it ran in, so this is the real one
      // and not a guess reversed out of a folder name.
      h.projectName && FIND.scope !== 'project'
        ? el('span', { class: 'find-proj', text: h.projectName, title: h.cwd || '' }) : null,
      h.account ? el('span', { class: 'find-acct', text: h.account }) : null,
      el('span', { class: 'spacer', style: 'flex:1' }),
      h.liveSession
        ? el('button', {
            class: 'btn sm', onclick: e => { e.stopPropagation(); closeFind(); openTerm(h.liveSession); },
          }, 'Open agent')
        : el('span', { class: 'find-old', text: 'conversation ' + h.sessionId.slice(0, 8) })),
    body);

  // The snippet is a window; the message is the thing. Clicking opens it out
  // rather than sending you somewhere, because most of the time reading it is
  // all you wanted.
  let full = null;
  row.onclick = () => {
    if (full) { full.remove(); full = null; return; }
    full = el('pre', { class: 'find-full', text: h.text || h.snippet || '' });
    row.append(full);
  };
  return row;
}

function whenLabel(d) {
  const days = Math.round((Date.now() - d.getTime()) / 86400000);
  const hhmm = String(d.getHours()).padStart(2, '0') + ':' + String(d.getMinutes()).padStart(2, '0');
  if (days <= 0) return 'today ' + hhmm;
  if (days === 1) return 'yesterday ' + hhmm;
  return d.toLocaleDateString(undefined, { day: 'numeric', month: 'short' }) + ' ' + hhmm;
}

// highlight returns the snippet as nodes with every occurrence marked. Built
// from text nodes rather than innerHTML, so a transcript that contains angle
// brackets stays a transcript.
function highlight(text, q) {
  const out = [];
  const low = text.toLowerCase(), needle = q.toLowerCase();
  let at = 0;
  while (needle) {
    const i = low.indexOf(needle, at);
    if (i < 0) break;
    if (i > at) out.push(document.createTextNode(text.slice(at, i)));
    out.push(el('mark', { text: text.slice(i, i + needle.length) }));
    at = i + needle.length;
  }
  out.push(document.createTextNode(text.slice(at)));
  return out;
}

// Ctrl-Shift-F, which is "find in files" in every editor that has one. Plain
// Ctrl-F is left alone: it is how you find something on the page in front of
// you, and taking it would be taking something that already worked.
document.addEventListener('keydown', e => {
  if (!(e.ctrlKey || e.metaKey) || !e.shiftKey || e.altKey) return;
  if (e.key.toLowerCase() !== 'f') return;
  e.preventDefault();
  e.stopPropagation();
  FIND.open ? closeFind() : openFind(window.getSelection ? String(getSelection()).trim() : '');
}, true);
