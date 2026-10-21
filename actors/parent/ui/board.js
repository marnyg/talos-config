// The task board as a Kanban: drag a card onto one of its state's exits
// and the move goes through POST /api/tasks/<id>/transition, the same
// call the task page's form makes. The page only offers what the flow
// allows (data-next on the column, .movable on the card); the parent
// still decides — a refused move shows its error and moves nothing.
// Without JS the board is read-only and the task page moves a task.
//
// The board also follows the server (talos-config-sgk9): every few
// seconds it polls /api/tasks?project=… and reconciles the columns —
// a card is rebuilt only when its rev (updated|session|retries) or
// column changed, added when new, dropped when gone; untouched cards
// keep their DOM node. The poll holds off while a drag is in flight or
// the move dialog is open, so a redraw cannot pull a card out from
// under the pointer, and while the tab is hidden.
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

  // ---- live refresh ----------------------------------------------------

  const project = board.dataset.project;
  const cap = Number(board.dataset.cap) || 50;
  const pollEvery = 4000;

  // ago mirrors the server's template func (web.go ago).
  const ago = (t) => {
    if (!t) return '—';
    const s = Math.max(0, Math.round(Date.now() / 1000 - t));
    if (s < 60) return `${s}s ago`;
    if (s < 3600) return `${Math.floor(s / 60)}m ago`;
    if (s < 48 * 3600) return `${Math.floor(s / 3600)}h ago`;
    return `${Math.floor(s / 86400)}d ago`;
  };
  const rev = (t) => `${t.updated}|${t.session || ''}|${t.retries || 0}`;
  const movable = (col, t) => {
    const drv = col.dataset.driver;
    return !col.dataset.terminal && drv !== 'children' && (drv === 'human' || !t.session);
  };

  // card builds a node the way tasks.html renders one.
  const card = (col, t) => {
    const li = document.createElement('li');
    const mv = movable(col, t);
    li.className = 'card' + (mv ? ' movable' : '');
    li.dataset.id = t.id;
    li.dataset.title = t.title;
    li.dataset.rev = rev(t);
    if (mv) li.draggable = true;
    const a = document.createElement('a');
    a.href = `/t/${encodeURIComponent(t.id)}`;
    a.draggable = false;
    a.textContent = t.title;
    li.append(a);
    const span = (cls, text) => {
      const s = document.createElement('span');
      s.className = cls;
      s.textContent = text;
      li.append(' ', s);
    };
    if (t.repo) span('mono small', t.repo);
    span('muted small ago', ago(t.updated));
    if (t.session) span('badge running', 'live');
    if (t.retries) span('badge failed', `retry ${t.retries}`);
    return li;
  };

  // reconcile puts the server's list into the columns: per column, in
  // the server's order (updated desc), reusing a card whose rev and
  // column are unchanged.
  const reconcile = (tasks) => {
    const byState = new Map(columns.map(c => [c.dataset.state, []]));
    for (const t of tasks) {
      const l = byState.get(t.state);
      if (l) l.push(t); // a state the flow no longer has: not shown
    }
    const have = new Map([...board.querySelectorAll('.card')].map(li => [li.dataset.id, li]));
    for (const col of columns) {
      const list = byState.get(col.dataset.state);
      const ul = col.querySelector('ul');
      const shown = list.slice(0, cap);
      let cursor = null; // last placed card; the next goes after it
      for (const t of shown) {
        let li = have.get(t.id);
        if (!li || li.dataset.rev !== rev(t) || li.closest('.column') !== col) {
          const fresh = card(col, t);
          if (li) li.replaceWith(fresh); else have.set(t.id, fresh);
          li = fresh;
        }
        const at = li.querySelector('.ago');
        if (at) at.textContent = ago(t.updated);
        const want = cursor ? cursor.nextSibling : ul.firstChild;
        if (li !== want) ul.insertBefore(li, want);
        cursor = li;
      }
      // Whatever follows the last placed card is stale: older cards
      // beyond the cap, cards that moved away or went, the old hidden
      // line.
      let n = cursor ? cursor.nextSibling : ul.firstChild;
      while (n) { const next = n.nextSibling; n.remove(); n = next; }
      if (list.length > shown.length) {
        const h = document.createElement('li');
        h.className = 'muted small hidden-count';
        h.textContent = `+${list.length - shown.length} older not shown`;
        ul.append(h);
      }
      const count = col.querySelector('h3 .count');
      if (count) count.textContent = String(list.length);
    }
  };

  let inFlight = false;
  const poll = async () => {
    if (inFlight || drag || dlg.open || document.hidden) return;
    inFlight = true;
    try {
      const r = await fetch(`/api/tasks?project=${encodeURIComponent(project)}`, { headers: { Accept: 'application/json' } });
      if (!r.ok) return; // a logged-out tab shows what it had; the next click will redirect
      const tasks = await r.json();
      if (drag || dlg.open) return; // a drag began during the fetch: this round is dropped
      reconcile(tasks);
    } catch (_) {
      // network blip: try again next tick
    } finally {
      inFlight = false;
    }
  };
  if (project) {
    setInterval(poll, pollEvery);
    document.addEventListener('visibilitychange', () => { if (!document.hidden) poll(); });
  }
})();
