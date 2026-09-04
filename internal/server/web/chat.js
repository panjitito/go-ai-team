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
  Bash: '❯', BashOutput: '❯', Glob: '🔍', Grep: '🔍',
  WebFetch: '🌐', WebSearch: '🌐', Task: '🤖', Agent: '🤖',
  TodoWrite: '☑', ExitPlanMode: '📋',
};

const CHAT = {
  poll: null,
  lastSig: '',
  atBottom: true,
};

// openAgent replaces the old terminal-first view: chat by default, terminal on
// demand, one composer for both.
function openAgent(sessionId, mode) {
  const sess = sessionById(sessionId);
  if (!sess) return;
  disposePanes();
  if (S.view !== 'term') S.lastTab = S.view;
  S.openSession = sessionId;
  S.view = 'term';
  S.chatMode = mode || S.chatMode || 'chat';

  stopChatPoll();
  closeTermSocket();

  const main = $('#main');
  main.innerHTML = '';

  const agent = sess.agentId ? agentById(sess.agentId) : null;
  const title = agent ? agent.name : (sess.label || (sess.kind === 'login' ? 'Sign in' : 'Terminal'));

  const wrap = el('div', { class: 'chat-wrap' },
    chatHeader(sess, title),
    el('div', { class: 'chat-banner', id: 'chatBanner', style: 'display:none' }),
    el('div', { class: 'chat-body', id: 'chatBody' }),
    el('div', { class: 'term-host', id: 'termHost', style: 'display:none' }),
    composer(sessionId));
  main.append(wrap);

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
    el('span', { style: 'flex:1' }),

    el('span', {
      class: 'token-badge', id: 'termTokens', title: 'Session monitor',
      onclick: () => openUsage(sessionId),
    }, '—'),

    el('div', { class: 'seg' },
      el('button', { class: 'btn sm', 'data-mode': 'chat', onclick: () => { S.chatMode = 'chat'; applyChatMode(sessionId); } }, 'Chat'),
      el('button', { class: 'btn sm', 'data-mode': 'term', onclick: () => { S.chatMode = 'term'; applyChatMode(sessionId); } }, 'Terminal')),

    el('button', { class: 'btn ghost sm', id: 'hushBtn', style: 'display:none', title: 'Stop speaking', onclick: () => Voice.hush() }, '⏹'),
    el('button', { class: 'btn ghost sm', title: 'Read the last answer aloud (Shift for a condensed read)', onclick: e => readAloud(sessionId, e.shiftKey) }, '🔊'),
    sess.agentId ? el('button', { class: 'btn ghost sm', title: 'Change role, keep the conversation', onclick: () => morphAgent(sess) }, '⟳') : null,
    sess.agentId ? el('button', { class: 'btn ghost sm', title: 'Fork a twin with this conversation', onclick: () => forkAgent(sess) }, '⑃') : null,
    sess.agentId ? el('button', { class: 'btn ghost sm', title: 'Run on another account', onclick: () => manualSwitch(sess) }, '↻') : null,
    el('button', { class: 'btn danger sm', onclick: () => stopSession(sessionId) }, 'Stop'));
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

  return el('div', { class: 'composer' },
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
      el('button', { class: 'btn primary', onclick: () => sendComposer(sessionId) }, 'Send')));
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
  stopChatPoll();
  closeTermSocket();
  S.openSession = null;
}

function renderConversation(d, sessionId) {
  updateTermTokens(d);
  const banner = $('#chatBanner');
  const body = $('#chatBody');
  if (!banner || !body) return;

  // The terminal-prompt banner: without it, a trust prompt looks like a hang.
  if (d.needsTerminal) {
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

  const st = $('#chatStatus');
  if (st) st.innerHTML = `<span class="dot ${d.status}"></span> ${d.status}`;

  // Repaint only when something actually changed: rebuilding the list every
  // 1.5 seconds would fight text selection and lose scroll position.
  const sig = JSON.stringify(d.messages.map(m => [
    m.id, m.blocks.length,
    m.blocks.map(b => b.kind === 'tool' ? (b.tool.pending ? 'p' : 'd') + b.tool.result.length : b.text.length).join(','),
  ]));
  if (sig === CHAT.lastSig) return;
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

  if (CHAT.atBottom) body.scrollTop = body.scrollHeight;
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
    else if (b.tool) bodyEl.append(toolEl(b.tool, openSet));
  }
  return el('div', { class: 'msg ' + (isUser ? 'user' : 'assistant') }, head, bodyEl);
}

function shortModel(m) {
  return String(m).replace(/^claude-/, '').replace(/-\d{8}$/, '');
}

function toolEl(t, openSet) {
  const wasOpen = openSet.has(t.id);
  const icon = TOOL_ICON[t.name] || '⚙';

  const card = el('div', {
    class: 'tool' + (t.isError ? ' err' : '') + (t.pending ? ' pending' : '') + (wasOpen ? ' open' : ''),
    'data-id': t.id,
  });

  const head = el('div', {
    class: 'tool-head',
    onclick: () => card.classList.toggle('open'),
  },
    el('span', { class: 'tool-icon', text: icon }),
    el('span', { class: 'tool-name', text: t.name }),
    t.summary ? el('span', { class: 'tool-sum', text: t.summary, title: t.summary }) : null,
    el('span', { style: 'flex:1' }),
    t.pending ? el('span', { class: 'tool-state run', text: 'running' })
      : t.isError ? el('span', { class: 'tool-state bad', text: 'failed' })
      : null,
    el('span', { class: 'tool-chev', text: '▾' }));

  const detail = el('div', { class: 'tool-detail' });
  if (t.input && t.input !== '{}') {
    detail.append(el('div', { class: 'tool-label', text: 'input' }),
      el('pre', { class: 'tool-pre', text: t.input }));
  }
  if (t.result) {
    detail.append(el('div', { class: 'tool-label', text: t.isError ? 'error' : 'result' }),
      el('pre', { class: 'tool-pre' + (t.isError ? ' err' : ''), text: t.result }));
  }
  if (!detail.children.length) {
    detail.append(el('div', { class: 'hint', style: 'padding:8px 10px', text: t.pending ? 'Still running…' : 'No output.' }));
  }

  card.append(head, detail);
  return card;
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
  const text = box.value;
  if (!text.trim()) return;
  box.value = '';
  box.style.height = 'auto';

  // Show it immediately: waiting for the transcript to catch up makes the app
  // feel like it dropped the message.
  const body = $('#chatBody');
  if (body && S.chatMode === 'chat') {
    let list = body.querySelector('.msgs');
    if (!list) { body.innerHTML = ''; list = el('div', { class: 'msgs' }); body.append(list); }
    list.append(messageEl(
      { role: 'user', blocks: [{ kind: 'text', text }], when: new Date().toISOString() },
      new Set()));
    CHAT.atBottom = true;
    body.scrollTop = body.scrollHeight;
    CHAT.lastSig = '';
  }

  try {
    await api(`/sessions/${sessionId}/input`, { method: 'POST', body: { data: text, enter: true } });
  } catch (e) {
    toast(e.message, 'bad');
    box.value = text;
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
