/* An agent, or a project, in a window of its own.
 *
 * One window is the wrong shape for the way this app is used. An agent runs for
 * half an hour and you want to watch it while working in another, which on a
 * desk with two monitors means two windows and not two tabs in one.
 *
 * A pop-out is the same page with a starting position in its address, so there
 * is no second UI to keep in step and no state to hand across: the window opens,
 * reads what it is for out of its own URL, and goes there. Everything else — the
 * event socket, the polling, the answers to questions — works because it is the
 * same app, connected to the same server, the way a second tab would be.
 *
 * The server opens it. The client asks for a target and never for a URL: a
 * handler that opens a native window on whatever address it is handed is one
 * that will one day be handed somebody else's.
 */
'use strict';

// popoutParams is what this window was opened for, read once at load.
const POPOUT = (() => {
  const q = new URLSearchParams(location.search);
  return {
    on: q.get('popout') === '1',
    session: q.get('session') || '',
    project: q.get('project') || '',
  };
})();

// applyPopoutChrome trims the window to what it was opened for.
//
// A pop-out on one agent has no use for the project list or the tabs — it is a
// window onto one conversation, and every pixel of frame around it is a pixel
// not showing the conversation. A pop-out on a project keeps its tabs, because
// moving between the board and the files is the whole reason to have it.
function applyPopoutChrome() {
  if (!POPOUT.on) return;
  document.body.classList.add('popout');
  document.body.classList.add(POPOUT.session ? 'popout-chat' : 'popout-project');
}

// goToPopoutTarget puts the window where it was opened to be, once the state it
// needs has been loaded.
function goToPopoutTarget() {
  if (POPOUT.session) {
    const s = sessionById(POPOUT.session);
    if (!s) {
      // Opened on something that has since stopped. Saying so beats a window
      // that looks broken.
      document.body.innerHTML =
        '<div class="empty" style="height:100vh"><h3>That agent is no longer running</h3>' +
        '<p>You can close this window.</p></div>';
      return;
    }
    if (s.projectId) S.selectedProject = s.projectId;
    openTerm(POPOUT.session);
    return;
  }
  if (POPOUT.project && projectById(POPOUT.project)) {
    S.selectedProject = POPOUT.project;
    render();
  }
}

// popOut asks the server for a window on a target.
//
// The reply says whether a real window opened. When it did not — a phone, or a
// build with no native window — the page opens a tab itself, which is a poor
// substitute for a second monitor and a great deal better than a button that
// does nothing.
async function popOut(target, title) {
  try {
    const r = await api('/windows', { method: 'POST', body: { ...target, title } });
    if (r.opened) return;
    const w = window.open(r.url, '_blank', 'noopener');
    if (!w) toast(r.reason || 'The browser blocked the new window.', 'bad');
  } catch (e) {
    toast(e.message, 'bad');
  }
}

function popOutSession(sessionId) {
  const s = sessionById(sessionId);
  popOut({ sessionId }, s ? (agentName(s.agentId) || 'Agent') + ' — Go AI Team' : 'Go AI Team');
}

function popOutProject(projectId) {
  const p = projectById(projectId);
  popOut({ projectId }, p ? p.name + ' — Go AI Team' : 'Go AI Team');
}
