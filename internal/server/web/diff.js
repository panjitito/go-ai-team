/* Reading a diff the way GitHub and VS Code show one.
 *
 * A unified diff coloured by its first character is readable, but it is not what
 * anyone reviews code in: there are no line numbers, so you cannot say where you
 * are; the two versions are interleaved, so a rewritten block is hard to compare;
 * and a one-character change looks exactly like a rewritten line.
 *
 * So the diff is parsed rather than tinted. Line numbers come from the hunk
 * headers, split view puts the two versions side by side, and a line that was
 * edited rather than replaced has the part that actually changed marked inside
 * it — which is the thing that turns "these two lines differ" into "this word
 * differs".
 */
'use strict';

const DIFF = { split: true };

// parseDiff turns a unified diff into hunks of rows.
//
// A row carries both line numbers, so either layout can be rendered from the
// same parse: { kind, oldNo, newNo, oldText, newText }.
function parseDiff(text) {
  const files = [];
  let file = null;
  let hunk = null;
  let oldNo = 0, newNo = 0;

  for (const raw of text.split('\n')) {
    if (raw.startsWith('diff --git')) {
      file = { header: raw, hunks: [] };
      files.push(file);
      hunk = null;
      continue;
    }
    if (raw.startsWith('--- ') || raw.startsWith('+++ ') ||
        raw.startsWith('index ') || raw.startsWith('new file') ||
        raw.startsWith('deleted file') || raw.startsWith('similarity ') ||
        raw.startsWith('rename ') || raw.startsWith('old mode') ||
        raw.startsWith('new mode')) {
      continue;
    }
    const at = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$/.exec(raw);
    if (at) {
      if (!file) { file = { header: '', hunks: [] }; files.push(file); }
      oldNo = parseInt(at[1], 10);
      newNo = parseInt(at[3], 10);
      hunk = { label: (at[5] || '').trim(), rows: [] };
      file.hunks.push(hunk);
      continue;
    }
    if (!hunk) continue;

    if (raw.startsWith('+')) {
      hunk.rows.push({ kind: 'add', newNo: newNo++, newText: raw.slice(1) });
    } else if (raw.startsWith('-')) {
      hunk.rows.push({ kind: 'del', oldNo: oldNo++, oldText: raw.slice(1) });
    } else if (raw.startsWith('\\')) {
      // "\ No newline at end of file" belongs to the previous line.
      continue;
    } else {
      const t = raw.startsWith(' ') ? raw.slice(1) : raw;
      hunk.rows.push({ kind: 'ctx', oldNo: oldNo++, newNo: newNo++, oldText: t, newText: t });
    }
  }
  return files;
}

// pairRows matches each removed line with the added line that replaced it, so a
// split view can show them opposite each other and an edited line can be marked
// word by word. Unmatched removals and additions stand alone.
function pairRows(rows) {
  const out = [];
  for (let i = 0; i < rows.length;) {
    if (rows[i].kind === 'ctx') { out.push({ left: rows[i], right: rows[i] }); i++; continue; }

    const dels = [], adds = [];
    while (i < rows.length && rows[i].kind === 'del') dels.push(rows[i++]);
    while (i < rows.length && rows[i].kind === 'add') adds.push(rows[i++]);

    const n = Math.max(dels.length, adds.length);
    for (let k = 0; k < n; k++) {
      out.push({ left: dels[k] || null, right: adds[k] || null });
    }
  }
  return out;
}

// inlineParts splits two versions of a line into [common prefix, changed middle,
// common suffix]. Cheap, and it catches what people actually do to a line:
// change a word, a number, a name.
function inlineParts(a, b) {
  let p = 0;
  while (p < a.length && p < b.length && a[p] === b[p]) p++;
  let s = 0;
  while (s < a.length - p && s < b.length - p &&
         a[a.length - 1 - s] === b[b.length - 1 - s]) s++;
  return {
    pre: a.slice(0, p),
    aMid: a.slice(p, a.length - s),
    bMid: b.slice(p, b.length - s),
    post: a.slice(a.length - s),
  };
}

// cellText fills one side of a row, marking the changed run when the line was
// edited rather than wholly replaced.
function cellText(host, text, other, kind) {
  if (text === undefined || text === null) return;
  if (!other || kind === 'ctx') {
    host.append(document.createTextNode(text === '' ? ' ' : text));
    return;
  }
  const { pre, aMid, bMid, post } = inlineParts(text, other);
  const mid = kind === 'del' ? aMid : bMid;
  // Nothing in common, or everything: not worth marking a fragment.
  if (!pre && !post) {
    host.append(document.createTextNode(text === '' ? ' ' : text));
    return;
  }
  if (pre) host.append(document.createTextNode(pre));
  if (mid) host.append(el('span', { class: 'dw-' + kind, text: mid }));
  if (post) host.append(document.createTextNode(post));
}

// renderDiff draws a unified diff. Split by default, with a toggle.
function renderDiff(text) {
  const box = el('div', { class: 'diffv' });
  if (!text || !text.trim()) {
    box.append(el('div', { class: 'hint', style: 'padding:14px', text: 'No textual diff (binary, or no change).' }));
    return box;
  }

  const files = parseDiff(text);
  const rowsTotal = files.reduce((n, f) => n + f.hunks.reduce((m, h) => m + h.rows.length, 0), 0);
  if (!rowsTotal) {
    box.append(el('div', { class: 'hint', style: 'padding:14px', text: 'No textual diff (binary, or no change).' }));
    return box;
  }

  let adds = 0, dels = 0;
  for (const f of files) for (const h of f.hunks) for (const r of h.rows) {
    if (r.kind === 'add') adds++; else if (r.kind === 'del') dels++;
  }

  const body = el('div', { class: 'diff-body' });
  const draw = () => {
    body.innerHTML = '';
    for (const f of files) {
      for (const h of f.hunks) {
        body.append(el('div', { class: 'diff-hunk' },
          el('span', { text: hunkRange(h) }),
          h.label ? el('span', { class: 'diff-hunk-label', text: h.label }) : null));
        body.append(DIFF.split ? splitTable(h) : unifiedTable(h));
      }
    }
  };

  const bar = el('div', { class: 'diff-bar' },
    el('span', { class: 'diff-stat' },
      el('span', { class: 'st-add', text: `+${adds}` }),
      el('span', { class: 'st-del', text: `−${dels}` })),
    el('span', { style: 'flex:1' }),
    el('div', { class: 'seg' },
      el('button', {
        class: 'btn sm' + (DIFF.split ? ' primary' : ''),
        onclick: () => { DIFF.split = true; draw(); syncDiffButtons(bar); },
      }, 'Split'),
      el('button', {
        class: 'btn sm' + (DIFF.split ? '' : ' primary'),
        onclick: () => { DIFF.split = false; draw(); syncDiffButtons(bar); },
      }, 'Unified')));

  box.append(bar, body);
  draw();
  return box;
}

function syncDiffButtons(bar) {
  const [split, unified] = bar.querySelectorAll('.seg .btn');
  split.classList.toggle('primary', DIFF.split);
  unified.classList.toggle('primary', !DIFF.split);
}

function hunkRange(h) {
  const olds = h.rows.filter(r => r.oldNo !== undefined).map(r => r.oldNo);
  const news = h.rows.filter(r => r.newNo !== undefined).map(r => r.newNo);
  const a = olds.length ? `${olds[0]}–${olds[olds.length - 1]}` : '—';
  const b = news.length ? `${news[0]}–${news[news.length - 1]}` : '—';
  return `${a}  →  ${b}`;
}

function splitTable(h) {
  const t = el('table', { class: 'diff-table split' });
  for (const { left, right } of pairRows(h.rows)) {
    const tr = el('tr');
    tr.append(
      el('td', { class: 'dn', text: left && left.oldNo !== undefined ? String(left.oldNo) : '' }),
      cell(left, right, 'left'),
      el('td', { class: 'dn', text: right && right.newNo !== undefined ? String(right.newNo) : '' }),
      cell(right, left, 'right'));
    t.append(tr);
  }
  return t;
}

function cell(row, counterpart, side) {
  if (!row) return el('td', { class: 'dc empty' });
  const kind = row.kind;
  const text = side === 'left' ? row.oldText : row.newText;
  const other = counterpart
    ? (side === 'left' ? counterpart.newText : counterpart.oldText)
    : null;
  const td = el('td', { class: 'dc ' + kind });
  // Only mark a fragment when one line replaced one other line.
  cellText(td, text, kind === 'ctx' ? null : other, kind);
  return td;
}

function unifiedTable(h) {
  const t = el('table', { class: 'diff-table unified' });
  for (const r of h.rows) {
    const tr = el('tr');
    const td = el('td', { class: 'dc ' + r.kind });
    const sign = r.kind === 'add' ? '+' : r.kind === 'del' ? '−' : ' ';
    td.append(el('span', { class: 'dsign', text: sign }));
    td.append(document.createTextNode(
      (r.kind === 'add' ? r.newText : r.oldText) || ' '));
    tr.append(
      el('td', { class: 'dn', text: r.oldNo !== undefined ? String(r.oldNo) : '' }),
      el('td', { class: 'dn', text: r.newNo !== undefined ? String(r.newNo) : '' }),
      td);
    t.append(tr);
  }
  return t;
}
