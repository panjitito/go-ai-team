// Pasting and dropping images into the composer.
//
// A textarea silently discards image data, so before this a pasted screenshot
// simply disappeared: nothing in the box, no error, and Send did nothing because
// the box was still empty. People paste screenshots constantly, so that was a
// hole rather than a missing extra.
//
// An image cannot be handed to the CLI as clipboard content — the CLI reads the
// clipboard of the machine it runs on, which is not necessarily the machine
// holding the picture, since this UI also opens from a phone. So the bytes are
// uploaded, written to a file beside the agent, and the path goes into the
// prompt. The agent then reads it like any other file.

const ATT = {
  // pending holds the attachments for the composer currently on screen.
  pending: [],
};

function attachReset() {
  ATT.pending = [];
  renderAttachments();
}

// attachFiles uploads images and shows them as thumbnails under the composer.
async function attachFiles(sessionId, files) {
  const images = [...files].filter(f => f && f.type && f.type.startsWith('image/'));
  if (!images.length) return false;

  for (const f of images) {
    const item = { name: f.name || 'pasted', status: 'uploading', url: URL.createObjectURL(f) };
    ATT.pending.push(item);
    renderAttachments();
    try {
      const res = await fetch(
        `/api/sessions/${sessionId}/attach?name=${encodeURIComponent(f.name || 'pasted')}`,
        { method: 'POST', headers: { 'Content-Type': f.type }, body: f });
      const data = await res.json().catch(() => null);
      if (!res.ok) throw new Error((data && data.error) || `${res.status} ${res.statusText}`);
      item.status = 'ready';
      item.path = data.path;
      item.size = data.size;
    } catch (e) {
      item.status = 'failed';
      item.error = e.message;
      toast(e.message, 'bad');
    }
    renderAttachments();
  }
  return true;
}

// renderAttachments draws the strip of thumbnails above the composer, so what is
// about to be sent is visible before it is sent.
function renderAttachments() {
  const strip = $('#attachStrip');
  if (!strip) return;
  strip.innerHTML = '';
  if (!ATT.pending.length) { strip.style.display = 'none'; return; }
  strip.style.display = '';

  ATT.pending.forEach((a, i) => {
    const img = el('img', { class: 'attach-thumb', src: a.url, alt: a.name });
    const label = a.status === 'uploading' ? 'sending…'
      : a.status === 'failed' ? (a.error || 'failed')
        : fmtBytes(a.size || 0);
    strip.append(el('div', { class: 'attach' + (a.status === 'failed' ? ' bad' : '') },
      img,
      el('div', { class: 'attach-meta' },
        el('div', { class: 'attach-name' }, a.name),
        el('div', { class: 'attach-sub' }, label)),
      el('button', {
        class: 'attach-x', title: 'Remove',
        onclick: () => { ATT.pending.splice(i, 1); renderAttachments(); },
      }, '×')));
  });
}

// attachPaths returns the file paths to put in front of the prompt. Only the
// uploads that actually landed are referenced: pointing the agent at a file that
// failed to upload would send it looking for something that is not there.
function attachPaths() {
  return ATT.pending.filter(a => a.status === 'ready' && a.path).map(a => a.path);
}

// attachBusy reports whether an upload is still in flight, so Send can wait
// rather than sending a prompt that references a file not yet written.
function attachBusy() {
  return ATT.pending.some(a => a.status === 'uploading');
}

// wireAttachments makes the composer accept a pasted image.
function wireAttachments(box, sessionId) {
  box.addEventListener('paste', async e => {
    const files = e.clipboardData && e.clipboardData.files;
    if (!files || !files.length) return;
    const images = [...files].some(f => f.type && f.type.startsWith('image/'));
    if (!images) return;
    // Only take over the paste once there is definitely an image in it, so
    // pasting ordinary text keeps working exactly as before.
    e.preventDefault();
    await attachFiles(sessionId, files);
  });
}

// wireDropZone makes the whole conversation area accept a dropped image.
//
// Kept separate from wireAttachments because the composer is built before the
// chat wrapper is put in the document: looking up #chatBody from inside the
// composer finds nothing, and dropping would quietly only work over the textarea
// itself. This is called after the view is mounted.
function wireDropZone(sessionId) {
  const host = $('#chatBody');
  if (!host) return;
  const stop = e => { e.preventDefault(); e.stopPropagation(); };
  ['dragenter', 'dragover'].forEach(t => host.addEventListener(t, e => {
    stop(e);
    host.classList.add('dropping');
  }));
  ['dragleave', 'drop'].forEach(t => host.addEventListener(t, e => {
    stop(e);
    host.classList.remove('dropping');
  }));
  host.addEventListener('drop', async e => {
    if (e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files.length) {
      await attachFiles(sessionId, e.dataTransfer.files);
    }
  });
}
