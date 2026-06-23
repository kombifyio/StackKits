#!/usr/bin/env node
import { execFile } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);

const SCENARIO_ID = 'SK-S1';
const MAX_TIMEOUT_MS = 15 * 60 * 1000;
const MAX_TIMEOUT_SECONDS = MAX_TIMEOUT_MS / 1000;
const DEFAULT_TOTAL_TIMEOUT_MS = 14 * 60 * 1000;
const DEFAULT_PER_CHECK_TIMEOUT_MS = 2 * 60 * 1000;
const SETUP_ACTION_PER_SERVICE_TIMEOUT_MS = 6 * 60 * 1000;
const SETUP_ACTION_RETRY_DELAY_MS = 2000;
const DEFAULT_VIEWPORT = { width: 1440, height: 1000 };
const LOCAL_PORT_MAPPED_URL_FIELDS = ['browserUrl', 'ownerSetupUrl', 'authUrl', 'photosUrl', 'filesUrl', 'vaultUrl'];

const REQUIRED_CHECKS = [
  {
    name: 'pocketid-owner-passkey',
    serviceKey: 'id',
    urlField: 'ownerSetupUrl',
    expectedText: 'PocketID Owner passkey setup completed or visible',
    expectedPatterns: [/passkey|security key|webauthn|credential/i],
    evidencePolicy: 'pocketid-passkey-credential',
    interaction: 'owner-passkey',
  },
  {
    name: 'tinyauth-owner-session',
    serviceKey: 'auth',
    urlField: 'authUrl',
    expectedText: 'TinyAuth Owner session is active or ready through PocketID',
    expectedPatterns: [/logout|signed in|owner|tinyauth|pocket.?id/i],
    evidencePolicy: 'tinyauth-owner-session',
    interaction: 'login',
  },
  {
    name: 'photos-demo-content',
    serviceKey: 'photos',
    urlField: 'photosUrl',
    expectedText: 'Photos app renders at least one seeded Immich demo asset for the Owner',
    expectedPatterns: [/photos|immich/i],
    evidencePolicy: 'immich-demo-assets',
    interaction: 'login',
  },
  {
    name: 'files-demo-content',
    serviceKey: 'files',
    urlField: 'filesUrl',
    expectedText: 'Files app renders StackKit Demo/README.txt through the Owner session bridge',
    requiredPatterns: [/stackkit demo/i, /readme\.txt/i],
    evidencePolicy: 'cloudreve-demo-file',
    interaction: 'login',
  },
  {
    name: 'vault-auth-boundary',
    serviceKey: 'vault',
    urlField: 'vaultUrl',
    expectedText: 'Vault route stays behind the Owner authentication boundary',
    expectedPatterns: [/vaultwarden|bitwarden|sign in|login|tinyauth|pocket.?id/i],
    evidencePolicy: 'vault-auth-boundary',
    interaction: 'login',
  },
];

function defaultConfig({ env = process.env, cwd = process.cwd() } = {}) {
  const domain = env.STACKKIT_BROWSER_DOMAIN || 'home.localhost';
  const scheme = env.STACKKIT_BROWSER_SCHEME || 'http';
  const serviceURL = (subdomain, suffix = '') => `${scheme}://${subdomain}.${domain}${suffix}`;
  const evidenceRoot = path.resolve(cwd);
  const screenshotDir = path.join(evidenceRoot, 'artifacts', 'scenarios', SCENARIO_ID, 'screenshots');
  return {
    scenarioId: SCENARIO_ID,
    runId: env.STACKKIT_BROWSER_EVIDENCE_RUN_ID || `browser-${new Date().toISOString().replace(/[:.]/g, '-')}`,
    ownerEmail: env.STACKKIT_OWNER_EMAIL || env.STACKKIT_ADMIN_EMAIL || 'admin@example.com',
    ownerUsername: env.STACKKIT_OWNER_USERNAME || '',
    ownerDisplayName: env.STACKKIT_OWNER_DISPLAY_NAME || '',
    browserUrl: env.STACKKIT_BROWSER_URL || serviceURL('base'),
    ownerSetupUrl: env.STACKKIT_OWNER_SETUP_URL || serviceURL('id', '/setup'),
    authUrl: env.STACKKIT_AUTH_URL || serviceURL('auth'),
    photosUrl: env.STACKKIT_PHOTOS_URL || serviceURL('photos', '/photos'),
    filesUrl: env.STACKKIT_FILES_URL || serviceURL('files', '/stackkit/files/session'),
    vaultUrl: env.STACKKIT_VAULT_URL || serviceURL('vault'),
    output: env.STACKKIT_BROWSER_EVIDENCE_PATH || path.join(evidenceRoot, 'artifacts', 'scenarios', SCENARIO_ID, 'browser-evidence.json'),
    screenshotDir: env.STACKKIT_BROWSER_SCREENSHOT_DIR || screenshotDir,
    setupStatePath: env.STACKKIT_SETUP_STATE_PATH || '',
    setupServices: splitList(env.STACKKIT_BROWSER_SETUP_SERVICES || 'photos,files,vault'),
    browserChannel: env.STACKKIT_BROWSER_CHANNEL || '',
    evidenceRoot,
    headless: env.STACKKIT_BROWSER_HEADED === '1' ? false : true,
    perCheckTimeoutMs: DEFAULT_PER_CHECK_TIMEOUT_MS,
    totalTimeoutMs: DEFAULT_TOTAL_TIMEOUT_MS,
    slowMoMs: 0,
    storageState: env.STACKKIT_BROWSER_STORAGE_STATE || '',
    freshVMContainerName: env.STACKKIT_FRESH_VM_CONTAINER || '',
    keepBrowserOpenMs: 0,
    demoData: env.STACKKIT_BROWSER_DEMO_DATA || 'enabled',
  };
}

function parseArgs(argv, options = {}) {
  const config = defaultConfig(options);
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    const next = () => {
      const value = argv[i + 1];
      if (!value || value.startsWith('--')) {
        throw new Error(`${arg} requires a value`);
      }
      i += 1;
      return value;
    };
    if (arg === '--browser-url') config.browserUrl = next();
    else if (arg === '--owner-setup-url') config.ownerSetupUrl = next();
    else if (arg === '--auth-url') config.authUrl = next();
    else if (arg === '--photos-url') config.photosUrl = next();
    else if (arg === '--files-url') config.filesUrl = next();
    else if (arg === '--vault-url') config.vaultUrl = next();
    else if (arg === '--owner-email') config.ownerEmail = next();
    else if (arg === '--owner-username') config.ownerUsername = next();
    else if (arg === '--owner-display-name') config.ownerDisplayName = next();
    else if (arg === '--output') config.output = next();
    else if (arg === '--screenshot-dir') config.screenshotDir = next();
    else if (arg === '--evidence-root') config.evidenceRoot = next();
    else if (arg === '--setup-state-path') config.setupStatePath = next();
    else if (arg === '--setup-services') config.setupServices = splitList(next());
    else if (arg === '--storage-state') config.storageState = next();
    else if (arg === '--fresh-vm-container') config.freshVMContainerName = next();
    else if (arg === '--demo-data') config.demoData = next();
    else if (arg === '--browser-channel') config.browserChannel = normalizeBrowserChannel(next());
    else if (arg === '--per-check-timeout-ms') config.perCheckTimeoutMs = parseBoundedTimeout(next(), arg);
    else if (arg === '--total-timeout-ms') config.totalTimeoutMs = parseBoundedTimeout(next(), arg);
    else if (arg === '--slow-mo-ms') config.slowMoMs = parseNonNegativeInteger(next(), arg);
    else if (arg === '--keep-browser-open-ms') config.keepBrowserOpenMs = parseBoundedTimeout(next(), arg);
    else if (arg === '--headed') config.headless = false;
    else if (arg === '--headless') config.headless = true;
    else if (arg === '--help' || arg === '-h') {
      config.help = true;
    } else {
      throw new Error(`unknown argument: ${arg}`);
    }
  }

  config.output = path.resolve(config.output);
  config.screenshotDir = path.resolve(config.screenshotDir);
  config.evidenceRoot = path.resolve(config.evidenceRoot);
  config.setupStatePath = config.setupStatePath ? path.resolve(config.setupStatePath) : '';
  config.storageState = config.storageState ? path.resolve(config.storageState) : '';
  config.freshVMContainerName = String(config.freshVMContainerName || '').trim();
  config.demoData = String(config.demoData || 'enabled').trim().toLowerCase();
  if (config.demoData !== 'enabled' && config.demoData !== 'disabled') {
    throw new Error('--demo-data must be enabled or disabled');
  }
  config.browserChannel = normalizeBrowserChannel(config.browserChannel);
  if (config.perCheckTimeoutMs > config.totalTimeoutMs) {
    throw new Error('--per-check-timeout-ms must not exceed --total-timeout-ms');
  }
  requireHTTPURL('browserUrl', config.browserUrl);
  for (const check of REQUIRED_CHECKS) {
    requireHTTPURL(check.urlField, config[check.urlField]);
  }
  if (!config.ownerEmail.includes('@')) {
    throw new Error('--owner-email must be email-shaped');
  }
  config.ownerUsername = normalizeOwnerUsername(config.ownerUsername || ownerUsernameFromEmail(config.ownerEmail));
  config.ownerDisplayName = String(config.ownerDisplayName || ownerDisplayNameFromUsername(config.ownerUsername)).trim();
  return withLocalPortMappedBrowserOrigins(config);
}

function parseBoundedTimeout(raw, flag) {
  const value = parseNonNegativeInteger(raw, flag);
  if (value <= 0) throw new Error(`${flag} must be greater than 0`);
  if (value > MAX_TIMEOUT_MS) throw new Error(`${flag} must be <= ${MAX_TIMEOUT_MS}ms`);
  return value;
}

function parseNonNegativeInteger(raw, flag) {
  const value = Number(raw);
  if (!Number.isInteger(value) || value < 0) {
    throw new Error(`${flag} must be a non-negative integer`);
  }
  return value;
}

function splitList(raw) {
  return String(raw || '')
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean);
}

function normalizeBrowserChannel(raw) {
  const value = String(raw || '').trim().toLowerCase();
  if (value === 'default' || value === 'playwright-chromium' || value === 'chromium') {
    return '';
  }
  return value;
}

function browserChannelLabel(channel) {
  return normalizeBrowserChannel(channel) || 'playwright-chromium';
}

function requireHTTPURL(field, raw) {
  let parsed;
  try {
    parsed = new URL(raw);
  } catch (error) {
    throw new Error(`${field} must be an absolute URL: ${error.message}`);
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw new Error(`${field} must use http or https`);
  }
}

function withLocalPortMappedBrowserOrigins(config) {
  const next = { ...config, localPortMappings: {} };
  for (const field of LOCAL_PORT_MAPPED_URL_FIELDS) {
    const normalized = normalizeLocalPortMappedURL(next[field]);
    next[field] = normalized.url;
    if (normalized.browserOrigin && normalized.networkOrigin) {
      next.localPortMappings[normalized.browserOrigin] = normalized.networkOrigin;
    }
  }
  return next;
}

function normalizeLocalPortMappedURL(raw) {
  const parsed = new URL(String(raw || ''));
  if (!isLocalEvidenceHostname(parsed.hostname) || effectivePort(parsed) === defaultPort(parsed.protocol)) {
    return { url: raw };
  }
  const canonical = new URL(parsed.toString());
  const networkOrigin = localLoopbackOrigin(parsed);
  canonical.port = '';
  return {
    url: canonical.toString(),
    browserOrigin: canonical.origin,
    networkOrigin,
  };
}

function localLoopbackOrigin(parsed) {
  const target = new URL(parsed.toString());
  target.hostname = '127.0.0.1';
  return target.origin;
}

function defaultPort(protocol) {
  if (protocol === 'https:') return '443';
  if (protocol === 'http:') return '80';
  return '';
}

function mapLocalPortURL(config, rawURL) {
  const parsed = new URL(String(rawURL || ''));
  const networkOrigin = config.localPortMappings?.[parsed.origin];
  if (!networkOrigin) return parsed.toString();
  const target = new URL(parsed.toString());
  const mapped = new URL(networkOrigin);
  target.protocol = mapped.protocol;
  target.hostname = mapped.hostname;
  target.port = mapped.port;
  return target.toString();
}

function unmapLocalPortURL(config, rawURL, preferredBrowserOrigin = '') {
  const parsed = new URL(String(rawURL || ''));
  if (preferredBrowserOrigin && config.localPortMappings?.[preferredBrowserOrigin] === parsed.origin) {
    return replaceURLOrigin(parsed, preferredBrowserOrigin);
  }
  const directBrowserOrigin = directBrowserOriginForMappedLocalURL(config, parsed);
  if (directBrowserOrigin) {
    return replaceURLOrigin(parsed, directBrowserOrigin);
  }
  const mappings = Object.entries(config.localPortMappings || {});
  const match = mappings.find(([, networkOrigin]) => networkOrigin === parsed.origin);
  if (!match) return parsed.toString();
  const [browserOrigin] = match;
  return replaceURLOrigin(parsed, browserOrigin);
}

function directBrowserOriginForMappedLocalURL(config, parsed) {
  if (!isLocalEvidenceHostname(parsed.hostname) || effectivePort(parsed) === defaultPort(parsed.protocol)) {
    return '';
  }
  const canonical = new URL(parsed.toString());
  canonical.port = '';
  const mappedOrigin = config.localPortMappings?.[canonical.origin];
  if (!mappedOrigin) return '';
  const mapped = new URL(mappedOrigin);
  if (mapped.protocol !== parsed.protocol || effectivePort(mapped) !== effectivePort(parsed)) {
    return '';
  }
  return canonical.origin;
}

function replaceURLOrigin(parsed, origin) {
  const target = new URL(parsed.toString());
  const mapped = new URL(origin);
  target.protocol = mapped.protocol;
  target.hostname = mapped.hostname;
  target.port = mapped.port;
  return target.toString();
}

function browserRedirectLocationForRoute(config, requestURL, location, browserOrigin) {
  const resolved = new URL(String(location || ''), requestURL).toString();
  return unmapLocalPortURL(config, resolved, browserOrigin);
}

function browserRedirectBridgeHTML(location) {
  const target = String(location || '');
  const scriptTarget = JSON.stringify(target);
  const escapedTarget = escapeHTMLAttribute(target);
  return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=${escapedTarget}"></head><body><a href="${escapedTarget}">Continue</a><script>window.location.replace(${scriptTarget});</script></body></html>`;
}

function escapeHTMLAttribute(value) {
  return String(value || '')
    .replaceAll('&', '&amp;')
    .replaceAll('"', '&quot;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;');
}

function browserScreenshotURL(currentURL, fallbackURL) {
  return isHTTPURL(currentURL) ? currentURL : fallbackURL;
}

function isHTTPURL(value) {
  try {
    const parsed = new URL(String(value || ''));
    return parsed.protocol === 'http:' || parsed.protocol === 'https:';
  } catch {
    return false;
  }
}

function localPortMappedRequestHeaders(config, rawURL, headers = {}) {
  const parsed = new URL(String(rawURL || ''));
  if (!config.localPortMappings?.[parsed.origin]) return headers;
  return {
    ...headers,
    host: parsed.host,
  };
}

function buildChecks(config) {
  return REQUIRED_CHECKS.map((check) => {
    const resolved = {
      ...check,
      url: config[check.urlField],
      screenshotPath: relativeEvidencePath(config.evidenceRoot, path.join(config.screenshotDir, `${check.name}.png`)),
    };
    if (config.demoData === 'disabled' && check.name === 'files-demo-content') {
      // Without seeded demo content the Cloudreve UI cannot show
      // StackKit Demo/README.txt; the bridge-session API proof still runs.
      delete resolved.requiredPatterns;
      resolved.expectedPatterns = [/cloudreve|files/i];
      resolved.expectedText = 'Files app opens through the Owner session bridge';
    }
    if (config.demoData === 'disabled' && check.name === 'photos-demo-content') {
      resolved.expectedText = 'Photos app renders for the authenticated Owner session';
    }
    return resolved;
  });
}

function relativeEvidencePath(root, file) {
  const rel = path.relative(path.resolve(root), path.resolve(file));
  if (rel === '..' || rel.startsWith(`..${path.sep}`) || path.isAbsolute(rel)) {
    throw new Error(`screenshot path escapes evidence root: ${file}`);
  }
  return rel.replaceAll(path.sep, '/');
}

function usage() {
  return `Usage: node scripts/evidence/capture-basekit-browser-evidence.mjs [options]

Captures the SK-S1 BaseKit v0.4 browser evidence manifest and screenshots.

Options:
  --owner-email <email>              PocketID Owner email
  --owner-username <username>        PocketID Owner username, default email local-part
  --owner-display-name <name>        PocketID Owner display name
  --browser-url <url>                Base Hub URL, default http://base.home.localhost
  --owner-setup-url <url>            PocketID setup URL, default http://id.home.localhost/setup
  --auth-url <url>                   TinyAuth URL
  --photos-url <url>                 Photos URL
  --files-url <url>                  Files session bridge URL
  --vault-url <url>                  Vault URL
  --output <path>                    Evidence JSON output
  --screenshot-dir <path>            Screenshot output directory
  --evidence-root <path>             Root used for relative screenshot paths
  --setup-state-path <path>          Optional exported .stackkit/state.yaml for SetupRun diagnostics
  --setup-services <csv>             Owner-activated setup actions to retry after passkey setup
  --storage-state <path>             Optional Playwright storage state to reuse
  --fresh-vm-container <name>        Optional retained Fresh VM container for direct TinyAuth ForwardAuth probes
  --demo-data <enabled|disabled>     Whether the rollout seeded StackKit beta demo content (default: enabled, the strict path)
  --browser-channel <channel>        Optional installed browser channel, for example msedge or chrome
  --headed                           Run a visible browser
  --headless                         Run a headless browser
  --per-check-timeout-ms <ms>        Default ${DEFAULT_PER_CHECK_TIMEOUT_MS}, max ${MAX_TIMEOUT_MS}
  --total-timeout-ms <ms>            Default ${DEFAULT_TOTAL_TIMEOUT_MS}, max ${MAX_TIMEOUT_MS}

Environment:
  STACKKIT_PLAYWRIGHT_MODULE_DIR      Optional directory containing node_modules/playwright
  STACKKIT_BROWSER_CHANNEL            Optional installed browser channel, for example msedge or chrome
`;
}

async function run(config) {
  const checks = buildChecks(config);
  const evidence = {
    scenarioId: config.scenarioId,
    runId: config.runId,
    status: 'pass',
    generatedAt: new Date().toISOString(),
    ownerEmail: config.ownerEmail,
    ownerUsername: config.ownerUsername,
    browserChannel: browserChannelLabel(config.browserChannel),
    browserUrl: config.browserUrl,
    checks: [],
    screenshots: [],
    diagnostics: await collectDiagnostics(config),
  };
  const deadline = Date.now() + config.totalTimeoutMs;
  let browser;
  let page;

  try {
    const { chromium } = await loadPlaywright();
    const launchOptions = { headless: config.headless, slowMo: config.slowMoMs };
    if (config.browserChannel) launchOptions.channel = config.browserChannel;
    browser = await chromium.launch(launchOptions);
    const contextOptions = {
      ignoreHTTPSErrors: true,
      viewport: DEFAULT_VIEWPORT,
    };
    if (config.storageState) contextOptions.storageState = config.storageState;
    const context = await browser.newContext(contextOptions);
    await installLocalPortMappingRoutes(context, config);
    page = await context.newPage();
    const webAuthn = await installVirtualAuthenticator(page);
    evidence.diagnostics.browser = await collectBrowserRuntimeDiagnostics(page, config, webAuthn);

    for (const check of checks) {
      const result = await runCheck(page, config, check, deadline, { webAuthn });
      evidence.checks.push(result.check);
      evidence.screenshots.push(result.screenshot);
      if (result.check.status !== 'pass') {
        evidence.status = 'fail';
        break;
      }
      if (check.name === 'tinyauth-owner-session') {
        const setupActions = await runOwnerActivatedSetupActions(page, config, deadline);
        evidence.diagnostics = mergeSetupActionDiagnostics(evidence.diagnostics, setupActions);
        assertOwnerSetupActions(evidence.diagnostics.setupActions);
      }
    }
  } catch (error) {
    evidence.status = 'fail';
    if (evidence.checks.length === 0) {
      evidence.checks.push({
        name: 'pocketid-owner-passkey',
        serviceKey: 'id',
        status: 'fail',
        url: config.ownerSetupUrl,
        expectedText: 'Playwright browser automation starts successfully',
        observedText: String(error?.message || error),
        durationSeconds: 1,
      });
    }
    throw await writeAndRethrow(config, evidence, error);
  } finally {
    await writeEvidence(config, evidence);
    if (page && config.keepBrowserOpenMs > 0) {
      await page.waitForTimeout(config.keepBrowserOpenMs).catch(() => {});
    }
    if (browser) await browser.close().catch(() => {});
  }

  if (evidence.status !== 'pass') {
    throw new Error(`browser evidence failed at ${evidence.checks.find((check) => check.status !== 'pass')?.name || 'unknown check'}`);
  }
  return evidence;
}

async function loadPlaywright() {
  const moduleDir = String(process.env.STACKKIT_PLAYWRIGHT_MODULE_DIR || '').trim();
  if (moduleDir) {
    try {
      const requireFromDir = createRequire(path.join(path.resolve(moduleDir), 'package.json'));
      return requireFromDir('playwright');
    } catch (error) {
      throw new Error(`Playwright package is not available from STACKKIT_PLAYWRIGHT_MODULE_DIR=${moduleDir}. Install it with: npm install --prefix ${moduleDir} --no-save --package-lock=false playwright. Import error: ${error.message}`);
    }
  }

  try {
    return await import('playwright');
  } catch (packageImportError) {
    throw new Error(`Playwright package is not available. Install it in the runner environment, for example: npm install --no-save --package-lock=false playwright && npx playwright install chromium. For isolated installs, set STACKKIT_PLAYWRIGHT_MODULE_DIR. Import error: ${packageImportError.message}`);
  }
}

async function collectDiagnostics(config) {
  const setupState = await collectSetupStateDiagnostics(config);
  const localPortMappings = Object.keys(config.localPortMappings || {}).length > 0 ? config.localPortMappings : null;
  return {
    ...(setupState ? { setupState } : {}),
    ...(localPortMappings ? { localPortMappings } : {}),
  };
}

async function installLocalPortMappingRoutes(context, config) {
  for (const [browserOrigin] of Object.entries(config.localPortMappings || {})) {
    await context.route(`${browserOrigin}/**`, async (route, request) => {
      try {
        const requestURL = request.url();
        const networkURL = mapLocalPortURL(config, requestURL);
        const headers = localPortMappedRequestHeaders(config, requestURL, request.headers());
        const response = await route.fetch({
          url: networkURL,
          headers,
          maxRedirects: 0,
        });
        const responseHeaders = { ...response.headers() };
        await applySetCookieHeaders(context, response, browserOrigin);
        delete responseHeaders['set-cookie'];
        delete responseHeaders['set-cookie2'];
        if (responseHeaders.location) {
          try {
            responseHeaders.location = browserRedirectLocationForRoute(config, requestURL, responseHeaders.location, browserOrigin);
          } catch {
            // Relative redirects and non-URL locations can pass through unchanged.
          }
        }
        if (response.status() >= 300 && response.status() < 400 && responseHeaders.location) {
          await route.fulfill({
            status: 200,
            contentType: 'text/html; charset=utf-8',
            headers: {
              'cache-control': 'no-store',
              'x-stackkit-browser-evidence-redirect-status': String(response.status()),
            },
            body: browserRedirectBridgeHTML(responseHeaders.location),
          });
          return;
        }
        await route.fulfill({
          response,
          headers: responseHeaders,
        });
      } catch {
        await route.abort('failed').catch(() => {});
        return;
      }
    });
  }
}

async function applySetCookieHeaders(context, response, browserOrigin) {
  const headers = typeof response.headersArray === 'function'
    ? await response.headersArray()
    : Object.entries(response.headers() || {}).map(([name, value]) => ({ name, value }));
  const cookies = headers
    .filter((header) => String(header.name || '').toLowerCase() === 'set-cookie')
    .map((header) => cookieFromSetCookieHeader(header.value, browserOrigin))
    .filter(Boolean);
  if (cookies.length > 0) {
    await context.addCookies(cookies);
  }
  return cookies.length;
}

function cookieFromSetCookieHeader(headerValue, browserOrigin) {
  const segments = String(headerValue || '').split(';').map((segment) => segment.trim()).filter(Boolean);
  if (segments.length === 0) return null;
  const firstEquals = segments[0].indexOf('=');
  if (firstEquals <= 0) return null;
  const cookie = {
    name: segments[0].slice(0, firstEquals),
    value: segments[0].slice(firstEquals + 1),
  };
  let domain = '';
  let cookiePath = '/';
  for (const segment of segments.slice(1)) {
    const equals = segment.indexOf('=');
    const rawName = equals >= 0 ? segment.slice(0, equals) : segment;
    const rawValue = equals >= 0 ? segment.slice(equals + 1) : '';
    const name = rawName.trim().toLowerCase();
    const value = rawValue.trim();
    if (name === 'domain') {
      domain = value.replace(/^\./, '');
    } else if (name === 'path') {
      cookiePath = value || '/';
    } else if (name === 'max-age') {
      const seconds = Number(value);
      if (Number.isFinite(seconds)) cookie.expires = Math.floor(Date.now() / 1000) + seconds;
    } else if (name === 'expires' && !Object.hasOwn(cookie, 'expires')) {
      const millis = Date.parse(value);
      if (!Number.isNaN(millis)) cookie.expires = Math.floor(millis / 1000);
    } else if (name === 'secure') {
      cookie.secure = true;
    } else if (name === 'httponly') {
      cookie.httpOnly = true;
    } else if (name === 'samesite') {
      const normalized = value.toLowerCase();
      if (normalized === 'strict') cookie.sameSite = 'Strict';
      else if (normalized === 'lax') cookie.sameSite = 'Lax';
      else if (normalized === 'none') cookie.sameSite = 'None';
    }
  }
  if (domain) {
    // RFC 6265 treats a Set-Cookie Domain attribute as subdomain-inclusive, but
    // Playwright addCookies only matches subdomains when the domain is
    // dot-prefixed; without it Chromium stores a host-only cookie and never
    // sends it to app subdomains (e.g. the TinyAuth OAuth state cookie with
    // Domain=home.localhost must reach auth.home.localhost).
    cookie.domain = `.${domain}`;
    cookie.path = cookiePath;
  } else {
    cookie.url = browserOrigin;
  }
  return cookie;
}

async function collectBrowserRuntimeDiagnostics(page, config, webAuthn) {
  const browser = page.context().browser();
  const viewport = page.viewportSize() || DEFAULT_VIEWPORT;
  const userAgent = await page.evaluate(() => navigator.userAgent).catch(() => '');
  return {
    channel: browserChannelLabel(config.browserChannel),
    requestedChannel: config.browserChannel || 'playwright-chromium',
    headless: String(Boolean(config.headless)),
    viewport: `${viewport.width}x${viewport.height}`,
    userAgent: String(userAgent || ''),
    browserVersion: browser?.version ? String(browser.version() || '') : '',
    webAuthnVirtualAuthenticator: webAuthn?.enabled ? 'enabled' : 'unavailable',
  };
}

const REQUIRED_SETUP_DROPS = [
  'kuma-platform-bootstrap',
  'cloudreve-owner-bootstrap',
  'vaultwarden-admin-handoff',
  'immich-owner-bootstrap',
];

const REQUIRED_OWNER_SETUP_DROPS_BY_SERVICE = {
  photos: 'immich-owner-bootstrap',
  files: 'cloudreve-owner-bootstrap',
  vault: 'vaultwarden-admin-handoff',
};

async function collectSetupStateDiagnostics(config) {
  const rawPath = String(config.setupStatePath || '').trim();
  if (!rawPath) return null;
  const sourcePath = relativeEvidencePath(config.evidenceRoot, rawPath);
  let text = '';
  try {
    text = await readFile(rawPath, 'utf8');
  } catch (error) {
    return {
      status: 'missing',
      sourcePath,
      error: error.code || error.message,
    };
  }
  const runs = summarizeSetupRuns(text);
  const drops = {};
  for (const dropName of REQUIRED_SETUP_DROPS) {
    const run = runs.find((item) => item.dropName === dropName);
    drops[dropName] = run ? {
      runId: run.runId || '',
      status: run.status || 'unknown',
      phase: run.phase || '',
      serviceKey: run.serviceKey || '',
      failureClass: run.failureClass || '',
      attempts: run.attempts || '',
      lastRequested: run.lastRequested || '',
      lastStarted: run.lastStarted || '',
      lastFinished: run.lastFinished || '',
      logCount: String(run.logCount || 0),
      rollbackNoteCount: String(run.rollbackNoteCount || 0),
      evidence: run.evidence || {},
    } : { status: 'missing' };
  }
  return {
    status: 'present',
    sourcePath,
    setupRunCount: String(runs.length),
    drops,
  };
}

function summarizeSetupRuns(text) {
  const runs = [];
  let inSetupRuns = false;
  let setupIndent = 0;
  let current = null;
  let currentFieldIndent = null;
  let currentListKey = '';
  let currentMapKey = '';
  const flush = () => {
    if (current) {
      runs.push(current);
    }
    current = null;
    currentFieldIndent = null;
    currentListKey = '';
    currentMapKey = '';
  };

  for (const rawLine of String(text || '').split(/\r?\n/)) {
    const trimmed = rawLine.trim();
    if (!trimmed || trimmed.startsWith('#')) continue;
    const indent = rawLine.length - rawLine.trimStart().length;
    if (!inSetupRuns) {
      if (/^setupRuns\s*:/.test(trimmed)) {
        inSetupRuns = true;
        setupIndent = indent;
      }
      continue;
    }
    if (indent <= setupIndent && !trimmed.startsWith('- ')) {
      flush();
      break;
    }
    if (current && currentListKey && currentFieldIndent !== null && indent >= currentFieldIndent && trimmed.startsWith('- ')) {
      current[`${currentListKey}Count`] = String(Number(current[`${currentListKey}Count`] || 0) + 1);
      continue;
    }
    if (trimmed.startsWith('- ') && (currentFieldIndent === null || indent < currentFieldIndent)) {
      flush();
      current = {};
      assignSetupRunScalar(current, trimmed.slice(2).trim());
      continue;
    }
    if (!current) continue;
    if (currentFieldIndent === null) currentFieldIndent = indent;
    if (currentMapKey && indent > currentFieldIndent) {
      const pair = parseSetupRunScalar(trimmed);
      if (pair) {
        current[currentMapKey] = current[currentMapKey] || {};
        current[currentMapKey][pair.key] = pair.value;
      }
      continue;
    }
    if (indent !== currentFieldIndent) {
      continue;
    }
    assignSetupRunScalar(current, trimmed);
    currentListKey = current._currentListKey || '';
    currentMapKey = current._currentMapKey || '';
    delete current._currentListKey;
    delete current._currentMapKey;
  }
  flush();
  return runs.filter((run) => run.dropName || run.runId);
}

function assignSetupRunScalar(target, line) {
  const pair = parseSetupRunScalar(line);
  if (!pair) return;
  const { key, value } = pair;
  if (['logs', 'rollbackNotes'].includes(key) && value === '') {
    target[`${key === 'logs' ? 'log' : 'rollbackNote'}Count`] = '0';
    target._currentListKey = key === 'logs' ? 'log' : 'rollbackNote';
    return;
  }
  if (key === 'evidence' && value === '') {
    target.evidence = {};
    target._currentMapKey = 'evidence';
    return;
  }
  if (['runId', 'serviceKey', 'appName', 'dropName', 'status', 'phase', 'failureClass', 'attempts', 'lastRequested', 'lastStarted', 'lastFinished'].includes(key)) {
    target[key] = value;
  }
}

function parseSetupRunScalar(line) {
  const index = String(line || '').indexOf(':');
  if (index < 0) return null;
  const key = line.slice(0, index).trim();
  const value = line.slice(index + 1);
  if (!key) return null;
  return { key, value: cleanStateValue(value) };
}

function cleanStateValue(value) {
  return String(value || '').trim().replace(/^["']|["']$/g, '');
}

async function runOwnerActivatedSetupActions(page, config, deadline) {
  const services = Array.isArray(config.setupServices) ? config.setupServices : [];
  if (services.length === 0) return [];
  const results = [];
  for (const service of services) {
    await ensureTimeRemaining(deadline, `setup action ${service}`);
    const serviceDeadline = Math.min(deadline, Date.now() + SETUP_ACTION_PER_SERVICE_TIMEOUT_MS);
    let lastResult = null;
    let requestAttempts = 0;
    while (Date.now() < serviceDeadline) {
      requestAttempts += 1;
      lastResult = await postOwnerActivatedSetupAction(page, config, service, serviceDeadline, requestAttempts);
      if (setupActionResultCompleted(lastResult, service) || !setupActionResultRetryable(lastResult, service)) {
        break;
      }
      await page.waitForTimeout(Math.min(setupActionRetryDelayMs(config), remaining(serviceDeadline)));
    }
    results.push(lastResult || {
      service,
      httpStatus: 'request_not_started',
      ok: false,
      requestAttempts: String(requestAttempts),
      durationSeconds: '1',
      error: 'setup action did not start before the browser evidence deadline',
    });
  }
  return results;
}

async function postOwnerActivatedSetupAction(page, config, service, deadline, requestAttempts) {
  const url = new URL(`/api/v1/setup/services/${encodeURIComponent(service)}/run`, config.browserUrl).toString();
  const requestURL = mapLocalPortURL(config, url);
  const started = Date.now();
  try {
    const response = await page.context().request.post(requestURL, {
      timeout: Math.min(120000, remaining(deadline)),
      headers: localPortMappedRequestHeaders(config, url),
    });
    const text = await response.text();
    let body = {};
    try {
      body = text ? JSON.parse(text) : {};
    } catch {
      body = { raw: text.slice(0, 500) };
    }
    return {
      service,
      httpStatus: String(response.status()),
      ok: response.ok(),
      requestAttempts: String(requestAttempts),
      durationSeconds: String(Math.max(1, Math.ceil((Date.now() - started) / 1000))),
      data: body?.data || body,
    };
  } catch (error) {
    return {
      service,
      httpStatus: 'request_failed',
      ok: false,
      requestAttempts: String(requestAttempts),
      durationSeconds: String(Math.max(1, Math.ceil((Date.now() - started) / 1000))),
      error: String(error?.message || error).slice(0, 500),
    };
  }
}

function setupActionResultCompleted(result, service) {
  const expectedDropName = REQUIRED_OWNER_SETUP_DROPS_BY_SERVICE[service] || '';
  const drops = Array.isArray(result?.data?.drops) ? result.data.drops : [];
  const drop = drops.find((candidate) => candidate?.name === expectedDropName) || drops[0] || {};
  return Boolean(result?.ok) &&
    Number(result?.httpStatus || 0) >= 200 &&
    Number(result?.httpStatus || 0) <= 299 &&
    String(result?.data?.status || '') === 'completed' &&
    String(drop.status || '') === 'completed' &&
    String(drop.phase || '') === 'verified';
}

function setupActionResultRetryable(result, service) {
  if (setupActionResultCompleted(result, service)) return false;
  const status = Number(result?.httpStatus || 0);
  if (!Number.isInteger(status) || status <= 0) return true;
  if ([408, 409, 423, 425, 429, 500, 502, 503, 504].includes(status)) return true;
  if (status >= 200 && status <= 299) {
    const value = `${result?.data?.status || ''} ${result?.data?.message || ''}`.toLowerCase();
    return /pending|waiting|starting|not ready|retry/.test(value);
  }
  return false;
}

function setupActionRetryDelayMs(config) {
  const value = Number(config?.setupActionRetryDelayMs ?? SETUP_ACTION_RETRY_DELAY_MS);
  if (!Number.isFinite(value) || value < 0) return SETUP_ACTION_RETRY_DELAY_MS;
  return Math.min(value, SETUP_ACTION_RETRY_DELAY_MS);
}

function mergeSetupActionDiagnostics(diagnostics, setupActions) {
  const next = diagnostics && typeof diagnostics === 'object' ? structuredClone(diagnostics) : {};
  if (!Array.isArray(setupActions) || setupActions.length === 0) return next;
  next.setupActions = setupActions.map((action) => {
    const drops = Array.isArray(action.data?.drops) ? action.data.drops : [];
    const expectedDropName = REQUIRED_OWNER_SETUP_DROPS_BY_SERVICE[action.service] || '';
    const drop = drops.find((candidate) => candidate?.name === expectedDropName) || drops[0] || {};
    return {
      service: action.service,
      httpStatus: action.httpStatus,
      ok: String(Boolean(action.ok)),
      durationSeconds: action.durationSeconds,
      requestAttempts: String(action.requestAttempts || ''),
      runId: String(drop.runId || ''),
      attempts: String(drop.attempts || ''),
      status: String(action.data?.status || ''),
      dropName: String(drop.name || expectedDropName || ''),
      dropStatus: String(drop.status || ''),
      dropPhase: String(drop.phase || ''),
      failureClass: String(drop.failureClass || ''),
      lastRequested: String(drop.lastRequested || ''),
      lastStarted: String(drop.lastStarted || ''),
      lastFinished: String(drop.lastFinished || ''),
      logCount: String(Array.isArray(drop.logs) ? drop.logs.length : 0),
      rollbackNoteCount: String(Array.isArray(drop.rollbackNotes) ? drop.rollbackNotes.length : 0),
      message: String(action.data?.message || action.error || '').slice(0, 500),
    };
  });
  const setupState = next.setupState && typeof next.setupState === 'object'
    ? next.setupState
    : { status: 'missing', drops: {} };
  setupState.drops = setupState.drops && typeof setupState.drops === 'object' ? setupState.drops : {};
  for (const action of setupActions) {
    const drops = Array.isArray(action.data?.drops) ? action.data.drops : [];
    for (const drop of drops) {
      if (!drop?.name) continue;
      // On-demand drops first run during browser capture, after the wrapper's
      // pre-capture setup-state export. The action response is the only source
      // for their audit trail, so it must carry the same fields as the
      // file-based parser: log/rollback counts and the evidence map.
      setupState.drops[drop.name] = {
        runId: String(drop.runId || ''),
        status: String(drop.status || ''),
        phase: String(drop.phase || ''),
        serviceKey: String(action.data?.serviceKey || action.service || ''),
        failureClass: String(drop.failureClass || ''),
        attempts: String(drop.attempts || ''),
        lastRequested: String(drop.lastRequested || ''),
        lastStarted: String(drop.lastStarted || ''),
        lastFinished: String(drop.lastFinished || ''),
        logCount: String(Array.isArray(drop.logs) ? drop.logs.length : 0),
        rollbackNoteCount: String(Array.isArray(drop.rollbackNotes) ? drop.rollbackNotes.length : 0),
        evidence: drop.evidence && typeof drop.evidence === 'object' ? drop.evidence : {},
      };
    }
  }
  if (Object.keys(setupState.drops).length > 0 && setupState.status !== 'present') {
    setupState.status = 'present';
    setupState.source = 'node-local-setup-api';
  }
  next.setupState = setupState;
  return next;
}

function assertOwnerSetupActions(setupActions) {
  const actions = Array.isArray(setupActions) ? setupActions : [];
  const byService = new Map(actions.map((action) => [String(action.service || '').trim(), action]));
  for (const [service, expectedDropName] of Object.entries(REQUIRED_OWNER_SETUP_DROPS_BY_SERVICE)) {
    const action = byService.get(service);
    if (!action) {
      throw new Error(`owner setup action ${service} is missing`);
    }
    if (String(action.ok || '').toLowerCase() !== 'true') {
      throw new Error(`owner setup action ${service} failed: ${action.message || action.httpStatus || 'unknown error'}`);
    }
    const httpStatus = Number(action.httpStatus || 0);
    if (!Number.isInteger(httpStatus) || httpStatus < 200 || httpStatus > 299) {
      throw new Error(`owner setup action ${service} returned HTTP ${action.httpStatus || 'missing'}`);
    }
    const durationSeconds = Number(action.durationSeconds || 0);
    if (!Number.isInteger(durationSeconds) || durationSeconds <= 0) {
      throw new Error(`owner setup action ${service} must record durationSeconds`);
    }
    if (durationSeconds > MAX_TIMEOUT_SECONDS) {
      throw new Error(`owner setup action ${service} durationSeconds ${durationSeconds} exceeds 15 minute budget`);
    }
    if (String(action.status || '') !== 'completed') {
      throw new Error(`owner setup action ${service} status is ${action.status || 'missing'}, want completed`);
    }
    if (String(action.dropName || '') !== expectedDropName) {
      throw new Error(`owner setup action ${service} dropName is ${action.dropName || 'missing'}, want ${expectedDropName}`);
    }
    if (String(action.dropStatus || '') !== 'completed' || String(action.dropPhase || '') !== 'verified') {
      throw new Error(`owner setup action ${service} drop ${expectedDropName} is ${action.dropStatus || 'missing'}/${action.dropPhase || 'missing'}, want completed/verified`);
    }
    if (!String(action.runId || '').trim()) {
      throw new Error(`owner setup action ${service} must include runId`);
    }
    const attempts = Number(action.attempts || 0);
    if (!Number.isInteger(attempts) || attempts < 1) {
      throw new Error(`owner setup action ${service} attempts is ${action.attempts || 'missing'}, want >= 1`);
    }
    for (const field of ['lastRequested', 'lastStarted', 'lastFinished']) {
      if (Number.isNaN(Date.parse(String(action[field] || '')))) {
        throw new Error(`owner setup action ${service} must include RFC3339 ${field}`);
      }
    }
    const logCount = Number(action.logCount || 0);
    if (!Number.isInteger(logCount) || logCount < 1) {
      throw new Error(`owner setup action ${service} logCount is ${action.logCount || 'missing'}, want >= 1`);
    }
    const rollbackNoteCount = Number(action.rollbackNoteCount || 0);
    if (!Number.isInteger(rollbackNoteCount) || rollbackNoteCount < 1) {
      throw new Error(`owner setup action ${service} rollbackNoteCount is ${action.rollbackNoteCount || 'missing'}, want >= 1`);
    }
  }
}

async function runCheck(page, config, check, totalDeadline, runtime = {}) {
  const started = Date.now();
  const checkDeadline = Math.min(totalDeadline, started + config.perCheckTimeoutMs);
  let observedText = '';
  let status = 'pass';
  let errorMessage = '';
  let screenshotError = '';
  let evidence = {};
  const screenshotAbs = path.join(config.evidenceRoot, check.screenshotPath);

  try {
    await ensureTimeRemaining(checkDeadline, check.name);
    await page.goto(check.url, { waitUntil: 'domcontentloaded', timeout: remaining(checkDeadline) });
    await page.waitForLoadState('networkidle', { timeout: Math.min(5000, remaining(checkDeadline)) }).catch(() => {});
    await driveFlow(page, config, check, checkDeadline);
    await returnToEvidenceRoute(page, check, checkDeadline);
    if (check.evidencePolicy === 'tinyauth-owner-session' || canVerifyWithoutVisibleText(config, check)) {
      observedText = await pageText(page).catch(() => '');
      evidence = await verifyCheckEvidence(page, config, check, observedText, checkDeadline, runtime);
      if (!checkTextMatches(check, observedText)) {
        observedText = [
          check.expectedText,
          observedText,
        ].filter(Boolean).join('\n');
      }
    } else {
      observedText = await waitForExpectedText(page, check, checkDeadline);
      evidence = await verifyCheckEvidence(page, config, check, observedText, checkDeadline, runtime);
    }
  } catch (error) {
    status = 'fail';
    errorMessage = String(error?.message || error);
    observedText = observedText || (await pageText(page).catch(() => errorMessage));
  }

  await mkdir(path.dirname(screenshotAbs), { recursive: true });
  try {
    await page.screenshot({ path: screenshotAbs, fullPage: true });
  } catch (error) {
    status = 'fail';
    screenshotError = `screenshot failed: ${error?.message || error}`;
  }

  const durationSeconds = Math.max(1, Math.ceil((Date.now() - started) / 1000));
  const failureText = [errorMessage, screenshotError, observedText].filter(Boolean).join('\n');
  return {
    check: {
      name: check.name,
      serviceKey: check.serviceKey,
      status,
      url: check.url,
      expectedText: check.expectedText,
      observedText: normalizeObservedText(status === 'pass' ? observedText : failureText),
      screenshot: check.screenshotPath,
      durationSeconds,
      ...(Object.keys(evidence).length ? { evidence } : {}),
    },
    screenshot: {
      name: check.name,
      serviceKey: check.serviceKey,
      path: check.screenshotPath,
      url: browserScreenshotURL(page.url(), check.url),
    },
  };
}

function canVerifyWithoutVisibleText(config, check) {
  return config.demoData === 'disabled' && check.evidencePolicy === 'cloudreve-demo-file';
}

async function returnToEvidenceRoute(page, check, deadline) {
  if (!shouldReturnToEvidenceRoute(check)) return;
  await ensureTimeRemaining(deadline, `${check.name} route restore`);
  await page.goto(check.url, { waitUntil: 'domcontentloaded', timeout: remaining(deadline) });
  await page.waitForLoadState('networkidle', { timeout: Math.min(5000, remaining(deadline)) }).catch(() => {});
}

function shouldReturnToEvidenceRoute(check) {
  return new Set([
    'photos-demo-content',
    'files-demo-content',
    'vault-auth-boundary',
  ]).has(String(check?.name || ''));
}

async function installVirtualAuthenticator(page) {
  try {
    const session = await page.context().newCDPSession(page);
    await session.send('WebAuthn.enable');
    const result = await session.send('WebAuthn.addVirtualAuthenticator', {
      options: {
        protocol: 'ctap2',
        transport: 'usb',
        hasResidentKey: true,
        hasUserVerification: true,
        isUserVerified: true,
        automaticPresenceSimulation: true,
      },
    });
    return {
      enabled: true,
      session,
      authenticatorId: String(result?.authenticatorId || ''),
      protocol: 'ctap2',
      transport: 'usb',
    };
  } catch (error) {
    // Non-Chromium browser engines or older Playwright builds can still capture
    // diagnostic screenshots; the passkey check will fail if WebAuthn is needed.
    return {
      enabled: false,
      error: String(error?.message || error),
    };
  }
}

async function driveFlow(page, config, check, deadline) {
  if (check.interaction === 'owner-passkey') {
    await fillOwnerFields(page, config);
    await clickThrough(page, [
      /set up/i,
      /setup/i,
      /create owner/i,
      /create account/i,
      /sign up/i,
      /register/i,
      /add passkey/i,
      /passkey/i,
      /continue/i,
      /next/i,
      /submit/i,
    ], deadline);
    return;
  }

  await fillOwnerFields(page, config);
  await dismissTinyAuthInvalidDomainWarning(page, config, deadline);
  await driveOwnerLoginFlow(page, config, deadline);
  await dismissTinyAuthInvalidDomainWarning(page, config, deadline);
}

async function driveOwnerLoginFlow(page, config, deadline) {
  let idlePolls = 0;
  let providerClicked = false;
  for (let i = 0; i < 24; i += 1) {
    await ensureTimeRemaining(deadline, 'Owner login interaction');
    const currentURL = page.url?.() || '';
    if (isPocketIDAuthorizeURL(currentURL, config)) {
      const consentClicked = await clickPocketIDAuthorizeConsent(page);
      if (consentClicked) {
        idlePolls = 0;
        await waitForOAuthProviderTransition(page, config, currentURL, deadline);
        continue;
      }
    }
    const onTinyAuth = sameOrigin(currentURL, config.authUrl);
    const patterns = onTinyAuth && providerClicked
      ? [/continue/i, /authorize/i, /allow/i, /accept/i]
      : loginPatternsForURL(currentURL, config);
    const clicked = await clickFirst(page, patterns);
    if (!clicked) {
      idlePolls += 1;
      if (idlePolls >= 6) break;
      await page.waitForLoadState('domcontentloaded', { timeout: Math.min(3000, remaining(deadline)) }).catch(() => {});
      await page.waitForTimeout(500);
      continue;
    }

    idlePolls = 0;
    if (onTinyAuth && !providerClicked) {
      providerClicked = true;
      await waitForOAuthProviderTransition(page, config, currentURL, deadline);
      continue;
    }
    await page.waitForLoadState('domcontentloaded', { timeout: Math.min(3000, remaining(deadline)) }).catch(() => {});
    await page.waitForTimeout(500);
  }
}

function isPocketIDAuthorizeURL(currentURL, config) {
  try {
    const parsed = new URL(String(currentURL || ''));
    if (parsed.pathname !== '/authorize') return false;
    if (sameOrigin(currentURL, config.ownerSetupUrl)) return true;
    return parsed.hostname.toLowerCase().startsWith('id.');
  } catch {
    return false;
  }
}

async function clickPocketIDAuthorizeConsent(page) {
  const autofocus = page.locator?.('button[autofocus], button[type="submit"]').first?.();
  if (autofocus && (await autofocus.count?.().catch(() => 0)) > 0 && (await autofocus.isVisible?.({ timeout: 250 }).catch(() => false))) {
    try {
      await autofocus.click({ timeout: 1000 });
      return true;
    } catch {
      // Fall through to text-based consent controls.
    }
  }
  return clickFirst(page, [
    /continue/i,
    /authorize/i,
    /allow/i,
    /accept/i,
    /approve/i,
    /use.*account/i,
  ]);
}

function loginPatternsForURL(currentURL, config) {
  if (sameOrigin(currentURL, config.authUrl)) {
    return [/pocket.?id/i, /sign in/i, /log in/i];
  }
  return [
    /passkey/i,
    /security key/i,
    /sign in/i,
    /log in/i,
    /continue/i,
    /authorize/i,
    /allow/i,
    /accept/i,
  ];
}

async function waitForOAuthProviderTransition(page, config, startURL, deadline) {
  const timeoutAt = Date.now() + Math.min(10000, remaining(deadline));
  while (Date.now() < timeoutAt) {
    const currentURL = page.url?.() || '';
    if (oauthProviderTransitioned(currentURL, startURL, config)) return true;
    await page.waitForLoadState('domcontentloaded', { timeout: Math.min(3000, remaining(deadline)) }).catch(() => {});
    await page.waitForTimeout(500);
  }
  return false;
}

function oauthProviderTransitioned(currentURL, startURL, config) {
  if (!currentURL || currentURL === startURL) return false;
  if (!sameOrigin(currentURL, startURL)) return true;
  if (!sameOrigin(currentURL, config.authUrl)) return true;
  try {
    const current = new URL(currentURL);
    return /\/api\/oauth\/callback|\/authorize/i.test(current.pathname) || current.searchParams.has('state');
  } catch {
    return false;
  }
}

async function dismissTinyAuthInvalidDomainWarning(page, config, deadline) {
  await ensureTimeRemaining(deadline, 'TinyAuth local domain warning');
  if (!isLocalPortMappedOrigin(page.url(), config.authUrl)) return false;

  const text = await pageText(page).catch(() => '');
  const currentOrigin = originOf(page.url());
  const expectedOrigin = originOf(config.authUrl);
  if (
    !/invalid\s+domain/i.test(text) ||
    !text.includes(expectedOrigin) ||
    !text.includes(currentOrigin)
  ) {
    return false;
  }

  const clicked = await clickFirst(page, [/^ignore$/i]);
  if (!clicked) return false;
  await page.waitForLoadState('domcontentloaded', { timeout: Math.min(3000, remaining(deadline)) }).catch(() => {});
  await page.waitForTimeout(500);
  return true;
}

function isLocalPortMappedOrigin(currentURL, expectedURL) {
  try {
    const current = new URL(String(currentURL || ''));
    const expected = new URL(String(expectedURL || ''));
    if (!isLocalEvidenceHostname(expected.hostname)) return false;
    if (current.protocol !== expected.protocol || current.hostname !== expected.hostname) return false;
    return effectivePort(current) !== effectivePort(expected);
  } catch {
    return false;
  }
}

function originOf(value) {
  try {
    return new URL(String(value || '')).origin;
  } catch {
    return '';
  }
}

function sameOrigin(a, b) {
  const left = originOf(a);
  const right = originOf(b);
  return left && right && left === right;
}

function isLocalEvidenceHostname(hostname) {
  const host = String(hostname || '').toLowerCase();
  return host === 'localhost' || host.endsWith('.localhost') || host === '127.0.0.1' || host === '::1';
}

function effectivePort(url) {
  if (url.port) return url.port;
  if (url.protocol === 'https:') return '443';
  if (url.protocol === 'http:') return '80';
  return '';
}

async function fillOwnerFields(page, config) {
  await fillFirstVisible(page, [
    'input[type="email"]',
    'input[name="email"]',
    'input[id*="email" i]',
    'input[autocomplete="email"]',
  ], config.ownerEmail);
  await fillFirstVisible(page, [
    'input[name="username"]',
    'input[id*="username" i]',
    'input[autocomplete="username"]',
  ], config.ownerUsername);
  await fillFirstVisible(page, [
    'input[name="displayName"]',
    'input[name="display_name"]',
    'input[id*="display" i]',
    'input[name="name"]',
  ], config.ownerDisplayName);
  await fillFirstVisible(page, [
    'input[name="firstName"]',
    'input[name="first_name"]',
    'input[id*="first" i]',
  ], config.ownerDisplayName.split(/\s+/)[0] || config.ownerUsername);
  await fillFirstVisible(page, [
    'input[name="lastName"]',
    'input[name="last_name"]',
    'input[id*="last" i]',
  ], config.ownerDisplayName.split(/\s+/).slice(1).join(' ') || 'Owner');
}

async function fillFirstVisible(page, selectors, value) {
  const text = String(value || '').trim();
  if (!text) return false;
  for (const selector of selectors) {
    const locator = page.locator(selector).first();
    if ((await locator.count().catch(() => 0)) === 0) continue;
    if (!(await locator.isVisible({ timeout: 250 }).catch(() => false))) continue;
    try {
      await locator.fill(text, { timeout: 1000 });
      return true;
    } catch {
      // Try the next selector.
    }
  }
  return false;
}

async function clickThrough(page, patterns, deadline) {
  let idlePolls = 0;
  for (let i = 0; i < 12; i += 1) {
    await ensureTimeRemaining(deadline, 'browser interaction');
    const clicked = await clickFirst(page, patterns);
    if (!clicked) {
      idlePolls += 1;
      if (idlePolls >= 6) break;
      await page.waitForLoadState('domcontentloaded', { timeout: Math.min(3000, remaining(deadline)) }).catch(() => {});
      await page.waitForTimeout(500);
      continue;
    }
    idlePolls = 0;
    await page.waitForLoadState('domcontentloaded', { timeout: Math.min(3000, remaining(deadline)) }).catch(() => {});
    await page.waitForTimeout(500);
  }
}

async function clickFirst(page, patterns) {
  for (const pattern of patterns) {
    for (const role of ['button', 'link']) {
      const locator = page.getByRole(role, { name: pattern }).first();
      if ((await locator.count().catch(() => 0)) > 0 && (await locator.isVisible({ timeout: 250 }).catch(() => false))) {
        try {
          await locator.click({ timeout: 1000 });
          return true;
        } catch {
          // Try the next matching control.
        }
      }
    }
    const textLocator = page.locator('button, a, input[type="submit"]').filter({ hasText: pattern }).first();
    if ((await textLocator.count().catch(() => 0)) > 0 && (await textLocator.isVisible({ timeout: 250 }).catch(() => false))) {
      try {
        await textLocator.click({ timeout: 1000 });
        return true;
      } catch {
        // Try the next pattern.
      }
    }
  }
  return false;
}

async function waitForExpectedText(page, check, deadline) {
  let lastText = '';
  while (Date.now() < deadline) {
    try {
      lastText = await pageText(page);
    } catch (error) {
      if (!isTransientNavigationError(error)) throw error;
      await page.waitForLoadState('domcontentloaded', { timeout: Math.min(3000, remaining(deadline)) }).catch(() => {});
      await page.waitForTimeout(500);
      continue;
    }
    if (checkTextMatches(check, lastText)) {
      return lastText;
    }
    await page.waitForTimeout(500);
  }
  throw new Error(`timed out waiting for ${check.expectedText}`);
}

async function pageText(page) {
  return page.evaluate(() => document.body?.innerText || document.documentElement?.innerText || '');
}

function isTransientNavigationError(error) {
  const message = String(error?.message || error || '');
  return /execution context was destroyed/i.test(message) ||
    /most likely because of a navigation/i.test(message) ||
    /navigation failed because page was closed/i.test(message) ||
    /target page, context or browser has been closed/i.test(message);
}

function checkTextMatches(check, text) {
  const value = String(text || '');
  const requiredPatterns = check.requiredPatterns || [];
  const expectedPatterns = check.expectedPatterns || [];
  return requiredPatterns.every((pattern) => pattern.test(value)) &&
    (expectedPatterns.length === 0 || expectedPatterns.some((pattern) => pattern.test(value)));
}

async function verifyCheckEvidence(page, config, check, observedText, deadline, runtime = {}) {
  if (check.evidencePolicy === 'pocketid-passkey-credential') {
    return verifyOwnerPasskeyCredential(runtime.webAuthn);
  }
  if (check.evidencePolicy === 'tinyauth-owner-session') {
    return verifyTinyAuthOwnerSession(page, config, observedText, deadline);
  }
  if (check.evidencePolicy === 'cloudreve-demo-file') {
    const demoEnabled = config.demoData !== 'disabled';
    if (demoEnabled && (!/stackkit demo/i.test(observedText) || !/readme\.txt/i.test(observedText))) {
      throw new Error('Files demo evidence did not show StackKit Demo/README.txt');
    }
    return verifyCloudreveDemoFile(page, deadline, demoEnabled);
  }
  if (check.evidencePolicy === 'immich-demo-assets') {
    return verifyImmichDemoAssets(page, deadline, config.ownerEmail, config.demoData !== 'disabled');
  }
  if (check.evidencePolicy === 'vault-auth-boundary') {
    return verifyVaultAuthBoundary(page, config, check, deadline);
  }
  return {};
}

async function verifyTinyAuthOwnerSession(page, config, observedText, deadline) {
  let lastError = '';
  while (Date.now() < deadline) {
    const sessionURL = page.url() || config.authUrl;
    const cookieURLs = uniqueHTTPURLs([config.authUrl, config.browserUrl, sessionURL]);
    const cookies = await page.context().cookies(cookieURLs);
    const sessionCookies = cookies.filter((cookie) => isTinyAuthSessionCookie(cookie, config.authUrl));
    const forwardAuth = await probeTinyAuthForwardAuth(page, config, deadline);
    const signal = tinyAuthOwnerSessionSignal(sessionURL, observedText, forwardAuth);

    if (sessionCookies.length > 0 && forwardAuth.ok && signal) {
      return {
        verification: 'tinyauth-forwardauth-session',
        authBoundary: 'tinyauth-pocketid',
        ownerSessionSignal: signal,
        sessionUrl: sessionURL,
        authUrl: config.authUrl,
        forwardAuthEndpoint: forwardAuth.url,
        forwardAuthProbe: forwardAuth.probe || 'browser-context-request',
        forwardAuthStatus: String(forwardAuth.status),
        sessionCookieCount: String(sessionCookies.length),
        sessionCookieNames: uniqueSorted(sessionCookies.map((cookie) => cookie.name)).join(','),
        sessionCookieDomains: uniqueSorted(sessionCookies.map((cookie) => cookie.domain)).join(','),
      };
    }

    lastError = [
      sessionCookies.length > 0 ? '' : 'no TinyAuth-like session cookie was present',
      forwardAuth.ok ? '' : `forwardauth probe failed${forwardAuth.status ? ` with HTTP ${forwardAuth.status}` : ''}${forwardAuth.error ? `: ${forwardAuth.error}` : ''}`,
      signal ? '' : 'no authenticated TinyAuth Owner-session signal was present',
    ].filter(Boolean).join('; ');
    await page.waitForTimeout(500);
  }
  throw new Error(lastError || 'TinyAuth Owner session was not provable from the browser context');
}

async function probeTinyAuthForwardAuth(page, config, deadline) {
  const endpoints = ['/api/auth/traefik', '/api/auth'];
  let last = { ok: false, status: 0, url: '', error: '' };
  for (const endpoint of endpoints) {
    await ensureTimeRemaining(deadline, `TinyAuth ${endpoint}`);
    const url = new URL(endpoint, config.authUrl).toString();
    const requestURL = mapLocalPortURL(config, url);
    try {
      const browserURL = new URL(config.browserUrl);
      const cookieHeader = await browserContextCookieHeader(page.context(), [config.authUrl, url]);
      if (config.freshVMContainerName) {
        const direct = await probeTinyAuthForwardAuthViaFreshVM(config, endpoint, cookieHeader, deadline);
        last = direct;
        if (direct.ok) return direct;
      }
      const response = await page.context().request.get(requestURL, {
        timeout: Math.min(30000, remaining(deadline)),
        maxRedirects: 0,
        headers: localPortMappedRequestHeaders(config, url, {
          'X-Forwarded-Host': browserURL.host,
          'X-Forwarded-Proto': browserURL.protocol.replace(':', ''),
          'X-Forwarded-Uri': '/',
          ...(cookieHeader ? { Cookie: cookieHeader } : {}),
        }),
      });
      last = { ok: response.ok(), status: response.status(), url, error: '', probe: 'browser-context-request' };
      if (response.ok()) return last;
    } catch (error) {
      last = { ok: false, status: 0, url, error: String(error?.message || error).slice(0, 300), probe: 'browser-context-request' };
    }
  }
  return last;
}

async function probeTinyAuthForwardAuthViaFreshVM(
  config,
  endpoint,
  cookieHeader,
  deadline,
  execFileImpl = execFileAsync,
) {
  const url = new URL(endpoint, config.authUrl).toString();
  if (!String(cookieHeader || '').trim()) {
    return {
      ok: false,
      status: 0,
      url,
      error: 'missing browser Cookie header for direct TinyAuth ForwardAuth probe',
      probe: 'fresh-vm-container',
    };
  }
  await ensureTimeRemaining(deadline, `TinyAuth ${endpoint} direct fresh VM probe`);
  const authURL = new URL(config.authUrl);
  const browserURL = new URL(config.browserUrl);
  const timeout = Math.min(30000, remaining(deadline));
  const args = [
    'exec',
    config.freshVMContainerName,
    'docker',
    'exec',
    'tinyauth',
    'wget',
    '-S',
    '-O',
    '-',
    `--header=Host: ${authURL.host}`,
    `--header=X-Forwarded-Host: ${browserURL.host}`,
    `--header=X-Forwarded-Proto: ${browserURL.protocol.replace(':', '')}`,
    '--header=X-Forwarded-Uri: /',
    `--header=Cookie: ${cookieHeader}`,
    `http://127.0.0.1:3000${endpoint}`,
  ];
  try {
    const result = await execFileImpl('docker', args, {
      timeout,
      maxBuffer: 1024 * 1024,
      windowsHide: true,
    });
    const status = parseHTTPStatus(`${result?.stderr || ''}\n${result?.stdout || ''}`) || 200;
    return {
      ok: status >= 200 && status < 300,
      status,
      url,
      error: '',
      probe: 'fresh-vm-container',
    };
  } catch (error) {
    const output = `${error?.stderr || ''}\n${error?.stdout || ''}`;
    const status = parseHTTPStatus(output);
    return {
      ok: false,
      status,
      url,
      error: String(error?.message || error).slice(0, 300),
      probe: 'fresh-vm-container',
    };
  }
}

function parseHTTPStatus(output) {
  const matches = [...String(output || '').matchAll(/HTTP\/\S+\s+(\d{3})/g)];
  if (matches.length === 0) return 0;
  return Number(matches[matches.length - 1][1]) || 0;
}

async function browserContextCookieHeader(context, urls) {
  const cookies = await context.cookies(uniqueHTTPURLs(urls));
  return cookies
    .filter((cookie) => cookie?.name)
    .map((cookie) => `${cookie.name}=${cookie.value || ''}`)
    .join('; ');
}

function uniqueHTTPURLs(values) {
  const urls = [];
  const seen = new Set();
  for (const value of values) {
    try {
      const parsed = new URL(String(value || ''));
      if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') continue;
      const normalized = parsed.toString();
      if (!seen.has(normalized)) {
        seen.add(normalized);
        urls.push(normalized);
      }
    } catch {
      // Ignore non-URL values.
    }
  }
  return urls;
}

function isTinyAuthSessionCookie(cookie, authURL) {
  if (!cookie || typeof cookie !== 'object') return false;
  const name = String(cookie.name || '').toLowerCase();
  const domain = String(cookie.domain || '').toLowerCase().replace(/^\./, '');
  let authHost = '';
  try {
    authHost = new URL(authURL).hostname.toLowerCase();
  } catch {
    authHost = '';
  }
  const nameLooksTinyAuth = /tinyauth/.test(name);
  const nameLooksAuthSession = /auth|session/.test(name);
  const domainIsAuthHost = domain === authHost;
  const scopedToAuth = authHost && (domain === authHost || authHost.endsWith(`.${domain}`) || domain.endsWith(`.${rootDomain(authHost)}`));
  return scopedToAuth && (nameLooksTinyAuth || (domainIsAuthHost && nameLooksAuthSession));
}

function rootDomain(hostname) {
  const parts = String(hostname || '').split('.').filter(Boolean);
  if (parts.length <= 2) return parts.join('.');
  return parts.slice(-2).join('.');
}

function tinyAuthOwnerSessionSignal(url, text, forwardAuth) {
  if (forwardAuth?.ok) return 'forwardauth-2xx';
  const value = `${url || ''}\n${text || ''}`;
  if (/log\s*out|logout/i.test(value)) return 'logout';
  if (/signed\s*in|authenticated/i.test(value)) return 'signed-in';
  if (/\bowner\b/i.test(value)) return 'owner';
  return '';
}

function uniqueSorted(values) {
  return [...new Set(values.map((value) => String(value || '').trim()).filter(Boolean))].sort();
}

async function verifyOwnerPasskeyCredential(webAuthn) {
  if (!webAuthn?.enabled || !webAuthn.session || !webAuthn.authenticatorId) {
    throw new Error(`PocketID passkey evidence requires a Chromium WebAuthn virtual authenticator${webAuthn?.error ? `: ${webAuthn.error}` : ''}`);
  }
  const result = await webAuthn.session.send('WebAuthn.getCredentials', {
    authenticatorId: webAuthn.authenticatorId,
  });
  const credentials = Array.isArray(result?.credentials) ? result.credentials : [];
  if (credentials.length < 1) {
    throw new Error('PocketID Owner passkey setup did not create a WebAuthn credential in the virtual authenticator');
  }
  const residentCredentials = credentials.filter((credential) => credential?.isResidentCredential).length;
  const rpIds = [...new Set(credentials.map((credential) => String(credential?.rpId || '').trim()).filter(Boolean))];
  return {
    verification: 'webauthn-virtual-authenticator',
    authenticatorProtocol: webAuthn.protocol,
    authenticatorTransport: webAuthn.transport,
    passkeyCredentials: String(credentials.length),
    residentCredentials: String(residentCredentials),
    relyingParties: rpIds.join(','),
  };
}

async function verifyCloudreveDemoFile(page, deadline, demoEnabled = true) {
  let lastError = '';
  while (Date.now() < deadline) {
    const result = await page.evaluate(async ({ folderName, fileName, requireDemoFile }) => {
      function tokenFromSession(session) {
        if (!session || typeof session !== 'object') return '';
        return String(
          session?.token?.access_token ||
          session?.token?.accessToken ||
          session?.access_token ||
          session?.accessToken ||
          '',
        ).trim();
      }

      function stackkitBridgeMarker() {
        try {
          const marker = JSON.parse(window.localStorage.getItem('stackkit_files_session_bridge') || '{}');
          return marker && typeof marker === 'object' ? marker : {};
        } catch {
          return {};
        }
      }

      async function listFiles(token, uri) {
        const query = new URLSearchParams({ uri, page_size: '200' });
        const response = await fetch(`/api/v4/file?${query.toString()}`, {
          headers: { authorization: `Bearer ${token}` },
        });
        if (!response.ok) {
          throw new Error(`Cloudreve file list ${uri} returned HTTP ${response.status}`);
        }
        const body = await response.json();
        if (body.code && body.code !== 0) {
          throw new Error(`Cloudreve file list ${uri} returned code ${body.code}: ${body.msg || ''}`);
        }
        return body?.data?.files || body?.files || [];
      }

      let state;
      try {
        state = JSON.parse(window.localStorage.getItem('cloudreve_session') || '{}');
      } catch (error) {
        return { ok: false, reason: `Cloudreve browser session is not valid JSON: ${error.message}` };
      }
      const sessions = state && typeof state.sessions === 'object' && state.sessions ? state.sessions : {};
      const current = String(state.current || '').trim();
      const candidates = [];
      if (current && sessions[current]) {
        candidates.push([current, sessions[current]]);
      }
      for (const entry of Object.entries(sessions)) {
        if (!candidates.some(([id]) => id === entry[0])) {
          candidates.push(entry);
        }
      }
      if (candidates.length === 0) {
        return { ok: false, reason: 'Cloudreve browser session is missing from localStorage' };
      }
      const bridge = stackkitBridgeMarker();
      if (bridge.verification !== 'stackkit-cloudreve-session-bridge') {
        return { ok: false, reason: 'Cloudreve browser session did not come from the StackKit Files session bridge' };
      }
      const bridgeCurrent = String(bridge.current || '').trim();
      if (!bridgeCurrent) {
        return { ok: false, reason: 'StackKit Files session bridge marker does not name the Cloudreve user' };
      }
      if (current !== bridgeCurrent) {
        return { ok: false, reason: `Cloudreve current user ${current || '<missing>'} does not match StackKit bridge user ${bridgeCurrent}` };
      }
      if (!sessions[bridgeCurrent]) {
        return { ok: false, reason: `Cloudreve session ${bridgeCurrent} named by the StackKit bridge marker is missing` };
      }

      let lastFailure = '';
      for (const [userId, session] of [[bridgeCurrent, sessions[bridgeCurrent]]]) {
        const token = tokenFromSession(session);
        if (!token) {
          lastFailure = `Cloudreve session ${userId || '<unknown>'} has no access token`;
          continue;
        }
        try {
          const rootFiles = await listFiles(token, 'cloudreve://my');
          if (!requireDemoFile) {
            // Demo data is disabled: the authenticated root listing already
            // proves the bridge-created Cloudreve session against the API.
            return {
              ok: true,
              currentUser: userId || current || '<unknown>',
              verification: 'cloudreve-browser-session-api',
              bridgeVerification: bridge.verification,
              bridgeCurrentUser: String(bridge.current || ''),
            };
          }
          const folder = rootFiles.find((file) => Number(file.type) === 1 && file.name === folderName);
          if (!folder) {
            lastFailure = `Cloudreve session ${userId || '<unknown>'} did not list ${folderName}`;
            continue;
          }
          for (const folderURI of [`cloudreve://my/${encodeURIComponent(folderName)}`, `cloudreve://my/${folderName}`]) {
            const childFiles = await listFiles(token, folderURI);
            const readme = childFiles.find((file) => Number(file.type) === 0 && file.name === fileName);
            if (readme) {
              return {
                ok: true,
                currentUser: userId || current || '<unknown>',
                verification: 'cloudreve-browser-session-api',
                bridgeVerification: bridge.verification,
                bridgeCurrentUser: String(bridge.current || ''),
              };
            }
            lastFailure = `Cloudreve folder ${folderURI} did not list ${fileName}`;
          }
        } catch (error) {
          lastFailure = error.message;
        }
      }
      return { ok: false, reason: lastFailure || 'Cloudreve demo file was not found from the browser session' };
    }, { folderName: 'StackKit Demo', fileName: 'README.txt', requireDemoFile: demoEnabled });
    if (result.ok) {
      if (!demoEnabled) {
        return {
          demoData: 'disabled',
          demoContent: 'cloudreve-owner-session',
          verification: result.verification,
          identityBridge: 'stackkit-cloudreve-local-session',
          bridgeVerification: result.bridgeVerification,
          bridgeCurrentUser: result.bridgeCurrentUser,
          cloudreveSessionUser: String(result.currentUser || ''),
        };
      }
      return {
        demoData: 'enabled',
        demoContent: 'cloudreve-demo-file',
        seededFolder: 'StackKit Demo',
        seededFile: 'README.txt',
        verification: result.verification,
        identityBridge: 'stackkit-cloudreve-local-session',
        bridgeVerification: result.bridgeVerification,
        bridgeCurrentUser: result.bridgeCurrentUser,
        cloudreveSessionUser: String(result.currentUser || ''),
      };
    }
    lastError = result.reason || (demoEnabled
      ? 'Cloudreve demo file was not provable from the browser session'
      : 'Cloudreve bridge session was not provable from the browser session');
    await page.waitForTimeout(500);
  }
  throw new Error(lastError || 'Cloudreve browser session evidence was not provable');
}

async function verifyImmichDemoAssets(page, deadline, ownerEmail = '', demoEnabled = true) {
  let lastError = '';
  while (Date.now() < deadline) {
    let result;
    try {
      result = await page.evaluate(async ({ expectedOwnerEmail, demoDeviceId, demoDeviceAssetId, demoFileName, requireDemoAsset }) => {
      const tokenCandidates = [];
      const seen = new Set();
      const addToken = (value) => {
        if (typeof value !== 'string' || !value.trim()) return;
        const matches = value.match(/eyJ[A-Za-z0-9_-]+?\.[A-Za-z0-9_-]+?\.[A-Za-z0-9_-]+/g) || [];
        for (const token of matches) {
          if (!seen.has(token)) {
            seen.add(token);
            tokenCandidates.push(token);
          }
        }
      };
      const visitValue = (value, depth = 0) => {
        if (depth > 3 || value == null) return;
        if (typeof value === 'string') {
          addToken(value);
          const trimmed = value.trim();
          if ((trimmed.startsWith('{') && trimmed.endsWith('}')) || (trimmed.startsWith('[') && trimmed.endsWith(']'))) {
            try {
              visitValue(JSON.parse(trimmed), depth + 1);
            } catch {
              // Plain strings are fine; only JSON-shaped values are decoded.
            }
          }
          return;
        }
        if (Array.isArray(value)) {
          for (const item of value) visitValue(item, depth + 1);
          return;
        }
        if (typeof value === 'object') {
          for (const item of Object.values(value)) visitValue(item, depth + 1);
        }
      };
      for (const storage of [window.localStorage, window.sessionStorage]) {
        for (let index = 0; index < storage.length; index += 1) {
          const key = storage.key(index);
          if (!key) continue;
          visitValue(key);
          visitValue(storage.getItem(key));
        }
      }
      async function fetchJSON(url, options) {
        const response = await fetch(url, options);
        if (!response.ok) {
          throw new Error(`${url} returned HTTP ${response.status}`);
        }
        return response.json();
      }
      function assetItems(body) {
        if (Array.isArray(body?.assets?.items)) return body.assets.items;
        if (Array.isArray(body?.assets)) return body.assets;
        if (Array.isArray(body?.items)) return body.items;
        if (Array.isArray(body?.data?.assets?.items)) return body.data.assets.items;
        return [];
      }
      function matchesDemoAsset(asset) {
        return String(asset?.deviceId || '') === demoDeviceId &&
          String(asset?.deviceAssetId || '') === demoDeviceAssetId &&
          String(asset?.originalFileName || asset?.fileName || '') === demoFileName;
      }
      const expectedEmail = String(expectedOwnerEmail || '').trim().toLowerCase();
      let lastFailure = '';
      // Current Immich web sessions are HttpOnly-cookie based, so the
      // same-origin cookie session is a first-class auth mode beside any
      // JWT-shaped tokens still found in web storage.
      const authModes = [{ label: 'cookie-session', headers: {} }];
      for (const token of tokenCandidates) {
        authModes.push({ label: 'bearer-token', headers: { authorization: `Bearer ${token}` } });
      }
      for (const mode of authModes) {
        try {
          const me = await fetchJSON('/api/users/me', {
            headers: { ...mode.headers },
          });
          const sessionEmail = String(me?.email || '').trim();
          if (expectedEmail && sessionEmail.toLowerCase() !== expectedEmail) {
            lastFailure = `Immich browser session email ${sessionEmail || '<missing>'} did not match Owner ${expectedOwnerEmail}`;
            continue;
          }
          if (!requireDemoAsset) {
            return {
              ok: true,
              method: 'immich-users-me',
              sessionAuth: mode.label,
              ownerEmail: sessionEmail,
              ownerId: String(me?.id || ''),
            };
          }
          const body = await fetchJSON('/api/search/metadata', {
            method: 'POST',
            headers: {
              ...mode.headers,
              'content-type': 'application/json',
            },
            body: JSON.stringify({
              deviceId: demoDeviceId,
              deviceAssetId: demoDeviceAssetId,
              originalFileName: demoFileName,
            }),
          });
          const assets = assetItems(body);
          const demoAsset = assets.find(matchesDemoAsset);
          if (demoAsset) {
            return {
              ok: true,
              total: assets.length,
              method: 'immich-search-metadata',
              sessionAuth: mode.label,
              ownerEmail: sessionEmail,
              ownerId: String(me?.id || ''),
              demoAssetDeviceId: String(demoAsset.deviceId || ''),
              demoAssetDeviceAssetId: String(demoAsset.deviceAssetId || ''),
              demoAssetFile: String(demoAsset.originalFileName || demoAsset.fileName || ''),
            };
          }
          lastFailure = `${demoFileName} was not returned by /api/search/metadata`;
        } catch (error) {
          // Try the next browser auth mode.
          lastFailure = error.message;
        }
      }
      return {
        ok: false,
        tokenCandidates: tokenCandidates.length,
        reason: lastFailure,
        text: typeof document === 'undefined' ? '' : document.body?.innerText || document.documentElement?.innerText || '',
      };
      }, {
        expectedOwnerEmail: ownerEmail,
        demoDeviceId: 'stackkit-demo',
        demoDeviceAssetId: 'stackkit-demo-photo-1',
        demoFileName: 'stackkit-demo-photo.png',
        requireDemoAsset: demoEnabled,
      });
    } catch (error) {
      if (!isTransientNavigationError(error)) throw error;
      lastError = `Immich browser session was still navigating: ${error.message || error}`;
      await page.waitForLoadState('domcontentloaded', { timeout: Math.min(3000, remaining(deadline)) }).catch(() => {});
      await page.waitForTimeout(500);
      continue;
    }
    if (result.ok) {
      if (!demoEnabled) {
        return {
          demoData: 'disabled',
          demoContent: 'immich-owner-session',
          verification: result.method,
          ownerVerification: 'immich-users-me',
          immichSessionAuth: String(result.sessionAuth || ''),
          immichOwnerEmail: String(result.ownerEmail || ''),
          immichOwnerId: String(result.ownerId || ''),
        };
      }
      return {
        demoData: 'enabled',
        demoContent: 'immich-demo-assets',
        immichDemoAssets: String(result.total),
        verification: result.method,
        ownerVerification: 'immich-users-me',
        immichSessionAuth: String(result.sessionAuth || ''),
        immichOwnerEmail: String(result.ownerEmail || ''),
        immichOwnerId: String(result.ownerId || ''),
        demoAssetDeviceId: String(result.demoAssetDeviceId || ''),
        demoAssetDeviceAssetId: String(result.demoAssetDeviceAssetId || ''),
        demoAssetFile: String(result.demoAssetFile || ''),
      };
    }
    lastError = demoEnabled
      ? `Immich StackKit demo asset was not provable from the Owner browser session (tokenCandidates=${result.tokenCandidates || 0}${result.reason ? `, lastError=${result.reason}` : ''})`
      : `Immich Owner browser session was not provable through /api/users/me (tokenCandidates=${result.tokenCandidates || 0}${result.reason ? `, lastError=${result.reason}` : ''})`;
    await page.waitForTimeout(500);
  }
  throw new Error(lastError || 'Immich Owner browser session evidence was not provable');
}

async function verifyVaultAuthBoundary(page, config, check, deadline) {
  const browser = page.context().browser();
  if (!browser) {
    throw new Error('Vault auth-boundary proof requires a browser-backed Playwright context');
  }

  let context;
  try {
    context = await browser.newContext({
      ignoreHTTPSErrors: true,
      viewport: DEFAULT_VIEWPORT,
    });
    // The anonymous context needs the same local-port bridge as the Owner
    // context, otherwise Docker-allocated Fresh-VM ports make the canonical
    // vault URL unreachable instead of proving the auth boundary.
    await installLocalPortMappingRoutes(context, config);
    const anonymousPage = await context.newPage();
    const response = await anonymousPage.goto(check.url, {
      waitUntil: 'domcontentloaded',
      timeout: remaining(deadline),
    });
    await anonymousPage.waitForLoadState('networkidle', { timeout: Math.min(5000, remaining(deadline)) }).catch(() => {});
    const anonymousURL = anonymousPage.url() || check.url;
    const anonymousStatus = response?.status?.() || 0;
    const anonymousText = await pageText(anonymousPage).catch(() => '');
    const boundarySignal = vaultBoundarySignal(anonymousURL, anonymousText, anonymousStatus);
    if (!boundarySignal) {
      throw new Error('Vault anonymous route did not show TinyAuth/PocketID boundary or an HTTP 401/403 rejection');
    }
    return {
      verification: 'anonymous-vault-route-check',
      authBoundary: 'tinyauth-pocketid',
      anonymousAccess: 'rejected',
      anonymousStatus: String(anonymousStatus),
      anonymousUrl: anonymousURL,
      anonymousBoundarySignal: boundarySignal,
    };
  } finally {
    if (context) await context.close().catch(() => {});
  }
}

function vaultBoundarySignal(url, text, status) {
  const value = `${url || ''}\n${text || ''}`;
  if (Number(status) === 401) return 'http-401';
  if (Number(status) === 403) return 'http-403';
  if (/tinyauth/i.test(value)) return 'tinyauth';
  if (/pocket.?id/i.test(value)) return 'pocketid';
  try {
    const parsed = new URL(String(url || ''));
    const host = parsed.hostname.toLowerCase();
    if (host.startsWith('auth.') || host.startsWith('id.')) return 'auth-host';
  } catch {
    // Non-URL values cannot be host-boundary evidence.
  }
  return '';
}

function normalizeObservedText(text) {
  return String(text || '').replace(/\s+/g, ' ').trim().slice(0, 500);
}

function ownerUsernameFromEmail(email) {
  return normalizeOwnerUsername(String(email || '').split('@')[0] || 'owner');
}

function ownerDisplayNameFromUsername(username) {
  const value = normalizeOwnerUsername(username);
  if (value === 'admin') return 'StackKit Owner';
  return value || 'owner';
}

function normalizeOwnerUsername(value) {
  return String(value || '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/^-+|-+$/g, '') || 'owner';
}

async function ensureTimeRemaining(deadline, label) {
  if (remaining(deadline) <= 0) {
    throw new Error(`${label} exceeded browser evidence time budget`);
  }
}

function remaining(deadline) {
  return Math.max(1, deadline - Date.now());
}

async function writeEvidence(config, evidence) {
  await mkdir(path.dirname(config.output), { recursive: true });
  await writeFile(config.output, `${JSON.stringify(evidence, null, 2)}\n`);
}

async function writeAndRethrow(config, evidence, error) {
  await writeEvidence(config, evidence).catch(() => {});
  return error;
}

async function main() {
  const config = parseArgs(process.argv.slice(2));
  if (config.help) {
    console.log(usage());
    return;
  }
  await run(config);
  console.log(`Browser evidence written to ${config.output}`);
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  main().catch((error) => {
    console.error(error?.message || error);
    process.exit(1);
  });
}

export {
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
  collectBrowserRuntimeDiagnostics,
  cookieFromSetCookieHeader,
  defaultConfig,
  dismissTinyAuthInvalidDomainWarning,
  driveOwnerLoginFlow,
  loadPlaywright,
  mapLocalPortURL,
  mergeSetupActionDiagnostics,
  normalizeOwnerUsername,
  normalizeBrowserChannel,
  ownerDisplayNameFromUsername,
  ownerUsernameFromEmail,
  parseArgs,
  parseHTTPStatus,
  probeTinyAuthForwardAuthViaFreshVM,
  relativeEvidencePath,
  returnToEvidenceRoute,
  runOwnerActivatedSetupActions,
  unmapLocalPortURL,
  usage,
  verifyCloudreveDemoFile,
  verifyImmichDemoAssets,
  verifyOwnerPasskeyCredential,
  verifyTinyAuthOwnerSession,
  verifyVaultAuthBoundary,
  withLocalPortMappedBrowserOrigins,
};
