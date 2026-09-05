/* Dragging projects and folders around the sidebar.
 *
 * Folders existed and could only be filled by opening a dialog and picking from
 * a dropdown, which is not how anyone thinks about putting a thing in a folder.
 * The tree is right there; dragging is the obvious gesture and it was the one
 * thing the tree did not do.
 *
 * Two rules keep this from destroying anything:
 *
 * A drop that changes nothing is not sent. Dragging a project back into the
 * folder it already lives in should not write to disk or redraw the tree.
 *
 * A folder cannot be dropped into itself or into anything inside it. That would
 * detach the whole subtree from the root — it would still exist, still be
 * pinned to accounts, and be permanently invisible. The server refuses this too;
 * doing it here as well means the UI never offers a drop it will not honour.
 */
'use strict';

const DND = {
  // what is being dragged: { kind: 'project'|'folder', id }
  held: null,
};

// dndDraggable marks a row as something that can be picked up.
function dndDraggable(row, kind, id) {
  row.setAttribute('draggable', 'true');
  row.addEventListener('dragstart', e => {
    DND.held = { kind, id };
    row.classList.add('dragging');
    // Some browsers refuse to start a drag with nothing in the transfer.
    try {
      e.dataTransfer.setData('text/plain', kind + ':' + id);
      e.dataTransfer.effectAllowed = 'move';
    } catch { /* the drag still works from DND.held */ }
    document.body.classList.add('dnd-active');
  });
  row.addEventListener('dragend', () => {
    DND.held = null;
    row.classList.remove('dragging');
    document.body.classList.remove('dnd-active');
    for (const n of document.querySelectorAll('.drop-into')) n.classList.remove('drop-into');
  });
}

// dndTarget makes a row accept a drop. `folderId` is '' for the root.
function dndTarget(row, folderId) {
  row.addEventListener('dragover', e => {
    if (!canDrop(folderId)) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    row.classList.add('drop-into');
  });
  row.addEventListener('dragleave', () => row.classList.remove('drop-into'));
  row.addEventListener('drop', async e => {
    e.preventDefault();
    e.stopPropagation();
    row.classList.remove('drop-into');
    const held = DND.held;
    DND.held = null;
    if (!held || !canDrop(folderId, held)) return;
    await moveInto(held, folderId);
  });
}

// canDrop decides whether this drop is allowed, and is what stops a folder being
// dropped inside itself.
function canDrop(folderId, held) {
  const h = held || DND.held;
  if (!h) return false;

  if (h.kind === 'project') {
    const p = projectById(h.id);
    // Already there: nothing to do, and no reason to light up a target.
    return !!p && (p.folderId || '') !== folderId;
  }

  if (h.kind === 'folder') {
    if (h.id === folderId) return false;
    const f = folderById(h.id);
    if (!f || (f.parentId || '') === folderId) return false;
    return !isDescendantFolder(folderId, h.id);
  }
  return false;
}

// isDescendantFolder reports whether `id` sits anywhere inside `ancestorId`.
//
// Walks up from the candidate rather than down from the ancestor, so it needs no
// index — and it counts steps, because a cycle already in the data must not spin
// this forever.
function isDescendantFolder(id, ancestorId) {
  let cur = id;
  for (let hops = 0; cur && hops < 64; hops++) {
    if (cur === ancestorId) return true;
    const f = folderById(cur);
    cur = f ? (f.parentId || '') : '';
  }
  return false;
}

function folderById(id) {
  return (S.folders || []).find(f => f.id === id);
}

async function moveInto(held, folderId) {
  const path = held.kind === 'project' ? '/projects/' : '/folders/';
  const body = held.kind === 'project' ? { folderId } : { parentId: folderId };
  try {
    await api(path + held.id, { method: 'PATCH', body });
  } catch (e) {
    toast(e.message, 'bad');
    return;
  }
  await loadAll();
  render();

  const name = held.kind === 'project'
    ? (projectById(held.id) || {}).name
    : (folderById(held.id) || {}).name;
  const into = folderId ? (folderById(folderId) || {}).name : 'Projects';
  toast(`${name || 'Moved'} → ${into}`, 'ok');
}
