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

  // ---- weight units ----
  // Storage and the /api/sets contract are kg; `unit` is display only
  // (the `weight_unit` setting, surfaced as #log[data-unit]).
  var LB_PER_KG = 2.2046226218;
  function round1(v) { return Math.round(v * 10) / 10; }
  function toDisplay(kg, unit) { return kg == null ? null : unit === 'lb' ? round1(kg * LB_PER_KG) : kg; }
  function toKg(v, unit) { return v == null ? null : unit === 'lb' ? Math.round(v / LB_PER_KG * 100) / 100 : v; }
  // plateSplit: per-side plates for `targetKg` on `barKg`, greedy over
  // `plates` (kg, any order). Returns { sides: [{plate, n}], rest } where
  // rest is the per-side kg the plate set cannot make (0 when exact).
  function plateSplit(targetKg, barKg, plates) {
    var side = (targetKg - barKg) / 2, out = [];
    if (!(side > 0)) return { sides: out, rest: side < 0 ? side : 0 };
    plates.slice().sort(function (a, b) { return b - a; }).forEach(function (p) {
      if (!(p > 0)) return;
      var n = Math.floor((side + 1e-9) / p); if (n > 0) { out.push({ plate: p, n: n }); side = Math.round((side - n * p) * 1000) / 1000; }
    });
    return { sides: out, rest: side };
  }
  // warmupRamp: warm-up sets leading up to `workingKg` on `barKg`:
  // bar×10, 50 %×5, 70 %×3, 90 %×1, each rounded DOWN to what the plate
  // set can load (multiples of 2×smallest plate above the bar). Steps
  // that land on the bar or repeat the previous step are dropped; an
  // empty list when the working weight is not above the bar. Pure, kg
  // in / kg out — the caller converts for display.
  function warmupRamp(workingKg, barKg, plates) {
    var out = [];
    if (!(workingKg > barKg)) return out;
    var small = plates.filter(function (p) { return p > 0; }).sort(function (a, b) { return a - b; })[0];
    var inc = small ? small * 2 : 2.5;
    out.push({ weight: barKg, reps: 10 });
    [[.5, 5], [.7, 3], [.9, 1]].forEach(function (step) {
      var w = barKg + Math.floor((workingKg * step[0] - barKg + 1e-9) / inc) * inc;
      w = Math.round(w * 1000) / 1000;
      if (w <= barKg || w === out[out.length - 1].weight) return;
      out.push({ weight: w, reps: step[1] });
    });
    return out;
  }
  window.Workouts = { plateSplit: plateSplit, warmupRamp: warmupRamp, toKg: toKg, toDisplay: toDisplay };

  // ---- active workout ----
  var log = document.getElementById('log');
  if (log) {
    var restDefault = +log.dataset.rest || 120;
    var unit = log.dataset.unit === 'lb' ? 'lb' : 'kg';
    var plates = (log.dataset.plates || '25,20,15,10,5,2.5,1.25').split(',').map(num).filter(function (p) { return p != null; });
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
      // An untouched weight input still carries the exact stored kg in
      // data-kg; once edited, the typed display value is converted.
      var wi = tr.querySelector('[name=weight]');
      var weight = !wi ? null : wi.dataset.kg != null ? num(wi.dataset.kg) : toKg(num(wi.value), unit);
      return { kind: tr.dataset.kind || 'normal', weight: weight, reps: num(g('reps')) == null ? null : Math.round(num(g('reps'))), distance: dist == null ? null : dist * 1000, duration: secs(g('duration')), completed: tr.classList.contains('done') };
    }
    // ---- offline writes ----
    // sw.js answers 202 {queued:true} when a write had to be parked in
    // IndexedDB; the row/slot shows `.unsaved` until the SW replays it
    // and posts {type:'saved'|'rejected', url}. Background Sync is a
    // bonus (no Safari); the SW also drains on `online` and on the next
    // successful fetch.
    // Add-set and warm-ups need server-issued ids, so they are online-only
    // (sw.js does not queue them); one message for both.
    function offline() { alert('Could not add sets \u2014 you seem to be offline. Try again when connected.'); }
    var sw = navigator.serviceWorker;
    function requestSync() {
      if (!sw || !sw.ready) return;
      sw.ready.then(function (reg) { if (reg.sync) return reg.sync.register('writes'); }).catch(function () {});
    }
    // mark(el, r): reflect a write's outcome on el; r is the api() result
    // (queued or not) or undefined for a hard failure.
    function mark(el, r) {
      el.classList.remove('rejected');
      if (r && r.queued) { el.classList.add('unsaved'); requestSync(); } else if (r) el.classList.remove('unsaved'); else el.classList.add('unsaved');
    }
    function targetOf(url) {
      var m = /^\/api\/(sets|exercise-slots)\/([^/]+)$/.exec(url || ''); if (!m) return null;
      return log.querySelector(m[1] === 'sets' ? 'tr.set[data-id="' + m[2] + '"]' : '.slot[data-id="' + m[2] + '"]');
    }
    if (sw) {
      sw.addEventListener('message', function (e) {
        var d = e.data || {}; var el = targetOf(d.url); if (!el) return;
        if (d.type === 'saved') { el.classList.remove('unsaved'); el.classList.remove('rejected'); }
        else if (d.type === 'rejected') { el.classList.remove('unsaved'); el.classList.add('rejected'); }
      });
      window.addEventListener('online', function () { if (sw.controller) sw.controller.postMessage({ type: 'flush' }); });
    }
    // slotSave writes the slot's note / rest / superset from its DOM state.
    // The API overwrites all three, so every save must carry the current
    // superset or it would be NULLed.
    function slotSave(slot) {
      var body = { note: slot.querySelector('.note').value, rest: slot.dataset.rest ? +slot.dataset.rest : null, superset: slot.dataset.superset ? +slot.dataset.superset : null };
      api('POST', '/api/exercise-slots/' + slot.dataset.id, body).then(function (r) { mark(slot, r); }, function () { mark(slot); });
    }

    // ---- supersets ----
    // Slots with the same data-superset form a group: a coloured left
    // border (one colour per group) and an SS<n> tag. The rest timer
    // starts only after the group's last slot; the others point onward.
    var SS_COLOURS = ['#4a9eff', '#e8a33d', '#c678dd', '#3dd68c'];
    function slots() { return Array.prototype.slice.call(log.querySelectorAll('.slot')); }
    function groupOf(slot) { var g = slot.dataset.superset; return g ? slots().filter(function (s) { return s.dataset.superset === g; }) : [slot]; }
    function renderSupersets() {
      slots().forEach(function (s) {
        var g = +s.dataset.superset || 0, tag = s.querySelector('.ss-tag'), btn = s.querySelector('[data-act=superset]');
        s.classList.toggle('ss', g > 0);
        if (g > 0) s.style.setProperty('--ss', SS_COLOURS[(g - 1) % SS_COLOURS.length]); else s.style.removeProperty('--ss');
        tag.hidden = !g; tag.textContent = 'SS' + (g || '');
        if (btn) btn.textContent = g ? 'Remove from superset' : 'Superset with\u2026';
      });
    }
    function superset(slot) {
      if (slot.dataset.superset) {
        var rest = groupOf(slot).filter(function (s) { return s !== slot; });
        slot.dataset.superset = ''; slotSave(slot);
        if (rest.length === 1) { rest[0].dataset.superset = ''; slotSave(rest[0]); } // a group of one is no group
        renderSupersets(); return;
      }
      var others = slots().filter(function (s) { return s !== slot; });
      if (!others.length) { alert('Add another exercise first.'); return; }
      // The picker is a dialog of the other slots (tho0: a prompt() was
      // the first cut); picking one joins its group or starts a new one.
      ssList.innerHTML = '';
      others.forEach(function (s) {
        var b = document.createElement('button'); b.type = 'button';
        b.textContent = s.querySelector('.ex-name').textContent + (s.dataset.superset ? ' (SS' + s.dataset.superset + ')' : '');
        b.onclick = function () { ssDialog.close(); joinSuperset(slot, s); };
        ssList.appendChild(b);
      });
      ssDialog.showModal();
    }
    function joinSuperset(slot, partner) {
      var g = partner.dataset.superset;
      if (!g) { var used = {}; slots().forEach(function (s) { if (s.dataset.superset) used[s.dataset.superset] = 1; }); for (g = 1; used[g]; g++) {} partner.dataset.superset = String(g); slotSave(partner); }
      slot.dataset.superset = String(g); slotSave(slot); renderSupersets();
    }
    var ssDialog = document.getElementById('ss-dialog'), ssList = document.getElementById('ss-list');
    renderSupersets();

    // ---- warm-up generator ----
    // The bar is the plate dialog's select (#plate-bar, kg values); the
    // last choice is remembered per device so the ramp and the calculator
    // agree (tho0). 20 kg until the user picks otherwise.
    var plateBar = document.getElementById('plate-bar');
    try { if (localStorage.bar && plateBar.querySelector('option[value="' + localStorage.bar + '"]')) plateBar.value = localStorage.bar; } catch (e) {}
    plateBar.addEventListener('change', function () { try { localStorage.bar = plateBar.value; } catch (e) {} });
    function barKg() { return +plateBar.value || 20; }
    function workingKg(slot) {
      var tr = Array.prototype.slice.call(slot.querySelectorAll('tr.set')).filter(function (r) { return (r.dataset.kind || 'normal') === 'normal'; })[0];
      var wi = tr && tr.querySelector('[name=weight]'); if (!wi) return null;
      return wi.dataset.kg != null ? num(wi.dataset.kg) : toKg(num(wi.value), unit);
    }
    function generateWarmups(slot) {
      var kg = workingKg(slot);
      if (kg == null) { alert('Set a working weight first.'); return; }
      var ramp = warmupRamp(kg, barKg(), plates);
      if (!ramp.length) { alert('Working weight is not above the bar \u2014 nothing to warm up with.'); return; }
      var tbody = slot.querySelector('tbody');
      // A second run replaces the ramp instead of stacking one (tho0):
      // the existing warm-up rows are deleted first.
      var old = Array.prototype.slice.call(tbody.querySelectorAll('tr.set[data-kind=warmup]'));
      if (old.length && !confirm('Replace the ' + old.length + ' existing warm-up set' + (old.length > 1 ? 's' : '') + '?')) return;
      Promise.all(old.map(function (tr) { return api('DELETE', '/api/sets/' + tr.dataset.id).then(function () { tr.remove(); }); }))
        .then(function () { return api('POST', '/api/exercise-slots/' + slot.dataset.id + '/warmups', ramp.map(function (r) { return { kind: 'warmup', weight: r.weight, reps: r.reps }; })); })
        .then(function (rows) { var first = tbody.firstChild; rows.forEach(function (s) { var tr = setRow(s, slot); if (tr) tbody.insertBefore(tr, first); }); renumber(tbody); })
        .catch(offline);
    }

    var pending = {};
    function save(tr, immediate) {
      clearTimeout(pending[tr.dataset.id]);
      var run = function () { delete pending[tr.dataset.id]; api('POST', '/api/sets/' + tr.dataset.id, rowData(tr)).then(function (r) { mark(tr, r); }, function () { mark(tr); }); };
      if (immediate) run(); else pending[tr.dataset.id] = setTimeout(run, 500);
    }
    log.addEventListener('input', function (e) { if (e.target.name === 'weight') delete e.target.dataset.kg; var tr = e.target.closest('tr.set'); if (tr) save(tr); });
    log.addEventListener('change', function (e) { var tr = e.target.closest('tr.set'); if (tr) save(tr, true); });
    // Enter moves to the next input
    log.addEventListener('keydown', function (e) {
      if (e.key !== 'Enter' || !e.target.matches('tr.set input')) return;
      e.preventDefault(); var all = Array.prototype.slice.call(log.querySelectorAll('tr.set input')); var n = all[all.indexOf(e.target) + 1]; if (n) n.focus(); else e.target.blur();
    });

    var kindDialog = document.getElementById('kind-dialog'), kindRow = null, kindPlates = document.getElementById('kind-plates');
    kindDialog.addEventListener('click', function (e) {
      var k = e.target.closest('[data-kind]'); if (!k || !kindRow) return;
      kindDialog.close();
      if (k.dataset.kind === 'plates') { openPlates(kindRow); return; }
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
      // Clone an existing row, else the slot's server-rendered blank
      // <template> (an emptied slot used to reload the page here).
      var tpl = slot.querySelector('tbody tr.set'), tr;
      if (tpl) { tr = tpl.cloneNode(true); } else { tr = slot.querySelector('template.set-tpl').content.firstElementChild.cloneNode(true); }
      var kind = s.kind || 'normal';
      tr.className = 'set kind-' + kind; tr.dataset.id = s.id; tr.dataset.kind = kind;
      tr.querySelector('.prev').textContent = '-';
      var set = function (n, v) { var i = tr.querySelector('[name=' + n + ']'); if (i) i.value = v == null ? '' : v; };
      set('weight', toDisplay(s.weight, unit)); var wi = tr.querySelector('[name=weight]'); if (wi) wi.dataset.kg = s.weight == null ? '' : s.weight;
      set('reps', s.reps); set('distance', s.distance == null ? null : s.distance / 1000); set('duration', s.duration == null ? null : fmt(s.duration));
      return tr;
    }

    // ---- plate calculator (per side, greedy over the `plates` setting) ----
    var plateDialog = document.getElementById('plate-dialog'), plateTarget = document.getElementById('plate-target'), plateList = document.getElementById('plate-list'), plateNote = document.getElementById('plate-note');
    function renderPlates() {
      var target = toKg(num(plateTarget.value), unit), bar = +plateBar.value;
      plateList.innerHTML = ''; plateNote.textContent = '';
      if (target == null) return;
      var r = plateSplit(target, bar, plates);
      if (target < bar) { plateNote.textContent = 'Lighter than the bar.'; return; }
      r.sides.forEach(function (s) { var li = document.createElement('li'); li.innerHTML = '<b>' + s.n + ' ×</b> ' + toDisplay(s.plate, unit) + ' ' + unit; plateList.appendChild(li); });
      if (!r.sides.length) plateNote.textContent = 'Empty bar.';
      if (r.rest > 0) plateNote.textContent = toDisplay(r.rest * 2, unit) + ' ' + unit + ' short — no plate small enough.';
    }
    function openPlates(tr) {
      var wi = tr.querySelector('[name=weight]'); if (!wi) return;
      plateTarget.value = wi.value; renderPlates(); plateDialog.showModal();
    }
    plateTarget.addEventListener('input', renderPlates);
    plateBar.addEventListener('change', renderPlates);

    log.addEventListener('click', function (e) {
      var t = e.target;
      if (t.closest('.kind')) { kindRow = t.closest('tr.set'); kindPlates.hidden = !kindRow.closest('.slot').dataset.plates; kindDialog.showModal(); return; }
      if (t.closest('.check')) {
        var tr = t.closest('tr.set'); var slot = tr.closest('.slot');
        tr.classList.toggle('done'); save(tr, true);
        if (tr.classList.contains('done')) {
          if (ding && ding.state === 'suspended') ding.resume();
          var grp = groupOf(slot), next = grp[grp.indexOf(slot) + 1];
          if (next) { var tag = slot.querySelector('.ss-tag'); tag.textContent = 'SS' + slot.dataset.superset + ' \u2192 next'; setTimeout(function () { renderSupersets(); }, 2500); next.scrollIntoView({ block: 'center', behavior: 'smooth' }); }
          else startRest(+slot.dataset.rest || restDefault);
        }
        return;
      }
      if (t.closest('.add-set')) {
        var slot2 = t.closest('.slot'); var tbody = slot2.querySelector('tbody');
        // Add-set needs the server-issued id, so it is online-only.
        api('POST', '/api/exercise-slots/' + slot2.dataset.id + '/sets').then(function (s) { var tr = setRow(s, slot2); if (tr) { tbody.appendChild(tr); renumber(tbody); } }).catch(offline);
        return;
      }
      var act = t.closest('[data-act]');
      if (act) {
        var slot3 = act.closest('.slot'); slot3.querySelector('details').removeAttribute('open');
        if (act.dataset.act === 'note') { var n = slot3.querySelector('.note'); n.hidden = false; n.focus(); }
        if (act.dataset.act === 'rest') {
          var v = prompt('Rest timer for this exercise (seconds, empty = default)', slot3.dataset.rest || '');
          if (v === null) return; slot3.dataset.rest = v; slotSave(slot3); act.textContent = 'Rest timer (' + (v === '' ? 'default' : fmt(+v)) + ')';
        }
        if (act.dataset.act === 'superset') superset(slot3);
        if (act.dataset.act === 'warmup') generateWarmups(slot3);
        if (act.dataset.act === 'remove' && confirm('Remove exercise and its sets?')) api('DELETE', '/api/exercise-slots/' + slot3.dataset.id).then(function () { slot3.remove(); renderSupersets(); });
      }
    });
    log.addEventListener('change', function (e) {
      if (e.target.matches('.note')) slotSave(e.target.closest('.slot'));
    });

    var finishBtn = document.getElementById('finish-btn');
    finishBtn.addEventListener('click', function (e) {
      if (finishBtn.dataset.template && confirm('Update the template with this workout?')) document.getElementById('update_template').value = '1';
    });
    window.addEventListener('beforeunload', function () { Object.keys(pending).forEach(function (id) { var tr = log.querySelector('tr.set[data-id="' + id + '"]'); if (tr) { clearTimeout(pending[id]); fetch('/api/sets/' + id, { method: 'POST', keepalive: true, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(rowData(tr)) }); } }); });
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
