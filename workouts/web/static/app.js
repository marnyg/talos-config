// Workouts — client glue. Pages are server-rendered; this file adds the
// live bits: elapsed timers, the active-workout set editor (debounced
// writes to /api), the rest timer, the template editor, and the PWA
// service-worker registration.
(function () {
  'use strict';

  if ('serviceWorker' in navigator) navigator.serviceWorker.register('/sw.js').catch(function () {});

  var flash = document.getElementById('flash');
  if (flash) { setTimeout(function () { flash.remove(); }, 3000); history.replaceState(null, '', location.pathname + location.hash); }

  // ---- elapsed timers ----
  function fmt(s) { s = Math.max(0, Math.floor(s)); var h = Math.floor(s / 3600), m = Math.floor(s % 3600 / 60), x = s % 60; return (h ? h + ':' + String(m).padStart(2, '0') : m) + ':' + String(x).padStart(2, '0'); }
  var since = document.querySelectorAll('[data-since]');
  if (since.length) setInterval(function () { since.forEach(function (el) { el.textContent = fmt(Date.now() / 1000 - +el.dataset.since); }); }, 1000);

  function api(method, url, body) {
    return fetch(url, { method: method, headers: { 'Content-Type': 'application/json' }, body: body == null ? undefined : JSON.stringify(body) })
      .then(function (r) { if (!r.ok) throw new Error(r.statusText); return r.json(); });
  }
  function num(v) { v = String(v || '').trim().replace(',', '.'); if (v === '') return null; var f = parseFloat(v); return isNaN(f) ? null : f; }
  function secs(v) { v = String(v || '').trim(); if (v === '') return null; var p = v.split(':').map(Number); if (p.some(isNaN)) return null; return p.length === 1 ? p[0] : p.length === 2 ? p[0] * 60 + p[1] : p[0] * 3600 + p[1] * 60 + p[2]; }

  // ---- active workout ----
  var log = document.getElementById('log');
  if (log) {
    var restDefault = +log.dataset.rest || 120;
    var restEl = document.getElementById('rest'), restLeft = document.getElementById('rest-left'), restEnd = 0, restTimer = null;
    var ding = null;
    try { ding = new (window.AudioContext || window.webkitAudioContext)(); } catch (e) {}
    function beep() {
      if (!ding) return; try { var o = ding.createOscillator(), g = ding.createGain(); o.connect(g); g.connect(ding.destination); o.frequency.value = 880; g.gain.value = .2; o.start(); o.stop(ding.currentTime + .25); } catch (e) {}
      if (navigator.vibrate) navigator.vibrate([200, 100, 200]);
    }
    function tickRest() {
      var left = Math.ceil((restEnd - Date.now()) / 1000);
      if (left <= 0) { stopRest(); beep(); return; }
      restLeft.textContent = fmt(left);
    }
    function startRest(s) { if (!s) return; restEnd = Date.now() + s * 1000; restEl.hidden = false; tickRest(); clearInterval(restTimer); restTimer = setInterval(tickRest, 250); }
    function stopRest() { clearInterval(restTimer); restEl.hidden = true; }
    document.getElementById('rest-skip').onclick = stopRest;
    document.getElementById('rest-plus').onclick = function () { restEnd += 30000; tickRest(); };
    document.getElementById('rest-minus').onclick = function () { restEnd -= 30000; tickRest(); };

    function rowData(tr) {
      var g = function (n) { var i = tr.querySelector('[name=' + n + ']'); return i ? i.value : null; };
      var dist = num(g('distance'));
      return { kind: tr.dataset.kind || 'normal', weight: num(g('weight')), reps: num(g('reps')) == null ? null : Math.round(num(g('reps'))), distance: dist == null ? null : dist * 1000, duration: secs(g('duration')), completed: tr.classList.contains('done') };
    }
    var pending = {};
    function save(tr, immediate) {
      clearTimeout(pending[tr.dataset.id]);
      var run = function () { api('POST', '/api/sets/' + tr.dataset.id, rowData(tr)).catch(function () { tr.classList.add('unsaved'); }); };
      if (immediate) run(); else pending[tr.dataset.id] = setTimeout(run, 500);
    }
    log.addEventListener('input', function (e) { var tr = e.target.closest('tr.set'); if (tr) save(tr); });
    log.addEventListener('change', function (e) { var tr = e.target.closest('tr.set'); if (tr) save(tr, true); });
    // Enter moves to the next input
    log.addEventListener('keydown', function (e) {
      if (e.key !== 'Enter' || !e.target.matches('tr.set input')) return;
      e.preventDefault(); var all = Array.prototype.slice.call(log.querySelectorAll('tr.set input')); var n = all[all.indexOf(e.target) + 1]; if (n) n.focus(); else e.target.blur();
    });

    var kindDialog = document.getElementById('kind-dialog'), kindRow = null;
    kindDialog.addEventListener('click', function (e) {
      var k = e.target.closest('[data-kind]'); if (!k || !kindRow) return;
      kindDialog.close();
      if (k.dataset.kind === 'delete') {
        var tr = kindRow, tbody = tr.parentNode;
        api('DELETE', '/api/sets/' + tr.dataset.id).then(function () { tr.remove(); renumber(tbody); });
        return;
      }
      kindRow.dataset.kind = k.dataset.kind; kindRow.className = 'set kind-' + k.dataset.kind + (kindRow.classList.contains('done') ? ' done' : '');
      renumber(kindRow.parentNode); save(kindRow, true);
    });
    function renumber(tbody) { var n = 0; tbody.querySelectorAll('tr.set').forEach(function (tr) { var b = tr.querySelector('.kind'); var k = tr.dataset.kind; if (k === 'warmup') b.textContent = 'W'; else if (k === 'drop') b.textContent = 'D'; else if (k === 'failure') b.textContent = 'F'; else b.textContent = ++n; }); }
    function setRow(s, slot) {
      var tpl = slot.querySelector('tr.set'); var tr;
      if (tpl) { tr = tpl.cloneNode(true); } else { return location.reload(); }
      tr.className = 'set kind-normal'; tr.dataset.id = s.id; tr.dataset.kind = 'normal';
      tr.querySelector('.prev').textContent = '-';
      var set = function (n, v) { var i = tr.querySelector('[name=' + n + ']'); if (i) i.value = v == null ? '' : v; };
      set('weight', s.weight); set('reps', s.reps); set('distance', s.distance == null ? null : s.distance / 1000); set('duration', s.duration == null ? null : fmt(s.duration));
      return tr;
    }

    log.addEventListener('click', function (e) {
      var t = e.target;
      if (t.closest('.kind')) { kindRow = t.closest('tr.set'); kindDialog.showModal(); return; }
      if (t.closest('.check')) {
        var tr = t.closest('tr.set'); var slot = tr.closest('.slot');
        tr.classList.toggle('done'); save(tr, true);
        if (tr.classList.contains('done')) { if (ding && ding.state === 'suspended') ding.resume(); startRest(+slot.dataset.rest || restDefault); }
        return;
      }
      if (t.closest('.add-set')) {
        var slot2 = t.closest('.slot'); var tbody = slot2.querySelector('tbody');
        api('POST', '/api/exercise-slots/' + slot2.dataset.id + '/sets').then(function (s) { var tr = setRow(s, slot2); if (tr) { tbody.appendChild(tr); renumber(tbody); } });
        return;
      }
      var act = t.closest('[data-act]');
      if (act) {
        var slot3 = act.closest('.slot'); slot3.querySelector('details').removeAttribute('open');
        if (act.dataset.act === 'note') { var n = slot3.querySelector('.note'); n.hidden = false; n.focus(); }
        if (act.dataset.act === 'rest') {
          var v = prompt('Rest timer for this exercise (seconds, empty = default)', slot3.dataset.rest || '');
          if (v === null) return; slot3.dataset.rest = v; api('POST', '/api/exercise-slots/' + slot3.dataset.id, { note: slot3.querySelector('.note').value, rest: v === '' ? null : +v }); act.textContent = 'Rest timer (' + (v === '' ? 'default' : fmt(+v)) + ')';
        }
        if (act.dataset.act === 'remove' && confirm('Remove exercise and its sets?')) api('DELETE', '/api/exercise-slots/' + slot3.dataset.id).then(function () { slot3.remove(); });
      }
    });
    log.addEventListener('change', function (e) {
      if (e.target.matches('.note')) { var slot = e.target.closest('.slot'); api('POST', '/api/exercise-slots/' + slot.dataset.id, { note: e.target.value, rest: slot.dataset.rest ? +slot.dataset.rest : null }); }
    });

    var finishBtn = document.getElementById('finish-btn');
    finishBtn.addEventListener('click', function (e) {
      if (finishBtn.dataset.template && confirm('Update the template with this workout?')) document.getElementById('update_template').value = '1';
    });
    window.addEventListener('beforeunload', function () { Object.keys(pending).forEach(function (id) { var tr = log.querySelector('tr.set[data-id="' + id + '"]'); if (tr) { clearTimeout(pending[id]); navigator.sendBeacon && fetch('/api/sets/' + id, { method: 'POST', keepalive: true, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(rowData(tr)) }); } }); });
  }

  // ---- template editor ----
  var ed = document.getElementById('tpl-editor');
  if (ed) {
    var state = window.TEMPLATE || {};
    var exs = (state.Exercises || []).map(function (e) {
      return { exercise: e.ExerciseID, name: e.Exercise.Name, cells: e.Exercise.CellTypes || [], note: e.Note || '', rest: e.RestS, sets: (e.Sets || []).map(function (s) { return { kind: s.Kind || 'normal', weight: s.WeightKg, reps: s.Reps, duration: s.DurationS, distance: s.DistanceM }; }) };
    });
    var list = document.getElementById('tpl-exercises');
    function hasW(c) { return c.some(function (x) { return /WEIGHT/.test(x); }); }
    function render() {
      list.innerHTML = '';
      exs.forEach(function (ex, i) {
        var d = document.createElement('div'); d.className = 'tpl-ex';
        var h = '<div class="row"><strong class="grow">' + esc(ex.name) + '</strong><button data-i="' + i + '" data-do="up">↑</button><button data-i="' + i + '" data-do="down">↓</button><button data-i="' + i + '" data-do="rm" class="danger">✕</button></div>';
        h += '<div class="row"><input placeholder="Note" data-i="' + i + '" data-f="note" value="' + esc(ex.note) + '"><input type="number" placeholder="Rest s" style="width:90px" data-i="' + i + '" data-f="rest" value="' + (ex.rest == null ? '' : ex.rest) + '"></div>';
        h += '<div class="tpl-set muted small"><span>SET</span><span>' + (hasW(ex.cells) ? 'KG' : ex.cells.indexOf('DISTANCE') >= 0 ? 'KM' : '') + '</span><span>' + (ex.cells.indexOf('REPS') >= 0 ? 'REPS' : ex.cells.indexOf('DURATION') >= 0 ? 'TIME' : '') + '</span><span></span></div>';
        ex.sets.forEach(function (s, j) {
          var a = hasW(ex.cells) ? '<input type="number" step="0.5" data-i="' + i + '" data-j="' + j + '" data-f="weight" value="' + (s.weight == null ? '' : s.weight) + '">' : ex.cells.indexOf('DISTANCE') >= 0 ? '<input type="number" step="0.01" data-i="' + i + '" data-j="' + j + '" data-f="distance" value="' + (s.distance == null ? '' : s.distance / 1000) + '">' : '<span></span>';
          var b = ex.cells.indexOf('REPS') >= 0 ? '<input type="number" data-i="' + i + '" data-j="' + j + '" data-f="reps" value="' + (s.reps == null ? '' : s.reps) + '">' : ex.cells.indexOf('DURATION') >= 0 ? '<input placeholder="m:ss" data-i="' + i + '" data-j="' + j + '" data-f="duration" value="' + (s.duration == null ? '' : fmt(s.duration)) + '">' : '<span></span>';
          h += '<div class="tpl-set"><button data-i="' + i + '" data-j="' + j + '" data-do="kind">' + (s.kind === 'warmup' ? 'W' : s.kind === 'drop' ? 'D' : s.kind === 'failure' ? 'F' : j + 1) + '</button>' + a + b + '<button data-i="' + i + '" data-j="' + j + '" data-do="rmset">✕</button></div>';
        });
        h += '<button class="wide" data-i="' + i + '" data-do="addset">+ ADD SET</button>';
        d.innerHTML = h; list.appendChild(d);
      });
    }
    function esc(s) { return String(s == null ? '' : s).replace(/[&<>"]/g, function (c) { return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]; }); }
    list.addEventListener('click', function (e) {
      var b = e.target.closest('[data-do]'); if (!b) return;
      var i = +b.dataset.i, j = +b.dataset.j, ex = exs[i];
      switch (b.dataset.do) {
        case 'up': if (i > 0) { exs.splice(i - 1, 0, exs.splice(i, 1)[0]); } break;
        case 'down': if (i < exs.length - 1) { exs.splice(i + 1, 0, exs.splice(i, 1)[0]); } break;
        case 'rm': exs.splice(i, 1); break;
        case 'addset': ex.sets.push(Object.assign({ kind: 'normal' }, ex.sets[ex.sets.length - 1] || {})); break;
        case 'rmset': ex.sets.splice(j, 1); break;
        case 'kind': var k = ['normal', 'warmup', 'drop', 'failure']; ex.sets[j].kind = k[(k.indexOf(ex.sets[j].kind) + 1) % k.length]; break;
      }
      render();
    });
    list.addEventListener('input', function (e) {
      var t = e.target; if (!t.dataset.f) return; var ex = exs[+t.dataset.i];
      if (t.dataset.f === 'note') ex.note = t.value;
      else if (t.dataset.f === 'rest') ex.rest = t.value === '' ? null : +t.value;
      else { var s = ex.sets[+t.dataset.j]; if (t.dataset.f === 'duration') s.duration = secs(t.value); else if (t.dataset.f === 'distance') { var d = num(t.value); s.distance = d == null ? null : d * 1000; } else if (t.dataset.f === 'reps') { var r = num(t.value); s.reps = r == null ? null : Math.round(r); } else s.weight = num(t.value); }
    });
    var picker = document.getElementById('picker'), pq = document.getElementById('picker-q');
    document.getElementById('tpl-add').onclick = function () { picker.showModal(); pq.focus(); };
    pq.addEventListener('input', function () { var q = pq.value.toLowerCase(); picker.querySelectorAll('.item').forEach(function (li) { li.hidden = li.dataset.name.toLowerCase().indexOf(q) < 0; }); });
    picker.addEventListener('click', function (e) {
      var li = e.target.closest('.item'); if (!li) return;
      exs.push({ exercise: li.dataset.id, name: li.dataset.name, cells: JSON.parse(li.dataset.cells || '[]'), note: '', rest: null, sets: [{ kind: 'normal' }, { kind: 'normal' }, { kind: 'normal' }] });
      picker.close(); render();
    });
    document.getElementById('tpl-save').onclick = function () {
      var body = { name: document.getElementById('tpl-name').value, folder: document.getElementById('tpl-folder').value || null, notes: document.getElementById('tpl-notes').value, exercises: exs.map(function (e) { return { exercise: e.exercise, note: e.note, rest: e.rest, sets: e.sets }; }) };
      api('POST', '/templates/' + ed.dataset.id, body).then(function (r) { location.href = r.redirect; }).catch(function (e) { alert('Save failed: ' + e.message); });
    };
    render();
  }
})();
