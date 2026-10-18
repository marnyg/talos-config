// The session page: render the stored stream, then tail it live over
// SSE (/api/sessions/<id>/stream). Renders pi's record shapes
// (docs/json.md): finished messages, tool calls/results, streaming
// deltas into a live bubble, the parent's steer/abort stamps, and the
// child_settled line. Everything else is ignored.
(() => {
  const main = document.querySelector('main[data-session]');
  if (!main) return;
  const id = main.dataset.session;
  const el = document.getElementById('transcript');
  const statusEl = document.getElementById('status');
  const resultEl = document.getElementById('result');
  const controls = document.getElementById('controls');
  let last = 0;
  let liveBubble = null;   // the assistant message being streamed
  let liveText = '';

  const h = (tag, cls, text) => {
    const n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text !== undefined) n.textContent = text;
    return n;
  };
  const add = (node, seq) => {
    if (seq) node.id = 'e' + seq;
    el.appendChild(node);
    if (nearBottom) window.scrollTo(0, document.body.scrollHeight);
  };
  let nearBottom = true;
  window.addEventListener('scroll', () => {
    nearBottom = window.innerHeight + window.scrollY >= document.body.scrollHeight - 80;
  });

  const partsText = (content) => {
    if (typeof content === 'string') return [{ kind: 'text', text: content }];
    if (!Array.isArray(content)) return [];
    return content.map(p => {
      if (p.type === 'text') return { kind: 'text', text: p.text || '' };
      if (p.type === 'thinking') return { kind: 'thinking', text: p.thinking || '' };
      if (p.type === 'toolCall') return { kind: 'call', name: p.name, args: p.arguments };
      if (p.type === 'toolResult') return { kind: 'text', text: partsText(p.content).map(x => x.text || '').join('\n') };
      return null;
    }).filter(Boolean);
  };

  const render = (ev) => {
    const r = ev.raw || {};
    switch (ev.type) {
      case 'message_end': {
        const m = r.message || {};
        if (m.role === 'user') {
          add(h('div', 'msg user', partsText(m.content).map(p => p.text || '').join('\n')), ev.seq);
        } else if (m.role === 'assistant') {
          if (liveBubble) { liveBubble.remove(); liveBubble = null; liveText = ''; }
          const d = h('div', 'msg assistant');
          for (const p of partsText(m.content)) {
            if (p.kind === 'text') d.appendChild(h('pre', 'text', p.text));
            else if (p.kind === 'thinking') { const t = h('details', 'thinking'); t.appendChild(h('summary', '', 'thinking')); t.appendChild(h('pre', '', p.text)); d.appendChild(t); }
            else if (p.kind === 'call') d.appendChild(h('div', 'call', '▸ ' + p.name + ' ' + JSON.stringify(p.args || {})));
          }
          add(d, ev.seq);
        }
        break;
      }
      case 'message_update': {
        const ame = r.assistantMessageEvent || {};
        if (ame.type === 'text_delta') {
          if (!liveBubble) { liveBubble = h('div', 'msg assistant live'); liveBubble.appendChild(h('pre', 'text', '')); add(liveBubble); }
          liveText += ame.delta || '';
          liveBubble.firstChild.textContent = liveText;
          if (nearBottom) window.scrollTo(0, document.body.scrollHeight);
        }
        break;
      }
      case 'tool_execution_end': {
        const text = partsText((r.result || {}).content).map(p => p.text || '').join('\n');
        const d = h('details', 'tool' + (r.isError ? ' error' : ''));
        d.appendChild(h('summary', '', (r.toolName || 'tool') + (r.isError ? ' (error)' : '') + ' — ' + text.split('\n')[0].slice(0, 120)));
        d.appendChild(h('pre', '', text.length > 20000 ? text.slice(0, 20000) + '\n…' : text));
        add(d, ev.seq);
        break;
      }
      case 'steer':
        add(h('div', 'msg steer', 'steer by ' + short(ev.from) + ': ' + (r.message || '')), ev.seq);
        break;
      case 'abort':
        add(h('div', 'msg steer', 'abort by ' + short(ev.from)), ev.seq);
        break;
      case 'child_settled': {
        if (liveBubble) { liveBubble.classList.remove('live'); liveBubble = null; }
        const d = h('div', 'settled ' + r.status, r.status + (r.reason ? ' — ' + r.reason : ''));
        if (r.pr) { d.appendChild(document.createTextNode(' ')); const a = h('a', '', 'PR'); a.href = r.pr; d.appendChild(a); }
        else if (r.branch) d.appendChild(h('span', 'mono', ' ' + r.branch));
        add(d, ev.seq);
        setStatus(r.status, r);
        break;
      }
    }
  };
  const short = (w) => (w && w.length > 12) ? w.slice(0, 6) + '…' + w.slice(-4) : (w || '?');
  const setStatus = (s, r) => {
    statusEl.textContent = s;
    statusEl.className = 'badge ' + s;
    if (controls) controls.hidden = true;
    if (r && (r.pr || r.branch)) {
      resultEl.textContent = '';
      if (r.pr) { const a = h('a', '', 'PR'); a.href = r.pr; resultEl.appendChild(a); }
      else resultEl.appendChild(h('span', 'mono', r.branch));
    }
  };

  const handle = (ev) => {
    if (ev.seq <= last) return;
    last = ev.seq;
    render(ev);
  };

  const tail = () => {
    const es = new EventSource('/api/sessions/' + id + '/stream?after=' + last);
    es.addEventListener('line', (e) => handle(JSON.parse(e.data)));
    es.addEventListener('end', () => { es.close(); fetch('/api/sessions/' + id).then(r => r.json()).then(s => setStatus(s.status, s)); });
    es.onerror = () => { es.close(); setTimeout(tail, 3000); };
  };

  fetch('/api/sessions/' + id + '/events?limit=5000').then(r => r.json()).then(evs => {
    evs.forEach(handle);
    if (main.dataset.live === 'true') tail();
    if (location.hash) { const t = document.querySelector(location.hash); if (t) { t.scrollIntoView(); t.classList.add('hit'); } }
  });

  const post = (path, body) => fetch(path, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body || {}),
  }).then(async r => { if (!r.ok) throw new Error((await r.json()).error || r.status); return r.json(); });

  const steer = document.getElementById('steer');
  if (steer) {
    steer.addEventListener('submit', (e) => {
      e.preventDefault();
      const ta = steer.querySelector('textarea');
      post('/api/sessions/' + id + '/steer', { message: ta.value }).then(() => { ta.value = ''; }).catch(err => alert(err.message));
    });
    document.getElementById('abort').addEventListener('click', () => {
      if (confirm('abort this run?')) post('/api/sessions/' + id + '/abort').catch(err => alert(err.message));
    });
  }
  document.querySelectorAll('.unshare').forEach(b => b.addEventListener('click', () => {
    fetch('/api/sessions/' + id + '/shares/' + encodeURIComponent(b.dataset.principal), { method: 'DELETE' }).then(() => location.reload());
  }));
})();
