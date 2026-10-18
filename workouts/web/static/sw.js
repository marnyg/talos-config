// Workouts service worker: app shell + media cache-first, pages
// network-first with a cached fallback so history/exercises still open
// in the gym basement.
//
// Writes (POST/DELETE to /api/sets/<id> and /api/exercise-slots/<id>)
// are network-first with an IndexedDB fallback: when the network fails
// the request is stored in the `writes` store — one entry per
// method+url, so a newer body for the same set replaces the older —
// and answered `202 {"queued":true}` so the page can mark the row
// "unsaved". The queue is drained in `seq` order on Background Sync
// (tag `writes`), on a `{type:'flush'}` message from the page (sent on
// `online`), and opportunistically before the next write / after the
// next successful page fetch. Each replay posts `{type:'saved'|
// 'rejected', url, status}` to all clients. A non-2xx reply drops the
// entry (the server refused it; retrying will not help); a network
// failure stops the drain and keeps the rest for next time.
// Add-set (POST .../sets) is not queued: the page needs the new id.
const VERSION = 'v4';
const SHELL = ['/static/app.css', '/static/app.js', '/static/icon.svg', '/static/icon-192.png', '/static/icon-512.png', '/manifest.webmanifest'];
const WRITE_PATH = /^\/api\/(sets|exercise-slots)\/[^/]+$/;
const DB = 'workouts-sw', STORE = 'writes';

self.addEventListener('install', e => {
  e.waitUntil(caches.open('shell-' + VERSION).then(c => c.addAll(SHELL)).then(() => self.skipWaiting()));
});
self.addEventListener('activate', e => {
  e.waitUntil(caches.keys().then(keys => Promise.all(keys.filter(k => !k.endsWith(VERSION)).map(k => caches.delete(k)))).then(() => self.clients.claim()));
});

// ---- write queue (IndexedDB) ----
function openDB() {
  return new Promise((ok, fail) => {
    const r = indexedDB.open(DB, 1);
    r.onupgradeneeded = () => { const s = r.result.createObjectStore(STORE, { keyPath: 'key' }); s.createIndex('seq', 'seq'); };
    r.onsuccess = () => ok(r.result); r.onerror = () => fail(r.error);
  });
}
function tx(mode, fn) {
  return openDB().then(db => new Promise((ok, fail) => {
    const t = db.transaction(STORE, mode), req = fn(t.objectStore(STORE));
    t.oncomplete = () => { db.close(); ok(req && req.result); };
    t.onerror = t.onabort = () => { db.close(); fail(t.error); };
  }));
}
// maybeQueued: false only once a drain has seen an empty store, so a
// fresh SW (which may have inherited a queue) checks on first chance.
let maybeQueued = true, draining = null, lastSeq = 0;
function enqueue(method, url, body) {
  lastSeq = Math.max(Date.now(), lastSeq + 1);
  maybeQueued = true;
  return tx('readwrite', s => s.put({ key: method + ' ' + url, seq: lastSeq, method, url, body, ts: Date.now() }));
}
function broadcast(msg) {
  return self.clients.matchAll({ includeUncontrolled: true }).then(cs => cs.forEach(c => c.postMessage(msg)));
}
function drain() {
  if (draining) return draining;
  draining = (async () => {
    const all = await tx('readonly', s => s.index('seq').getAll());
    for (const w of all || []) {
      let res;
      try {
        res = await fetch(w.url, { method: w.method, headers: w.body == null ? {} : { 'Content-Type': 'application/json' }, body: w.body });
      } catch (err) { return; } // still offline: keep this and the rest for next time
      await tx('readwrite', s => s.delete(w.key));
      await broadcast({ type: res.ok ? 'saved' : 'rejected', url: w.url, status: res.status });
    }
    maybeQueued = false;
  })().catch(() => {}).then(() => { draining = null; });
  return draining;
}
async function writeThrough(req, url) {
  const copy = req.clone();
  if (maybeQueued) await drain(); // keep queued writes ahead of this one
  try {
    return await fetch(req);
  } catch (err) {
    const body = req.method === 'POST' ? await copy.text() : null;
    await enqueue(req.method, url.pathname, body);
    return new Response('{"queued":true}', { status: 202, headers: { 'Content-Type': 'application/json' } });
  }
}

self.addEventListener('sync', e => { if (e.tag === 'writes') e.waitUntil(drain()); });
self.addEventListener('message', e => { if (e.data && e.data.type === 'flush') e.waitUntil(drain()); });

self.addEventListener('fetch', e => {
  const req = e.request;
  const url = new URL(req.url);
  if ((req.method === 'POST' || req.method === 'DELETE') && url.origin === location.origin && WRITE_PATH.test(url.pathname)) {
    e.respondWith(writeThrough(req, url));
    return;
  }
  if (req.method !== 'GET') return;
  if (url.pathname.startsWith('/static/') || url.pathname === '/manifest.webmanifest') {
    e.respondWith(caches.match(req).then(r => r || fetch(req)));
    return;
  }
  if (url.pathname.startsWith('/media/')) {
    e.respondWith(caches.open('media-' + VERSION).then(async c => {
      const hit = await c.match(req); if (hit) return hit;
      const res = await fetch(req); if (res.ok) c.put(req, res.clone()); return res;
    }));
    return;
  }
  if (url.pathname.startsWith('/api/')) return;
  e.respondWith(fetch(req).then(res => {
    if (res.ok) {
      caches.open('pages-' + VERSION).then(c => c.put(req, res.clone()));
      if (maybeQueued) e.waitUntil(drain()); // back online: replay what the basement left behind
    }
    return res;
  }).catch(() => caches.match(req).then(r => r || new Response('<h1>Offline</h1><p>This page is not cached yet.</p>', { headers: { 'Content-Type': 'text/html' } }))));
});
