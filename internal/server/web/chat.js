/* The conversation view.
 *
 * The terminal shows what the CLI drew. This shows what actually happened,
 * rendered from the transcript: prose as prose, tool calls as cards you can
 * open, code in real code blocks. The terminal is still there behind a toggle,
 * because some things — the trust prompt, a permission request, /login — are
 * drawn straight to it and answered with the keyboard.
 *
 * There is exactly one input on screen. The composer writes to the PTY, which is
 * how the CLI receives anything; the terminal's own prompt is only visible in
 * terminal mode, so the two can never appear stacked.
 */
'use strict';

// Icons by tool. A glanceable shape beats reading the tool name every time.
const TOOL_ICON = {
  Read: '👁', Write: '✎', Edit: '✎', MultiEdit: '✎', NotebookEdit: '✎',
  Bash: '❯', BashOutput: '❯', PowerShell: '❯', Glob: '🔍', Grep: '🔍',
  WebFetch: '🌐', WebSearch: '🌐', Task: '🤖', Agent: '🤖',
  TodoWrite: '☑', TaskCreate: '☑', TaskUpdate: '☑', ExitPlanMode: '📋',
  AskUserQuestion: '💬', ToolSearch: '🔍', SendUserFile: '📎',
};

const CHAT = {
  poll: null,
  lastSig: '',
  atBottom: true,
  // awaiting covers the gap between pressing Send and the first poll noticing
  // that the agent has started. sentAt bounds it so it cannot stick.
  awaiting: false,
  sentAt: 0,
  // lastId is the newest turn the last poll saw; sentAfter is what that was when
  // Send was pressed. Comparing the two is how a reply to this message is told
  // from the one that was already on screen.
  lastId: '',
  sentAfter: '',
  // thinkingSince drives the elapsed count; thinkingTimer is its ticker.
  thinkingSince: 0,
  thinkingTimer: null,
};

// conversational reports whether a session has a transcript to render. Only a
// real agent does; a sign-in PTY and a dev command are terminals and nothing
// else.
function conversational(sess) {
  return sess && sess.kind === 'agent';
}

// openAgent replaces the old terminal-first view: chat by default, terminal on
// demand, one composer for both.
function openAgent(sessionId, mode) {
  const sess = sessionById(sessionId);
  if (!sess) return;
  disposePanes();
  if (S.view !== 'term') S.lastTab = S.view;
  S.openSession = sessionId;
  S.view = 'term';

  // The conversation view is rendered from the transcript the CLI writes, and
  // only a Claude agent has one. A sign-in and a dev command do not, so opening
  // them in chat showed an empty pane with the terminal hidden behind it — and
  // signing in is done *in* that terminal. "Type /login and press Enter" then
  // appeared to do nothing, because the thing being typed into was not on
  // screen. Those open on the terminal, which is their real interface.
  //
  // The chosen mode is remembered separately from the effective one, so being
  // forced to the terminal here does not silently change what agents open in.
  if (mode) S.chatPref = mode;
  S.chatMode = conversational(sess) ? (S.chatPref || 'chat') : 'term';

  stopChatPoll();
  closeTermSocket();

  const main = $('#main');
  main.innerHTML = '';

  const agent = sess.agentId ? agentById(sess.agentId) : null;
  const title = agent ? agent.name : (sess.label || (sess.kind === 'login' ? 'Sign in' : 'Terminal'));

  // The conversation and the file rail sit side by side; the header and the
  // composer span both, because one names the session and the other is where you
  // type — neither belongs to a column.
  const wrap = el('div', { class: 'chat-wrap' },
    chatHeader(sess, title),
    el('div', { class: 'chat-split' },
      el('div', { class: 'chat-col' },
        el('div', { class: 'chat-banner', id: 'chatBanner', style: 'display:none' }),
        el('div', { class: 'chat-body', id: 'chatBody' }),
        el('div', { class: 'term-host', id: 'termHost', style: 'display:none' })),
      el('div', { class: 'chat-rail', id: 'chatRail', style: 'display:none' })),
    composer(sessionId));
  main.append(wrap);
  // After mounting, so the drop zone can find the conversation area.
  wireDropZone(sessionId);
  attachReset();

  // The scroll position decides whether new messages pull the view down. A
  // person reading back through history should not be yanked to the bottom
  // every time a tool call lands.
  const body = $('#chatBody');
  body.addEventListener('scroll', () => {
    CHAT.atBottom = body.scrollHeight - body.scrollTop - body.clientHeight < 80;
  });

  applyChatMode(sessionId);
}

function applyChatMode(sessionId) {
  const chat = S.chatMode === 'chat';
  $('#chatBody').style.display = chat ? '' : 'none';
  $('#termHost').style.display = chat ? 'none' : '';

  // Exactly one input, in both modes. The terminal has its own prompt, so
  // leaving the composer on screen next to it is the stacked-input problem
  // again — and worse than cosmetic here: the composer delivers text as a
  // bracketed paste, which is not how you answer a TUI prompt or type a slash
  // command. In terminal mode you type in the terminal.
  const comp = document.querySelector('.composer-wrap');
  if (comp) comp.style.display = chat ? '' : 'none';

  // The banner belongs to the conversation view only. It exists to say "this
  // question is not in the transcript, go and look at the terminal" — so on the
  // terminal it is answering a question you are already looking at, while
  // repeating its text and taking half the screen. It was also never hidden on
  // the way in, so whatever it last said stayed pinned above the terminal.
  const bnr = $('#chatBanner');
  if (bnr && !chat) bnr.style.display = 'none';

  for (const b of document.querySelectorAll('[data-mode]')) {
    b.classList.toggle('primary', b.getAttribute('data-mode') === S.chatMode);
  }
  if (chat) {
    closeTermSocket();
    CHAT.lastSig = '';
    startChatPoll(sessionId);
  } else {
    stopChatPoll();
    mountTerminal(sessionId);
  }
}

function chatHeader(sess, title) {
  const sessionId = sess.id;
  return el('div', { class: 'chat-head' },
    el('button', {
      class: 'btn ghost sm',
      onclick: () => { leaveAgent(); S.view = S.lastTab || 'grid'; render(); },
    }, '←'),
    el('strong', { class: 'chat-title', text: title }),
    el('span', { class: 'pill', id: 'chatStatus' },
      el('span', { class: 'dot ' + sess.status }), sess.status),
    el('span', { class: 'pill', title: sess.accountDir || '' },
      el('span', { class: 'acct-dot', style: `background:${sess.accountColor || '#3a4250'}` }),
      sess.accountName || 'system default'),
    endpointBadge(sess),
    el('span', { style: 'flex:1' }),

    el('span', {
      class: 'token-badge', id: 'termTokens', title: 'Session monitor',
      onclick: () => openUsage(sessionId),
    }, '—'),

    conversational(sess) ? el('div', { class: 'seg' },
      el('button', { class: 'btn sm', 'data-mode': 'chat', onclick: () => { S.chatPref = 'chat'; S.chatMode = 'chat'; applyChatMode(sessionId); } }, 'Chat'),
      el('button', { class: 'btn sm', 'data-mode': 'term', onclick: () => { S.chatPref = 'term'; S.chatMode = 'term'; applyChatMode(sessionId); } }, 'Terminal')) : null,

    el('button', { class: 'btn ghost sm', id: 'hushBtn', style: 'display:none', title: 'Stop speaking', onclick: () => Voice.hush() }, '⏹'),
    el('button', { class: 'btn ghost sm', title: 'Read the last answer aloud (Shift for a condensed read)', onclick: e => readAloud(sessionId, e.shiftKey) }, '🔊'),
    // Not in a window that is already this agent's own: the button would open
    // a second window onto what you are already looking at.
    POPOUT.session === sessionId ? null : el('button', {
      class: 'btn ghost sm', title: 'Open this agent in its own window',
      onclick: () => popOutSession(sessionId),
    }, '⧉'),
    sess.agentId ? el('button', { class: 'btn ghost sm', title: 'Change role, keep the conversation', onclick: () => morphAgent(sess) }, '⟳') : null,
    sess.agentId ? el('button', { class: 'btn ghost sm', title: 'Fork a twin with this conversation', onclick: () => forkAgent(sess) }, '⑃') : null,
    sess.agentId ? el('button', { class: 'btn ghost sm', title: 'Run on another account', onclick: () => manualSwitch(sess) }, '↻') : null,
    conversational(sess) ? el('button', {
      class: 'btn ghost sm', title: 'Rewind — restore the code and conversation to an earlier point',
      onclick: () => openRewind(sessionId),
    }, '⟲') : null,
    conversational(sess) ? el('button', {
      class: 'btn ghost sm', id: 'railBtn', title: 'Files this agent has changed',
      onclick: toggleRail,
    }, '📄', el('span', { class: 'rail-badge', id: 'railCount', style: 'display:none' })) : null,
    // Interrupt is not Stop, and putting them side by side is the point: one
    // ends the turn, the other ends the session. It only appears while there is
    // a turn to interrupt.
    el('button', {
      class: 'btn sm', id: 'interruptBtn', style: 'display:none',
      title: 'Stop this turn and keep the session (Escape)',
      onclick: () => interruptSession(sessionId),
    }, 'Interrupt'),
    el('button', { class: 'btn danger sm', title: 'End the session', onclick: () => stopSession(sessionId) }, 'Stop'));
}

function composer(sessionId) {
  const box = el('textarea', {
    id: 'composerBox', rows: '1',
    placeholder: 'Message this agent…  (Enter to send, Shift+Enter for a new line)',
  });
  box.addEventListener('keydown', e => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); sendComposer(sessionId); }
  });
  box.addEventListener('input', () => {
    box.style.height = 'auto';
    box.style.height = Math.min(box.scrollHeight, 180) + 'px';
  });
  wireAttachments(box, sessionId);
  // "@" completes a path from this project's files.
  const sess = sessionById(sessionId);
  wireMentions(box, sess && sess.projectId);

  return el('div', { class: 'composer-wrap' },
    runBarEl(sessionId),
    el('div', { class: 'attach-strip', id: 'attachStrip', style: 'display:none' }),
    el('div', { class: 'composer' },
    el('div', { class: 'composer-side' },
      el('button', { class: 'btn ghost sm', title: 'Prompt library', onclick: openPrompts }, '📋'),
      el('button', {
        class: 'btn ghost sm', id: 'voiceBtn', title: 'Dictate',
        onclick: () => Voice.dictate((text, done) => {
          const b = $('#composerBox');
          if (!b) return;
          b.value = text;
          b.dispatchEvent(new Event('input'));
          if (done) b.focus();
        }),
      }, '🎤')),
    box,
    el('div', { class: 'composer-side' },
      el('button', { class: 'btn sm', title: 'Right-size the model before sending', onclick: () => adaptiveCheck(sessionId) }, '⚖'),
      el('button', { class: 'btn primary', onclick: () => sendComposer(sessionId) }, 'Send'))));
}

// ---------------------------------------------------------------- polling

function startChatPoll(sessionId) {
  const tick = async () => {
    if (S.openSession !== sessionId || S.chatMode !== 'chat') return;
    try {
      const d = await api(`/sessions/${sessionId}/conversation`);
      renderConversation(d, sessionId);
    } catch { /* a poll failure is not worth a toast every 1.5s */ }
  };
  tick();
  CHAT.poll = setInterval(tick, 1500);
}

function stopChatPoll() {
  if (CHAT.poll) { clearInterval(CHAT.poll); CHAT.poll = null; }
}

function leaveAgent() {
  CHAT.awaiting = false;
  stopThinkingClock();
  stopChatPoll();
  closeTermSocket();
  S.openSession = null;
}

function renderConversation(d, sessionId) {
  updateTermTokens(d);
  // The session too: the run bar's explanation for an empty strip is about the
  // account, and the conversation payload is about the conversation.
  updateRunBar(d, sessionById(sessionId));
  const all = d.messages || [];
  CHAT.lastId = all.length ? (all[all.length - 1].id || '') : '';
  renderRail(d, sessionId);
  const banner = $('#chatBanner');
  const body = $('#chatBody');
  if (!banner || !body) return;

  // A permission prompt becomes buttons. Everything else the CLI draws itself —
  // the trust dialog, the sign-in code — still gets the banner below, because
  // those cannot be reduced to a numbered choice.
  const asking = renderAsk(d, sessionId);
  const stopBtn = $('#interruptBtn');
  if (stopBtn) stopBtn.style.display = d.status === 'working' ? '' : 'none';

  // The terminal-prompt banner: without it, a trust prompt looks like a hang.
  // It shares a slot with the panel above, so it only runs when there is no
  // question to answer here.
  if (asking) {
    // nothing to add: the panel owns the banner while a question is up.
  } else if (d.needsTerminal) {
    banner.style.display = '';
    banner.innerHTML = '';
    banner.append(
      el('div', { class: 'banner-main' },
        el('strong', { text: d.promptTitle || 'It is waiting for an answer in the terminal' }),
        el('div', { class: 'hint', text: 'This one is drawn by the CLI itself, so it has to be answered there.' })),
      d.promptText ? el('pre', { class: 'banner-pre', text: d.promptText }) : null,
      el('button', {
        class: 'btn primary sm',
        onclick: () => { S.chatMode = 'term'; applyChatMode(sessionId); },
      }, 'Answer in the terminal →'));
  } else {
    banner.style.display = 'none';
  }

  // "waiting" is what the status says either way, but only one of the two is
  // waiting on *you*, and that is worth naming.
  const st = $('#chatStatus');
  if (st) {
    st.innerHTML = asking
      ? '<span class="dot waiting"></span> needs you'
      : `<span class="dot ${d.status}"></span> ${d.status}`;
  }

  // Repaint only when something actually changed: rebuilding the list every
  // 1.5 seconds would fight text selection and lose scroll position.
  // Every field is read defensively. This runs before anything is drawn, so a
  // missing one does not produce a small glitch — it throws, the poll swallows
  // it, and the view silently stops updating.
  const sig = JSON.stringify(d.messages.map(m => [
    m.id, (m.blocks || []).length,
    (m.blocks || []).map(b => b.kind === 'tool'
      ? ((b.tool || {}).pending ? 'p' : 'd') + ((b.tool || {}).result || '').length
      : (b.text || '').length).join(','),
  ]));
  if (sig === CHAT.lastSig) {
    // Nothing new to draw, but the agent may well be working — and the whole
    // point of the indicator is the stretch where nothing is being drawn.
    syncThinking(d);
    return;
  }
  CHAT.lastSig = sig;

  const open = new Set([...body.querySelectorAll('.tool.open')].map(n => n.dataset.id));
  body.innerHTML = '';

  if (!d.messages.length) {
    body.append(el('div', { class: 'chat-empty' },
      el('div', { class: 'chat-empty-title', text: d.ready ? 'Nothing said yet' : 'Starting the agent…' }),
      el('div', { class: 'hint', text: d.ready
        ? 'Type below to give it something to do.'
        : 'The conversation appears here as soon as the CLI writes its first turn.' })));
    return;
  }

  const list = el('div', { class: 'msgs' });
  for (const m of d.messages) list.append(messageEl(m, open));
  body.append(list);
  syncThinking(d);

  if (CHAT.atBottom) body.scrollTop = body.scrollHeight;
}

// ------------------------------------------------------------ the wait

/* Showing that the agent is working.
 *
 * Between pressing Send and the first words coming back there can be a long
 * silence: the CLI is thinking, or reading files, and none of that reaches the
 * transcript until a turn is written. The conversation view showed nothing at
 * all in that gap — the message you had just sent, and then stillness — while
 * the terminal tab, one click away, was visibly busy. That is the worst possible
 * split: the app looks broken precisely when it is working hardest.
 *
 * The indicator is deliberately outside the repaint gate. The list is only
 * rebuilt when the transcript changes, and the whole point here is the stretch
 * where the transcript is not changing.
 */

// syncThinking shows or hides the indicator from the latest poll.
function syncThinking(d) {
  // A question on screen is the agent waiting on you, not working. Leaving the
  // indicator spinning over a panel of buttons says the opposite of the truth.
  if (d.ask) {
    CHAT.awaiting = false;
    showThinking(false, '');
    return;
  }
  const working = d.status === 'working' || d.status === 'starting';

  // Once the server agrees the agent is busy, its status drives everything and
  // the optimistic flag set on send has done its job.
  if (working) CHAT.awaiting = false;

  // The flag covers the gap before the first poll notices. It cannot stick: a
  // *new* assistant turn clears it, and so does a wait long enough that nothing
  // can plausibly still be starting.
  //
  // "New" is by identity, not by clock. This used to accept any assistant turn
  // stamped within a second of the send, so sending a follow-up just after the
  // agent finished cleared the flag on the very next poll — the indicator
  // appeared and vanished again before the CLI had even been handed the message.
  // The transcript's timestamp and the browser's clock are two different clocks
  // besides, which is a poor thing to compare across.
  if (CHAT.awaiting) {
    const last = d.messages && d.messages.length ? d.messages[d.messages.length - 1] : null;
    const replied = last && last.role === 'assistant' && last.id && last.id !== CHAT.sentAfter;
    if (replied || Date.now() - CHAT.sentAt > 30000) CHAT.awaiting = false;
  }

  showThinking(working || CHAT.awaiting, activityOf(d));
}

// activityOf names what the agent is doing, when the transcript says.
function activityOf(d) {
  const msgs = (d && d.messages) || [];
  for (let i = msgs.length - 1; i >= 0; i--) {
    const blocks = msgs[i].blocks || [];
    for (let j = blocks.length - 1; j >= 0; j--) {
      const b = blocks[j];
      if (b.kind === 'tool' && b.tool && b.tool.pending) return b.tool.name;
    }
    // Only the most recent turn is worth inspecting; older tools are finished.
    if (msgs[i].role === 'assistant') break;
  }
  return '';
}

// showThinking creates, updates or removes the indicator.
function showThinking(on, activity) {
  const body = $('#chatBody');
  if (!body) return;
  let node = body.querySelector('.thinking');

  if (!on) {
    if (node) node.remove();
    stopThinkingClock();
    return;
  }

  if (!node) {
    CHAT.thinkingSince = CHAT.thinkingSince || Date.now();
    node = el('div', { class: 'thinking' },
      el('div', { class: 'thinking-dots' }, el('i'), el('i'), el('i')),
      el('span', { class: 'thinking-what' }),
      el('span', { class: 'thinking-for' }));
    body.append(node);
    startThinkingClock();
    if (CHAT.atBottom) body.scrollTop = body.scrollHeight;
  }
  node.querySelector('.thinking-what').textContent = activity ? activity : 'Working';
  paintThinkingClock();
}

// The elapsed count ticks locally once a second. Driving it from the 1.5s poll
// would make it stutter and skip, which reads as the app being stuck.
function startThinkingClock() {
  if (CHAT.thinkingTimer) return;
  CHAT.thinkingTimer = setInterval(paintThinkingClock, 1000);
}

function stopThinkingClock() {
  if (CHAT.thinkingTimer) { clearInterval(CHAT.thinkingTimer); CHAT.thinkingTimer = null; }
  CHAT.thinkingSince = 0;
}

function paintThinkingClock() {
  const node = document.querySelector('.thinking .thinking-for');
  if (!node || !CHAT.thinkingSince) return;
  const s = Math.max(0, Math.round((Date.now() - CHAT.thinkingSince) / 1000));
  node.textContent = s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`;
}

function messageEl(m, openSet) {
  const isUser = m.role === 'user';
  const head = el('div', { class: 'msg-head' },
    el('span', { class: 'msg-who', text: isUser ? 'You' : 'Claude' }),
    m.model ? el('span', { class: 'msg-model', text: shortModel(m.model) }) : null,
    m.when ? el('span', { class: 'msg-when', text: new Date(m.when).toLocaleTimeString() }) : null);

  const bodyEl = el('div', { class: 'msg-body' });
  for (const b of m.blocks) {
    if (b.kind === 'text') bodyEl.append(MD.render(b.text));
    else if (b.kind === 'image') bodyEl.append(imageEl(b));
    // An empty one is what the current CLI writes: the block is there, the
    // reasoning is not, only a signature. A card with nothing in it is worse
    // than no card.
    else if (b.kind === 'thinking') { if ((b.text || '').trim()) bodyEl.append(thinkingEl(b, openSet)); }
    else if (b.tool) bodyEl.append(toolEl(b.tool, openSet));
  }
  return el('div', { class: 'msg ' + (isUser ? 'user' : 'assistant') }, head, bodyEl);
}

// thinkingEl renders the model's reasoning, folded.
//
// Claude Code shows it and keeps it behind a keystroke; this used to drop it
// outright, so a long stretch of reasoning looked like nothing happening at all.
// Folded, because it is context when you go looking and noise when you are
// reading the answer — and the first line is enough to decide which.
//
// It reuses the tool card's open/closed set, so unfolding one survives the
// repaint that lands a second later.
function thinkingEl(b, openSet) {
  const id = 'think:' + (b.text || '').length + ':' + (b.text || '').slice(0, 24);
  const open = openSet.has(id);
  const card = el('div', { class: 'tool think' + (open ? ' open' : ''), 'data-id': id });
  card.append(
    el('div', { class: 'tool-head', onclick: () => card.classList.toggle('open') },
      el('span', { class: 'tool-icon', text: '✻' }),
      el('span', { class: 'tool-name', text: 'thinking' }),
      el('span', { class: 'tool-sum', text: firstSentence(b.text) }),
      el('span', { style: 'flex:1' }),
      el('span', { class: 'tool-chev', text: '▾' })),
    el('div', { class: 'tool-detail' }, el('div', { class: 'think-body' }, MD.render(b.text))));
  return card;
}

function firstSentence(s) {
  const line = String(s || '').trim().split('\n').find(l => l.trim()) || '';
  return line.length > 140 ? line.slice(0, 140) + '…' : line;
}

// imageEl renders a pasted image inside a message.
//
// Sending a screenshot puts its path into the prompt, because that is how the
// CLI is handed a picture. Showing the message back as that path meant you sent
// an image and got a filename — no way to tell at a glance which screenshot went,
// or whether the right one did. The picture is shown instead, named, and
// clicking it opens it full size.
function imageEl(b) {
  const img = el('img', {
    class: 'msg-img', src: b.url, alt: b.name || 'pasted image', loading: 'lazy',
  });
  img.addEventListener('click', () => lightbox(b.url, b.name));
  const fig = el('figure', { class: 'msg-fig' }, img);
  if (b.name) fig.append(el('figcaption', { text: b.name }));
  // A file that has since been cleaned up should say so, rather than leave a
  // broken-image icon with no explanation.
  img.addEventListener('error', () => {
    fig.replaceChildren(el('div', { class: 'msg-img-gone' },
      (b.name || 'image') + ' — the file is no longer on disk'));
  });
  return fig;
}

// lightbox shows one image full size over the app, dismissed by clicking it or
// pressing Escape.
function lightbox(url, name) {
  const box = el('div', { class: 'lightbox' },
    el('img', { class: 'lightbox-img', src: url, alt: name || '' }),
    name ? el('div', { class: 'lightbox-name', text: name }) : null);
  const onKey = e => { if (e.key === 'Escape') close(); };
  function close() {
    box.remove();
    document.removeEventListener('keydown', onKey);
  }
  box.addEventListener('click', close);
  document.addEventListener('keydown', onKey);
  document.body.append(box);
}

function shortModel(m) {
  return String(m).replace(/^claude-/, '').replace(/-\d{8}$/, '');
}

function toolEl(t, openSet) {
  const wasOpen = openSet.has(t.id);
  const { name, via } = toolTitle(t.name);
  const icon = TOOL_ICON[t.name] || (via ? '🔌' : '⚙');

  const card = el('div', {
    class: 'tool' + (t.isError ? ' err' : '') + (t.pending ? ' pending' : '') + (wasOpen ? ' open' : ''),
    'data-id': t.id,
  });

  const head = el('div', {
    class: 'tool-head',
    onclick: () => card.classList.toggle('open'),
  },
    el('span', { class: 'tool-icon', text: icon }),
    el('span', { class: 'tool-name', text: name }),
    // Which MCP server provided it. "mcp__MSSQL_ReSM__query_ReSM" is unreadable
    // as one word, and where the agent is reaching is the interesting half.
    via ? el('span', { class: 'tool-via', text: via, title: t.name }) : null,
    t.summary ? el('span', { class: 'tool-sum', text: t.summary, title: t.summary }) : null,
    el('span', { style: 'flex:1' }),
    t.pending ? el('span', { class: 'tool-state run', text: 'running' })
      : t.isError ? el('span', { class: 'tool-state bad', text: 'failed' })
      : null,
    el('span', { class: 'tool-chev', text: '▾' }));

  const detail = el('div', { class: 'tool-detail' });

  // What this particular tool did, rendered as the thing it is. The raw text
  // stays available underneath, because a rendering is an interpretation and
  // sometimes the interpretation is not what you came for.
  const body = toolBody(t);
  if (body && body.length) {
    detail.append(el('div', { class: 'tv-body' }, body));
    if (t.result && !RAW_IS_SHOWN[t.name]) {
      detail.append(rawToggle(t));
    }
  } else {
    if (t.input && t.input !== '{}') {
      detail.append(el('div', { class: 'tool-label', text: 'input' }),
        el('pre', { class: 'tool-pre', text: t.input }));
    }
    if (t.result) {
      detail.append(el('div', { class: 'tool-label', text: t.isError ? 'error' : 'result' }),
        el('pre', { class: 'tool-pre' + (t.isError ? ' err' : ''), text: t.result }));
    }
  }
  if (!detail.children.length) {
    detail.append(el('div', { class: 'hint', style: 'padding:8px 10px', text: t.pending ? 'Still running…' : 'No output.' }));
  }

  card.append(head, detail);
  return card;
}

// Tools whose rendered body already contains the whole result, so offering it
// again underneath would just be the same text twice.
const RAW_IS_SHOWN = { Bash: true, BashOutput: true, PowerShell: true, Read: true };

// rawToggle offers the untouched result, folded.
function rawToggle(t) {
  const pre = el('pre', { class: 'tool-pre' + (t.isError ? ' err' : ''), text: t.result, style: 'display:none' });
  const btn = el('button', {
    class: 'tv-raw',
    onclick: () => {
      const on = pre.style.display === 'none';
      pre.style.display = on ? '' : 'none';
      btn.textContent = on ? 'hide the raw result' : 'show the raw result';
    },
  }, 'show the raw result');
  return el('div', { class: 'tv-rawwrap' }, btn, pre);
}

// ---------------------------------------------------------------- terminal

function mountTerminal(sessionId) {
  const host = $('#termHost');
  if (!host || S.term) return;

  S.term = new Terminal({
    cursorBlink: true,
    fontFamily: 'ui-monospace, "Cascadia Code", Consolas, monospace',
    fontSize: 12.5,
    scrollback: 8000,
    allowProposedApi: true,
    theme: {
      background: '#07090b', foreground: '#e6e9ef', cursor: '#ff8a3d',
      black: '#11141a', red: '#ef4444', green: '#22c55e', yellow: '#f59e0b',
      blue: '#3b82f6', magenta: '#a855f7', cyan: '#06b6d4', white: '#e6e9ef',
      brightBlack: '#626c7a', brightRed: '#f87171', brightGreen: '#4ade80',
      brightYellow: '#fbbf24', brightBlue: '#60a5fa', brightMagenta: '#c084fc',
      brightCyan: '#22d3ee', brightWhite: '#ffffff',
    },
  });
  S.fit = new FitAddon.FitAddon();
  S.term.loadAddon(S.fit);
  S.term.open(host);
  try { S.fit.fit(); } catch {}

  const enc = new TextEncoder();
  S.term.onData(d => {
    if (S.termSocket && S.termSocket.readyState === 1) S.termSocket.send(enc.encode(d));
  });
  S.term.focus();

  connectTermSocket(sessionId);

  const ro = new ResizeObserver(() => {
    try {
      S.fit.fit();
      if (S.termSocket && S.termSocket.readyState === 1) {
        S.termSocket.send(JSON.stringify({ type: 'resize', cols: S.term.cols, rows: S.term.rows }));
      }
    } catch {}
  });
  ro.observe(host);
  S.termRO = ro;
}

function connectTermSocket(sessionId) {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const cols = S.term ? S.term.cols : 120, rows = S.term ? S.term.rows : 32;
  const ws = new WebSocket(
    `${proto}://${location.host}/ws/pty?session=${encodeURIComponent(sessionId)}&cols=${cols}&rows=${rows}`);
  ws.binaryType = 'arraybuffer';
  S.termSocket = ws;

  const dec = new TextDecoder();
  ws.onmessage = ev => {
    if (typeof ev.data === 'string') {
      try {
        const msg = JSON.parse(ev.data);
        if (msg.type === 'session') patchSession(msg.session);
      } catch {}
      return;
    }
    if (S.term) S.term.write(dec.decode(new Uint8Array(ev.data)));
  };
  ws.onclose = () => {
    if (S.term && S.openSession === sessionId) {
      S.term.write('\r\n\x1b[38;5;244m[terminal disconnected]\x1b[0m\r\n');
    }
  };
}

function closeTermSocket() {
  if (S.termSocket) { try { S.termSocket.close(); } catch {} S.termSocket = null; }
  if (S.termRO) { try { S.termRO.disconnect(); } catch {} S.termRO = null; }
  if (S.term) { try { S.term.dispose(); } catch {} S.term = null; }
}

// ---------------------------------------------------------------- sending

async function sendComposer(sessionId) {
  const box = $('#composerBox');
  if (!box) return;

  // An upload still in flight means the file is not on disk yet. Sending now
  // would point the agent at a path that does not exist.
  if (attachBusy()) { toast('Still uploading that image…'); return; }

  // Pasted images go in front of the prompt as paths, which is how the CLI is
  // given a picture: it reads the file. A paste on its own is a complete message
  // — "look at this" is a normal thing to send — so an empty box with an
  // attachment is not treated as empty.
  const paths = attachPaths();
  const typed = box.value.trim();
  if (!typed && !paths.length) return;
  const text = paths.length ? paths.join('\n') + (typed ? '\n' + typed : '') : box.value;

  box.value = '';
  box.style.height = 'auto';
  attachReset();

  // Show it immediately: waiting for the transcript to catch up makes the app
  // feel like it dropped the message.
  const body = $('#chatBody');
  let echo = null;
  if (body && S.chatMode === 'chat') {
    let list = body.querySelector('.msgs');
    if (!list) { body.innerHTML = ''; list = el('div', { class: 'msgs' }); body.append(list); }
    echo = messageEl(
      { role: 'user', blocks: [{ kind: 'text', text }], when: new Date().toISOString() },
      new Set());
    list.append(echo);
    CHAT.atBottom = true;
    body.scrollTop = body.scrollHeight;
    CHAT.lastSig = '';
  }

  // Say it is working straight away, rather than after the next poll notices.
  // The delay is only a second and a half, but it lands exactly where a person
  // is asking themselves whether the message went anywhere at all.
  CHAT.awaiting = true;
  CHAT.sentAt = Date.now();
  CHAT.sentAfter = CHAT.lastId;
  CHAT.thinkingSince = Date.now();
  showThinking(true, '');

  try {
    await api(`/sessions/${sessionId}/input`, { method: 'POST', body: { data: text, enter: true } });
  } catch (e) {
    // Delivery is confirmed against the terminal now, so this really can mean
    // the CLI never took the message. Leaving the optimistic bubble on screen
    // would claim it was sent, so it comes back out and the text is returned to
    // the box for another try.
    if (echo) echo.remove();
    CHAT.awaiting = false;
    showThinking(false, '');
    toast(e.message, 'bad');
    box.value = text;
    box.dispatchEvent(new Event('input'));
  }
}

// The header token badge.
//
// It takes its numbers from whatever the caller already has. The conversation
// poll carries them, so the badge fills in on the same request that draws the
// messages; without that it sat at an em-dash until an unrelated session-list
// refresh happened to land, which looked like a broken meter.
function updateTermTokens(from) {
  const badge = $('#termTokens');
  if (!badge || !S.openSession) return;
  const s = from || sessionById(S.openSession);
  if (!s) return;
  const total = s.totalTokens || 0;
  badge.textContent = total
    ? `${fmtNum(total)} tok · ${(s.cacheHitRate || 0).toFixed(0)}% cache`
    : 'no tokens yet';
  badge.className = 'token-badge' + (total > 2e6 ? ' hot' : '');
}
