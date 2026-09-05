/* Answering the CLI's questions without leaving the conversation.
 *
 * Two kinds, and the same panel serves both. "Do you want to create hello.txt?"
 * is the most frequent thing Claude Code says; the picker AskUserQuestion draws
 * is the one where the agent wants a decision from you before it carries on.
 * Neither reaches the transcript — both are drawn to the terminal — so the
 * conversation could only say that something had been asked and point at
 * another tab.
 *
 * Clicking an option moves the CLI's own highlight onto that row and presses
 * Enter, which is what a person at the terminal does. It is checked before the
 * Enter goes out: the cursor's position is read back off the screen first.
 * Nothing here decides anything on your behalf, and nothing reaches around the
 * permission system.
 */
'use strict';

const ASK = {
  // sig is what is currently drawn, so the panel is not rebuilt under the
  // pointer every 1.5 seconds — which would eat the click you were making.
  sig: '',
  // busy stops a second click while the first is in flight.
  busy: false,
};

function askSig(ask) {
  if (!ask) return '';
  return (ask.steps || '') + '|' + ask.question + '|' +
    ask.options.map(o => o.number + o.label + (o.selected ? '*' : '')).join('~');
}

// renderAsk draws, updates or removes the panel. Returns true when a question
// is on screen, so the caller knows not to also show the "go to the terminal"
// banner or the working indicator.
function renderAsk(d, sessionId) {
  const host = $('#chatBanner');
  if (!host) return false;
  const ask = d && d.ask;
  const sig = askSig(ask);

  if (!ask) {
    if (ASK.sig) {
      ASK.sig = '';
      host.innerHTML = '';
      host.style.display = 'none';
      host.classList.remove('as-ask');
    }
    return false;
  }
  if (sig === ASK.sig) return true;

  ASK.sig = sig;
  ASK.busy = false;
  host.style.display = '';
  host.innerHTML = '';
  // The slot is shared with the amber "answer this in the terminal" banner,
  // which brings its own frame. Two nested boxes around one question looks like
  // a mistake, so the host's chrome steps aside while the panel is in it.
  host.classList.add('as-ask');
  host.append(askPanel(ask, sessionId));
  return true;
}

function askPanel(ask, sessionId) {
  const buttons = el('div', { class: 'ask-options' });
  for (const o of ask.options) {
    buttons.append(el('button', {
      class: 'ask-opt' + (o.selected ? ' selected' : '') + (isNo(o) ? ' no' : ''),
      onclick: e => answerAsk(sessionId, o.number, e.currentTarget),
    },
      el('span', { class: 'ask-num', text: String(o.number) }),
      el('span', { class: 'ask-label' },
        el('span', { text: o.label }),
        // The picker prints this under the option, and it is often the part
        // that decides the answer.
        o.description ? el('span', { class: 'ask-desc', text: o.description }) : null)));
  }

  return el('div', { class: 'ask' },
    // The step strip, when the agent asked more than one thing. Without it the
    // second question arrives as a surprise after answering the first.
    ask.steps ? el('div', { class: 'ask-steps', text: ask.steps }) : null,
    el('div', { class: 'ask-q' },
      el('span', { class: 'ask-icon', text: ask.steps ? '💬' : '🔐' }),
      el('strong', { text: ask.question })),
    buttons,
    el('div', { class: 'ask-foot' },
      el('span', { class: 'hint', text: 'Answered in the CLI, exactly as pressing the key would.' }),
      ask.cancel ? el('button', {
        class: 'btn ghost sm', title: 'Escape — dismiss the question and say what to do instead',
        onclick: () => interruptSession(sessionId, 'Cancelled'),
      }, 'Esc') : null));
}

// isNo marks the declining option so it does not look like the other three.
function isNo(o) {
  return /^no\b/i.test(o.label || '');
}

async function answerAsk(sessionId, n, btn) {
  if (ASK.busy) return;
  ASK.busy = true;
  if (btn) btn.classList.add('picked');
  try {
    await api(`/sessions/${sessionId}/answer`, { method: 'POST', body: { option: n } });
    // Clear it now rather than waiting for the next poll: the question has been
    // answered and leaving the buttons up invites a second, stale click.
    ASK.sig = '';
    const host = $('#chatBanner');
    if (host) { host.innerHTML = ''; host.style.display = 'none'; }
    CHAT.awaiting = true;
    CHAT.sentAt = Date.now();
  } catch (e) {
    ASK.busy = false;
    if (btn) btn.classList.remove('picked');
    toast(e.message, 'bad');
  }
}

// openRewind puts the CLI's own rewind picker on screen.
//
// A hand-off, not a reimplementation, and that is the point rather than a
// shortcut. The picker is an arrow-key list, not a numbered box, so it cannot
// become buttons the way a permission prompt can — and what it does is restore
// your files to an earlier point. Choosing the wrong row loses work. The CLI's
// own list, driven by the person, is the version of this worth having; all this
// does is save typing the command and switching tab.
async function openRewind(sessionId) {
  S.chatMode = 'term';
  applyChatMode(sessionId);
  try {
    await api(`/sessions/${sessionId}/command`, { method: 'POST', body: { text: '/rewind' } });
  } catch (e) {
    toast(e.message, 'bad');
    return;
  }
  toast('Pick a point with the arrow keys, then Enter', 'ok');
}

// interruptSession stops the current turn and leaves the session running.
//
// The missing half of Stop. Stop kills the process and takes the conversation
// with it, which is far more than you want when an agent has simply gone off in
// the wrong direction — the CLI's own answer to that is Escape.
async function interruptSession(sessionId, note) {
  try {
    await api(`/sessions/${sessionId}/interrupt`, { method: 'POST', body: {} });
    toast(note || 'Interrupted', 'ok');
    CHAT.awaiting = false;
    stopThinkingClock();
  } catch (e) {
    toast(e.message, 'bad');
  }
}
