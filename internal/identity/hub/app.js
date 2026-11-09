'use strict';
// Homelab Hub: the device client of the homelab identity target (§6.1-§6.3).
// The device key is a WebCrypto P-256 key generated non-extractable and kept in
// IndexedDB; only its public half ever leaves this browser. Every identity
// request carries the device credential and a fresh DPoP proof (RFC 9449).

const enc = new TextEncoder();
const $ = (selector) => document.querySelector(selector);
const b64u = (bytes) => btoa(String.fromCharCode(...new Uint8Array(bytes))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
const fromB64u = (text) => Uint8Array.from(atob(text.replace(/-/g, '+').replace(/_/g, '/') + '==='.slice((text.length + 3) % 4)), (c) => c.charCodeAt(0));
const sha256 = async (text) => b64u(await crypto.subtle.digest('SHA-256', enc.encode(text)));
const random = () => b64u(crypto.getRandomValues(new Uint8Array(32)));
const paths = {
  renew: '/api/v1/identity/home/device/renew',
  devices: '/api/v1/identity/home/devices',
  notices: '/api/v1/identity/home/notices',
  push: '/api/v1/identity/home/push/subscription',
  recoveryNotices: '/api/v1/identity/recovery/device-notices',
  approvals: '/api/v1/identity/home/step-up/requests',
  appPasses: '/api/v1/identity/home/app-passes',
};
// What waits for a passkey user verification: a launch or an app pass.
let afterVerification = null;

// ---- storage -------------------------------------------------------------
function openStore() {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open('stackkit-hub', 1);
    request.onupgradeneeded = () => request.result.createObjectStore('state');
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}
async function stored(key, value) {
  const db = await openStore();
  try {
    return await new Promise((resolve, reject) => {
      const tx = db.transaction('state', value === undefined ? 'readonly' : 'readwrite');
      const store = tx.objectStore('state');
      const request = value === undefined ? store.get(key) : value === null ? store.delete(key) : store.put(value, key);
      tx.oncomplete = () => resolve(request.result);
      tx.onerror = () => reject(tx.error);
    });
  } finally {
    db.close();
  }
}

// ---- device key and proofs -----------------------------------------------
async function deviceKey() {
  let pair = await stored('deviceKey');
  if (!pair) {
    // extractable=false: the private key can sign but never be exported.
    pair = await crypto.subtle.generateKey({ name: 'ECDSA', namedCurve: 'P-256' }, false, ['sign', 'verify']);
    await stored('deviceKey', pair);
  }
  return pair;
}
async function publicJWK(pair) {
  const jwk = await crypto.subtle.exportKey('jwk', pair.publicKey);
  return { crv: jwk.crv, kty: jwk.kty, x: jwk.x, y: jwk.y };
}
async function thumbprint(pair) {
  const jwk = await publicJWK(pair);
  return sha256(JSON.stringify({ crv: jwk.crv, kty: jwk.kty, x: jwk.x, y: jwk.y }));
}
async function signJWT(pair, header, claims) {
  const input = b64u(enc.encode(JSON.stringify(header))) + '.' + b64u(enc.encode(JSON.stringify(claims)));
  const signature = await crypto.subtle.sign({ name: 'ECDSA', hash: 'SHA-256' }, pair.privateKey, enc.encode(input));
  return input + '.' + b64u(signature);
}
async function proof(method, url, jti, credential) {
  const pair = await deviceKey();
  const claims = { jti: jti || random(), htm: method, htu: url, iat: Math.floor(Date.now() / 1000) };
  if (credential) claims.ath = await sha256(credential);
  return signJWT(pair, { typ: 'dpop+jwt', alg: 'ES256', jwk: await publicJWK(pair) }, claims);
}

// ---- server calls --------------------------------------------------------
let config;
async function loadConfig() {
  const response = await fetch('/api/v1/identity/hub/config', { cache: 'no-store' });
  if (!response.ok) throw new Error('The Hub is not ready on this server yet.');
  config = await response.json();
}
async function answer(response) {
  let body = {};
  try { body = await response.json(); } catch { /* empty */ }
  if (!response.ok) {
    const error = new Error(body.user_guidance || body.reason_code || ('Request failed: ' + response.status));
    error.reason = body.reason_code;
    error.status = response.status;
    throw error;
  }
  return body;
}
async function deviceRequest(method, path, body) {
  let credential = await currentCredential();
  const url = config.hubOrigin + path;
  const headers = { Authorization: 'DPoP ' + credential.credential, DPoP: await proof(method, url, null, credential.credential) };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  return answer(await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), cache: 'no-store' }));
}
async function currentCredential() {
  const credential = await stored('credential');
  if (!credential) throw new Error('This device is not connected yet.');
  if (Date.parse(credential.expiresAt) - Date.now() < 5 * 60 * 1000) return renew();
  return credential;
}

// renew asks for a new one-hour credential with a DPoP proof; after 24 hours
// without a passkey sign-in the server asks for one first.
async function renew(userVerification, jti) {
  const url = config.hubOrigin + paths.renew;
  const response = await fetch(paths.renew, {
    method: 'POST', cache: 'no-store',
    headers: { DPoP: await proof('POST', url, jti), 'Content-Type': 'application/json' },
    body: userVerification ? JSON.stringify({ userVerification }) : '',
  });
  if (response.status === 401) {
    const body = await response.clone().json().catch(() => ({}));
    if (body.reason_code === 'user_verification_required') {
      await verifyUser();
      return new Promise(() => {});
    }
  }
  const credential = await answer(response);
  await stored('credential', credential);
  await stored('pending', null);
  return credential;
}

// verifyUser signs the person in with their passkey and renews with it; the
// step that waited (afterVerification) continues after the callback.
async function verifyUser() {
  const next = random();
  await signIn({ action: 'renew', jti: next, then: afterVerification }, await sha256('stackkit-device-user-verification/v1\0' + await thumbprint(await deviceKey()) + '\0' + next));
}

// ---- passkey sign-in (authorization code + PKCE at the Authority) --------
async function signIn(flow, nonce) {
  const verifier = random();
  const state = random();
  await stored('flow', { ...flow, verifier, state, at: Date.now() });
  const authorize = new URL('/authorize', config.issuer);
  authorize.search = new URLSearchParams({
    client_id: config.clientId, redirect_uri: config.redirectUri, response_type: 'code', scope: 'openid',
    state, nonce, code_challenge: await sha256(verifier), code_challenge_method: 'S256',
  }).toString();
  location.assign(authorize.toString());
}
async function finishSignIn() {
  const params = new URLSearchParams(location.search);
  const flow = await stored('flow');
  await stored('flow', null);
  history.replaceState(null, '', '/device/');
  if (!flow || params.get('state') !== flow.state || !params.get('code') || Date.now() - flow.at > 10 * 60 * 1000) {
    throw new Error('The sign-in could not be matched to this device. Try again.');
  }
  const { idToken } = await answer(await fetch('/api/v1/identity/hub/token', {
    method: 'POST', cache: 'no-store', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ code: params.get('code'), verifier: flow.verifier }),
  }));
  if (flow.action === 'enroll') return submitEnrollment(flow, idToken);
  if (flow.action === 'renew') {
    await renew(idToken, flow.jti);
    if (flow.then && flow.then.launch) return launchInto(flow.then.launch.target, flow.then.launch.state);
    if (flow.then && flow.then.appPass) return createAppPass(flow.then.appPass.host, flow.then.appPass.name);
    return status('Your passkey is confirmed; this device stays connected.');
  }
  if (flow.action === 'approve') {
    await deviceRequest('POST', paths.approvals + '/' + encodeURIComponent(flow.id), { stepUp: idToken });
    return status('Approved. The console continues now.');
  }
  if (flow.action === 'confirm') {
    await deviceRequest('POST', '/api/v1/identity/home/enrollments/' + encodeURIComponent(flow.deviceId) + '/confirm', { challenge: flow.challenge, stepUp: idToken });
    return status('The other device is confirmed.');
  }
  throw new Error('Unknown sign-in step.');
}

// ---- enrollment ----------------------------------------------------------
async function enroll(name) {
  const jti = random();
  const nonce = await sha256('stackkit-device-enrollment/v1\0' + await thumbprint(await deviceKey()) + '\0' + jti);
  await signIn({ action: 'enroll', name, jti }, nonce);
}
// At home the Hub reaches the Authority's LAN enrollment listener directly;
// only that listener decides home. Anywhere else (or when the listener
// refuses), the same sign-in is submitted away and waits for a confirmation.
async function submitEnrollment(flow, idToken) {
  const body = JSON.stringify({ name: flow.name, custody: 'software', person: idToken });
  const post = async (url) => fetch(url, { method: 'POST', cache: 'no-store', headers: { 'Content-Type': 'application/json', DPoP: await proof('POST', url, flow.jti) }, body });
  let response;
  if (config.homeEnrollmentUrl) {
    try { response = await post(config.homeEnrollmentUrl); } catch { response = undefined; }
    if (response && (response.status === 403 || response.status === 503)) {
      const reason = (await response.clone().json().catch(() => ({}))).reason_code;
      if (reason === 'not_at_home' || reason === 'enrollment_listener_required' || reason === 'enrollment_unavailable') response = undefined;
    }
  }
  if (!response) response = await post(config.awayEnrollmentUrl);
  const outcome = await answer(response);
  await stored('name', flow.name);
  if (outcome.credential) {
    await stored('credential', outcome.credential);
    await stored('pending', null);
    status('This device is connected. Your other devices and the Owner were told and can undo it within 24 hours.');
  } else {
    await stored('pending', { deviceId: outcome.device.id, until: outcome.device.approval.pendingUntil });
    status('Away from home: confirm this device from one of your enrolled devices (or ask the Owner), then open the Hub again.');
  }
}

// ---- browser launch (homelab identity target §6.3) ------------------------
// The Access Gate sent this browser here for one URL of the homelab. The Hub
// signs a 60 second launch proof with the device key for exactly that host;
// the Gate exchanges it for a session of this browser on that host.
async function launchInto(target, state) {
  const url = new URL(target);
  if (url.protocol !== 'https:' || url.port !== '' || !(config.launchHosts || []).includes(url.hostname)) {
    throw new Error('This link does not lead to a service of your homelab.');
  }
  afterVerification = { launch: { target, state } };
  const credential = await currentCredential();
  const launchURL = 'https://' + url.host + '/.well-known/stackkit/launch';
  const pair = await deviceKey();
  const proof = await signJWT(pair, { typ: 'stackkit-launch+jwt', alg: 'ES256', jwk: await publicJWK(pair) },
    { jti: random(), htm: 'GET', htu: launchURL, iat: Math.floor(Date.now() / 1000), ath: await sha256(credential.credential), state, target });
  status('Opening ' + url.hostname + ' ...');
  location.replace(launchURL + '?' + new URLSearchParams({ state, target, credential: credential.credential, proof }).toString());
}

// ---- app passes ------------------------------------------------------------
// An app (Immich, a WebDAV client) sends its pass in the Stackkit-App-Pass
// header. Issuing one needs a passkey sign-in on this device within 10 minutes.
async function createAppPass(host, name) {
  try {
    const { secret, header } = await deviceRequest('POST', paths.appPasses, { host, name });
    $('#app-pass-secret').value = header + ': ' + secret;
    $('#app-pass-created').hidden = false;
    return status('App pass created. Enter it in the app now; it is shown only once.');
  } catch (error) {
    if (error.reason !== 'user_verification_required') throw error;
    afterVerification = { appPass: { host, name } };
    await verifyUser();
    return undefined;
  }
}

// ---- notices and push ----------------------------------------------------
async function confirmNotice(notice) {
  const challenge = random();
  const nonce = await sha256('stackkit.homelab-step-up/v1\0' + notice.stepUp.class + '\0' + notice.stepUp.digest + '\0' + challenge);
  await signIn({ action: 'confirm', deviceId: notice.deviceId, challenge }, nonce);
}
// An Owner approval: a console action that changes identity custody waits
// for the Owner's passkey step-up bound to exactly its action digest.
async function approve(request) {
  const nonce = await sha256('stackkit.homelab-step-up/v1\0' + request.stepUp.class + '\0' + request.stepUp.digest + '\0' + request.challenge);
  await signIn({ action: 'approve', id: request.id }, nonce);
}
async function enablePush() {
  if (!('serviceWorker' in navigator) || !('PushManager' in window)) throw new Error('This browser has no Web Push. On iPhone and iPad, add the Hub to the Home Screen first.');
  if (!config.vapidPublicKey) throw new Error('This server does not send notifications yet.');
  const registration = await navigator.serviceWorker.ready;
  if ((await Notification.requestPermission()) !== 'granted') throw new Error('Notifications are blocked for the Hub.');
  const subscription = await registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: fromB64u(config.vapidPublicKey) });
  const json = subscription.toJSON();
  await deviceRequest('POST', paths.push, { endpoint: json.endpoint, keys: { p256dh: json.keys.p256dh, auth: json.keys.auth } });
  status('Notifications are on for this device.');
}

// ---- view ----------------------------------------------------------------
function status(message) { $('#status').textContent = message; }
function fail(error) { status(error && error.message ? error.message : 'Something went wrong.'); }
function item(text, actions) {
  const li = document.createElement('li');
  const label = document.createElement('span');
  label.textContent = text;
  li.append(label);
  for (const [title, run] of actions) {
    const button = document.createElement('button');
    button.type = 'button';
    button.textContent = title;
    button.addEventListener('click', () => run().then(render).catch(fail));
    li.append(button);
  }
  return li;
}
async function render() {
  let credential = await stored('credential');
  const pending = await stored('pending');
  if (!credential && pending) {
    // An away enrollment waits for a confirmation. Once an enrolled device or
    // the Owner confirmed it, the device's own DPoP-signed renewal returns
    // its first credential; until then the Authority refuses and the Hub waits.
    credential = await renew().catch(() => undefined);
  }
  $('#connect').hidden = Boolean(credential);
  $('#connected').hidden = !credential;
  if (pending && !credential) status('Waiting for the confirmation of this device.');
  if (!credential) return;
  $('#device-name').textContent = (await stored('name')) || credential.device.name;
  const { devices } = await deviceRequest('GET', paths.devices);
  const self = credential.device.id;
  $('#devices').replaceChildren(...devices.map((device) => item(
    device.name + (device.id === self ? ' (this device)' : '') + (device.revoked ? ' - revoked' : device.approval.mode === 'pending' ? ' - waiting' : ''),
    device.revoked ? [] : [['Revoke', async () => { await deviceRequest('POST', paths.devices + '/' + encodeURIComponent(device.id) + '/revoke', {}); if (device.id === self) { await stored('credential', null); } }]],
  )));
  const { notices } = await deviceRequest('GET', paths.notices);
  $('#notices').replaceChildren(...notices.filter((n) => n.state === 'open').map((notice) => item(
    (notice.kind === 'enrollment-pending' ? 'Waiting: ' : 'Enrolled at home: ') + notice.deviceName,
    [['Undo', () => deviceRequest('POST', paths.notices + '/' + encodeURIComponent(notice.id) + '/undo', {})]]
      .concat(notice.kind === 'enrollment-pending' && notice.deviceId !== self ? [['Confirm', () => confirmNotice(notice)]] : []),
  )));
  const { passes, hosts } = await deviceRequest('GET', paths.appPasses);
  $('#apps-section').hidden = hosts.length === 0 && passes.length === 0;
  $('#app-host').replaceChildren(...hosts.map((host) => { const option = document.createElement('option'); option.value = host; option.textContent = host; return option; }));
  $('#app-passes').replaceChildren(...passes.map((pass) => item(
    pass.name + ' - ' + pass.host + (pass.revokedAt ? ' - revoked' : ' - until ' + pass.expiresAt.slice(0, 10)),
    pass.revokedAt ? [] : [['Revoke', () => deviceRequest('POST', paths.appPasses + '/' + encodeURIComponent(pass.id) + '/revoke', {})]],
  )));
  // Owner approvals answer only on the home network; away the list is empty.
  const { requests } = await deviceRequest('GET', paths.approvals).catch((error) => {
    if (error.reason === 'lan_only') return { requests: [] };
    throw error;
  });
  $('#approvals-section').hidden = requests.length === 0;
  $('#approvals').replaceChildren(...requests.map((request) => item(request.summary, [['Approve', () => approve(request)]])));
}

// The handlers are attached before anything is awaited, so an early tap is
// never a plain form submission; each waits for the configuration.
const ready = loadConfig();
$('#enroll').addEventListener('submit', (event) => { event.preventDefault(); ready.then(() => enroll($('#name').value)).catch(fail); });
$('#renew').addEventListener('click', () => ready.then(() => renew()).then(render).then(() => status('Renewed.')).catch(fail));
$('#push').addEventListener('click', () => ready.then(enablePush).catch(fail));
$('#refresh').addEventListener('click', () => ready.then(render).catch(fail));
$('#app-pass').addEventListener('submit', (event) => { event.preventDefault(); ready.then(() => createAppPass($('#app-host').value, $('#app-name').value)).then(render).catch(fail); });

async function start() {
  if ('serviceWorker' in navigator) navigator.serviceWorker.register('/device/sw.js', { scope: '/device/' }).catch(() => {});
  const ios = /iPhone|iPad|iPod/.test(navigator.userAgent);
  $('#install-hint').hidden = !(ios && !navigator.standalone);
  await ready;
  if (location.pathname === '/device/callback') await finishSignIn();
  if (location.pathname === '/device/launch') {
    const params = new URLSearchParams(location.search);
    return launchInto(params.get('target') || '', params.get('state') || '');
  }
  await render();
}
start().catch(fail);
