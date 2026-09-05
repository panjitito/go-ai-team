/* The keyboard, written down.
 *
 * There are enough shortcuts here now that the ones nobody stumbles across are
 * the ones nobody uses, and a shortcut nobody uses may as well not exist. This
 * is the list, opened with "?" the way every application with a keyboard has
 * done since before any of this.
 *
 * It is written by hand rather than collected from the handlers, which means it
 * can go out of date — so the test reads the handlers and checks that every
 * combination the app actually binds appears here.
 */
'use strict';

const KEYS = [
  ['Getting around', [
    ['Ctrl K', 'Go to an agent, a project or a panel'],
    ['Ctrl P', 'The same list, for the other half of the world'],
    ['Ctrl Shift F', 'Search everything the agents have said'],
    ['Ctrl B', 'Collapse the projects panel, or bring it back'],
    ['Esc', 'Close whatever is open'],
  ]],
  ['In the palette and the menus', [
    ['↑ ↓', 'Move'],
    ['Enter', 'Choose'],
    ['Ctrl N / Ctrl P', 'Move, without leaving the home row'],
  ]],
  ['Talking to an agent', [
    ['Enter', 'Send'],
    ['Shift Enter', 'A new line instead'],
    ['@', 'A file in the project, completed as you type'],
    ['Esc', 'Interrupt the turn, leaving the session running'],
  ]],
  ['Editing a file', [
    ['Ctrl S', 'Save'],
    ['Tab', 'An actual tab, rather than the next field'],
  ]],
  ['This list', [
    ['?', 'Open it'],
  ]],
];

function openKeys() {
  const body = el('div', { class: 'keys' });
  for (const [group, rows] of KEYS) {
    body.append(el('div', { class: 'keys-group', text: group }));
    const t = el('table', { class: 'keys-table' });
    for (const [combo, what] of rows) {
      t.append(el('tr', {},
        el('td', { class: 'keys-combo' },
          combo.split(' ').map(k => el('kbd', { text: k }))),
        el('td', { text: what })));
    }
    body.append(t);
  }
  body.append(el('div', { class: 'hint', style: 'margin-top:14px' },
    'Desktop alerts, for when an agent needs you while you are somewhere else, ' +
    'are in Settings.'));
  modal('Keyboard', body, [['Close', 'btn primary', closeModal]]);
}

// "?" — but only when it is a question and not a character being typed. The
// composer is where the cursor spends most of its life and a help panel opening
// mid-sentence would be worse than no help panel.
document.addEventListener('keydown', e => {
  if (e.ctrlKey || e.metaKey || e.altKey) return;
  if (e.key !== '?' && e.key !== 'F1') return;
  if (typingInto(e.target)) return;
  e.preventDefault();
  openKeys();
});

function typingInto(node) {
  if (!node) return false;
  const tag = (node.tagName || '').toLowerCase();
  return tag === 'input' || tag === 'textarea' || tag === 'select' || node.isContentEditable;
}
