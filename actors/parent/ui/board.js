// The task board as a Kanban: drag a card onto one of its state's exits
// and the move goes through POST /api/tasks/<id>/transition, the same
// call the task page's form makes. The page only offers what the flow
// allows (data-next on the column, .movable on the card); the parent
// still decides — a refused move shows its error and moves nothing.
// Without JS the board is read-only and the task page moves a task.
(() => {
  const board = document.getElementById('board');
  const dlg = document.getElementById('move');
  if (!board || !dlg) return;
  const form = dlg.querySelector('form');
  const note = form.elements.note;
  const errEl = dlg.querySelector('.error');
  const columns = [...board.querySelectorAll('.column')];
  const column = (state) => columns.find(c => c.dataset.state === state);

  let drag = null;   // {id, title, from, next: Set}
  let pending = null; // {id, to}

  const clear = () => {
    for (const c of columns) c.classList.remove('drop-ok', 'drop-no', 'drop-over');
    board.classList.remove('dragging');
  };

  board.addEventListener('dragstart', (e) => {
    const card = e.target.closest && e.target.closest('.card.movable');
    if (!card) return;
    const from = card.closest('.column');
    drag = {
      id: card.dataset.id,
      title: card.dataset.title,
      from: from.dataset.state,
      next: new Set(from.dataset.next.split(/\s+/).filter(Boolean)),
    };
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', drag.id);
    board.classList.add('dragging');
    for (const c of columns) {
      if (c === from) continue;
      c.classList.add(drag.next.has(c.dataset.state) ? 'drop-ok' : 'drop-no');
    }
  });
  board.addEventListener('dragend', () => { drag = null; clear(); });

  const target = (e) => {
    const c = e.target.closest && e.target.closest('.column');
    return drag && c && drag.next.has(c.dataset.state) ? c : null;
  };
  board.addEventListener('dragover', (e) => {
    const c = target(e);
    if (!c) return;
    e.preventDefault(); // accept the drop
    e.dataTransfer.dropEffect = 'move';
    for (const o of columns) o.classList.toggle('drop-over', o === c);
  });
  board.addEventListener('drop', (e) => {
    const c = target(e);
    if (!c) return;
    e.preventDefault();
    const d = drag;
    drag = null;
    clear();
    ask(d, c);
  });

  // ask confirms the move and takes the note the next step reads (an
  // agent state's persona gets it in its prompt as the previous step).
  const ask = (d, to) => {
    pending = { id: d.id, to: to.dataset.state };
    dlg.querySelector('.title').textContent = d.title;
    dlg.querySelector('.route').textContent = `${d.from} → ${to.dataset.state} (${to.dataset.driver})`;
    dlg.querySelector('.doc').textContent = to.dataset.doc || '';
    note.value = '';
    note.placeholder = to.dataset.driver === 'agent'
      ? 'note for the agent that picks it up (optional)'
      : 'note for the next step (optional)';
    errEl.hidden = true;
    dlg.showModal();
    note.focus();
  };

  form.addEventListener('submit', async (e) => {
    const action = e.submitter ? e.submitter.value : 'move';
    if (action !== 'move' || !pending) { pending = null; return; } // closes
    e.preventDefault();
    const buttons = [...form.querySelectorAll('button')];
    buttons.forEach(b => { b.disabled = true; });
    try {
      const r = await fetch(`/api/tasks/${encodeURIComponent(pending.id)}/transition`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ to: pending.to, note: note.value.trim() }),
      });
      if (!r.ok) {
        const body = await r.json().catch(() => ({}));
        throw new Error(body.error || `HTTP ${r.status}`);
      }
      // Reload rather than move the card by hand: entering an agent
      // state spawns a session, and the server's view is the truth.
      location.reload();
    } catch (err) {
      errEl.textContent = err.message;
      errEl.hidden = false;
      buttons.forEach(b => { b.disabled = false; });
    }
  });
  dlg.addEventListener('close', () => { pending = null; });
})();
