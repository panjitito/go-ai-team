/* Completing a file path from the composer.
 *
 * Claude Code completes paths when you type "@". This box had no equivalent, so
 * naming a file meant knowing its path exactly and typing all of it — and a path
 * with a typo in it is worse than none, because the agent goes looking and
 * reports that the file does not exist.
 *
 * The list comes from git where there is a repository, so it is the project's
 * files and not its node_modules. Arrow keys move, Enter or Tab accepts, Escape
 * dismisses — and Enter only accepts while the list is open, so the key that
 * sends a message still sends it the rest of the time.
 */
'use strict';

const MENTION = {
  box: null,
  menu: null,
  items: [],
  active: 0,
  // start is where the "@" sits, so accepting knows what to replace.
  start: -1,
  seq: 0,
  wired: false,
};

// wireMentions attaches path completion to a composer.
function wireMentions(box, projectId) {
  if (!box || !projectId) return;
  box.addEventListener('input', () => refreshMentions(box, projectId));
  box.addEventListener('blur', () => setTimeout(closeMentions, 150));
  installMentionKeys();
}

// The key handler lives on the document, in the capture phase, and is installed
// exactly once.
//
// Not on the textarea: listeners on the element the event is aimed at run in the
// order they were added regardless of the capture flag, and the composer's own
// "Enter sends" handler is added first. Capturing at the document is genuinely
// earlier, which is what lets Enter accept a highlighted path instead of sending
// half a sentence.
function installMentionKeys() {
  if (MENTION.wired) return;
  MENTION.wired = true;
  document.addEventListener('keydown', e => {
    if (!MENTION.menu || !MENTION.items.length) return;
    if (e.target !== MENTION.box) return closeMentions();
    mentionKeydown(e);
  }, true);
}

// mentionQuery finds the "@word" the cursor is inside, if any.
//
// Only at a word boundary: an email address or a decorator in pasted code has an
// "@" in the middle of a word and is not a file reference.
function mentionQuery(box) {
  const upto = box.value.slice(0, box.selectionStart);
  const at = upto.lastIndexOf('@');
  if (at < 0) return null;
  if (at > 0 && !/\s/.test(upto[at - 1])) return null;
  const frag = upto.slice(at + 1);
  if (/\s/.test(frag)) return null;
  return { start: at, text: frag };
}

async function refreshMentions(box, projectId) {
  const q = mentionQuery(box);
  if (!q) return closeMentions();

  const seq = ++MENTION.seq;
  let rows = [];
  try {
    rows = await api(`/files/find?projectId=${encodeURIComponent(projectId)}` +
      `&q=${encodeURIComponent(q.text)}&limit=12`) || [];
  } catch {
    return closeMentions();
  }
  // A slower earlier request must not overwrite a newer answer.
  if (seq !== MENTION.seq) return;
  if (!rows.length) return closeMentions();

  MENTION.box = box;
  MENTION.items = rows;
  MENTION.active = 0;
  MENTION.start = q.start;
  drawMentions();
}

function drawMentions() {
  closeMentions(true);
  const menu = el('div', { class: 'mention-menu' });
  MENTION.items.forEach((r, i) => {
    const dir = r.path.slice(0, r.path.length - r.name.length);
    menu.append(el('button', {
      class: 'mention-item' + (i === MENTION.active ? ' active' : ''),
      // mousedown, not click: the composer's blur would close the list first.
      onmousedown: e => { e.preventDefault(); acceptMention(i); },
    },
      el('span', { class: 'mention-name', text: r.name }),
      dir ? el('span', { class: 'mention-dir', text: dir }) : null));
  });
  document.body.append(menu);
  MENTION.menu = menu;

  const r = MENTION.box.getBoundingClientRect();
  menu.style.left = Math.max(8, r.left) + 'px';
  // Above the box: the composer is at the bottom of the window, so a list
  // dropping downwards would open off screen.
  menu.style.top = Math.max(8, r.top - menu.offsetHeight - 6) + 'px';
  menu.style.width = Math.min(r.width, 520) + 'px';
}

function closeMentions(keepState) {
  if (MENTION.menu) { MENTION.menu.remove(); MENTION.menu = null; }
  if (!keepState) { MENTION.items = []; MENTION.start = -1; }
}

function mentionKeydown(e) {
  switch (e.key) {
    case 'ArrowDown':
      e.preventDefault(); e.stopPropagation();
      MENTION.active = (MENTION.active + 1) % MENTION.items.length;
      return drawMentions();
    case 'ArrowUp':
      e.preventDefault(); e.stopPropagation();
      MENTION.active = (MENTION.active - 1 + MENTION.items.length) % MENTION.items.length;
      return drawMentions();
    case 'Enter':
    case 'Tab':
      e.preventDefault(); e.stopPropagation();
      return acceptMention(MENTION.active);
    case 'Escape':
      e.preventDefault(); e.stopPropagation();
      return closeMentions();
  }
}

// acceptMention replaces the typed fragment with the chosen path.
function acceptMention(i) {
  const row = MENTION.items[i];
  const box = MENTION.box;
  if (!row || !box || MENTION.start < 0) return closeMentions();

  const before = box.value.slice(0, MENTION.start);
  const after = box.value.slice(box.selectionStart);
  const insert = '@' + row.path + ' ';
  box.value = before + insert + after;
  const caret = before.length + insert.length;
  box.setSelectionRange(caret, caret);
  closeMentions();
  box.dispatchEvent(new Event('input'));
  box.focus();
}
