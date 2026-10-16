// Workouts service worker: app shell + media cache-first, pages
// network-first with a cached fallback so history/exercises still open
// in the gym basement. Writes are not queued offline (v0): the live
// screen shows "unsaved" and retries on the next change.
const VERSION = 'v1';
const SHELL = ['/static/app.css', '/static/app.js', '/static/icon.svg', '/static/icon-192.png', '/static/icon-512.png', '/manifest.webmanifest'];

self.addEventListener('install', e => {
  e.waitUntil(caches.open('shell-' + VERSION).then(c => c.addAll(SHELL)).then(() => self.skipWaiting()));
});
self.addEventListener('activate', e => {
  e.waitUntil(caches.keys().then(keys => Promise.all(keys.filter(k => !k.endsWith(VERSION)).map(k => caches.delete(k)))).then(() => self.clients.claim()));
});
self.addEventListener('fetch', e => {
  const req = e.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
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
    if (res.ok) caches.open('pages-' + VERSION).then(c => c.put(req, res.clone()));
    return res;
  }).catch(() => caches.match(req).then(r => r || new Response('<h1>Offline</h1><p>This page is not cached yet.</p>', { headers: { 'Content-Type': 'text/html' } }))));
});
