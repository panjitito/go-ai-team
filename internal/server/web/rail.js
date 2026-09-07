/* The files this agent has changed, beside the conversation.
 *
 * It is all in the transcript already — every Write and Edit names its file —
 * but spread through a scrolling conversation and mixed in with everything else
 * the agent did. After twenty minutes the question "what has it changed" is
 * answered by reading back through a hundred tool cards, and "let me look at
 * that file" means leaving the conversation for the Files tab and finding it.
 *
 * So: the list, newest first, next to the thing that produced it. Clicking a
 * file shows its diff without leaving the conversation, because what you almost
 * always want is not the file but what changed in it.
 *
 * Collected from the agent's own tool calls rather than from git. The two answer
 * different questions — git says what differs from the last commit, this says
 * what this agent did — and a file it edited and then reverted belongs here and
 * not in git's answer.
 */
'use strict';

const RAIL = {
  // open is remembered per browser: on a narrow window the rail costs more than
  // it gives, and that is a per-device judgement rather than a shared setting.
  key: 'goaiteam.rail.open',
  sig: '',
};

function railOpen() {
  try {
    // Open by default. The list is the answer to a question people have
    // constantly, and an empty rail takes no room because it is not shown.
    return localStorage.getItem(RAIL.key) !== '0';
  } catch {
    return true;
  }
}

function toggleRail() {
  try {
    localStorage.setItem(RAIL.key, railOpen() ? '0' : '1');
  } catch { /* nothing to remember it in; it still toggles for this session */ }
  RAIL.sig = '';
  const d = CHAT.last;
  if (d) renderRail(d, S.openSession);
}

// renderRail draws the list from the latest poll.
function renderRail(d, sessionId) {
  CHAT.last = d;
  const rail = $('#chatRail');
  if (!rail) return;

  const files = (d && d.files) || [];
  const count = $('#railCount');
  if (count) {
    count.textContent = files.length ? String(files.length) : '';
    count.style.display = files.length ? '' : 'none';
  }

  // Nothing changed yet, or the person closed it: no rail, and the conversation
  // gets the whole width.
  if (!files.length || !railOpen() || S.chatMode !== 'chat') {
    rail.style.display = 'none';
    RAIL.sig = '';
    return;
  }

  const sig = files.map(f => f.rel + ':' + f.edits).join('|');
  if (sig === RAIL.sig) return;
  RAIL.sig = sig;

  rail.style.display = '';
  rail.innerHTML = '';
  rail.append(el('div', { class: 'rail-head' },
    el('span', { text: 'Changed' }),
    el('span', { class: 'rail-n', text: String(files.length) }),
    el('span', { style: 'flex:1' }),
    el('button', {
      class: 'btn ghost sm', title: 'Hide this panel', onclick: toggleRail,
    }, '✕')));

  const list = el('div', { class: 'rail-list' });
  for (const f of files) {
    const away = outsideProject(f.rel);
    list.append(el('div', {
      class: 'rail-item' + (away ? ' away' : ''),
      title: away ? f.path + ' (outside the project)' : f.path,
      onclick: () => openChangedFile(sessionId, f),
    },
      el('span', { class: 'rail-mark' + (f.created ? ' new' : ''), text: f.created ? '+' : '·' }),
      el('span', { class: 'rail-name' },
        el('span', { class: 'rail-file', text: f.name }),
        // The directory is what tells two files of the same name apart, and
        // nothing more, so it is quiet and truncates from the left. For a file
        // outside the project that is the whole path, and saying so is the
        // point: it is why there is no diff to show for it.
        // An outside path is Windows-shaped and has no "/" in it, so it must not
        // depend on dirOf finding one — that is the whole reason it is here.
        (away || dirOf(f.rel))
          ? el('span', { class: 'rail-dir', text: away ? 'outside the project' : dirOf(f.rel) })
          : null),
      f.edits > 1 ? el('span', { class: 'rail-edits', text: '×' + f.edits }) : null));
  }
  rail.append(list);
}

function dirOf(rel) {
  const i = String(rel || '').lastIndexOf('/');
  return i > 0 ? rel.slice(0, i) : '';
}

// openChangedFile shows what changed in it, falling back to the file itself.
//
// The diff first, because that is what you want after an agent has been editing
// — the file in full is the answer to a different question, and is one click
// further on.
async function openChangedFile(sessionId, f) {
  const sess = sessionById(sessionId);
  const projectId = sess && sess.projectId;
  const body = el('div', { id: 'railDiff' }, el('div', { class: 'hint', text: 'Loading…' }));

  const buttons = [['Close', 'btn', closeModal]];
  // Only offered where it can actually be done. The file browser opens paths
  // inside the project and refuses everything else, so putting the button on a
  // file that lives elsewhere is an invitation to an error message.
  if (!outsideProject(f.rel)) {
    buttons.unshift(['Open in Files', 'btn', async () => {
      closeModal();
      const p = projectById(projectId);
      if (!p) return;
      // The Files view has to exist before a file can be opened into its pane.
      S.selectedProject = p.id;
      S.view = 'files';
      leaveAgent();
      render();
      await new Promise(r => setTimeout(r, 60));
      openFile(p, f.rel).catch(e => toast(e.message, 'bad'));
    }]);
  }
  modal(f.name || f.rel, body, buttons, true);

  // A file the agent wrote outside the project — its own memory, something in a
  // sibling directory — has no relative path and never had one. Asking git
  // about it, or asking the file reader that only opens paths inside the
  // project, both end in a refusal that reads like a fault. Say where it is
  // instead of failing at it.
  if (outsideProject(f.rel)) {
    body.innerHTML = '';
    body.append(
      el('div', { class: 'hint', text:
        'This is outside the project, so there is no diff for it here and the ' +
        'file browser will not open it.' }),
      el('pre', { class: 'tool-pre', text: f.rel }));
    return;
  }

  try {
    const res = await fetch(`/api/git/diff?projectId=${encodeURIComponent(projectId)}` +
      `&path=${encodeURIComponent(f.rel)}`);
    const text = await res.text();
    const isDiff = res.headers.get('X-Diff-Kind') === 'diff' || looksLikeDiff(text);
    body.innerHTML = '';
    if (res.ok && isDiff && text.trim()) {
      body.append(renderDiff(text));
      return;
    }
    // Not a diff: committed already, not a repository, or a file git does not
    // track. Show the file — and say why, in the server's own words when it
    // gave them. It used to say "no uncommitted diff" over a project that had
    // no repository at all, which is a different thing and a worse answer.
    const why = res.ok && text.trim() && !isDiff
      ? text.trim()
      : 'No uncommitted diff for this file — showing it as it stands.';
    const data = await api(`/files/read?projectId=${encodeURIComponent(projectId)}` +
      `&path=${encodeURIComponent(f.rel)}`);
    body.append(
      el('div', { class: 'hint', text: why }),
      viewerEl(data.content || '', f.rel));
  } catch (e) {
    body.innerHTML = '';
    body.append(el('div', { class: 'hint', text: e.message }));
  }
}

// outsideProject reports whether a rail entry kept its absolute path, which is
// what the collector does for anything it could not make relative to the
// agent's working directory.
function outsideProject(rel) {
  const s = String(rel || '');
  return /^[A-Za-z]:[\\/]/.test(s) || s.startsWith('/') || s.startsWith('\\\\');
}
