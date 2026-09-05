/* Worktrees: which agent is working where, and cleaning up after them.
 *
 * An agent with a worktree has its own checkout on its own branch, so several
 * agents can work on one repository without editing the same files. What that
 * leaves behind is directories and branches, and something has to show them —
 * otherwise a project quietly accumulates a dozen checkouts nobody remembers
 * making.
 *
 * Removing one is the only destructive thing in here, and it says so twice: git
 * refuses a tree holding uncommitted work, and the second attempt spells out
 * what is about to be lost before it passes force.
 */
'use strict';

async function openWorktrees(project) {
  const body = el('div', { id: 'wtBody' }, el('div', { class: 'hint', text: 'Loading…' }));
  modal(`Worktrees — ${project.name}`, body, [['Close', 'btn', closeModal]], true);
  await paintWorktrees(project);
}

async function paintWorktrees(project) {
  const body = $('#wtBody');
  if (!body) return;
  let rows = [];
  try {
    rows = await api('/worktrees?projectId=' + encodeURIComponent(project.id)) || [];
  } catch (e) {
    body.innerHTML = '';
    body.append(el('div', { class: 'hint', text: e.message }));
    return;
  }

  body.innerHTML = '';
  body.append(el('div', { class: 'hint', text:
    'Each agent set to use a worktree gets its own checkout on its own branch, off the same history. ' +
    'Merge the branch when you are happy with the work; removing the checkout does not delete the branch.' }));

  if (rows.length <= 1) {
    body.append(el('div', { class: 'empty', style: 'padding:26px' },
      el('h3', { text: 'No agent worktrees' }),
      el('p', { text: 'Turn on "Give this agent a git worktree of its own" in an agent’s settings, and its next start will make one.' })));
    return;
  }

  const table = el('table', { class: 'kv wt-table' });
  for (const r of rows) {
    const who = r.main
      ? el('span', { class: 'pill' }, 'the project itself')
      : r.agentName
        ? el('span', { class: 'pill' }, r.agentName)
        : el('span', { class: 'pill', title: 'Not made by this app' }, 'unknown');

    table.append(el('tr', {},
      el('td', {}, who,
        r.running ? el('span', { class: 'pill warn', style: 'margin-left:6px' }, 'running') : null),
      el('td', {}, el('span', { class: 'mono', text: r.branch || '(detached)' })),
      el('td', { class: 'wt-path mono', title: r.path }, r.path),
      el('td', { style: 'text-align:right' },
        r.main || !r.agentId ? null : el('button', {
          class: 'btn danger sm',
          disabled: r.running ? 'disabled' : null,
          title: r.running ? 'Stop the agent first' : 'Delete this checkout',
          onclick: () => removeWorktree(project, r),
        }, 'Remove'))));
  }
  body.append(table);
}

// removeWorktree deletes one checkout, asking git first and the person second.
//
// The first attempt never forces. git refuses a tree holding uncommitted work,
// and that refusal is the prompt: it is the only thing that actually knows
// whether there is anything to lose.
async function removeWorktree(project, row) {
  try {
    await api('/worktrees/remove', {
      method: 'POST', body: { projectId: project.id, path: row.path },
    });
    toast(`Removed ${row.branch || row.path}`, 'ok');
    return paintWorktrees(project);
  } catch (e) {
    if (!/force/i.test(e.message)) {
      toast(e.message, 'bad');
      return;
    }
    confirmModal(
      'Throw away uncommitted work?',
      `${row.agentName || 'This agent'}’s checkout on ${row.branch} has changes that were never committed. ` +
      'Removing it now loses them. Committing first, or merging the branch, keeps them.',
      'Remove and lose the changes',
      async () => {
        try {
          await api('/worktrees/remove', {
            method: 'POST', body: { projectId: project.id, path: row.path, force: true },
          });
          toast(`Removed ${row.branch || row.path}`, 'ok');
        } catch (err) {
          toast(err.message, 'bad');
        }
        openWorktrees(project);
      });
  }
}

// confirmModal asks before something irreversible, naming what is at stake
// rather than asking "are you sure".
function confirmModal(title, text, danger, onYes) {
  modal(title, el('div', {}, el('p', { text })), [
    ['Cancel', 'btn', closeModal],
    [danger, 'btn danger', () => { closeModal(); onYes(); }],
  ]);
}
