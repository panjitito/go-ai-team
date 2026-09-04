/* Browsing and editing the project's files.
 *
 * Not an IDE. There is no project-wide search here and no language server —
 * this is what you reach for when a review turns up a typo and opening a second
 * editor to fix one line is absurd.
 *
 * The one thing it takes seriously is that something else is writing these files
 * at the same time. Every file is loaded with the hash of what was read, and a
 * save carries that hash back; if an agent has rewritten the file since, the
 * server refuses rather than overwriting, and you are told so with your text
 * still in the box.
 */
'use strict';

const FILES = {
  // open directories, by project-relative path.
  expanded: new Set(),
  // the file on screen: { path, sha, original, readOnly, binary }
  current: null,
  editing: false,
  dirty: false,
};

async function viewFiles(main) {
  const p = projectById(S.selectedProject);
  if (!p) return main.append(el('div', { class: 'empty', text: 'Pick a project first.' }));

  main.append(viewHeader(p, 'Files',
    el('span', { class: 'path', text: p.path })));

  const wrap = el('div', { class: 'files' });
  const tree = el('div', { class: 'files-tree', id: 'filesTree' });
  const pane = el('div', { class: 'files-pane', id: 'filesPane' });
  wrap.append(tree, pane);
  main.append(wrap);

  pane.append(el('div', { class: 'empty' },
    el('h3', { text: 'Nothing open' }),
    el('p', { text: 'Pick a file on the left to read it. Changed files are marked.' })));

  await mountDir(tree, p, '', 0);
}

// mountDir renders one directory's children into a container, lazily.
async function mountDir(host, p, path, depth) {
  let data;
  try {
    data = await api(`/files/tree?projectId=${encodeURIComponent(p.id)}&path=${encodeURIComponent(path)}`);
  } catch (e) {
    host.append(el('div', { class: 'hint', style: 'padding:8px', text: e.message }));
    return;
  }
  for (const e of (data.entries || [])) {
    host.append(e.dir ? dirRow(p, e, depth) : fileRow(p, e, depth));
  }
  if (!(data.entries || []).length) {
    host.append(el('div', { class: 'hint', style: `padding:4px 0 4px ${12 + depth * 13}px`, text: 'empty' }));
  }
}

function dirRow(p, e, depth) {
  const open = FILES.expanded.has(e.path);
  const caret = el('span', { class: 'tw-caret', text: open ? '▾' : '▸' });
  const kids = el('div', { class: 'tw-kids' });
  const row = el('div', {
    class: 'tw-row dir',
    style: `padding-left:${6 + depth * 13}px`,
    onclick: async () => {
      const nowOpen = !FILES.expanded.has(e.path);
      if (nowOpen) {
        FILES.expanded.add(e.path);
        caret.textContent = '▾';
        kids.innerHTML = '';
        await mountDir(kids, p, e.path, depth + 1);
      } else {
        FILES.expanded.delete(e.path);
        caret.textContent = '▸';
        kids.innerHTML = '';
      }
    },
  }, caret, el('span', { class: 'tw-name', text: e.name }));

  const box = el('div', {}, row, kids);
  if (open) mountDir(kids, p, e.path, depth + 1);
  return box;
}

function fileRow(p, e, depth) {
  const row = el('div', {
    class: 'tw-row file' + (e.status ? ' changed' : ''),
    style: `padding-left:${20 + depth * 13}px`,
    'data-path': e.path,
    onclick: () => openFile(p, e.path),
  },
    el('span', { class: 'tw-name', text: e.name }),
    e.status ? el('span', { class: 'tw-status', text: e.status }) : null);
  return row;
}

// ---------------------------------------------------------------- one file

async function openFile(p, path) {
  if (FILES.dirty && !confirm('Discard the unsaved changes in ' + FILES.current.path + '?')) return;

  for (const r of document.querySelectorAll('.tw-row.file')) {
    r.classList.toggle('active', r.getAttribute('data-path') === path);
  }
  const pane = $('#filesPane');
  pane.innerHTML = '';
  pane.append(el('div', { class: 'hint', style: 'padding:16px', text: 'Opening…' }));

  let f;
  try {
    f = await api(`/files/read?projectId=${encodeURIComponent(p.id)}&path=${encodeURIComponent(path)}`);
  } catch (e) {
    pane.innerHTML = '';
    pane.append(el('div', { class: 'hint', style: 'padding:16px', text: e.message }));
    return;
  }

  FILES.current = { path, sha: f.sha, original: f.content || '', readOnly: f.readOnly };
  FILES.editing = false;
  FILES.dirty = false;
  renderFile(p, f);
}

function renderFile(p, f) {
  const pane = $('#filesPane');
  pane.innerHTML = '';

  const status = el('span', { class: 'hint', id: 'fileStatus', text: '' });
  const head = el('div', { class: 'file-head' },
    el('strong', { class: 'file-name', text: FILES.current.path }),
    el('span', { class: 'pill', text: fmtBytes(f.size || 0) }),
    status,
    el('span', { style: 'flex:1' }));

  if (f.readOnly) {
    head.append(el('span', { class: 'pill', text: f.binary ? 'binary' : 'too large' }));
    pane.append(head, el('div', { class: 'empty' },
      el('p', { text: f.reason || 'This file is not editable here.' })));
    return;
  }

  const editBtn = el('button', { class: 'btn sm', onclick: () => toggleEdit(p, f) },
    FILES.editing ? 'Done' : 'Edit');
  const saveBtn = el('button', {
    class: 'btn primary sm', id: 'fileSave', style: FILES.editing ? '' : 'display:none',
    onclick: () => saveFile(p),
  }, 'Save');
  const revertBtn = el('button', {
    class: 'btn sm', id: 'fileRevert', style: FILES.editing ? '' : 'display:none',
    onclick: () => { setBody(FILES.current.original); markDirty(false); },
  }, 'Revert');
  head.append(revertBtn, saveBtn, editBtn);
  pane.append(head);

  pane.append(FILES.editing ? editorEl() : viewerEl(FILES.current.original, FILES.current.path));
}

// viewerEl shows the file read-only, with line numbers and light highlighting.
function viewerEl(text, path) {
  const lines = text.split('\n');
  const gutter = el('div', { class: 'code-gutter' });
  const body = el('pre', { class: 'code-body' });
  const lang = langOf(path);
  lines.forEach((line, i) => {
    gutter.append(el('div', { class: 'ln', text: String(i + 1) }));
    const row = el('div', { class: 'cl' });
    highlightInto(row, line, lang);
    body.append(row);
  });
  const box = el('div', { class: 'code-wrap' }, gutter, body);
  // One scroller: the gutter follows the code rather than having its own bar.
  body.addEventListener('scroll', () => { gutter.scrollTop = body.scrollTop; });
  return box;
}

// editorEl is a plain textarea with a matching gutter.
//
// Deliberately not a highlighted overlay. Keeping a transparent textarea aligned
// to a coloured layer underneath depends on identical font metrics, wrapping and
// padding in every browser, and when it drifts you are typing into text that is
// one pixel away from what you can see. Reading is highlighted; editing is
// honest.
function editorEl() {
  const ta = el('textarea', {
    class: 'code-edit', id: 'fileEditor', spellcheck: 'false', wrap: 'off',
  });
  ta.value = FILES.current.original;
  const gutter = el('div', { class: 'code-gutter', id: 'editGutter' });
  const paint = () => {
    const n = ta.value.split('\n').length;
    gutter.innerHTML = '';
    for (let i = 1; i <= n; i++) gutter.append(el('div', { class: 'ln', text: String(i) }));
  };
  paint();
  ta.addEventListener('input', () => {
    paint();
    markDirty(ta.value !== FILES.current.original);
  });
  ta.addEventListener('scroll', () => { gutter.scrollTop = ta.scrollTop; });
  // Tab indents rather than leaving the field, which is what anyone editing code
  // expects and what a browser does not do.
  ta.addEventListener('keydown', e => {
    if (e.key === 'Tab') {
      e.preventDefault();
      const s = ta.selectionStart, t = ta.selectionEnd;
      ta.value = ta.value.slice(0, s) + '\t' + ta.value.slice(t);
      ta.selectionStart = ta.selectionEnd = s + 1;
      ta.dispatchEvent(new Event('input'));
    }
    if ((e.ctrlKey || e.metaKey) && e.key === 's') {
      e.preventDefault();
      const p = projectById(S.selectedProject);
      if (p) saveFile(p);
    }
  });
  return el('div', { class: 'code-wrap' }, gutter, ta);
}

function setBody(text) {
  const ta = $('#fileEditor');
  if (ta) { ta.value = text; ta.dispatchEvent(new Event('input')); }
}

function markDirty(on) {
  FILES.dirty = on;
  const st = $('#fileStatus');
  if (st) st.textContent = on ? 'unsaved changes' : '';
}

function toggleEdit(p, f) {
  if (FILES.editing && FILES.dirty &&
      !confirm('Discard the unsaved changes in ' + FILES.current.path + '?')) return;
  FILES.editing = !FILES.editing;
  FILES.dirty = false;
  renderFile(p, f);
}

async function saveFile(p) {
  const ta = $('#fileEditor');
  if (!ta || !FILES.current) return;
  const body = {
    projectId: p.id, path: FILES.current.path, content: ta.value, sha: FILES.current.sha,
  };
  try {
    const res = await api('/files/write', { method: 'PUT', body });
    FILES.current.sha = res.sha;
    FILES.current.original = ta.value;
    markDirty(false);
    toast('Saved ' + FILES.current.path, 'ok');
  } catch (e) {
    // The conflict case is the one that matters: an agent wrote this file while
    // it was open. Say so plainly and leave the text alone — it is the only copy
    // of what was typed.
    toast(e.message, 'bad');
  }
}

// ---------------------------------------------------------------- highlight

// A small tokenizer. Not a parser and not trying to be: strings, comments,
// numbers and keywords are what makes code readable at a glance, and a real
// highlighter would be more code than this whole feature.
const KEYWORDS = {
  go: 'func var const type struct interface map chan go defer return if else for range switch case default break continue package import nil true false error string int int64 bool byte rune',
  js: 'function const let var return if else for while switch case break continue new class extends async await import export from default null undefined true false typeof instanceof this try catch finally throw',
  py: 'def class return if elif else for while import from as with try except finally raise lambda None True False and or not in is pass yield async await self',
  sh: 'if then else fi for while do done case esac function return export local echo set',
  css: '',
  json: 'true false null',
  md: '',
};

function langOf(path) {
  const m = /\.([A-Za-z0-9]+)$/.exec(path || '');
  const ext = m ? m[1].toLowerCase() : '';
  if (ext === 'go') return 'go';
  if (['js', 'mjs', 'cjs', 'ts', 'tsx', 'jsx'].includes(ext)) return 'js';
  if (['py'].includes(ext)) return 'py';
  if (['sh', 'bash', 'zsh'].includes(ext)) return 'sh';
  if (['json'].includes(ext)) return 'json';
  if (['css'].includes(ext)) return 'css';
  if (['md', 'markdown'].includes(ext)) return 'md';
  return '';
}

// highlightInto appends spans for one line. Builds nodes rather than assigning
// innerHTML, because this is the contents of an arbitrary file.
function highlightInto(row, line, lang) {
  if (line === '') { row.append(document.createTextNode(' ')); return; }
  if (!lang) { row.append(document.createTextNode(line)); return; }

  const words = new Set((KEYWORDS[lang] || '').split(/\s+/).filter(Boolean));
  const push = (text, cls) => {
    if (!text) return;
    row.append(cls ? el('span', { class: 'tk-' + cls, text }) : document.createTextNode(text));
  };

  // A whole-line comment is the common case and worth short-circuiting.
  const trimmed = line.trimStart();
  const lineComment = (lang === 'py' || lang === 'sh') ? '#' : '//';
  if (trimmed.startsWith(lineComment)) { push(line, 'com'); return; }

  // Split into strings, numbers, words and everything else.
  const re = /("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|`(?:[^`\\]|\\.)*`|\b\d[\d_.xXa-fA-F]*\b|[A-Za-z_$][\w$]*)/g;
  let last = 0, m;
  while ((m = re.exec(line)) !== null) {
    push(line.slice(last, m.index), null);
    const tok = m[0];
    if (/^["'`]/.test(tok)) push(tok, 'str');
    else if (/^\d/.test(tok)) push(tok, 'num');
    else if (words.has(tok)) push(tok, 'kw');
    else push(tok, null);
    last = m.index + tok.length;
  }
  push(line.slice(last), null);
}
