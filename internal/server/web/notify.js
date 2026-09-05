/* Telling you an agent wants you, when you are not looking at the app.
 *
 * The whole point of running eight agents is that you are not watching any of
 * them. You send the work off and go and do something else — and then the only
 * way to find out that one of them stopped to ask a question is to come back and
 * look, which is the thing you were trying to avoid. A count in the window title
 * only helps if the window is on screen.
 *
 * So: a desktop notification and a short chime when an agent needs an answer,
 * when a turn finishes, or when a session dies. Off until it is asked for,
 * because permission prompts nobody invited are their own kind of rude, and
 * silent while you are actually looking at the app — a notification for
 * something already on your screen is noise, and noise is what teaches people to
 * turn notifications off.
 */
'use strict';

const ALERTS = {
  // What each session looked like last time we checked, so a notification fires
  // on the change rather than every five seconds for as long as it is true.
  was: new Map(),
  // Fired within the last moment, waiting to be raised together.
  pending: [],
  timer: null,
  audio: null,
};

const ALERT_KEY = 'goaiteam.alerts';
const ALERT_SOUND_KEY = 'goaiteam.alerts.sound';

// 'off' — nothing. 'needs' — only a question or a death. 'all' — finished turns too.
function alertMode() {
  try { return localStorage.getItem(ALERT_KEY) || 'off'; } catch { return 'off'; }
}
function alertSound() {
  try { return localStorage.getItem(ALERT_SOUND_KEY) !== '0'; } catch { return true; }
}

// setAlertMode is the Settings control. Asking for permission at the moment
// somebody turns this on is the one time the browser prompt makes sense.
async function setAlertMode(mode) {
  try { localStorage.setItem(ALERT_KEY, mode); } catch {}
  if (mode === 'off' || typeof Notification === 'undefined') return mode;
  if (Notification.permission === 'granted') return mode;
  if (Notification.permission === 'denied') {
    toast('This window is blocked from showing notifications; the chime will still play.', 'warn');
    return mode;
  }
  let p = 'denied';
  try { p = await Notification.requestPermission(); } catch {}
  if (p !== 'granted') {
    toast('No notification permission, so alerts will be the chime only.', 'warn');
  }
  return mode;
}
function setAlertSound(on) {
  try { localStorage.setItem(ALERT_SOUND_KEY, on ? '1' : '0'); } catch {}
}

// Looking straight at it counts as already knowing.
function watching() {
  return document.visibilityState === 'visible' && document.hasFocus();
}

// alertScan compares the sessions against the last time it ran and raises what
// changed. Called after every socket message, which is also the only time
// anything can have changed.
function alertScan() {
  const mode = alertMode();
  const live = new Set();

  for (const s of S.sessions) {
    if (s.kind !== 'agent') continue;
    live.add(s.id);
    const now = { needsYou: !!s.needsYou, status: s.status };
    const before = ALERTS.was.get(s.id);
    ALERTS.was.set(s.id, now);

    // The first sighting of a session is not a change. Reconnecting the socket
    // re-sends every session, and without this the whole board would announce
    // itself at once.
    if (!before || mode === 'off') continue;

    if (now.needsYou && !before.needsYou) {
      queueAlert({
        id: s.id, kind: 'needs',
        title: (agentName(s.agentId) || 'An agent') + ' needs you',
        body: s.question || 'It is asking something.',
      });
      continue;
    }
    if (now.status === 'error' && before.status !== 'error') {
      queueAlert({
        id: s.id, kind: 'error',
        title: (agentName(s.agentId) || 'An agent') + ' stopped',
        body: s.error || 'The session ended with an error.',
      });
      continue;
    }
    // A turn ending is the quiet one, and the one you asked for when you went
    // away: it means the work you sent is done.
    if (mode === 'all' && now.status === 'waiting' && before.status === 'working' && !now.needsYou) {
      queueAlert({
        id: s.id, kind: 'done',
        title: (agentName(s.agentId) || 'An agent') + ' finished',
        body: projectNameOf(s) || 'The turn is over.',
      });
    }
  }

  // Forget sessions that have gone, or the map grows for the life of the tab.
  for (const id of [...ALERTS.was.keys()]) if (!live.has(id)) ALERTS.was.delete(id);
}

function projectNameOf(s) {
  const a = s.agentId && agentById(s.agentId);
  const p = a && projectById(a.projectId);
  return p ? p.name : '';
}

// queueAlert holds fire for a moment so a wave of them arrives as one line.
//
// Sending the same prompt to six agents means six turns ending within a second
// of each other, and six separate notifications stacked up the side of the
// screen is worse than none: you dismiss the pile without reading it.
function queueAlert(a) {
  ALERTS.pending.push(a);
  if (ALERTS.timer) return;
  ALERTS.timer = setTimeout(() => {
    ALERTS.timer = null;
    const batch = ALERTS.pending.splice(0);
    if (!batch.length) return;
    if (watching()) return;   // decided at the end of the wait, not the start

    if (alertSound()) chime(batch.some(x => x.kind !== 'done'));

    if (batch.length <= 2) {
      for (const a of batch) raise(a.title, a.body, a.id);
      return;
    }
    const needs = batch.filter(x => x.kind === 'needs').length;
    const title = needs
      ? `${needs} of ${batch.length} agents need you`
      : `${batch.length} agents finished`;
    raise(title, batch.map(x => x.title).join('\n'), batch[0].id);
  }, 700);
}

function raise(title, body, sessionId) {
  if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return;
  let n;
  try {
    // The tag keeps one notification per session: an agent that asks, is
    // answered and asks again replaces its own rather than stacking.
    n = new Notification(title, { body, tag: 'goaiteam-' + sessionId, icon: notifyIcon() });
  } catch {
    return;   // some platforms need a service worker; the chime still played
  }
  n.onclick = () => {
    window.focus();
    n.close();
    const s = sessionById(sessionId);
    if (s) openTerm(sessionId);
  };
  setTimeout(() => n.close(), 20000);
}

function notifyIcon() {
  return 'data:image/svg+xml,' + encodeURIComponent(
    "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'>" +
    "<rect width='32' height='32' rx='8' fill='#ff8a3d'/>" +
    "<text x='16' y='23' font-size='19' font-family='sans-serif' font-weight='bold'" +
    " text-anchor='middle' fill='#1a1005'>G</text></svg>");
}

// chime is two short notes, synthesised rather than shipped: no asset to load,
// nothing to go missing, and quiet enough not to make anyone jump. Rising for
// something that wants an answer, falling for a turn that simply ended.
function chime(urgent) {
  try {
    const Ctx = window.AudioContext || window.webkitAudioContext;
    if (!Ctx) return;
    ALERTS.audio = ALERTS.audio || new Ctx();
    const ac = ALERTS.audio;
    if (ac.state === 'suspended') ac.resume().catch(() => {});
    const notes = urgent ? [660, 880] : [660, 495];
    notes.forEach((hz, i) => {
      const t = ac.currentTime + i * 0.13;
      const osc = ac.createOscillator();
      const gain = ac.createGain();
      osc.type = 'sine';
      osc.frequency.value = hz;
      gain.gain.setValueAtTime(0.0001, t);
      gain.gain.exponentialRampToValueAtTime(0.09, t + 0.015);
      gain.gain.exponentialRampToValueAtTime(0.0001, t + 0.12);
      osc.connect(gain).connect(ac.destination);
      osc.start(t);
      osc.stop(t + 0.14);
    });
  } catch {
    // No audio device, or a policy that will not let a page make noise before
    // it has been clicked. Neither is worth an error in the console.
  }
}

// alertsSection is the control, in Settings. It is a browser preference, not an
// app one — permission belongs to this window and this profile — so it saves
// itself rather than waiting for the Save button next to the server settings.
function alertsSection() {
  const mode = alertMode();
  const sel = el('select', { id: 'setAlerts' },
    el('option', { value: 'off', selected: mode === 'off' ? 'selected' : null },
      'Off'),
    el('option', { value: 'needs', selected: mode === 'needs' ? 'selected' : null },
      'When an agent needs an answer, or hits an error'),
    el('option', { value: 'all', selected: mode === 'all' ? 'selected' : null },
      'Every time an agent stops, for any reason'));
  sel.onchange = async () => {
    await setAlertMode(sel.value);
    toast(sel.value === 'off' ? 'Alerts off' : 'Alerts on for this window', 'ok');
  };

  const snd = el('input', { type: 'checkbox', id: 'setAlertSound',
    checked: alertSound() ? 'checked' : null });
  snd.onchange = () => { setAlertSound(snd.checked); if (snd.checked) chime(true); };

  return el('div', { style: 'margin-top:18px;padding-top:12px;border-top:1px solid var(--line)' },
    el('label', { text: 'Desktop alerts, on this computer' }),
    sel,
    el('label', { class: 'switch', style: 'margin-top:8px' }, snd,
      el('span', { text: 'Play a chime' })),
    el('div', { class: 'hint', text: 'Nothing is raised while this window is in front of you — only when you are somewhere else. Saved in this browser profile, not on the server.' }));
}
