'use strict';
// Hub service worker: makes the Hub installable, keeps its shell and shows the
// Authority's notices (enrollment, recovery) by Web Push. The shell is served
// from the cache and refreshed only when the network serves it, which is the
// LAN listener: the Access Gate keeps /device/ local-only, so a readable edge
// on a remote path never replaces the code that holds the device key. The
// worker never touches an identity route.
const SHELL = 'stackkit-hub-shell-v2';
const ASSETS = ['/device/', '/device/app.js', '/device/app.css', '/device/manifest.webmanifest', '/device/icon-192.png', '/device/icon-512.png'];

self.addEventListener('install', (event) => {
  event.waitUntil(caches.open(SHELL).then((cache) => cache.addAll(ASSETS)).then(() => self.skipWaiting()));
});
self.addEventListener('activate', (event) => {
  event.waitUntil(caches.keys().then((keys) => Promise.all(keys.filter((key) => key !== SHELL).map((key) => caches.delete(key)))).then(() => self.clients.claim()));
});
function refresh(path) {
  return fetch(path, { cache: 'no-store' }).then((response) => {
    if (response.ok && response.type === 'basic') return caches.open(SHELL).then((cache) => cache.put(path, response));
    return undefined;
  }).catch(() => undefined);
}
self.addEventListener('fetch', (event) => {
  const url = new URL(event.request.url);
  if (event.request.method !== 'GET' || url.origin !== self.location.origin || !url.pathname.startsWith('/device/')) return;
  // Every navigation inside the scope (the callback included) is the shell.
  const path = event.request.mode === 'navigate' ? '/device/' : url.pathname;
  if (!ASSETS.includes(path)) return;
  event.respondWith(caches.match(path).then((cached) => {
    const update = refresh(path);
    if (cached) { event.waitUntil(update); return cached; }
    return update.then(() => caches.match(path)).then((fresh) => fresh || fetch(event.request));
  }));
});

const TEXT = {
  'enrolled-at-home': (n) => ['New device at home', `${n.deviceName || 'A device'} was connected at home. Open the Hub to undo it within 24 hours.`],
  'enrollment-pending': (n) => ['Confirm a new device', `${n.deviceName || 'A device'} asks to join your account. Open the Hub to confirm or decline it.`],
  'recovery-public-certificate-renewal-failing': () => ['Public certificate not renewed', 'The public certificate of your homelab expires within 14 days and its renewal keeps failing. Check the DNS token, then run stackkit reach public-certificate issue.'],
};
self.addEventListener('push', (event) => {
  let notice = {};
  try { notice = event.data ? event.data.json() : {}; } catch { notice = {}; }
  const make = TEXT[notice.kind] || (() => ['Homelab identity', notice.kind && notice.kind.startsWith('recovery-') ? 'An Owner recovery event happened. Open the Hub for details.' : 'Open the Hub for details.']);
  const [title, body] = make(notice);
  event.waitUntil(self.registration.showNotification(title, { body, tag: notice.noticeId || 'hub', icon: '/device/icon-192.png', data: { url: '/device/' } }));
});
self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  event.waitUntil(self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((windows) => {
    const open = windows.find((client) => new URL(client.url).pathname.startsWith('/device/'));
    return open ? open.focus() : self.clients.openWindow('/device/');
  }));
});
