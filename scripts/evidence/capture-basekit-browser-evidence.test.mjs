import test from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import { mkdir, mkdtemp, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';

import {
  DEFAULT_PER_CHECK_TIMEOUT_MS,
  DEFAULT_TOTAL_TIMEOUT_MS,
  MAX_TIMEOUT_MS,
  MAX_TIMEOUT_SECONDS,
  REQUIRED_CHECKS,
  SETUP_ACTION_PER_SERVICE_TIMEOUT_MS,
  assertOwnerSetupActions,
  buildChecks,
  browserChannelLabel,
  browserRedirectBridgeHTML,
  browserRedirectLocationForRoute,
  browserScreenshotURL,
  checkTextMatches,
  clickThrough,
  collectSetupStateDiagnostics,
  cookieFromSetCookieHeader,
  defaultConfig,
  dismissTinyAuthInvalidDomainWarning,
  driveOwnerLoginFlow,
  loadPlaywright,
  mergeSetupActionDiagnostics,
  mapLocalPortURL,
  normalizeBrowserChannel,
  normalizeOwnerUsername,
  ownerDisplayNameFromUsername,
  ownerUsernameFromEmail,
  parseArgs,
  parseHTTPStatus,
  probeTinyAuthForwardAuthViaFreshVM,
  relativeEvidencePath,
  runOwnerActivatedSetupActions,
  unmapLocalPortURL,
  usage,
  verifyCloudreveDemoFile,
  verifyImmichDemoAssets,
  verifyOwnerPasskeyCredential,
  verifyTinyAuthOwnerSession,
  verifyVaultAuthBoundary,
  withLocalPortMappedBrowserOrigins,
} from './capture-basekit-browser-evidence.mjs';

test('default config targets the SK-S1 local BaseKit browser surface', () => {
  const cwd = path.resolve('tmp', 'repo');
  const config = defaultConfig({ cwd, env: { STACKKIT_OWNER_EMAIL: 'owner@example.com' } });

  assert.equal(config.browserUrl, 'http://base.home.localhost');
  assert.equal(config.ownerSetupUrl, 'http://id.home.localhost/setup');
  assert.equal(config.filesUrl, 'http://files.home.localhost/stackkit/files/session');
  assert.equal(config.ownerEmail, 'owner@example.com');
  assert.equal(config.ownerUsername, '');
  assert.deepEqual(config.setupServices, ['photos', 'files', 'vault']);
  assert.equal(config.perCheckTimeoutMs, DEFAULT_PER_CHECK_TIMEOUT_MS);
  assert.equal(config.totalTimeoutMs, DEFAULT_TOTAL_TIMEOUT_MS);
  assert.equal(SETUP_ACTION_PER_SERVICE_TIMEOUT_MS, 6 * 60 * 1000);
});

test('default config uses the canonical SK-S1 synthetic admin email when no owner is supplied', () => {
  const config = parseArgs([], { cwd: path.resolve('tmp', 'repo'), env: {} });

  assert.equal(config.ownerEmail, 'admin@example.com');
  assert.equal(config.ownerUsername, 'admin');
  assert.equal(config.ownerDisplayName, 'StackKit Owner');
});

test('parseArgs accepts explicit local runner inputs', () => {
  const config = parseArgs(
    [
      '--owner-email',
      'beta@example.com',
      '--owner-username',
      'beta-owner',
      '--owner-display-name',
      'Beta Owner',
      '--browser-url',
      'http://base.home.localhost',
      '--owner-setup-url',
      'http://id.home.localhost/setup',
      '--output',
      'artifacts/scenarios/SK-S1/browser-evidence.json',
      '--screenshot-dir',
      'artifacts/scenarios/SK-S1/screenshots',
      '--headed',
      '--per-check-timeout-ms',
      '60000',
      '--total-timeout-ms',
      '600000',
      '--setup-services',
      'photos,vault',
      '--browser-channel',
      'msedge',
      '--fresh-vm-container',
      'stackkits-e2e-test-ubuntu',
    ],
    { cwd: process.cwd(), env: {} },
  );

  assert.equal(config.ownerEmail, 'beta@example.com');
  assert.equal(config.ownerUsername, 'beta-owner');
  assert.equal(config.ownerDisplayName, 'Beta Owner');
  assert.equal(config.headless, false);
  assert.deepEqual(config.setupServices, ['photos', 'vault']);
  assert.equal(config.browserChannel, 'msedge');
  assert.equal(config.freshVMContainerName, 'stackkits-e2e-test-ubuntu');
  assert.equal(config.perCheckTimeoutMs, 60000);
  assert.equal(config.totalTimeoutMs, 600000);
});

test('parseArgs normalizes local mapped ports back to canonical browser origins', () => {
  const config = parseArgs(
    [
      '--browser-url',
      'http://base.home.localhost:57725/',
      '--owner-setup-url',
      'http://id.home.localhost:57725/setup',
      '--auth-url',
      'http://auth.home.localhost:57725/',
      '--photos-url',
      'http://photos.home.localhost:57725/photos',
      '--files-url',
      'http://files.home.localhost:57725/stackkit/files/session',
      '--vault-url',
      'http://vault.home.localhost:57725/',
    ],
    { cwd: process.cwd(), env: {} },
  );

  assert.equal(config.browserUrl, 'http://base.home.localhost/');
  assert.equal(config.ownerSetupUrl, 'http://id.home.localhost/setup');
  assert.equal(config.authUrl, 'http://auth.home.localhost/');
  assert.equal(config.photosUrl, 'http://photos.home.localhost/photos');
  assert.equal(config.filesUrl, 'http://files.home.localhost/stackkit/files/session');
  assert.equal(config.vaultUrl, 'http://vault.home.localhost/');
  assert.equal(config.localPortMappings['http://id.home.localhost'], 'http://127.0.0.1:57725');
  assert.equal(config.localPortMappings['http://auth.home.localhost'], 'http://127.0.0.1:57725');
  assert.equal(
    mapLocalPortURL(config, 'http://auth.home.localhost/api/auth/traefik'),
    'http://127.0.0.1:57725/api/auth/traefik',
  );
  assert.equal(
    unmapLocalPortURL(config, 'http://127.0.0.1:57725/oauth/callback', 'http://auth.home.localhost'),
    'http://auth.home.localhost/oauth/callback',
  );
  assert.equal(
    unmapLocalPortURL(config, 'http://id.home.localhost:57725/authorize?client_id=stackkit-tinyauth', 'http://auth.home.localhost'),
    'http://id.home.localhost/authorize?client_id=stackkit-tinyauth',
  );
});

test('local mapped redirects are bridged back to browser origins', () => {
  const config = parseArgs(
    [
      '--browser-url',
      'http://base.home.localhost:57725/',
      '--owner-setup-url',
      'http://id.home.localhost:57725/setup',
      '--auth-url',
      'http://auth.home.localhost:57725/',
      '--photos-url',
      'http://photos.home.localhost:57725/photos',
      '--files-url',
      'http://files.home.localhost:57725/stackkit/files/session',
      '--vault-url',
      'http://vault.home.localhost:57725/',
    ],
    { cwd: process.cwd(), env: {} },
  );

  assert.equal(
    browserRedirectLocationForRoute(
      config,
      'http://photos.home.localhost/photos',
      'http://127.0.0.1:57725/login',
      'http://photos.home.localhost',
    ),
    'http://photos.home.localhost/login',
  );
  assert.equal(
    browserRedirectLocationForRoute(
      config,
      'http://photos.home.localhost/photos',
      'http://auth.home.localhost/login?redirect_uri=http%3A%2F%2Fphotos.home.localhost%2Fphotos',
      'http://photos.home.localhost',
    ),
    'http://auth.home.localhost/login?redirect_uri=http%3A%2F%2Fphotos.home.localhost%2Fphotos',
  );

  const bridge = browserRedirectBridgeHTML('http://auth.home.localhost/login?x="y"&next=<done>');
  assert.match(bridge, /window\.location\.replace\("http:\/\/auth\.home\.localhost\/login\?x=\\\"y\\\"&next=<done>"\)/);
  assert.match(bridge, /url=http:\/\/auth\.home\.localhost\/login\?x=&quot;y&quot;&amp;next=&lt;done&gt;/);
});

test('browserScreenshotURL falls back when Playwright ends on a browser error page', () => {
  assert.equal(
    browserScreenshotURL('chrome-error://chromewebdata/', 'http://auth.home.localhost/'),
    'http://auth.home.localhost/',
  );
  assert.equal(
    browserScreenshotURL('http://auth.home.localhost/', 'http://base.home.localhost/'),
    'http://auth.home.localhost/',
  );
});

test('cookieFromSetCookieHeader maps local proxy cookies to the canonical browser origin', () => {
  const cookie = cookieFromSetCookieHeader(
    'tinyauth=session-value; Path=/; HttpOnly; SameSite=Lax',
    'http://auth.home.localhost',
  );

  assert.deepEqual(cookie, {
    name: 'tinyauth',
    value: 'session-value',
    httpOnly: true,
    sameSite: 'Lax',
    url: 'http://auth.home.localhost',
  });
});

test('cookieFromSetCookieHeader preserves explicit local parent domains', () => {
  const cookie = cookieFromSetCookieHeader(
    'tinyauth-oauth=state; Domain=home.localhost; Path=/; HttpOnly',
    'http://auth.home.localhost',
  );

  assert.deepEqual(cookie, {
    name: 'tinyauth-oauth',
    value: 'state',
    httpOnly: true,
    domain: '.home.localhost',
    path: '/',
  });
});

test('cookieFromSetCookieHeader preserves explicit non-local cookie domains', () => {
  const cookie = cookieFromSetCookieHeader(
    'session=abc=def; Domain=.auth.example.com; Path=/api; Secure; SameSite=None',
    'https://auth.example.com',
  );

  assert.equal(cookie.name, 'session');
  assert.equal(cookie.value, 'abc=def');
  assert.equal(cookie.domain, '.auth.example.com');
  assert.equal(cookie.path, '/api');
  assert.equal(cookie.secure, true);
  assert.equal(cookie.sameSite, 'None');
  assert.equal(cookie.url, undefined);
});

test('local mapped port normalization refuses public hostnames', () => {
  const config = withLocalPortMappedBrowserOrigins({
    browserUrl: 'https://base.example.com:8443/',
    ownerSetupUrl: 'https://id.example.com:8443/setup',
    authUrl: 'https://auth.example.com:8443/',
    photosUrl: 'https://photos.example.com:8443/photos',
    filesUrl: 'https://files.example.com:8443/stackkit/files/session',
    vaultUrl: 'https://vault.example.com:8443/',
  });

  assert.equal(config.authUrl, 'https://auth.example.com:8443/');
  assert.deepEqual(config.localPortMappings, {});
});

test('owner identity helpers keep email, username, and display name distinct', () => {
  assert.equal(ownerUsernameFromEmail('Admin.User@example.com'), 'admin.user');
  assert.equal(normalizeOwnerUsername('Beta Owner!'), 'beta-owner');
  assert.equal(ownerDisplayNameFromUsername('admin'), 'StackKit Owner');
  assert.equal(ownerDisplayNameFromUsername('beta-owner'), 'beta-owner');
  assert.equal(normalizeBrowserChannel('playwright-chromium'), '');
  assert.equal(normalizeBrowserChannel('MSedge'), 'msedge');
  assert.equal(browserChannelLabel(''), 'playwright-chromium');
  assert.equal(browserChannelLabel('chrome'), 'chrome');
});

test('parseArgs rejects waits beyond the global 15 minute budget', () => {
  assert.throws(
    () => parseArgs(['--total-timeout-ms', String(MAX_TIMEOUT_MS + 1)], { env: {}, cwd: process.cwd() }),
    /must be <= 900000ms/,
  );
  assert.throws(
    () => parseArgs(['--per-check-timeout-ms', String(MAX_TIMEOUT_MS + 1)], { env: {}, cwd: process.cwd() }),
    /must be <= 900000ms/,
  );
});

test('buildChecks emits the required v0.4 browser evidence checks', () => {
  const config = parseArgs([], { env: { STACKKIT_OWNER_EMAIL: 'owner@example.com' }, cwd: process.cwd() });
  const checks = buildChecks(config);
  const passkeyCheck = checks.find((check) => check.name === 'pocketid-owner-passkey');
  const tinyAuthCheck = checks.find((check) => check.name === 'tinyauth-owner-session');

  assert.deepEqual(
    checks.map((check) => check.name),
    REQUIRED_CHECKS.map((check) => check.name),
  );
  assert.ok(checks.every((check) => check.screenshotPath.startsWith('artifacts/scenarios/SK-S1/screenshots/')));
  assert.ok(checks.every((check) => check.screenshotPath.endsWith('.png')));
  assert.equal(passkeyCheck.evidencePolicy, 'pocketid-passkey-credential');
  assert.equal(tinyAuthCheck.evidencePolicy, 'tinyauth-owner-session');
});

test('content checks require seeded Files and Photos evidence, not generic app pages', () => {
  const filesCheck = REQUIRED_CHECKS.find((check) => check.name === 'files-demo-content');
  const photosCheck = REQUIRED_CHECKS.find((check) => check.name === 'photos-demo-content');

  assert.ok(filesCheck);
  assert.ok(photosCheck);
  assert.equal(filesCheck.evidencePolicy, 'cloudreve-demo-file');
  assert.equal(photosCheck.evidencePolicy, 'immich-demo-assets');
  assert.equal(checkTextMatches(filesCheck, 'Cloudreve Files'), false);
  assert.equal(checkTextMatches(filesCheck, 'StackKit Demo\nREADME.txt'), true);
  assert.equal(checkTextMatches(photosCheck, 'Immich Photos'), true);
});

test('verifyOwnerPasskeyCredential proves WebAuthn credential creation', async () => {
  const evidence = await verifyOwnerPasskeyCredential({
    enabled: true,
    authenticatorId: 'authenticator-1',
    protocol: 'ctap2',
    transport: 'usb',
    session: {
      send: async (method, payload) => {
        assert.equal(method, 'WebAuthn.getCredentials');
        assert.equal(payload.authenticatorId, 'authenticator-1');
        return {
          credentials: [{
            rpId: 'id.home.localhost',
            isResidentCredential: true,
          }],
        };
      },
    },
  });

  assert.equal(evidence.verification, 'webauthn-virtual-authenticator');
  assert.equal(evidence.passkeyCredentials, '1');
  assert.equal(evidence.residentCredentials, '1');
  assert.equal(evidence.relyingParties, 'id.home.localhost');
});

test('verifyOwnerPasskeyCredential rejects missing WebAuthn credentials', async () => {
  await assert.rejects(
    () => verifyOwnerPasskeyCredential({
      enabled: true,
      authenticatorId: 'authenticator-1',
      protocol: 'ctap2',
      transport: 'usb',
      session: {
        send: async () => ({ credentials: [] }),
      },
    }),
    /did not create a WebAuthn credential/,
  );
});

test('verifyTinyAuthOwnerSession proves the browser session through ForwardAuth and a TinyAuth cookie', async () => {
  const requests = [];
  const page = {
    url: () => 'http://auth.home.localhost',
    waitForTimeout: async () => {},
    context: () => ({
      cookies: async (urls) => {
        assert.ok(urls.includes('http://auth.home.localhost/'));
        return [{
          name: 'tinyauth',
          value: 'session-value',
          domain: '.home.localhost',
        }];
      },
      request: {
        get: async (url, options = {}) => {
          requests.push({ url, options });
          assert.equal(options.maxRedirects, 0);
          assert.equal(options.headers['X-Forwarded-Host'], 'base.home.localhost');
          assert.equal(options.headers.Cookie, 'tinyauth=session-value');
          return {
            ok: () => true,
            status: () => 200,
          };
        },
      },
    }),
  };

  const evidence = await verifyTinyAuthOwnerSession(
    page,
    { authUrl: 'http://auth.home.localhost', browserUrl: 'http://base.home.localhost' },
    '',
    Date.now() + 1000,
  );

  assert.equal(evidence.verification, 'tinyauth-forwardauth-session');
  assert.equal(evidence.authBoundary, 'tinyauth-pocketid');
  assert.equal(evidence.ownerSessionSignal, 'forwardauth-2xx');
  assert.equal(evidence.forwardAuthStatus, '200');
  assert.equal(evidence.sessionCookieCount, '1');
  assert.equal(evidence.sessionCookieNames, 'tinyauth');
  assert.equal(evidence.sessionCookieDomains, '.home.localhost');
  assert.equal(requests[0].url, 'http://auth.home.localhost/api/auth/traefik');
});

test('verifyTinyAuthOwnerSession rejects page text without a TinyAuth session cookie', async () => {
  const page = {
    url: () => 'http://auth.home.localhost',
    waitForTimeout: async () => {},
    context: () => ({
      cookies: async () => [],
      request: {
        get: async () => ({
          ok: () => true,
          status: () => 200,
        }),
      },
    }),
  };

  await assert.rejects(
    () => verifyTinyAuthOwnerSession(
      page,
      { authUrl: 'http://auth.home.localhost', browserUrl: 'http://base.home.localhost' },
      'TinyAuth Owner signed in',
      Date.now() + 1,
    ),
    /no TinyAuth-like session cookie/,
  );
});

test('probeTinyAuthForwardAuthViaFreshVM executes TinyAuth ForwardAuth inside the retained VM with browser cookies', async () => {
  const calls = [];
  const result = await probeTinyAuthForwardAuthViaFreshVM(
    {
      authUrl: 'http://auth.home.localhost',
      browserUrl: 'http://base.home.localhost',
      freshVMContainerName: 'stackkits-e2e-test-ubuntu',
    },
    '/api/auth/traefik',
    'tinyauth=session-value',
    Date.now() + 1000,
    async (file, args, options) => {
      calls.push({ file, args, options });
      return { stdout: 'OK', stderr: '  HTTP/1.1 200 OK\n' };
    },
  );

  assert.equal(result.ok, true);
  assert.equal(result.status, 200);
  assert.equal(result.url, 'http://auth.home.localhost/api/auth/traefik');
  assert.equal(result.probe, 'fresh-vm-container');
  assert.equal(calls[0].file, 'docker');
  assert.deepEqual(calls[0].args.slice(0, 5), ['exec', 'stackkits-e2e-test-ubuntu', 'docker', 'exec', 'tinyauth']);
  assert.ok(calls[0].args.includes('--header=Host: auth.home.localhost'));
  assert.ok(calls[0].args.includes('--header=X-Forwarded-Host: base.home.localhost'));
  assert.ok(calls[0].args.includes('--header=X-Forwarded-Uri: /'));
  assert.ok(calls[0].args.includes('--header=Cookie: tinyauth=session-value'));
  assert.equal(calls[0].options.windowsHide, true);
});

test('parseHTTPStatus returns the final HTTP status from wget output', () => {
  assert.equal(parseHTTPStatus('HTTP/1.1 302 Found\nHTTP/1.1 200 OK'), 200);
  assert.equal(parseHTTPStatus('no status'), 0);
});

test('dismissTinyAuthInvalidDomainWarning only accepts local TinyAuth port mapping', async () => {
  let clicked = false;
  const localPage = invalidDomainPage({
    url: 'http://auth.home.localhost:60752/',
    text: [
      'Invalid Domain',
      'Expected: http://auth.home.localhost',
      'Current: http://auth.home.localhost:60752',
      'Ignore',
    ].join('\n'),
    onClick: () => {
      clicked = true;
    },
  });

  const dismissed = await dismissTinyAuthInvalidDomainWarning(
    localPage,
    { authUrl: 'http://auth.home.localhost' },
    Date.now() + 1000,
  );

  assert.equal(dismissed, true);
  assert.equal(clicked, true);

  let publicClicked = false;
  const publicPage = invalidDomainPage({
    url: 'https://auth.example.com:444/',
    text: [
      'Invalid Domain',
      'Expected: https://auth.example.com',
      'Current: https://auth.example.com:444',
      'Ignore',
    ].join('\n'),
    onClick: () => {
      publicClicked = true;
    },
  });

  const publicDismissed = await dismissTinyAuthInvalidDomainWarning(
    publicPage,
    { authUrl: 'https://auth.example.com' },
    Date.now() + 1000,
  );

  assert.equal(publicDismissed, false);
  assert.equal(publicClicked, false);
});

test('clickThrough waits through transient OAuth transition states', async () => {
  let visible = true;
  let clicks = 0;
  let idleWaits = 0;
  const locator = {
    first: () => locator,
    filter: () => locator,
    count: async () => (visible ? 1 : 0),
    isVisible: async () => visible,
    click: async () => {
      clicks += 1;
      visible = false;
    },
  };
  const page = {
    getByRole: () => locator,
    locator: () => locator,
    waitForLoadState: async () => {},
    waitForTimeout: async () => {
      if (clicks === 1 && !visible) {
        idleWaits += 1;
        if (idleWaits >= 2) visible = true;
      }
    },
  };

  await clickThrough(page, [/sign in/i], Date.now() + 1000);

  assert.equal(clicks, 2);
});

test('driveOwnerLoginFlow does not restart TinyAuth OAuth while the provider redirect is pending', async () => {
  let providerClicks = 0;
  let currentURL = 'http://auth.home.localhost/';
  let waits = 0;
  const providerButton = {
    first: () => providerButton,
    filter: () => providerButton,
    count: async () => 1,
    isVisible: async () => true,
    click: async () => {
      providerClicks += 1;
    },
  };
  const empty = {
    first: () => empty,
    filter: () => empty,
    count: async () => 0,
    isVisible: async () => false,
    click: async () => {},
  };
  const page = {
    url: () => currentURL,
    getByRole: (_role, options = {}) => ({
      first: () => options.name?.test?.('PocketID') ? providerButton : empty,
    }),
    locator: () => ({
      filter: () => ({
        first: () => empty,
      }),
    }),
    waitForLoadState: async () => {},
    waitForTimeout: async () => {
      waits += 1;
      if (waits >= 3) currentURL = 'http://id.home.localhost/authorize?client_id=stackkit-tinyauth';
    },
  };

  await driveOwnerLoginFlow(
    page,
    { authUrl: 'http://auth.home.localhost', browserUrl: 'http://base.home.localhost' },
    Date.now() + 1000,
  );

  assert.equal(providerClicks, 1);
});

test('verifyImmichDemoAssets proves the StackKit demo asset through the Owner browser session', async () => {
  const previousWindow = globalThis.window;
  const previousFetch = globalThis.fetch;
  const requests = [];
  const token = 'eyJhbGci.eyJzdWIi.signature';
  globalThis.window = {
    localStorage: browserStorage({ immich_session: token }),
    sessionStorage: browserStorage({}),
  };
  globalThis.fetch = async (url, options = {}) => {
    requests.push({ url: String(url), options });
    assert.equal(options.headers.authorization, `Bearer ${token}`);
    if (String(url) === '/api/users/me') {
      return {
        ok: true,
        json: async () => ({ id: 'immich-owner-id', email: 'owner@example.com' }),
      };
    }
    if (String(url) === '/api/search/metadata') {
      assert.deepEqual(JSON.parse(options.body), {
        deviceId: 'stackkit-demo',
        deviceAssetId: 'stackkit-demo-photo-1',
        originalFileName: 'stackkit-demo-photo.png',
      });
      return {
        ok: true,
        json: async () => ({
          assets: {
            items: [{
              deviceId: 'stackkit-demo',
              deviceAssetId: 'stackkit-demo-photo-1',
              originalFileName: 'stackkit-demo-photo.png',
            }],
          },
        }),
      };
    }
    return { ok: false, status: 404, json: async () => ({}) };
  };
  try {
    const evidence = await verifyImmichDemoAssets(
      {
        evaluate: async (callback, args) => callback(args),
        waitForTimeout: async () => {},
      },
      Date.now() + 1000,
      'owner@example.com',
    );

    assert.equal(evidence.demoContent, 'immich-demo-assets');
    assert.equal(evidence.verification, 'immich-search-metadata');
    assert.equal(evidence.ownerVerification, 'immich-users-me');
    assert.equal(evidence.immichOwnerEmail, 'owner@example.com');
    assert.equal(evidence.immichOwnerId, 'immich-owner-id');
    assert.equal(evidence.demoAssetDeviceId, 'stackkit-demo');
    assert.equal(evidence.demoAssetDeviceAssetId, 'stackkit-demo-photo-1');
    assert.equal(evidence.demoAssetFile, 'stackkit-demo-photo.png');
    assert.ok(requests.some((request) => request.url === '/api/search/metadata'));
  } finally {
    if (previousWindow === undefined) delete globalThis.window;
    else globalThis.window = previousWindow;
    if (previousFetch === undefined) delete globalThis.fetch;
    else globalThis.fetch = previousFetch;
  }
});

test('verifyImmichDemoAssets proves the cookie-session Owner without seeded assets when demo data is disabled', async () => {
  const previousWindow = globalThis.window;
  const previousFetch = globalThis.fetch;
  const requests = [];
  globalThis.window = {
    localStorage: browserStorage({}),
    sessionStorage: browserStorage({}),
  };
  globalThis.fetch = async (url, options = {}) => {
    requests.push(String(url));
    assert.equal(options.headers?.authorization, undefined);
    if (String(url) === '/api/users/me') {
      return {
        ok: true,
        json: async () => ({ id: 'immich-owner-id', email: 'owner@example.com' }),
      };
    }
    throw new Error('Immich demo metadata search must not run when demo data is disabled');
  };
  try {
    const evidence = await verifyImmichDemoAssets(
      {
        evaluate: async (callback, args) => callback(args),
        waitForTimeout: async () => {},
      },
      Date.now() + 1000,
      'owner@example.com',
      false,
    );

    assert.equal(evidence.demoData, 'disabled');
    assert.equal(evidence.demoContent, 'immich-owner-session');
    assert.equal(evidence.verification, 'immich-users-me');
    assert.equal(evidence.ownerVerification, 'immich-users-me');
    assert.equal(evidence.immichSessionAuth, 'cookie-session');
    assert.equal(evidence.immichOwnerEmail, 'owner@example.com');
    assert.equal(evidence.immichOwnerId, 'immich-owner-id');
    assert.equal(evidence.immichDemoAssets, undefined);
    assert.equal(evidence.demoAssetFile, undefined);
    assert.deepEqual(requests, ['/api/users/me']);
  } finally {
    if (previousWindow === undefined) delete globalThis.window;
    else globalThis.window = previousWindow;
    if (previousFetch === undefined) delete globalThis.fetch;
    else globalThis.fetch = previousFetch;
  }
});

test('verifyImmichDemoAssets rejects browser sessions for a different Owner email', async () => {
  const previousWindow = globalThis.window;
  const previousFetch = globalThis.fetch;
  const token = 'eyJhbGci.eyJzdWIi.signature';
  globalThis.window = {
    localStorage: browserStorage({ immich_session: token }),
    sessionStorage: browserStorage({}),
  };
  globalThis.fetch = async (url, options = {}) => {
    assert.equal(options.headers.authorization, `Bearer ${token}`);
    if (String(url) === '/api/users/me') {
      return {
        ok: true,
        json: async () => ({ id: 'wrong-owner-id', email: 'someone@example.com' }),
      };
    }
    throw new Error('Immich demo metadata search should not run for a mismatched Owner session');
  };
  try {
    await assert.rejects(
      () => verifyImmichDemoAssets(
        {
          evaluate: async (callback, args) => callback(args),
          waitForTimeout: async () => {},
        },
        Date.now() + 10,
        'owner@example.com',
      ),
      /did not match Owner/,
    );
  } finally {
    if (previousWindow === undefined) delete globalThis.window;
    else globalThis.window = previousWindow;
    if (previousFetch === undefined) delete globalThis.fetch;
    else globalThis.fetch = previousFetch;
  }
});

test('verifyVaultAuthBoundary proves anonymous Vault access is stopped by TinyAuth/PocketID', async () => {
  const context = {
    closed: false,
    newPage: async () => ({
      goto: async () => ({ status: () => 200 }),
      waitForLoadState: async () => {},
      url: () => 'http://auth.home.localhost/login',
      evaluate: async () => 'TinyAuth\nSign in with PocketID',
    }),
    close: async () => {
      context.closed = true;
    },
  };
  const evidence = await verifyVaultAuthBoundary(
    browserBackedPage(context),
    { localPortMappings: {} },
    { url: 'http://vault.home.localhost' },
    Date.now() + 1000,
  );

  assert.equal(evidence.verification, 'anonymous-vault-route-check');
  assert.equal(evidence.authBoundary, 'tinyauth-pocketid');
  assert.equal(evidence.anonymousAccess, 'rejected');
  assert.equal(evidence.anonymousBoundarySignal, 'tinyauth');
  assert.equal(context.closed, true);
});

test('verifyVaultAuthBoundary installs the local-port bridge in the anonymous context', async () => {
  const routedOrigins = [];
  const context = {
    route: async (pattern) => {
      routedOrigins.push(String(pattern));
    },
    newPage: async () => ({
      goto: async () => ({ status: () => 401 }),
      waitForLoadState: async () => {},
      url: () => 'http://vault.home.localhost',
      evaluate: async () => 'Unauthorized',
    }),
    close: async () => {},
  };
  const evidence = await verifyVaultAuthBoundary(
    browserBackedPage(context),
    {
      localPortMappings: {
        'http://vault.home.localhost': 'http://127.0.0.1:58012',
      },
    },
    { url: 'http://vault.home.localhost' },
    Date.now() + 1000,
  );

  assert.deepEqual(routedOrigins, ['http://vault.home.localhost/**']);
  assert.equal(evidence.anonymousBoundarySignal, 'http-401');
});

test('verifyVaultAuthBoundary rejects a direct anonymous Vaultwarden login page', async () => {
  const context = {
    newPage: async () => ({
      goto: async () => ({ status: () => 200 }),
      waitForLoadState: async () => {},
      url: () => 'http://vault.home.localhost',
      evaluate: async () => 'Vaultwarden\nLog in',
    }),
    close: async () => {},
  };

  await assert.rejects(
    () => verifyVaultAuthBoundary(
      browserBackedPage(context),
      { localPortMappings: {} },
      { url: 'http://vault.home.localhost' },
      Date.now() + 1000,
    ),
    /TinyAuth\/PocketID boundary/,
  );
});

function browserBackedPage(anonymousContext) {
  return {
    context: () => ({
      browser: () => ({
        newContext: async () => anonymousContext,
      }),
    }),
  };
}

function invalidDomainPage({ url, text, onClick }) {
  const button = {
    count: async () => 1,
    isVisible: async () => true,
    click: async () => onClick(),
  };
  const empty = {
    count: async () => 0,
    isVisible: async () => false,
    click: async () => {},
  };
  return {
    url: () => url,
    evaluate: async () => text,
    getByRole: (_role, options = {}) => ({
      first: () => options.name?.test?.('Ignore') ? button : empty,
    }),
    locator: () => ({
      filter: () => ({
        first: () => empty,
      }),
    }),
    waitForLoadState: async () => {},
    waitForTimeout: async () => {},
  };
}

test('verifyCloudreveDemoFile proves seeded files through the browser session API', async () => {
  const previousWindow = globalThis.window;
  const previousFetch = globalThis.fetch;
  const requests = [];
  globalThis.window = {
    localStorage: {
      getItem: (key) => {
        if (key === 'stackkit_files_session_bridge') {
          return JSON.stringify({
            verification: 'stackkit-cloudreve-session-bridge',
            current: 'owner-user',
          });
        }
        if (key !== 'cloudreve_session') return null;
        return JSON.stringify({
          current: 'owner-user',
          sessions: {
            'owner-user': {
              token: { access_token: 'cloudreve-owner-token' },
            },
          },
        });
      },
    },
  };
  globalThis.fetch = async (url, options) => {
    requests.push(String(url));
    assert.equal(options.headers.authorization, 'Bearer cloudreve-owner-token');
    if (String(url).includes('uri=cloudreve%3A%2F%2Fmy&')) {
      return { ok: true, json: async () => ({ code: 0, data: { files: [{ type: 1, name: 'StackKit Demo' }] } }) };
    }
    if (String(url).includes('StackKit%2520Demo') || String(url).includes('StackKit+Demo')) {
      return { ok: true, json: async () => ({ code: 0, data: { files: [{ type: 0, name: 'README.txt' }] } }) };
    }
    return { ok: false, status: 404, json: async () => ({}) };
  };
  try {
    const evidence = await verifyCloudreveDemoFile(
      {
        evaluate: async (callback, args) => callback(args),
        waitForTimeout: async () => {},
      },
      Date.now() + 1000,
    );

    assert.equal(evidence.demoContent, 'cloudreve-demo-file');
    assert.equal(evidence.verification, 'cloudreve-browser-session-api');
    assert.equal(evidence.identityBridge, 'stackkit-cloudreve-local-session');
    assert.equal(evidence.bridgeVerification, 'stackkit-cloudreve-session-bridge');
    assert.equal(evidence.bridgeCurrentUser, 'owner-user');
    assert.equal(evidence.cloudreveSessionUser, 'owner-user');
    assert.ok(requests.length >= 2);
  } finally {
    if (previousWindow === undefined) delete globalThis.window;
    else globalThis.window = previousWindow;
    if (previousFetch === undefined) delete globalThis.fetch;
    else globalThis.fetch = previousFetch;
  }
});

test('verifyCloudreveDemoFile proves the bridge session without seeded files when demo data is disabled', async () => {
  const previousWindow = globalThis.window;
  const previousFetch = globalThis.fetch;
  const requests = [];
  globalThis.window = {
    localStorage: {
      getItem: (key) => {
        if (key === 'stackkit_files_session_bridge') {
          return JSON.stringify({
            verification: 'stackkit-cloudreve-session-bridge',
            current: 'owner-user',
          });
        }
        if (key !== 'cloudreve_session') return null;
        return JSON.stringify({
          current: 'owner-user',
          sessions: {
            'owner-user': {
              token: { access_token: 'cloudreve-owner-token' },
            },
          },
        });
      },
    },
  };
  globalThis.fetch = async (url, options) => {
    requests.push(String(url));
    assert.equal(options.headers.authorization, 'Bearer cloudreve-owner-token');
    if (String(url).includes('uri=cloudreve%3A%2F%2Fmy&')) {
      return { ok: true, json: async () => ({ code: 0, data: { files: [] } }) };
    }
    throw new Error('Cloudreve folder listing must not require seeded demo content when demo data is disabled');
  };
  try {
    const evidence = await verifyCloudreveDemoFile(
      {
        evaluate: async (callback, args) => callback(args),
        waitForTimeout: async () => {},
      },
      Date.now() + 1000,
      false,
    );

    assert.equal(evidence.demoData, 'disabled');
    assert.equal(evidence.demoContent, 'cloudreve-owner-session');
    assert.equal(evidence.verification, 'cloudreve-browser-session-api');
    assert.equal(evidence.identityBridge, 'stackkit-cloudreve-local-session');
    assert.equal(evidence.bridgeVerification, 'stackkit-cloudreve-session-bridge');
    assert.equal(evidence.bridgeCurrentUser, 'owner-user');
    assert.equal(evidence.cloudreveSessionUser, 'owner-user');
    assert.equal(evidence.seededFolder, undefined);
    assert.equal(evidence.seededFile, undefined);
    assert.equal(requests.length, 1);
  } finally {
    if (previousWindow === undefined) delete globalThis.window;
    else globalThis.window = previousWindow;
    if (previousFetch === undefined) delete globalThis.fetch;
    else globalThis.fetch = previousFetch;
  }
});

test('verifyCloudreveDemoFile rejects app-local sessions without StackKit bridge marker', async () => {
  const previousWindow = globalThis.window;
  const previousFetch = globalThis.fetch;
  globalThis.window = {
    localStorage: {
      getItem: (key) => {
        if (key !== 'cloudreve_session') return null;
        return JSON.stringify({
          current: 'owner-user',
          sessions: {
            'owner-user': {
              token: { access_token: 'cloudreve-owner-token' },
            },
          },
        });
      },
    },
  };
  globalThis.fetch = async () => {
    throw new Error('Cloudreve API should not be called without the StackKit bridge marker');
  };
  try {
    await assert.rejects(
      () => verifyCloudreveDemoFile(
        {
          evaluate: async (callback, args) => callback(args),
          waitForTimeout: async () => {},
        },
        Date.now() + 1,
      ),
      /StackKit Files session bridge/,
    );
  } finally {
    if (previousWindow === undefined) delete globalThis.window;
    else globalThis.window = previousWindow;
    if (previousFetch === undefined) delete globalThis.fetch;
    else globalThis.fetch = previousFetch;
  }
});

test('verifyCloudreveDemoFile rejects stale StackKit bridge markers for another Cloudreve user', async () => {
  const previousWindow = globalThis.window;
  const previousFetch = globalThis.fetch;
  globalThis.window = {
    localStorage: {
      getItem: (key) => {
        if (key === 'stackkit_files_session_bridge') {
          return JSON.stringify({
            verification: 'stackkit-cloudreve-session-bridge',
            current: 'stale-user',
          });
        }
        if (key !== 'cloudreve_session') return null;
        return JSON.stringify({
          current: 'owner-user',
          sessions: {
            'owner-user': {
              token: { access_token: 'cloudreve-owner-token' },
            },
          },
        });
      },
    },
  };
  globalThis.fetch = async () => {
    throw new Error('Cloudreve API should not be called with a stale StackKit bridge marker');
  };
  try {
    await assert.rejects(
      () => verifyCloudreveDemoFile(
        {
          evaluate: async (callback, args) => callback(args),
          waitForTimeout: async () => {},
        },
        Date.now() + 1,
      ),
      /does not match StackKit bridge user/,
    );
  } finally {
    if (previousWindow === undefined) delete globalThis.window;
    else globalThis.window = previousWindow;
    if (previousFetch === undefined) delete globalThis.fetch;
    else globalThis.fetch = previousFetch;
  }
});

function browserStorage(values) {
  const entries = Object.entries(values);
  return {
    length: entries.length,
    key: (index) => entries[index]?.[0] || null,
    getItem: (key) => Object.hasOwn(values, key) ? values[key] : null,
  };
}

test('collectSetupStateDiagnostics summarizes exported SetupRun state without raw state text', async () => {
  const root = await mkdtemp(path.join(tmpdir(), 'stackkit-browser-setup-state-'));
  const statePath = path.join(root, 'artifacts', 'scenarios', 'SK-S1', 'setup-state.yaml');
  await mkdir(path.dirname(statePath), { recursive: true });
  await writeFile(
    statePath,
    [
      'setupRuns:',
      '- runId: setup-files-cloudreve-owner-bootstrap',
      '  serviceKey: files',
      '  appName: cloudreve',
      '  dropName: cloudreve-owner-bootstrap',
      '  status: completed',
      '  phase: verified',
      '  attempts: 1',
      '  lastRequested: 2026-05-28T12:00:00Z',
      '  lastStarted: 2026-05-28T12:00:01Z',
      '  lastFinished: 2026-05-28T12:00:02Z',
      '  evidence:',
      '    status: nested-value-that-must-not-be-read',
      '- runId: setup-photos-immich-owner-bootstrap',
      '  serviceKey: photos',
      '  appName: immich',
      '  dropName: immich-owner-bootstrap',
      '  status: waiting',
      '  phase: owner_activated',
      '  failureClass: owner_missing',
      '  attempts: 2',
      '  lastRequested: 2026-05-28T12:01:00Z',
      '  lastStarted: 2026-05-28T12:01:01Z',
      '  lastFinished: 2026-05-28T12:01:02Z',
    ].join('\n'),
    'utf8',
  );

  const diagnostics = await collectSetupStateDiagnostics({
    evidenceRoot: root,
    setupStatePath: statePath,
  });

  assert.equal(diagnostics.status, 'present');
  assert.equal(diagnostics.sourcePath, 'artifacts/scenarios/SK-S1/setup-state.yaml');
  assert.equal(diagnostics.setupRunCount, '2');
  assert.equal(diagnostics.drops['cloudreve-owner-bootstrap'].status, 'completed');
  assert.equal(diagnostics.drops['cloudreve-owner-bootstrap'].runId, 'setup-files-cloudreve-owner-bootstrap');
  assert.equal(diagnostics.drops['cloudreve-owner-bootstrap'].attempts, '1');
  assert.equal(diagnostics.drops['immich-owner-bootstrap'].phase, 'owner_activated');
  assert.equal(diagnostics.drops['immich-owner-bootstrap'].lastFinished, '2026-05-28T12:01:02Z');
  assert.equal(diagnostics.drops['kuma-platform-bootstrap'].status, 'missing');
  assert.equal(Object.hasOwn(diagnostics, 'raw'), false);
});

test('runOwnerActivatedSetupActions posts owner-dependent setup services through the browser context', async () => {
  const posted = [];
  const actions = await runOwnerActivatedSetupActions(
    {
      context: () => ({
        request: {
          post: async (url) => {
            posted.push(url);
            return {
              ok: () => true,
              status: () => 200,
              text: async () => JSON.stringify({
                data: {
                  serviceKey: 'photos',
                  status: 'completed',
                  message: 'Setup completed.',
                  drops: [{
                    name: 'immich-owner-bootstrap',
                    runId: 'setup-photos-immich-owner-bootstrap',
                    status: 'completed',
                    phase: 'verified',
                    attempts: 2,
                    lastRequested: '2026-05-28T12:02:00Z',
                    lastStarted: '2026-05-28T12:02:01Z',
                    lastFinished: '2026-05-28T12:02:02Z',
                    logs: [{ message: 'requested' }, { message: 'verified' }],
                    rollbackNotes: ['Remove beta demo assets before retrying.'],
                  }],
                },
              }),
            };
          },
        },
      }),
    },
    { browserUrl: 'http://base.home.localhost', setupServices: ['photos'] },
    Date.now() + 1000,
  );

  assert.equal(posted[0], 'http://base.home.localhost/api/v1/setup/services/photos/run');
  assert.equal(actions[0].service, 'photos');
  assert.equal(actions[0].data.drops[0].name, 'immich-owner-bootstrap');
});

test('runOwnerActivatedSetupActions retries transient service warmup failures', async () => {
  const posted = [];
  let waits = 0;
  const actions = await runOwnerActivatedSetupActions(
    {
      waitForTimeout: async () => {
        waits += 1;
      },
      context: () => ({
        request: {
          post: async (url) => {
            posted.push(url);
            if (posted.length === 1) {
              return {
                ok: () => false,
                status: () => 502,
                text: async () => 'Bad Gateway',
              };
            }
            return {
              ok: () => true,
              status: () => 200,
              text: async () => JSON.stringify({
                data: {
                  serviceKey: 'photos',
                  status: 'completed',
                  message: 'Setup completed.',
                  drops: [{
                    name: 'immich-owner-bootstrap',
                    runId: 'setup-photos-immich-owner-bootstrap',
                    status: 'completed',
                    phase: 'verified',
                    attempts: 1,
                    lastRequested: '2026-05-28T12:02:00Z',
                    lastStarted: '2026-05-28T12:02:01Z',
                    lastFinished: '2026-05-28T12:02:02Z',
                    logs: [{ message: 'requested' }],
                    rollbackNotes: ['Remove beta demo assets before retrying.'],
                  }],
                },
              }),
            };
          },
        },
      }),
    },
    {
      browserUrl: 'http://base.home.localhost',
      setupServices: ['photos'],
      setupActionRetryDelayMs: 0,
    },
    Date.now() + 1000,
  );

  assert.equal(posted.length, 2);
  assert.equal(waits, 1);
  assert.equal(actions[0].httpStatus, '200');
  assert.equal(actions[0].requestAttempts, '2');
  assert.equal(actions[0].data.status, 'completed');
});

test('mergeSetupActionDiagnostics upgrades waiting setup diagnostics from live setup responses', () => {
  const diagnostics = mergeSetupActionDiagnostics(
    {
      setupState: {
        status: 'present',
        drops: {
          'immich-owner-bootstrap': {
            status: 'waiting',
            phase: 'owner_activated',
            serviceKey: 'photos',
          },
        },
      },
    },
    [{
      service: 'photos',
      httpStatus: '200',
      ok: true,
      durationSeconds: '2',
      data: {
        serviceKey: 'photos',
        status: 'completed',
        message: 'Setup completed.',
        drops: [{
          name: 'immich-owner-bootstrap',
          runId: 'setup-photos-immich-owner-bootstrap',
          attempts: 2,
          status: 'completed',
          phase: 'verified',
          lastRequested: '2026-05-28T12:02:00Z',
          lastStarted: '2026-05-28T12:02:01Z',
          lastFinished: '2026-05-28T12:02:02Z',
          logs: [{ message: 'requested' }, { message: 'verified' }],
          rollbackNotes: ['Remove beta demo assets before retrying.'],
          evidence: {
            ownerProvisioning: 'pocketid-oidc-owner-admin',
            appLocalSessionHandoff: 'pocketid-oidc-auto-launch',
          },
        }],
      },
    }],
  );

  assert.equal(diagnostics.setupActions[0].service, 'photos');
  assert.equal(diagnostics.setupActions[0].status, 'completed');
  assert.equal(diagnostics.setupActions[0].dropName, 'immich-owner-bootstrap');
  assert.equal(diagnostics.setupActions[0].dropStatus, 'completed');
  assert.equal(diagnostics.setupActions[0].dropPhase, 'verified');
  assert.equal(diagnostics.setupActions[0].runId, 'setup-photos-immich-owner-bootstrap');
  assert.equal(diagnostics.setupActions[0].attempts, '2');
  assert.equal(diagnostics.setupActions[0].lastFinished, '2026-05-28T12:02:02Z');
  assert.equal(diagnostics.setupActions[0].logCount, '2');
  assert.equal(diagnostics.setupActions[0].rollbackNoteCount, '1');
  assert.equal(diagnostics.setupState.drops['immich-owner-bootstrap'].status, 'completed');
  assert.equal(diagnostics.setupState.drops['immich-owner-bootstrap'].phase, 'verified');
  assert.equal(diagnostics.setupState.drops['immich-owner-bootstrap'].runId, 'setup-photos-immich-owner-bootstrap');
  assert.equal(diagnostics.setupState.drops['immich-owner-bootstrap'].logCount, '2');
  assert.equal(diagnostics.setupState.drops['immich-owner-bootstrap'].rollbackNoteCount, '1');
  assert.deepEqual(diagnostics.setupState.drops['immich-owner-bootstrap'].evidence, {
    ownerProvisioning: 'pocketid-oidc-owner-admin',
    appLocalSessionHandoff: 'pocketid-oidc-auto-launch',
  });
});

test('assertOwnerSetupActions rejects setup actions outside the gate budget', () => {
  assert.throws(
    () => assertOwnerSetupActions([
      ownerSetupAction('photos'),
      ownerSetupAction('files'),
      ownerSetupAction('vault', { durationSeconds: String(MAX_TIMEOUT_SECONDS + 1) }),
    ]),
    /owner setup action vault durationSeconds 901 exceeds 15 minute budget/,
  );
});

test('relativeEvidencePath fails closed when screenshots escape the evidence root', () => {
  const root = path.resolve('artifacts');
  assert.throws(() => relativeEvidencePath(root, path.resolve('other', 'screenshot.png')), /escapes evidence root/);
});

function ownerSetupAction(service, overrides = {}) {
  const dropNames = {
    photos: 'immich-owner-bootstrap',
    files: 'cloudreve-owner-bootstrap',
    vault: 'vaultwarden-admin-handoff',
  };
  return {
    service,
    httpStatus: '200',
    ok: 'true',
    durationSeconds: '2',
    runId: `setup-${service}`,
    attempts: '1',
    status: 'completed',
    dropName: dropNames[service],
    dropStatus: 'completed',
    dropPhase: 'verified',
    lastRequested: '2026-05-28T12:00:00Z',
    lastStarted: '2026-05-28T12:00:01Z',
    lastFinished: '2026-05-28T12:00:02Z',
    logCount: '3',
    rollbackNoteCount: '1',
    ...overrides,
  };
}

test('usage documents the Playwright runner inputs', () => {
  const text = usage();
  assert.match(text, /--owner-setup-url/);
  assert.match(text, /--owner-username/);
  assert.match(text, /--setup-state-path/);
  assert.match(text, /--setup-services/);
  assert.match(text, /--browser-channel/);
  assert.match(text, /--per-check-timeout-ms/);
  assert.match(text, /STACKKIT_PLAYWRIGHT_MODULE_DIR/);
  assert.match(text, /STACKKIT_BROWSER_CHANNEL/);
});

test('loadPlaywright reports isolated module directory setup guidance when configured path is unusable', async () => {
  const previous = process.env.STACKKIT_PLAYWRIGHT_MODULE_DIR;
  process.env.STACKKIT_PLAYWRIGHT_MODULE_DIR = path.join(process.cwd(), 'does-not-exist');
  try {
    await assert.rejects(() => loadPlaywright(), /STACKKIT_PLAYWRIGHT_MODULE_DIR/);
  } finally {
    if (previous === undefined) delete process.env.STACKKIT_PLAYWRIGHT_MODULE_DIR;
    else process.env.STACKKIT_PLAYWRIGHT_MODULE_DIR = previous;
  }
});
