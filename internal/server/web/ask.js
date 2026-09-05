/* Answering the CLI's permission prompts without leaving the conversation.
 *
 * "Do you want to create hello.txt?" is the single most frequent thing Claude
 * Code says, and until now the conversation view could only tell you it had been
 * said and point at another tab. The prompt is drawn by the CLI, not written to
 * the transcript, so there was nothing to render — but it is on the terminal,
 * and the terminal is right here.
 *
 * Clicking an option writes that digit into the pty. It is the same keystroke a
 * person sitting at the terminal would send, answered by the CLI in its own
 * interface: nothing here decides anything on your behalf, and nothing reaches
 * around the permission system.
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
  return ask.question + '|' + ask.options.map(o => o.number + o.label + (o.selected ? '*' : '')).join('~');
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
      el('span', { class: 'ask-label', text: o.label })));
  }

  return el('div', { class: 'ask' },
    el('div', { class: 'ask-q' }, el('span', { class: 'ask-icon', text: '🔐' }),
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
