/* Tool calls, rendered as what they are.
 *
 * Every tool card used to show the same two things: the input as raw JSON and
 * the result as raw text. For a Bash call that is nearly right. For an Edit it
 * is a wall of escaped newlines with the actual change buried in it, and for
 * AskUserQuestion it is forty lines of JSON where the interesting part — what
 * was asked and what you answered — is one line at the bottom.
 *
 * So each tool gets a body that suits it: a diff for an edit, the file for a
 * write, the command and its output for a shell call, the questions and their
 * answers for a question. Everything else falls back to a readable list of
 * fields rather than a JSON dump, and the raw text is always one click further
 * on for when the rendering is not what you needed.
 */
'use strict';

// parseInput reads a tool's input, which arrives as pretty-printed JSON.
function parseInput(t) {
  if (!t || !t.input) return null;
  try {
    const v = JSON.parse(t.input);
    return v && typeof v === 'object' ? v : null;
  } catch {
    return null;
  }
}

// toolTitle splits an MCP tool into the server that provides it and its own
// name. "mcp__MSSQL_ReSM__query_ReSM" is unreadable as one word, and the server
// is the part that says where the agent is reaching.
function toolTitle(name) {
  const m = /^mcp__(.+?)__(.+)$/.exec(name || '');
  if (!m) return { name, via: '' };
  return { name: m[2], via: m[1] };
}

// ---------------------------------------------------------------- bodies

function bashBody(t, input) {
  const out = [];
  if (input && input.description) {
    out.push(el('div', { class: 'tv-note', text: input.description }));
  }
  if (input && input.command) {
    out.push(el('pre', { class: 'tv-cmd' }, el('span', { class: 'tv-prompt', text: '$ ' }),
      document.createTextNode(input.command)));
  }
  if (t.result) {
    out.push(el('pre', { class: 'tool-pre' + (t.isError ? ' err' : ''), text: t.result }));
  }
  return out;
}

function writeBody(t, input) {
  const out = [];
  if (!input || typeof input.content !== 'string') return null;
  out.push(el('div', { class: 'tv-note', text: shortish(input.file_path) }));
  // The content, not a JSON string containing the content.
  out.push(viewerEl(input.content, input.file_path || ''));
  return out;
}

function editBody(t, input) {
  if (!input || typeof input.old_string !== 'string' || typeof input.new_string !== 'string') {
    return null;
  }
  return [
    el('div', { class: 'tv-note' },
      shortish(input.file_path),
      input.replace_all ? el('span', { class: 'pill', style: 'margin-left:6px' }, 'every match') : null),
    lineDiff(input.old_string, input.new_string),
  ];
}

function multiEditBody(t, input) {
  if (!input || !Array.isArray(input.edits)) return null;
  const out = [el('div', { class: 'tv-note' }, shortish(input.file_path),
    el('span', { class: 'pill', style: 'margin-left:6px' }, `${input.edits.length} edits`))];
  input.edits.forEach((e, i) => {
    out.push(el('div', { class: 'tv-sub', text: `edit ${i + 1}` }));
    out.push(lineDiff(String(e.old_string || ''), String(e.new_string || '')));
  });
  return out;
}

function readBody(t, input) {
  const out = [];
  if (input) {
    const where = [];
    if (input.offset) where.push('from line ' + input.offset);
    if (input.limit) where.push(input.limit + ' lines');
    out.push(el('div', { class: 'tv-note' }, shortish(input.file_path || input.notebook_path),
      where.length ? el('span', { class: 'pill', style: 'margin-left:6px' }, where.join(' · ')) : null));
  }
  if (t.result) {
    // Read answers with "1\tline", which is a gutter pretending to be text.
    out.push(numberedEl(t.result));
  }
  return out.length ? out : null;
}

function askBody(t, input) {
  if (!input || !Array.isArray(input.questions)) return null;
  const answers = parseAnswers(t.result);
  const out = [];
  for (const q of input.questions) {
    const picked = answers[q.question] || '';
    out.push(el('div', { class: 'tv-q' },
      q.header ? el('span', { class: 'pill' }, q.header) : null,
      el('strong', { text: q.question || '' })));

    const list = el('div', { class: 'tv-opts' });
    for (const o of (q.options || [])) {
      const chosen = picked && o.label && picked.includes(o.label);
      list.append(el('div', { class: 'tv-opt' + (chosen ? ' chosen' : '') },
        el('span', { class: 'tv-tick', text: chosen ? '✓' : '·' }),
        el('span', {},
          el('span', { class: 'tv-opt-label', text: o.label || '' }),
          o.description ? el('span', { class: 'tv-opt-desc', text: o.description }) : null)));
    }
    out.push(list);

    // An answer that matched no option — the "type something" path — still has
    // to show, or the card says a question was asked and never answered.
    if (picked && !(q.options || []).some(o => o.label && picked.includes(o.label))) {
      out.push(el('div', { class: 'tv-answer', text: '✓ ' + picked }));
    }
  }
  if (!Object.keys(answers).length && !t.pending) {
    out.push(el('div', { class: 'hint', text: 'No answer recorded.' }));
  }
  return out;
}

// parseAnswers reads the result line AskUserQuestion writes:
//   Your questions have been answered: "Which?"="This one", "And?"="That".
function parseAnswers(result) {
  const map = {};
  if (!result) return map;
  const re = /"((?:[^"\\]|\\.)*)"\s*=\s*"((?:[^"\\]|\\.)*)"/g;
  let m;
  while ((m = re.exec(result)) !== null) {
    map[unescapeQuoted(m[1])] = unescapeQuoted(m[2]);
  }
  return map;
}

function unescapeQuoted(s) {
  return String(s).replace(/\\(.)/g, '$1');
}

function taskBody(t, input) {
  if (!input) return null;
  const rows = [];
  if (input.subject) rows.push(el('div', { class: 'tv-q' }, el('strong', { text: input.subject })));
  if (input.description) rows.push(el('div', { class: 'tv-note', text: input.description }));
  if (input.taskId || input.status) {
    rows.push(el('div', { class: 'tv-note' },
      input.taskId ? el('span', { class: 'pill' }, 'task ' + input.taskId) : null,
      input.status ? el('span', { class: 'pill', style: 'margin-left:6px' },
        String(input.status).replace(/_/g, ' ')) : null));
  }
  return rows.length ? rows : null;
}

// fieldsBody is the fallback: the input as labelled values rather than JSON.
//
// Long strings still get a block of their own, because a forty-line prompt
// inside a table cell is not readable either.
function fieldsBody(t, input) {
  if (!input) return null;
  const keys = Object.keys(input);
  if (!keys.length) return null;

  const out = [];
  const table = el('table', { class: 'kv tv-fields' });
  for (const k of keys) {
    const v = input[k];
    if (typeof v === 'string' && (v.length > 120 || v.includes('\n'))) {
      out.push(el('div', { class: 'tv-sub', text: k }));
      out.push(el('pre', { class: 'tool-pre', text: v }));
      continue;
    }
    table.append(el('tr', {},
      el('td', { class: 'tv-key', text: k }),
      el('td', { text: typeof v === 'object' ? JSON.stringify(v) : String(v) })));
  }
  if (table.children.length) out.unshift(table);
  return out.length ? out : null;
}

const TOOL_BODY = {
  Bash: bashBody, BashOutput: bashBody, PowerShell: bashBody,
  Write: writeBody,
  Edit: editBody,
  MultiEdit: multiEditBody,
  Read: readBody,
  AskUserQuestion: askBody,
  TaskCreate: taskBody, TaskUpdate: taskBody,
};

// toolBody builds the body for one call, or null to fall back to the raw view.
function toolBody(t) {
  const input = parseInput(t);
  const fn = TOOL_BODY[t.name] || fieldsBody;
  try {
    return fn(t, input);
  } catch {
    // A tool whose shape is not what was expected is not a reason to lose the
    // card; the raw text below still says everything.
    return null;
  }
}

// ---------------------------------------------------------------- pieces

function shortish(p) {
  const s = String(p || '');
  if (s.length <= 64) return s;
  return '…' + s.slice(-63);
}

// numberedEl renders "1\ttext" lines with the number as a gutter.
function numberedEl(text) {
  const wrap = el('div', { class: 'tv-num' });
  const gutter = el('div', { class: 'code-gutter' });
  const body = el('pre', { class: 'code-body' });
  let n = 0;
  for (const line of String(text).split('\n')) {
    const m = /^(\s*\d+)\t(.*)$/.exec(line);
    gutter.append(el('div', { text: m ? m[1].trim() : '' }));
    body.append(el('div', { text: m ? m[2] : line }));
    if (++n > 400) break;
  }
  wrap.append(gutter, body);
  return wrap;
}

// lineDiff shows what an edit changed, line by line.
//
// No line numbers, deliberately. An Edit gives the text before and after and
// says nothing about where in the file it sits, so any number here would be
// invented — and a diff with confident wrong numbers is worse than one with
// none.
function lineDiff(before, after) {
  const a = before.split('\n');
  const b = after.split('\n');
  const rows = diffLines(a, b);

  const host = el('div', { class: 'tv-diff' });
  let shown = 0;
  for (const r of rows) {
    if (shown >= 300) {
      host.append(el('div', { class: 'tv-diff-more', text: `… ${rows.length - shown} more lines` }));
      break;
    }
    shown++;
    host.append(el('div', { class: 'tv-row ' + r.kind },
      el('span', { class: 'tv-sign', text: r.kind === 'add' ? '+' : r.kind === 'del' ? '−' : ' ' }),
      el('span', { class: 'tv-text', text: r.text })));
  }
  return host;
}

// diffLines is a longest-common-subsequence diff over lines.
//
// Bounded: the table is quadratic, and an edit that replaces a thousand lines
// with another thousand is not worth a million cells to describe. Past the
// bound it degrades to "all of this went, all of that arrived", which is what a
// wholesale replacement is anyway.
function diffLines(a, b) {
  const MAX = 600;
  if (a.length > MAX || b.length > MAX) {
    return [...a.map(t => ({ kind: 'del', text: t })), ...b.map(t => ({ kind: 'add', text: t }))];
  }

  const n = a.length, m = b.length;
  const lcs = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1));
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      lcs[i][j] = a[i] === b[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
    }
  }

  const rows = [];
  let i = 0, j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) { rows.push({ kind: 'ctx', text: a[i] }); i++; j++; }
    else if (lcs[i + 1][j] >= lcs[i][j + 1]) { rows.push({ kind: 'del', text: a[i] }); i++; }
    else { rows.push({ kind: 'add', text: b[j] }); j++; }
  }
  while (i < n) rows.push({ kind: 'del', text: a[i++] });
  while (j < m) rows.push({ kind: 'add', text: b[j++] });
  return rows;
}
