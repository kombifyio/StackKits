#!/usr/bin/env node
import { createHash } from 'node:crypto';
import { readFile, readdir, stat, writeFile } from 'node:fs/promises';
import { createReadStream } from 'node:fs';
import path from 'node:path';

const VALID_STATUS = new Set(['pass', 'fail', 'pending', 'not_applicable']);
const PUBLIC_SK_S1_PENDING_SUMMARY =
  'SK-S1 public product evidence is BLOCKED until an authorized leased Proxmox product run supplies separate provenance; Candidate and compatibility evidence do not substitute.';
const PUBLIC_SK_S1_PENDING_GATE =
  'SK-S1 public product evidence is BLOCKED pending an authorized leased Proxmox product run.';
const PUBLIC_BROWSER_PENDING_SUMMARY =
  'SK-S1 browser evidence is BLOCKED until it is produced by the separately authorized leased Proxmox product run.';

const LEGACY_V04_MISSING_ALTERNATIVES = [
  {
    service: 'Photos',
    message:
      'Photos has no accepted v0.4 alternative yet; Immich remains the beta default and the gap is release-blocking unless documented as a beta limitation.',
  },
  {
    service: 'Vault',
    message:
      'Vault has no accepted v0.4 alternative yet; Vaultwarden remains the beta default and the gap is release-blocking unless documented as a beta limitation.',
  },
];

const CURRENT_MISSING_ALTERNATIVES = [
  {
    service: 'Photos',
    message:
      'Photos currently uses Immich as its supported default; no additional Photos alternative is claimed or verified by this release.',
  },
  {
    service: 'Vault',
    message:
      'Vault currently uses Vaultwarden as its supported default; no additional Vault alternative is claimed or verified by this release.',
  },
];

const LEGACY_V04_KNOWN_LIMITATIONS = [
  'v0.4 browser evidence still must prove PocketID/passkey Owner login, TinyAuth ForwardAuth session acceptance, and default L3 app content; Immich StackKit demo photo and Cloudreve StackKit Demo/README.txt need live browser proof.',
];

const CURRENT_KNOWN_LIMITATIONS = [
  'Live browser evidence still must prove PocketID/passkey Owner login, TinyAuth ForwardAuth session acceptance, and default L3 app content before those support claims are marked verified.',
];

const LEGACY_V04_DEFAULT_KNOWN_LIMITATIONS = [
  'v0.4 is a BaseKit beta-hardening release and does not claim production readiness.',
  'Unreleased kit definitions remain out of v0.4 scope.',
  'Dokploy remains draft/non-beta until its full bootstrap path has evidence.',
];

const CURRENT_DEFAULT_KNOWN_LIMITATIONS = [
  'Architecture v2 is a governed contract checkpoint; product v2 generation and apply remain fail-closed until concrete typed renderers and kit-specific owner/module realizations are complete.',
  'Modern Homelab is included as a public Preview definition but remains excluded from the supported live runtime until its concrete federation bridge, edge/verifier, TLS, health, and multi-site evidence exist.',
  'Dokploy remains draft/non-beta until its full bootstrap path has evidence.',
];

const REQUIRED_BROWSER_EVIDENCE_CHECKS = [
  'pocketid-owner-passkey',
  'tinyauth-owner-session',
  'photos-demo-content',
  'files-demo-content',
  'vault-auth-boundary',
];

const REQUIRED_BROWSER_CHECK_ROUTES = {
  'pocketid-owner-passkey': { host: 'id.home.localhost', paths: ['/setup', '/settings/account'] },
  'tinyauth-owner-session': { host: 'auth.home.localhost', paths: ['/', '/logout'] },
  'photos-demo-content': { host: 'photos.home.localhost', paths: ['/photos'] },
  'files-demo-content': { host: 'files.home.localhost', paths: ['/stackkit/files/session', '/home'] },
  'vault-auth-boundary': { host: 'vault.home.localhost', paths: ['/'] },
};

const MAX_BROWSER_CHECK_DURATION_SECONDS = 15 * 60;
const MIN_BROWSER_SCREENSHOT_WIDTH = 320;
const MIN_BROWSER_SCREENSHOT_HEIGHT = 240;

const REQUIRED_BROWSER_PREFLIGHT_CHECKS = [
  'Required command: go',
  'Required command: node',
  'Required command: npm',
  'Required command: docker',
  'Docker Desktop availability',
  'Docker Desktop context',
  'Install isolated Playwright package',
  'Install isolated Playwright Chromium',
  'Playwright package availability',
  'Playwright Chromium availability',
];

const REQUIRED_SETUP_DROPS = [
  'kuma-platform-bootstrap',
  'cloudreve-owner-bootstrap',
  'vaultwarden-admin-handoff',
  'immich-owner-bootstrap',
];

const REQUIRED_OWNER_SETUP_SERVICES = [
  'photos',
  'files',
  'vault',
];

const REQUIRED_OWNER_SETUP_DROPS_BY_SERVICE = {
  photos: 'immich-owner-bootstrap',
  files: 'cloudreve-owner-bootstrap',
  vault: 'vaultwarden-admin-handoff',
};

const CANONICAL_SCENARIOS = [
  {
    id: 'SK-S1',
    label: 'SK-S1 local no-mail Coolify beta',
    pendingSummary: PUBLIC_SK_S1_PENDING_SUMMARY,
    pendingGate: PUBLIC_SK_S1_PENDING_GATE,
  },
  {
    id: 'SK-S2',
    label: 'SK-S2 kombify.me cloud-owner Komodo beta',
    pendingSummary: 'SK-S2 kombify.me cloud-owner Komodo beta scenario artifact has not been attached to release evidence.',
    pendingGate: 'SK-S2 kombify.me cloud-owner Komodo beta scenario is pending released-archive evidence.',
  },
  {
    id: 'SK-S3',
    label: 'SK-S3 custom-domain explicit-owner Coolify beta',
    pendingSummary: 'SK-S3 custom-domain explicit-owner Coolify beta scenario artifact has not been attached to release evidence.',
    pendingGate: 'SK-S3 custom-domain explicit-owner Coolify beta scenario is pending released-archive evidence.',
  },
  {
    id: 'SK-S5',
    label: 'SK-S5 missing-mail negative',
    pendingSummary: 'SK-S5 missing-mail negative scenario artifact has not been attached to release evidence.',
    pendingGate: 'SK-S5 missing-mail negative scenario is pending released-archive evidence.',
  },
  {
    id: 'SK-S6',
    label: 'SK-S6 HA backup release-readiness',
    defaultStatus: 'not_applicable',
    pendingSummary:
      'SK-S6 HA backup release-readiness is not applicable to this BaseKit-only release evidence unless HA or multi-server backup readiness is claimed.',
    pendingGate: 'SK-S6 HA backup release-readiness contract evidence is pending.',
  },
];
const SECURITY_BASELINE_SCENARIOS = new Set(['SK-S1', 'SK-S2', 'SK-S3']);
const SECURITY_BASELINE_SCHEMA_VERSION = 'stackkit.security-baseline/v1';
const SECURITY_BASELINE_MODE = 'public-beta';
const REQUIRED_SECURITY_BASELINE_CONTROLS = new Map([
  ['firewall', 'enabled'],
  ['sshPasswordAuthentication', 'disabled'],
  ['fail2ban', 'enabled'],
  ['unattendedUpgrades', 'security'],
  ['sysctl', 'applied'],
]);

const ALLOWED_BROWSER_FAILURE_PHASES = new Set([
  'wrapper',
  'command-preflight',
  'browser-preflight',
  'fresh-vm-rollout',
  'setup-state-export',
  'homelab-artifact',
  'browser-capture',
  'manifest-validation',
]);

function parseArgs(argv) {
  const opts = {
    checks: new Map(),
    knownLimitations: [],
    missingAlternatives: [],
    pendingGates: [],
    scenarioArtifacts: [],
    scenarioEvidence: [],
    browserEvidence: '',
    browserPreflight: '',
    browserEvidenceRoot: process.cwd(),
    dist: 'dist',
    output: 'dist/release-evidence.json',
    visibility: 'unknown',
  };
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    const next = argv[i + 1];
    switch (arg) {
      case '--tag':
        opts.tag = next;
        i += 1;
        break;
      case '--commit':
        opts.commit = next;
        i += 1;
        break;
      case '--source-repo':
        opts.sourceRepository = next;
        i += 1;
        break;
      case '--release-repo':
        opts.releaseRepository = next;
        i += 1;
        break;
      case '--visibility':
        opts.visibility = next;
        i += 1;
        break;
      case '--release-url':
        opts.releaseUrl = next;
        i += 1;
        break;
      case '--workflow-run-id':
        opts.workflowRunId = next;
        i += 1;
        break;
      case '--workflow-run-url':
        opts.workflowRunUrl = next;
        i += 1;
        break;
      case '--dist':
        opts.dist = next;
        i += 1;
        break;
      case '--output':
        opts.output = next;
        i += 1;
        break;
      case '--check':
        addCheck(opts.checks, next);
        i += 1;
        break;
      case '--known-limitation':
        opts.knownLimitations.push(next);
        i += 1;
        break;
      case '--missing-alternative':
        opts.missingAlternatives.push(next);
        i += 1;
        break;
      case '--pending-gate':
        opts.pendingGates.push(next);
        i += 1;
        break;
      case '--scenario-evidence':
        opts.scenarioEvidence.push(parseScenarioEvidence(next));
        i += 1;
        break;
      case '--scenario-artifact':
        opts.scenarioArtifacts.push(next);
        i += 1;
        break;
      case '--browser-evidence':
        opts.browserEvidence = next;
        i += 1;
        break;
      case '--browser-preflight':
        opts.browserPreflight = next;
        i += 1;
        break;
      case '--browser-evidence-root':
        opts.browserEvidenceRoot = next;
        i += 1;
        break;
      default:
        throw new Error(`unknown argument: ${arg}`);
    }
  }
  return opts;
}

function addCheck(checks, value) {
  if (!value || !value.includes('=')) {
    throw new Error('--check expects name=status[,summary]');
  }
  const [name, rawRest] = value.split('=', 2);
  const [status, ...summaryParts] = rawRest.split(',');
  if (!VALID_STATUS.has(status)) {
    throw new Error(`invalid status for ${name}: ${status}`);
  }
  checks.set(name, {
    status,
    ...(summaryParts.length ? { summary: summaryParts.join(',').trim() } : {}),
  });
}

function parseScenarioEvidence(value) {
  if (!value || !value.includes('=')) {
    throw new Error('--scenario-evidence expects scenarioId=status[,summary]');
  }
  const [scenarioId, rawRest] = value.split('=', 2);
  const [status, ...summaryParts] = rawRest.split(',');
  if (!VALID_STATUS.has(status)) {
    throw new Error(`invalid status for ${scenarioId}: ${status}`);
  }
  return {
    scenarioId,
    status,
    ...(summaryParts.length ? { summary: summaryParts.join(',').trim() } : {}),
  };
}

function normalizeScenarioArtifactStatus(value) {
  const status = String(value || '').trim().toLowerCase();
  if (['pass', 'passed', 'success', 'succeeded'].includes(status)) return 'pass';
  if (['fail', 'failed', 'failure', 'error'].includes(status)) return 'fail';
  if (status === 'pending') return 'pending';
  if (status === 'not_applicable') return 'not_applicable';
  return '';
}

async function loadScenarioArtifactEvidence(artifactPath) {
  let artifact;
  try {
    artifact = JSON.parse(await readFile(artifactPath, 'utf8'));
  } catch (error) {
    return {
      scenarioId: 'unknown',
      status: 'fail',
      summary: `scenario artifact is unreadable; ${error.code || error.message}.`,
      url: artifactPath,
    };
  }

  const scenarioId = String(artifact.scenarioId || '').trim() || 'unknown';
  const status = normalizeScenarioArtifactStatus(artifact.status);
  const simulation = artifact.simulation || {};
  const expectedHealthChecks = Array.isArray(simulation.healthChecks)
    ? simulation.healthChecks.map((item) => String(item || '').trim()).filter(Boolean)
    : [];
  const expectedSetupActions = Array.isArray(simulation.setupActions)
    ? simulation.setupActions.map((item) => String(item || '').trim()).filter(Boolean)
    : [];
  const simulationStatus = artifact.simulationStatus;
  const platformAppRefCount = [
    ...(Array.isArray(artifact.platformSystemApps) ? artifact.platformSystemApps : []),
    ...(Array.isArray(artifact.platformApps) ? artifact.platformApps : []),
  ].filter((app) => String(app?.externalId || '').trim()).length;
  const issues = [];
  if (scenarioId === 'unknown') {
    issues.push('scenarioId is missing');
  }
  if (!status) {
    issues.push(`status is ${artifact.status || 'missing'}`);
  }
  if (!String(artifact.runId || '').trim()) {
    issues.push('runId is missing');
  }
  if (expectedHealthChecks.length > 0 || expectedSetupActions.length > 0) {
    if (!simulationStatus || typeof simulationStatus !== 'object' || Array.isArray(simulationStatus)) {
      issues.push('simulationStatus is missing');
    } else {
      const simulationStatusValue = String(simulationStatus.status || '').trim();
      const observedSetupActions = Array.isArray(simulationStatus.observedSetupActions)
        ? simulationStatus.observedSetupActions.map((item) => String(item || '').trim()).filter(Boolean)
        : [];
      const missingSetupActions = Array.isArray(simulationStatus.missingSetupActions)
        ? simulationStatus.missingSetupActions.map((item) => String(item || '').trim()).filter(Boolean)
        : [];
      const observedHealthChecks = Array.isArray(simulationStatus.observedHealthChecks)
        ? simulationStatus.observedHealthChecks.map((item) => String(item || '').trim()).filter(Boolean)
        : [];
      const missingHealthChecks = Array.isArray(simulationStatus.missingHealthChecks)
        ? simulationStatus.missingHealthChecks.map((item) => String(item || '').trim()).filter(Boolean)
        : [];
      const onDemandSetupActions = onDemandSetupActionEvidence(artifact);
      const acceptedSetupActions = new Set([...observedSetupActions, ...onDemandSetupActions]);
      const blockingMissingSetupActions = missingSetupActions.filter((action) => !onDemandSetupActions.has(action));
      const unobservedSetupActions = expectedSetupActions.filter((action) => !acceptedSetupActions.has(action));
      const observed = new Set(observedHealthChecks);
      const unobserved = expectedHealthChecks.filter((check) => !observed.has(check));
      const incompleteOnlyForOnDemandSetup =
        simulationStatusValue === 'incomplete' &&
        blockingMissingSetupActions.length === 0 &&
        missingHealthChecks.length === 0 &&
        unobservedSetupActions.length === 0 &&
        unobserved.length === 0;
      if (simulationStatusValue !== 'pass' && !incompleteOnlyForOnDemandSetup) {
        issues.push(`simulationStatus is ${simulationStatusValue || 'missing'}`);
      }
      if (blockingMissingSetupActions.length > 0) {
        issues.push(`missingSetupActions=${blockingMissingSetupActions.join(',')}`);
      }
      if (missingHealthChecks.length > 0) {
        issues.push(`missingHealthChecks=${missingHealthChecks.join(',')}`);
      }
      if (unobservedSetupActions.length > 0) {
        issues.push(`observedSetupActions missing ${unobservedSetupActions.join(',')}`);
      }
      if (unobserved.length > 0) {
        issues.push(`observedHealthChecks missing ${unobserved.join(',')}`);
      }
    }
  }
  issues.push(...scenarioSecurityBaselineIssues(scenarioId, artifact.securityBaseline));

  const passed = status === 'pass' && issues.length === 0;
  const securityBaselineSummary = SECURITY_BASELINE_SCENARIOS.has(scenarioId) ? ', and security baseline evidence' : '';
  return {
    scenarioId,
    source: 'homelab-artifact',
    status: passed ? 'pass' : 'fail',
    summary: passed
      ? `${scenarioId} Homelab artifact passed with ${expectedHealthChecks.length} observed simulation health checks, ${expectedSetupActions.length} setup action evidence entries, ${platformAppRefCount} platform app refs${securityBaselineSummary}.`
      : `${scenarioId} Homelab artifact is incomplete or failing; ${issues.join('; ')}.`,
    url: artifactPath,
  };
}

function scenarioSecurityBaselineIssues(scenarioId, securityBaseline) {
  if (!SECURITY_BASELINE_SCENARIOS.has(scenarioId)) return [];
  const issues = [];
  if (!securityBaseline || typeof securityBaseline !== 'object' || Array.isArray(securityBaseline)) {
    return ['securityBaseline is missing'];
  }
  if (String(securityBaseline.schemaVersion || '').trim() !== SECURITY_BASELINE_SCHEMA_VERSION) {
    issues.push(`securityBaseline.schemaVersion=${securityBaseline.schemaVersion || 'missing'}`);
  }
  if (String(securityBaseline.mode || '').trim() !== SECURITY_BASELINE_MODE) {
    issues.push(`securityBaseline.mode=${securityBaseline.mode || 'missing'}`);
  }
  if (!Number.isFinite(Date.parse(String(securityBaseline.appliedAt || '').trim()))) {
    issues.push('securityBaseline.appliedAt is missing or invalid');
  }
  if (String(securityBaseline.status || '').trim() !== 'pass') {
    issues.push(`securityBaseline is ${securityBaseline.status || 'missing'}`);
  }
  const controls = securityBaseline.controls;
  if (!controls || typeof controls !== 'object' || Array.isArray(controls)) {
    issues.push('securityBaseline.controls is missing');
    return issues;
  }
  for (const [key, want] of REQUIRED_SECURITY_BASELINE_CONTROLS.entries()) {
    const got = String(controls[key] || '').trim();
    if (got !== want) {
      issues.push(`securityBaseline.controls.${key}=${got || 'missing'}`);
    }
  }
  const rootLogin = String(controls.sshRootLogin || '').trim();
  if (rootLogin !== 'key-only' && rootLogin !== 'disabled') {
    issues.push(`securityBaseline.controls.sshRootLogin=${rootLogin || 'missing'}`);
  }
  return issues;
}

function onDemandSetupActionEvidence(artifact) {
  const actions = new Set();
  const apps = [
    ...(Array.isArray(artifact?.platformSystemApps) ? artifact.platformSystemApps : []),
    ...(Array.isArray(artifact?.platformApps) ? artifact.platformApps : []),
  ];
  for (const app of apps) {
    if (String(app?.setupPolicy || '').trim().toLowerCase() !== 'on_demand') continue;
    if (!String(app?.externalId || '').trim()) continue;
    if (!platformAppEvidenceAcceptable(String(app?.observedStatus || ''), 'on_demand')) continue;
    for (const drop of Array.isArray(app?.setupDrops) ? app.setupDrops : []) {
      const name = String(drop?.name || '').trim();
      if (name) actions.add(name);
    }
  }
  return actions;
}

function platformAppEvidenceAcceptable(status, setupPolicy) {
  const normalizedStatus = String(status || '').trim().toLowerCase();
  if (normalizedStatus.startsWith('running') || normalizedStatus === 'docker:running') {
    return true;
  }
  return setupPolicy === 'on_demand' && normalizedStatus === 'deploy:accepted';
}

function required(opts, name) {
  if (!opts[name]) {
    throw new Error(`missing required --${name.replace(/[A-Z]/g, (m) => `-${m.toLowerCase()}`)}`);
  }
  return opts[name];
}

async function sha256File(filePath) {
  const hash = createHash('sha256');
  await new Promise((resolve, reject) => {
    const stream = createReadStream(filePath);
    stream.on('data', (chunk) => hash.update(chunk));
    stream.on('error', reject);
    stream.on('end', resolve);
  });
  return hash.digest('hex');
}

function artifactKind(name) {
  if (name === 'checksums.txt') return 'checksum';
  if (name.endsWith('.spdx.json') || name.endsWith('.cdx.json')) return 'sbom';
  if (name === 'release-evidence.json') return 'evidence';
  if (name.endsWith('.tar.gz') || name.endsWith('.zip')) return 'archive';
  if (name.endsWith('.deb') || name.endsWith('.rpm') || name.endsWith('.apk')) return 'package';
  return 'other';
}

async function collectArtifacts(distDir, outputPath) {
  const entries = await readdir(distDir, { withFileTypes: true }).catch((err) => {
    if (err.code === 'ENOENT') return [];
    throw err;
  });
  const artifacts = [];
  for (const entry of entries) {
    if (!entry.isFile()) continue;
    const filePath = path.join(distDir, entry.name);
    if (path.resolve(filePath) === path.resolve(outputPath)) continue;
    const info = await stat(filePath);
    artifacts.push({
      name: entry.name,
      kind: artifactKind(entry.name),
      sha256: await sha256File(filePath),
      sizeBytes: info.size,
    });
  }
  artifacts.sort((a, b) => a.name.localeCompare(b.name));
  return artifacts;
}

function checkFromMap(checks, name, fallbackStatus, summary) {
  if (checks.has(name)) return checks.get(name);
  return { status: fallbackStatus, ...(summary ? { summary } : {}) };
}

function isLegacyV04Tag(tag) {
  return /^v0\.4\./.test(String(tag || '').trim());
}

function releaseEvidenceDefaults(tag) {
  if (isLegacyV04Tag(tag)) {
    return {
      missingAlternatives: LEGACY_V04_MISSING_ALTERNATIVES,
      requiredKnownLimitations: LEGACY_V04_KNOWN_LIMITATIONS,
      defaultKnownLimitations: LEGACY_V04_DEFAULT_KNOWN_LIMITATIONS,
    };
  }
  return {
    missingAlternatives: CURRENT_MISSING_ALTERNATIVES,
    requiredKnownLimitations: CURRENT_KNOWN_LIMITATIONS,
    defaultKnownLimitations: CURRENT_DEFAULT_KNOWN_LIMITATIONS,
  };
}

function mergeRequiredMissingAlternatives(values, requiredAlternatives) {
  const result = [];
  const seen = new Set();
  const add = (value) => {
    if (!value || seen.has(value)) return;
    seen.add(value);
    result.push(value);
  };

  for (const value of values) {
    add(value);
  }
  for (const required of requiredAlternatives) {
    const covered = result.some((value) => value.toLowerCase().startsWith(`${required.service.toLowerCase()} `));
    if (!covered) {
      add(required.message);
    }
  }
  return result;
}

function mergeRequiredKnownLimitations(values, requiredLimitations, browserEvidencePassed = false) {
  const result = [];
  const seen = new Set();
  const add = (value) => {
    if (!value || seen.has(value)) return;
    seen.add(value);
    result.push(value);
  };

  for (const value of values) {
    add(value);
  }
  if (!browserEvidencePassed) {
    for (const required of requiredLimitations) {
      add(required);
    }
  }
  return result;
}

function mergeRequiredScenarioEvidence(values) {
  const canonicalIDs = new Set(CANONICAL_SCENARIOS.map((scenario) => scenario.id));
  const byID = new Map();
  const nonCanonicalOrder = [];

  for (const value of values) {
    const scenarioId = String(value.scenarioId || '').trim();
    if (!scenarioId) continue;
    if (!byID.has(scenarioId) && !canonicalIDs.has(scenarioId)) {
      nonCanonicalOrder.push(scenarioId);
    }
    byID.set(scenarioId, { ...value, scenarioId });
  }

  const result = [];
  for (const scenario of CANONICAL_SCENARIOS) {
    if (byID.has(scenario.id)) {
      result.push(byID.get(scenario.id));
    } else {
      result.push({
        scenarioId: scenario.id,
        status: scenario.defaultStatus || 'pending',
        summary: scenario.pendingSummary,
      });
    }
  }
  for (const scenarioId of nonCanonicalOrder) {
    result.push(byID.get(scenarioId));
  }
  return result;
}

function enforcePublicSKS1EvidenceBoundary(values, visibility) {
  if (visibility !== 'public') return values;
  const retained = values.filter((value) => {
    const scenarioId = String(value.scenarioId || '').trim();
    return scenarioId !== 'SK-S1' && !scenarioId.startsWith('SK-S1-');
  });
  retained.push({
    scenarioId: 'SK-S1',
    status: 'pending',
    summary: PUBLIC_SK_S1_PENDING_SUMMARY,
  });
  return retained;
}

function publicSKS1PendingCheck(summary = PUBLIC_SK_S1_PENDING_SUMMARY) {
  return {
    status: 'pending',
    summary,
  };
}

function securityBaselineReleaseCheck(scenarioEvidence) {
  const pending = [];
  const failed = [];
  for (const scenarioId of SECURITY_BASELINE_SCENARIOS) {
    const evidence = scenarioEvidence.find((item) => item.scenarioId === scenarioId);
    if (!evidence) {
      pending.push(scenarioId);
      continue;
    }
    if (evidence.status === 'pass') {
      if (evidence.source === 'homelab-artifact') continue;
      pending.push(`${scenarioId} homelab artifact proof`);
      continue;
    }
    if (evidence.status === 'fail') {
      failed.push(scenarioId);
    } else {
      pending.push(scenarioId);
    }
  }

  if (failed.length > 0) {
    return {
      status: 'fail',
      summary: `Host security baseline evidence failed or is incomplete for ${failed.join(', ')}${
        pending.length > 0 ? `; pending ${pending.join(', ')}` : ''
      }.`,
    };
  }
  if (pending.length > 0) {
    return {
      status: 'pending',
      summary: `Host security baseline evidence is pending for ${pending.join(', ')}.`,
    };
  }
  return {
    status: 'pass',
    summary:
      'SK-S1, SK-S2, and SK-S3 released-content artifacts include measured public-beta host security baseline evidence.',
  };
}

function mergePendingGates(values, scenarioEvidence) {
  const result = [];
  const seen = new Set();
  const add = (value) => {
    if (!value || seen.has(value)) return;
    seen.add(value);
    result.push(value);
  };

  for (const value of values) {
    add(value);
  }

  for (const scenario of CANONICAL_SCENARIOS) {
    const evidence = scenarioEvidence.find((item) => item.scenarioId === scenario.id);
    if (!evidence || evidence.status === 'pass' || evidence.status === 'not_applicable') continue;
    const alreadyCovered = result.some((gate) => gate.toLowerCase().includes(scenario.id.toLowerCase()));
    if (alreadyCovered) continue;
    if (evidence.status === 'pending') {
      add(scenario.pendingGate);
    } else {
      add(
        `${scenario.label} scenario evidence is ${evidence.status}; ${
          evidence.summary || 'inspect release-evidence scenarioEvidence.'
        }`,
      );
    }
  }
  return result;
}

async function loadBrowserEvidence(
  browserEvidencePath,
  browserEvidenceRoot,
  expectedBrowserChannel = '',
  expectedEvidenceRoot = '',
  expectedRunId = '',
) {
  if (!browserEvidencePath) return null;
  const data = await readFile(browserEvidencePath, 'utf8');
  const evidence = JSON.parse(data);
  const checks = Array.isArray(evidence.checks) ? evidence.checks : [];
  const screenshots = Array.isArray(evidence.screenshots) ? evidence.screenshots : [];
  const screenshotsByPath = new Map();
  for (const screenshot of screenshots) {
    const screenshotPath = String(screenshot?.path || '').trim();
    if (screenshotPath) {
      screenshotsByPath.set(screenshotPath, screenshot);
    }
  }
  const ownerUsername = String(evidence.ownerUsername || '').trim();
  const ownerEmail = String(evidence.ownerEmail || '').trim();
  const runId = String(evidence.runId || '').trim();
  const browserChannel = browserChannelLabel(evidence.browserChannel);
  const expectedChannel = browserChannelLabel(expectedBrowserChannel);
  const preflightRunId = String(expectedRunId || '').trim();
  const manifestError = String(evidence.error || '').trim();
  const failurePhase = String(evidence.failurePhase || '').trim();
  const status = String(evidence.status || '').trim();
  const issues = [];
  if (!runId) {
    issues.push('runId must be recorded');
  }
  if (!Number.isFinite(Date.parse(String(evidence.generatedAt || '').trim()))) {
    issues.push('generatedAt must be RFC3339');
  }
  if (runId && preflightRunId && runId !== preflightRunId) {
    issues.push(`browser evidence runId ${runId} does not match preflight runId ${preflightRunId}`);
  }
  if (!ownerEmail || !ownerEmail.includes('@')) {
    issues.push('ownerEmail must be email-shaped');
  }
  if (ownerUsername && ownerUsername.includes('@')) {
    issues.push('ownerUsername must be a username, not an email address');
  }
  if (!browserChannel) {
    issues.push('browserChannel must be recorded');
  }
  if (browserChannel && expectedChannel && browserChannel !== expectedChannel) {
    issues.push(`browserChannel ${browserChannel} does not match preflight browserChannel ${expectedChannel}`);
  }
  const rootMismatch = validateBrowserEvidenceRootMatch(browserEvidenceRoot, expectedEvidenceRoot);
  if (rootMismatch) {
    issues.push(rootMismatch);
  }
  const browserURLIssue = validateBaseKitBrowserURL(evidence.browserUrl);
  if (browserURLIssue) {
    issues.push(`browserUrl ${browserURLIssue}`);
  }
  if (status === 'fail') {
    issues.push(...validateBrowserFailureEvidenceContract(evidence, checks, screenshots, browserEvidenceRoot));
    if (issues.length > 0) {
      const failureCause = browserFailureCause(manifestError, checks);
      return {
        path: browserEvidencePath,
        scenarioId: evidence.scenarioId || 'unknown',
        status: 'fail',
        summary: `SK-S1 browser evidence failure manifest is incomplete or invalid; ${issues.join('; ')}${
          failureCause ? `; ${failureCause}` : ''
        }.`,
      };
    }
    return {
      path: browserEvidencePath,
      scenarioId: evidence.scenarioId || 'unknown',
      status: 'fail',
      summary: `SK-S1 browser evidence failed during ${failurePhase || 'browser evidence'}; ${browserFailureCause(
        manifestError,
        checks,
      )}${browserFailureNativeCommandCause(evidence)}.`,
    };
  }
  if (manifestError) {
    issues.push(`error: ${manifestError}`);
  }
  const missing = REQUIRED_BROWSER_EVIDENCE_CHECKS.filter(
    (name) => !checks.some((check) => check.name === name && check.status === 'pass' && check.screenshot),
  );
  for (const name of missing) {
    issues.push(`missing check ${name}`);
  }
  for (const check of checks.filter((item) => REQUIRED_BROWSER_EVIDENCE_CHECKS.includes(item.name))) {
    const screenshotPath = String(check.screenshot || '').trim();
    if (!screenshotPath) {
      issues.push(`${check.name} does not reference a screenshot`);
      continue;
    }
    if (!screenshotsByPath.has(screenshotPath)) {
      issues.push(`${check.name} references screenshot ${screenshotPath} missing from screenshots[]`);
      continue;
    }
    const screenshot = screenshotsByPath.get(screenshotPath);
    const checkIssue = validateBrowserCheckContract(check);
    if (checkIssue) {
      issues.push(`${check.name}: ${checkIssue}`);
    }
    const validation = await validateBrowserEvidenceScreenshot(browserEvidenceRoot, screenshotPath);
    if (validation) {
      issues.push(`${check.name} screenshot ${screenshotPath}: ${validation}`);
    }
    const screenshotURL = String(screenshot?.url || '').trim();
    if (screenshotURL) {
      const screenshotURLIssue = validateBrowserURL(screenshotURL);
      if (screenshotURLIssue) {
        issues.push(`${check.name} screenshot ${screenshotPath}: url ${screenshotURLIssue}`);
      } else {
        const screenshotRouteIssue = validateBaseKitBrowserCheckURL(check.name, screenshotURL);
        if (screenshotRouteIssue) {
          issues.push(`${check.name} screenshot ${screenshotPath}: url ${screenshotRouteIssue}`);
        }
      }
    }
    const contentValidation = validateBrowserEvidenceCheckContent(check, ownerEmail);
    if (contentValidation) {
      issues.push(`${check.name}: ${contentValidation}`);
    }
  }
  const setupDiagnosticsIssue = validateBrowserSetupDiagnostics(evidence);
  if (setupDiagnosticsIssue) {
    issues.push(setupDiagnosticsIssue);
  }
  const browserDiagnosticsIssue = validateBrowserRuntimeDiagnostics(evidence, browserChannel);
  if (browserDiagnosticsIssue) {
    issues.push(browserDiagnosticsIssue);
  }
  const setupActionsIssue = validateBrowserSetupActions(evidence);
  if (setupActionsIssue) {
    issues.push(setupActionsIssue);
  }
  const setupStateFileIssue = await validateBrowserSetupStateFile(browserEvidenceRoot, evidence);
  if (setupStateFileIssue) {
    issues.push(setupStateFileIssue);
  }
  const passed =
    evidence.scenarioId === 'SK-S1' &&
    evidence.status === 'pass' &&
    checks.length >= REQUIRED_BROWSER_EVIDENCE_CHECKS.length &&
    screenshots.length >= REQUIRED_BROWSER_EVIDENCE_CHECKS.length &&
    issues.length === 0;
  return {
    path: browserEvidencePath,
    scenarioId: evidence.scenarioId || 'unknown',
    status: passed ? 'pass' : 'fail',
    summary: passed
      ? `SK-S1 browser evidence proves PocketID/passkey Owner login reaches TinyAuth plus seeded Photos/Files content and Vault auth boundary with ${browserChannel}.`
      : `SK-S1 browser evidence manifest is incomplete or failing${issues.length ? `; ${issues.join('; ')}` : ''}.`,
  };
}

async function loadBrowserPreflight(browserPreflightPath) {
  if (!browserPreflightPath) return null;
  let evidence;
  try {
    evidence = JSON.parse(await readFile(browserPreflightPath, 'utf8'));
  } catch (error) {
    return {
      path: browserPreflightPath,
      scenarioId: 'SK-S1',
      status: 'fail',
      browserChannel: '',
      evidenceRoot: '',
      runId: '',
      summary: `SK-S1 browser preflight report is unreadable; ${error.code || error.message}.`,
    };
  }
  const issues = validateBrowserPreflightEvidence(evidence);
  const scenarioId = evidence?.scenarioId || 'unknown';
  const browserChannel = browserChannelLabel(evidence?.browserChannel);
  const evidenceRoot = String(evidence?.evidenceRoot || '').trim();
  const runId = String(evidence?.runId || '').trim();
  if (issues.length > 0) {
    return {
      path: browserPreflightPath,
      scenarioId,
      status: 'fail',
      browserChannel,
      evidenceRoot,
      runId,
      summary: `SK-S1 browser preflight report is incomplete or invalid; ${issues.join('; ')}.`,
    };
  }
  const status = String(evidence.status || '').trim();
  if (status === 'pass') {
    return {
      path: browserPreflightPath,
      scenarioId,
      status: 'pass',
      browserChannel,
      evidenceRoot,
      runId,
      summary: `SK-S1 browser preflight confirms Docker Desktop, Node/npm, isolated Playwright package, and ${browserChannel} launch prerequisites are ready.`,
    };
  }
  const failedChecks = Array.isArray(evidence.failedChecks) ? evidence.failedChecks.join(', ') : '';
  const nativeCommandSummary = browserPreflightNativeCommandSummary(evidence.checks || []);
  return {
    path: browserPreflightPath,
    scenarioId,
    status: 'fail',
    browserChannel,
    evidenceRoot,
    runId,
    summary: `SK-S1 browser preflight failed${failedChecks ? `; failedChecks=${failedChecks}` : ''}${
      nativeCommandSummary ? `; ${nativeCommandSummary}` : ''
    }.`,
  };
}

function browserPreflightNativeCommandSummary(checks) {
  const values = [];
  for (const check of Array.isArray(checks) ? checks : []) {
    if (String(check?.status || '').trim() !== 'fail') continue;
    const nativeCommand = check?.nativeCommand;
    if (!nativeCommand || typeof nativeCommand !== 'object' || Array.isArray(nativeCommand)) continue;
    const name = String(nativeCommand.name || check?.name || 'unnamed').trim();
    const filePath = String(nativeCommand.filePath || '').trim();
    const failureClass = String(nativeCommand.failureClass || '').trim();
    const hostIssue = String(nativeCommand.hostIssue || '').trim();
    const timeoutSeconds = Number(nativeCommand.timeoutSeconds || 0);
    const timeout = Number.isInteger(timeoutSeconds) && timeoutSeconds > 0 ? ` timeout=${timeoutSeconds}s` : '';
    values.push(
      `${name || 'unnamed'}${filePath ? ` (${filePath})` : ''}${failureClass ? ` class=${failureClass}` : ''}${
        hostIssue ? ` hostIssue=${hostIssue}` : ''
      }${timeout}`,
    );
  }
  return values.length ? `nativeCommands=${values.join(', ')}` : '';
}

function validateBrowserEvidenceRootMatch(browserEvidenceRoot, preflightEvidenceRoot) {
  const left = evidenceRootKey(browserEvidenceRoot);
  const right = evidenceRootKey(preflightEvidenceRoot);
  if (left === '.') return '';
  if (!left || !right || left === right) return '';
  return `browser evidence root ${browserEvidenceRoot || 'missing'} does not match preflight evidenceRoot ${preflightEvidenceRoot || 'missing'}`;
}

function evidenceRootKey(raw) {
  const value = String(raw || '').trim();
  if (!value) return '';
  return value.replace(/\\/g, '/').replace(/\/+$/g, '').toLowerCase();
}

function browserChannelLabel(raw) {
  const value = String(raw || '').trim();
  if (!value) return '';
  const normalized = value.toLowerCase();
  if (normalized === 'default' || normalized === 'chromium' || normalized === 'playwright-chromium') {
    return 'playwright-chromium';
  }
  return normalized;
}

function validateBrowserPreflightEvidence(evidence) {
  const issues = [];
  if (!evidence || typeof evidence !== 'object') {
    return ['report must be a JSON object'];
  }
  if (String(evidence.scenarioId || '').trim() !== 'SK-S1') {
    issues.push(`scenarioId is ${evidence.scenarioId || 'missing'}, want SK-S1`);
  }
  if (!String(evidence.runId || '').trim()) {
    issues.push('runId is missing');
  }
  if (String(evidence.kind || '').trim() !== 'browser-evidence-preflight') {
    issues.push(`kind is ${evidence.kind || 'missing'}, want browser-evidence-preflight`);
  }
  const status = String(evidence.status || '').trim();
  if (!['pass', 'fail'].includes(status)) {
    issues.push(`status is ${status || 'missing'}, want pass or fail`);
  }
  if (!Number.isFinite(Date.parse(String(evidence.generatedAt || '').trim()))) {
    issues.push('generatedAt must be RFC3339');
  }
  if (!String(evidence.evidenceRoot || '').trim()) {
    issues.push('evidenceRoot is missing');
  }
  if (!String(evidence.playwrightModuleDir || '').trim()) {
    issues.push('playwrightModuleDir is missing');
  }
  if (!String(evidence.browserChannel || '').trim()) {
    issues.push('browserChannel is missing');
  }
  const phaseTimeoutSeconds = Number(evidence.phaseTimeoutSeconds || 0);
  if (!Number.isInteger(phaseTimeoutSeconds) || phaseTimeoutSeconds <= 0) {
    issues.push('phaseTimeoutSeconds must be positive');
  } else if (phaseTimeoutSeconds > MAX_BROWSER_CHECK_DURATION_SECONDS) {
    issues.push(`phaseTimeoutSeconds ${phaseTimeoutSeconds} exceeds 15 minute budget`);
  }
  const browserChannel = browserChannelLabel(evidence.browserChannel);
  const checkIssues = validateBrowserPreflightChecks(evidence.checks || [], browserChannel);
  issues.push(...checkIssues.issues);
  const failedChecks = checkIssues.failedChecks;
  const reportedFailedChecks = Array.isArray(evidence.failedChecks)
    ? evidence.failedChecks.map((value) => String(value || '').trim()).filter(Boolean)
    : [];
  if (status === 'pass') {
    if (failedChecks.length > 0) issues.push(`status is pass but failed checks are present: ${failedChecks.join(', ')}`);
    if (reportedFailedChecks.length > 0) issues.push('status is pass but failedChecks is not empty');
    if (String(evidence.error || '').trim()) issues.push('status is pass but error is set');
  }
  if (status === 'fail') {
    if (failedChecks.length === 0) issues.push('status is fail but no checks failed');
    if (!String(evidence.error || '').trim()) issues.push('status is fail but error is empty');
    if (!sameStringList(reportedFailedChecks, failedChecks)) {
      issues.push(`failedChecks = [${reportedFailedChecks.join(', ')}], want [${failedChecks.join(', ')}]`);
    }
  }
  return issues;
}

function validateBrowserPreflightChecks(checks, browserChannel = '') {
  const issues = [];
  const failedChecks = [];
  if (!Array.isArray(checks) || checks.length === 0) {
    return { issues: ['checks are missing'], failedChecks };
  }
  const checksByName = new Map();
  for (const check of checks) {
    const name = String(check?.name || '').trim();
    if (!name) {
      issues.push('contains a check without name');
      continue;
    }
    if (checksByName.has(name)) {
      issues.push(`contains duplicate check ${name}`);
      continue;
    }
    const status = String(check?.status || '').trim();
    if (!['pass', 'fail', 'skipped'].includes(status)) {
      issues.push(`${name} status is ${status || 'missing'}, want pass, fail, or skipped`);
    }
    const timeoutSeconds = Number(check?.timeoutSeconds ?? -1);
    if (!Number.isInteger(timeoutSeconds) || timeoutSeconds < 0) {
      issues.push(`${name} timeoutSeconds must be non-negative`);
    } else if (timeoutSeconds > MAX_BROWSER_CHECK_DURATION_SECONDS) {
      issues.push(`${name} timeoutSeconds ${timeoutSeconds} exceeds 15 minute budget`);
    }
    if (status === 'fail') {
      if (!String(check?.error || '').trim()) {
        issues.push(`${name} failed without error`);
      }
      for (const issue of validateBrowserPreflightNativeCommandDiagnostics(check?.nativeCommand)) {
        issues.push(`${name}: ${issue}`);
      }
      failedChecks.push(name);
    }
    checksByName.set(name, check);
  }
  for (const required of REQUIRED_BROWSER_PREFLIGHT_CHECKS) {
    if (!checksByName.has(required)) {
      issues.push(`missing required check ${required}`);
      continue;
    }
    const skippedIssue = validateBrowserPreflightRequiredStatus(checksByName.get(required), browserChannel);
    if (skippedIssue) {
      issues.push(skippedIssue);
    }
    const evidenceIssue = validateBrowserPreflightRequiredEvidence(checksByName.get(required), browserChannel);
    if (evidenceIssue) {
      issues.push(evidenceIssue);
    }
  }
  return { issues, failedChecks };
}

function validateBrowserPreflightNativeCommandDiagnostics(nativeCommand) {
  if (nativeCommand === undefined || nativeCommand === null) return [];
  const issues = [];
  if (!nativeCommand || typeof nativeCommand !== 'object' || Array.isArray(nativeCommand)) {
    return ['nativeCommand must be an object when present'];
  }
  if (!String(nativeCommand.name || '').trim()) {
    issues.push('nativeCommand must include name');
  }
  if (!String(nativeCommand.filePath || '').trim()) {
    issues.push('nativeCommand must include filePath');
  }
  const timeoutSeconds = Number(nativeCommand.timeoutSeconds || 0);
  if (!Number.isInteger(timeoutSeconds) || timeoutSeconds <= 0) {
    issues.push('nativeCommand must include positive timeoutSeconds');
  } else if (timeoutSeconds > MAX_BROWSER_CHECK_DURATION_SECONDS) {
    issues.push(`nativeCommand timeoutSeconds ${timeoutSeconds} exceeds 15 minute budget`);
  }
  const failureClass = String(nativeCommand.failureClass || '').trim();
  if (failureClass && !['start_failed', 'timeout', 'exit_nonzero'].includes(failureClass)) {
    issues.push(`nativeCommand failureClass is ${failureClass}, want start_failed, timeout, or exit_nonzero`);
  }
  const hostIssue = String(nativeCommand.hostIssue || '').trim();
  if (hostIssue) {
    if (hostIssue !== 'windows-createprocessasuser-access-denied') {
      issues.push(`nativeCommand hostIssue is ${hostIssue}, want windows-createprocessasuser-access-denied`);
    }
    if (failureClass !== 'start_failed') {
      issues.push(`nativeCommand hostIssue ${hostIssue} requires failureClass start_failed`);
    }
  }
  if ('exitCode' in nativeCommand) {
    const exitCode = Number(nativeCommand.exitCode);
    if (!Number.isInteger(exitCode) || exitCode < 0) {
      issues.push('nativeCommand exitCode must be a non-negative integer when present');
    }
  }
  if ('environment' in nativeCommand || 'env' in nativeCommand) {
    issues.push('nativeCommand must not include environment values');
  }
  if ('arguments' in nativeCommand && !Array.isArray(nativeCommand.arguments)) {
    issues.push('nativeCommand arguments must be an array when present');
  }
  return issues;
}

function validateBrowserPreflightRequiredStatus(check, browserChannel = '') {
  const status = String(check?.status || '').trim();
  if (status !== 'skipped') return '';
  if (check.name === 'Install isolated Playwright Chromium' && browserChannel !== 'playwright-chromium') {
    return '';
  }
  return `${check.name} is skipped; only Install isolated Playwright Chromium may be skipped when browserChannel is an installed browser channel`;
}

function validateBrowserPreflightRequiredEvidence(check, browserChannel = '') {
  if (String(check?.status || '').trim() !== 'pass') return '';
  const evidence = check?.evidence && typeof check.evidence === 'object' ? check.evidence : {};
  // Docker Desktop reports desktop-linux; plain Docker Engine hosts (CI
  // runners, Linux servers) report default. Both are usable Linux engines.
  const dockerContext = String(evidence.output || '').trim();
  if (check.name === 'Docker Desktop context' && dockerContext !== 'desktop-linux' && dockerContext !== 'default') {
    return `${check.name} output ${evidence.output || 'missing'}, want desktop-linux or default`;
  }
  if (check.name === 'Playwright package availability' && String(evidence.output || '').trim() !== 'playwright=available') {
    return `${check.name} output ${evidence.output || 'missing'}, want playwright=available`;
  }
  if (check.name === 'Playwright Chromium availability') {
    const want = browserChannel === 'playwright-chromium' ? 'chromium=available' : `browser-channel=${browserChannel}`;
    if (String(evidence.output || '').trim() !== want) {
      return `${check.name} output ${evidence.output || 'missing'}, want ${want}`;
    }
  }
  return '';
}

function sameStringList(left, right) {
  if (left.length !== right.length) return false;
  return left.every((value, index) => value === right[index]);
}

function validateBrowserSetupDiagnostics(evidence) {
  const setupState = evidence?.diagnostics?.setupState;
  if (!setupState) {
    return 'missing SetupRun diagnostics';
  }
  if (setupState.status !== 'present') {
    return `SetupRun diagnostics status is ${setupState.status || 'missing'}, want present`;
  }
  const drops = setupState.drops && typeof setupState.drops === 'object' ? setupState.drops : {};
  for (const dropName of REQUIRED_SETUP_DROPS) {
    const drop = drops[dropName];
    if (!drop || drop.status === 'missing') {
      return `SetupRun diagnostics missing ${dropName}`;
    }
    if (drop.status !== 'completed') {
      return `SetupRun ${dropName} status is ${drop.status || 'missing'}, want completed`;
    }
    if (drop.phase !== 'verified') {
      return `SetupRun ${dropName} phase is ${drop.phase || 'missing'}, want verified`;
    }
    const setupRunIssue = validateBrowserSetupRunReference(`SetupRun ${dropName}`, drop);
    if (setupRunIssue) return setupRunIssue;
    const auditTrailIssue = validateBrowserSetupRunAuditTrail(`SetupRun ${dropName}`, drop);
    if (auditTrailIssue) return auditTrailIssue;
    const evidenceIssue = validateBrowserSetupStateEvidence(`SetupRun ${dropName}`, dropName, drop.evidence || {});
    if (evidenceIssue) return evidenceIssue;
  }
  return '';
}

function validateBrowserRuntimeDiagnostics(evidence, browserChannel) {
  const browser = evidence?.diagnostics?.browser;
  if (!browser || typeof browser !== 'object') {
    return 'missing browser runtime diagnostics';
  }
  if (String(browser.channel || '').trim() !== browserChannel) {
    return `browser runtime channel ${browser.channel || 'missing'} does not match browserChannel ${browserChannel || 'missing'}`;
  }
  if (!String(browser.requestedChannel || '').trim()) {
    return 'browser runtime requestedChannel is missing';
  }
  if (!['true', 'false'].includes(String(browser.headless || '').trim())) {
    return `browser runtime headless ${browser.headless || 'missing'} must be true or false`;
  }
  const viewport = String(browser.viewport || '').trim();
  const match = viewport.match(/^(\d+)x(\d+)$/);
  if (!match) {
    return `browser runtime viewport ${viewport || 'missing'} must be WIDTHxHEIGHT`;
  }
  const width = Number(match[1]);
  const height = Number(match[2]);
  if (width < MIN_BROWSER_SCREENSHOT_WIDTH || height < MIN_BROWSER_SCREENSHOT_HEIGHT) {
    return `browser runtime viewport ${viewport} is smaller than ${MIN_BROWSER_SCREENSHOT_WIDTH}x${MIN_BROWSER_SCREENSHOT_HEIGHT}`;
  }
  if (!String(browser.userAgent || '').trim()) {
    return 'browser runtime userAgent is missing';
  }
  if (!String(browser.browserVersion || '').trim()) {
    return 'browser runtime browserVersion is missing';
  }
  if (String(browser.webAuthnVirtualAuthenticator || '').trim() !== 'enabled') {
    return `browser runtime webAuthnVirtualAuthenticator ${browser.webAuthnVirtualAuthenticator || 'missing'} must be enabled`;
  }
  return '';
}

function validateBrowserSetupActions(evidence) {
  const actions = Array.isArray(evidence?.diagnostics?.setupActions) ? evidence.diagnostics.setupActions : [];
  const actionsByService = new Map();
  for (const action of actions) {
    const service = String(action?.service || '').trim();
    if (!service) {
      return 'setupActions contains an action without service';
    }
    if (actionsByService.has(service)) {
      return `setupActions contains duplicate service ${service}`;
    }
    actionsByService.set(service, action);
  }
  for (const service of REQUIRED_OWNER_SETUP_SERVICES) {
    const action = actionsByService.get(service);
    if (!action) {
      return `setupActions missing owner-activated service ${service}`;
    }
    if (String(action.ok || '').trim().toLowerCase() !== 'true') {
      return `setupAction ${service} ok is ${action.ok || 'missing'}, want true`;
    }
    const httpStatus = Number(action.httpStatus || 0);
    if (!Number.isInteger(httpStatus) || httpStatus < 200 || httpStatus > 299) {
      return `setupAction ${service} httpStatus is ${action.httpStatus || 'missing'}, want 2xx`;
    }
    const duration = Number(action.durationSeconds || 0);
    if (!Number.isInteger(duration) || duration <= 0) {
      return `setupAction ${service} must record durationSeconds`;
    }
    if (duration > MAX_BROWSER_CHECK_DURATION_SECONDS) {
      return `setupAction ${service} durationSeconds ${duration} exceeds 15 minute budget`;
    }
    if (String(action.status || '').trim() !== 'completed') {
      return `setupAction ${service} status is ${action.status || 'missing'}, want completed`;
    }
    const expectedDropName = REQUIRED_OWNER_SETUP_DROPS_BY_SERVICE[service];
    if (String(action.dropName || '').trim() !== expectedDropName) {
      return `setupAction ${service} dropName is ${action.dropName || 'missing'}, want ${expectedDropName}`;
    }
    if (String(action.dropStatus || '').trim() !== 'completed') {
      return `setupAction ${service} dropStatus is ${action.dropStatus || 'missing'}, want completed`;
    }
    if (String(action.dropPhase || '').trim() !== 'verified') {
      return `setupAction ${service} dropPhase is ${action.dropPhase || 'missing'}, want verified`;
    }
    const setupRunIssue = validateBrowserSetupRunReference(`setupAction ${service}`, action);
    if (setupRunIssue) return setupRunIssue;
    const logCount = Number(action.logCount || 0);
    if (!Number.isInteger(logCount) || logCount < 1) {
      return `setupAction ${service} logCount is ${action.logCount || 'missing'}, want >= 1`;
    }
    const rollbackNoteCount = Number(action.rollbackNoteCount || 0);
    if (!Number.isInteger(rollbackNoteCount) || rollbackNoteCount < 1) {
      return `setupAction ${service} rollbackNoteCount is ${action.rollbackNoteCount || 'missing'}, want >= 1`;
    }
  }
  return '';
}

function validateBrowserSetupRunReference(label, item) {
  if (!String(item?.runId || '').trim()) {
    return `${label} must include runId`;
  }
  const attempts = Number(item?.attempts || 0);
  if (!Number.isInteger(attempts) || attempts < 1) {
    return `${label} attempts is ${item?.attempts || 'missing'}, want >= 1`;
  }
  for (const field of ['lastRequested', 'lastStarted', 'lastFinished']) {
    if (Number.isNaN(Date.parse(String(item?.[field] || '')))) {
      return `${label} must include RFC3339 ${field}`;
    }
  }
  return '';
}

function validateBrowserSetupRunAuditTrail(label, item) {
  const logCount = Number(item?.logCount || 0);
  if (!Number.isInteger(logCount) || logCount < 1) {
    return `${label} logCount is ${item?.logCount || 'missing'}, want >= 1`;
  }
  const rollbackNoteCount = Number(item?.rollbackNoteCount || 0);
  if (!Number.isInteger(rollbackNoteCount) || rollbackNoteCount < 1) {
    return `${label} rollbackNoteCount is ${item?.rollbackNoteCount || 'missing'}, want >= 1`;
  }
  return '';
}

function validateBrowserCheckContract(check) {
  const urlIssue = validateBrowserURL(check.url);
  if (urlIssue) {
    return `url ${urlIssue}`;
  }
  const routeIssue = validateBaseKitBrowserCheckURL(check.name, check.url);
  if (routeIssue) {
    return `url ${routeIssue}`;
  }
  const duration = Number(check.durationSeconds || 0);
  if (!Number.isInteger(duration) || duration <= 0) {
    return 'must record durationSeconds';
  }
  if (duration > MAX_BROWSER_CHECK_DURATION_SECONDS) {
    return `durationSeconds ${duration} exceeds 15 minute budget`;
  }
  if (!String(check.expectedText || '').trim()) {
    return 'must record expectedText';
  }
  if (!String(check.observedText || '').trim()) {
    return 'must record observedText';
  }
  return '';
}

function validateBrowserURL(raw) {
  let parsed;
  try {
    parsed = new URL(String(raw || '').trim());
  } catch {
    return 'must be an absolute URL';
  }
  if (!['http:', 'https:'].includes(parsed.protocol)) {
    return `scheme is ${parsed.protocol.replace(/:$/, '') || 'missing'}, want http or https`;
  }
  if (!parsed.host) {
    return 'must include a host';
  }
  return '';
}

function validateBaseKitBrowserURL(raw) {
  const urlIssue = validateBrowserURL(raw);
  if (urlIssue) return urlIssue;
  const parsed = new URL(String(raw || '').trim());
  if (parsed.protocol !== 'http:') {
    return `scheme is ${parsed.protocol.replace(/:$/, '') || 'missing'}, want http for SK-S1 local Base Hub`;
  }
  if (parsed.hostname !== 'base.home.localhost') {
    return `host is ${parsed.hostname || 'missing'}, want base.home.localhost`;
  }
  if (parsed.pathname && parsed.pathname !== '/') {
    return `path is ${parsed.pathname}, want /`;
  }
  return '';
}

function validateBaseKitBrowserCheckURL(checkName, raw) {
  const route = REQUIRED_BROWSER_CHECK_ROUTES[String(checkName || '').trim()];
  if (!route) return '';
  const parsed = new URL(String(raw || '').trim());
  if (parsed.protocol !== 'http:') {
    return `scheme is ${parsed.protocol.replace(/:$/, '') || 'missing'}, want http`;
  }
  if (parsed.hostname !== route.host) {
    return `host is ${parsed.hostname || 'missing'}, want ${route.host}`;
  }
  const expectedPaths = Array.isArray(route.paths) && route.paths.length > 0 ? route.paths : ['/'];
  const gotPath = parsed.pathname || '/';
  if (!expectedPaths.includes(gotPath)) {
    return `path is ${gotPath}, want ${expectedPaths.join(' or ')}`;
  }
  return '';
}

function validateBrowserFailureEvidenceContract(evidence, checks, screenshots, browserEvidenceRoot) {
  const issues = [];
  if (String(evidence.scenarioId || '').trim() !== 'SK-S1') {
    issues.push(`scenarioId is ${evidence.scenarioId || 'missing'}, want SK-S1`);
  }
  if (String(evidence.status || '').trim() !== 'fail') {
    issues.push(`status is ${evidence.status || 'missing'}, want fail`);
  }
  if (!String(evidence.error || '').trim() && !hasFailedBrowserEvidenceCheck(checks)) {
    issues.push('must include error or at least one failed check');
  }
  if (checks.length === 0 && !String(evidence.failurePhase || '').trim()) {
    issues.push('failurePhase must be recorded when checks are empty');
  }
  const failurePhase = String(evidence.failurePhase || '').trim();
  if (failurePhase && !ALLOWED_BROWSER_FAILURE_PHASES.has(failurePhase)) {
    issues.push(
      `failurePhase is ${failurePhase}, want one of ${Array.from(ALLOWED_BROWSER_FAILURE_PHASES).join(', ')}`,
    );
  }
  issues.push(...validateBrowserFailureWrapperDiagnostics(evidence?.diagnostics?.wrapper, failurePhase, browserEvidenceRoot));
  const seenChecks = new Set();
  for (const check of checks) {
    if (!check || typeof check !== 'object') {
      issues.push('contains a non-object check');
      continue;
    }
    const name = String(check.name || '').trim();
    if (!name) {
      issues.push('contains a check without name');
      continue;
    }
    if (seenChecks.has(name)) {
      issues.push(`contains duplicate check ${name}`);
    }
    seenChecks.add(name);
    const checkIssues = validateBrowserFailureCheckContract(check);
    for (const issue of checkIssues) {
      issues.push(`${name}: ${issue}`);
    }
  }
  for (const screenshot of screenshots) {
    if (!screenshot || typeof screenshot !== 'object') {
      issues.push('contains a non-object screenshot');
      continue;
    }
    const screenshotIssues = validateBrowserFailureScreenshotContract(screenshot);
    for (const issue of screenshotIssues) {
      issues.push(`screenshot ${String(screenshot.name || 'unknown').trim() || 'unknown'}: ${issue}`);
    }
  }
  return issues;
}

function validateBrowserFailureWrapperDiagnostics(wrapper, failurePhase, browserEvidenceRoot) {
  const phase = String(failurePhase || '').trim();
  if (!phase) return [];
  const issues = [];
  if (!wrapper || typeof wrapper !== 'object') {
    return [`failurePhase ${phase} must include wrapper diagnostics`];
  }
  if (String(wrapper.phase || '').trim() !== phase) {
    issues.push(`wrapper phase is ${wrapper.phase || 'missing'}, want ${phase}`);
  }
  const evidenceRoot = String(wrapper.evidenceRoot || '').trim();
  if (!evidenceRoot) {
    issues.push('wrapper diagnostics must include evidenceRoot');
  } else if (validateBrowserEvidenceRootMatch(browserEvidenceRoot, evidenceRoot)) {
    issues.push(`wrapper evidenceRoot ${evidenceRoot} does not match browser evidence root ${browserEvidenceRoot || 'missing'}`);
  }
  for (const field of ['preflightReportPath', 'homelabPath']) {
    if (!String(wrapper[field] || '').trim()) {
      issues.push(`wrapper diagnostics must include ${field}`);
    }
  }
  const nativeCommandIssues = validateBrowserFailureNativeCommandDiagnostics(wrapper.nativeCommand);
  issues.push(...nativeCommandIssues);
  return issues;
}

function validateBrowserFailureNativeCommandDiagnostics(nativeCommand) {
  if (nativeCommand === undefined || nativeCommand === null) return [];
  const issues = [];
  if (!nativeCommand || typeof nativeCommand !== 'object' || Array.isArray(nativeCommand)) {
    return ['wrapper nativeCommand must be an object when present'];
  }
  if (!String(nativeCommand.name || '').trim()) {
    issues.push('wrapper nativeCommand must include name');
  }
  if (!String(nativeCommand.filePath || '').trim()) {
    issues.push('wrapper nativeCommand must include filePath');
  }
  const timeoutSeconds = Number(nativeCommand.timeoutSeconds || 0);
  if (!Number.isInteger(timeoutSeconds) || timeoutSeconds <= 0) {
    issues.push('wrapper nativeCommand must include positive timeoutSeconds');
  } else if (timeoutSeconds > MAX_BROWSER_CHECK_DURATION_SECONDS) {
    issues.push(`wrapper nativeCommand timeoutSeconds ${timeoutSeconds} exceeds 15 minute budget`);
  }
  const failureClass = String(nativeCommand.failureClass || '').trim();
  if (failureClass && !['start_failed', 'timeout', 'exit_nonzero'].includes(failureClass)) {
    issues.push(`wrapper nativeCommand failureClass is ${failureClass}, want start_failed, timeout, or exit_nonzero`);
  }
  const hostIssue = String(nativeCommand.hostIssue || '').trim();
  if (hostIssue) {
    if (hostIssue !== 'windows-createprocessasuser-access-denied') {
      issues.push(`wrapper nativeCommand hostIssue is ${hostIssue}, want windows-createprocessasuser-access-denied`);
    }
    if (failureClass !== 'start_failed') {
      issues.push(`wrapper nativeCommand hostIssue ${hostIssue} requires failureClass start_failed`);
    }
  }
  if ('exitCode' in nativeCommand) {
    const exitCode = Number(nativeCommand.exitCode);
    if (!Number.isInteger(exitCode) || exitCode < 0) {
      issues.push('wrapper nativeCommand exitCode must be a non-negative integer when present');
    }
  }
  if ('environment' in nativeCommand || 'env' in nativeCommand) {
    issues.push('wrapper nativeCommand must not include environment values');
  }
  if ('arguments' in nativeCommand && !Array.isArray(nativeCommand.arguments)) {
    issues.push('wrapper nativeCommand arguments must be an array when present');
  }
  return issues;
}

function hasFailedBrowserEvidenceCheck(checks) {
  return checks.some((check) => String(check.status || '').trim() === 'fail');
}

function browserFailureCause(manifestError, checks) {
  const error = String(manifestError || '').trim();
  if (error) return `error: ${error}`;
  const failedChecks = checks
    .filter((check) => String(check.status || '').trim() === 'fail')
    .map((check) => String(check.name || '').trim() || 'unnamed')
    .join(', ');
  return failedChecks ? `failedChecks=${failedChecks}` : 'failed without recorded cause';
}

function browserFailureNativeCommandCause(evidence) {
  const nativeCommand = evidence?.diagnostics?.wrapper?.nativeCommand;
  if (!nativeCommand || typeof nativeCommand !== 'object' || Array.isArray(nativeCommand)) return '';
  const name = String(nativeCommand.name || '').trim();
  const filePath = String(nativeCommand.filePath || '').trim();
  if (!name && !filePath) return '';
  const failureClass = String(nativeCommand.failureClass || '').trim();
  const hostIssue = String(nativeCommand.hostIssue || '').trim();
  const timeoutSeconds = Number(nativeCommand.timeoutSeconds || 0);
  const timeout = Number.isInteger(timeoutSeconds) && timeoutSeconds > 0 ? ` timeout=${timeoutSeconds}s` : '';
  return `; nativeCommand=${name || 'unnamed'}${filePath ? ` (${filePath})` : ''}${
    failureClass ? ` class=${failureClass}` : ''
  }${hostIssue ? ` hostIssue=${hostIssue}` : ''}${timeout}`;
}

function validateBrowserFailureCheckContract(check) {
  const issues = [];
  const status = String(check.status || '').trim();
  if (!['pass', 'fail'].includes(status)) {
    issues.push(`status is ${status || 'missing'}, want pass or fail`);
  }
  const rawURL = String(check.url || '').trim();
  if (rawURL) {
    const urlIssue = validateBrowserURL(rawURL);
    if (urlIssue) issues.push(`url ${urlIssue}`);
  }
  const rawDuration = String(check.durationSeconds ?? '').trim();
  if (rawDuration) {
    const duration = Number(rawDuration);
    if (!Number.isInteger(duration) || duration <= 0) {
      issues.push('durationSeconds must be positive when recorded');
    } else if (duration > MAX_BROWSER_CHECK_DURATION_SECONDS) {
      issues.push(`durationSeconds ${duration} exceeds 15 minute budget`);
    }
  }
  const screenshotPath = String(check.screenshot || '').trim();
  if (screenshotPath) {
    const pathIssue = validateBrowserFailureRelativePath(
      `screenshot ${screenshotPath}`,
      screenshotPath,
      ['.png', '.jpg', '.jpeg', '.webp'],
    );
    if (pathIssue) issues.push(pathIssue);
  }
  return issues;
}

function validateBrowserFailureScreenshotContract(screenshot) {
  const issues = [];
  if (!String(screenshot.name || '').trim()) {
    issues.push('must include name');
  }
  const screenshotPath = String(screenshot.path || '').trim();
  if (!screenshotPath) {
    issues.push('path must be recorded');
  } else {
    const pathIssue = validateBrowserFailureRelativePath(
      `path ${screenshotPath}`,
      screenshotPath,
      ['.png', '.jpg', '.jpeg', '.webp'],
    );
    if (pathIssue) issues.push(pathIssue);
  }
  const rawURL = String(screenshot.url || '').trim();
  if (rawURL) {
    const urlIssue = validateBrowserURL(rawURL);
    if (urlIssue) issues.push(`url ${urlIssue}`);
  }
  return issues;
}

function validateBrowserFailureRelativePath(label, rawPath, allowedExts = []) {
  const resolved = resolveEvidenceRelativePath(process.cwd(), rawPath);
  if (resolved.issue) return `${label}: ${resolved.issue}`;
  if (allowedExts.length > 0) {
    const ext = path.extname(resolved.clean).toLowerCase();
    if (!allowedExts.includes(ext)) {
      return `${label}: path must end with ${allowedExts.join(', ')}`;
    }
  }
  return '';
}

async function validateBrowserSetupStateFile(root, evidence) {
  const setupState = evidence?.diagnostics?.setupState;
  const sourcePath = String(setupState?.sourcePath || '').trim();
  if (!sourcePath) {
    return 'SetupRun diagnostics must include sourcePath';
  }
  const resolved = resolveEvidenceRelativePath(root, sourcePath);
  if (resolved.issue) {
    return `SetupRun diagnostics sourcePath ${sourcePath}: ${resolved.issue}`;
  }
  let content;
  try {
    content = await readFile(resolved.path, 'utf8');
  } catch (error) {
    return `SetupRun diagnostics sourcePath ${sourcePath}: file missing (${error.code || error.message})`;
  }
  if (!content.trim()) {
    return `SetupRun diagnostics sourcePath ${sourcePath}: empty file`;
  }
  const drops = parseSetupRunDrops(content);
  for (const dropName of REQUIRED_SETUP_DROPS) {
    const drop = drops.get(dropName);
    if (!drop) {
      return `SetupRun diagnostics sourcePath ${sourcePath}: missing ${dropName}`;
    }
    if (drop.status !== 'completed') {
      return `SetupRun diagnostics sourcePath ${sourcePath}: ${dropName} status is ${drop.status || 'missing'}, want completed`;
    }
    if (drop.phase !== 'verified') {
      return `SetupRun diagnostics sourcePath ${sourcePath}: ${dropName} phase is ${drop.phase || 'missing'}, want verified`;
    }
    const setupRunIssue = validateBrowserSetupRunReference(`SetupRun diagnostics sourcePath ${sourcePath}: ${dropName}`, drop);
    if (setupRunIssue) return setupRunIssue;
    const auditTrailIssue = validateBrowserSetupRunAuditTrail(`SetupRun diagnostics sourcePath ${sourcePath}: ${dropName}`, drop);
    if (auditTrailIssue) return auditTrailIssue;
    const evidenceIssue = validateBrowserSetupStateEvidence(`SetupRun diagnostics sourcePath ${sourcePath}: ${dropName}`, dropName, drop.evidence || {});
    if (evidenceIssue) return evidenceIssue;
  }
  return '';
}

function validateBrowserSetupStateEvidence(label, dropName, evidence) {
  const required = {
    'cloudreve-owner-bootstrap': {
      credentialRole: 'technical-admin-bootstrap',
      appLocalAccount: 'stackkit-admin-created',
      demoData: 'seeded-when-enabled',
      outerAuthBoundary: 'tinyauth-pocketid',
      ownerLogin: 'pocketid-passkey',
      identityBridge: 'stackkit-cloudreve-local-session',
      appLocalSessionHandoff: 'stackkit-session-bridge-prepared',
      readyToUseContentStatus: 'pending-browser-evidence',
    },
    'vaultwarden-admin-handoff': {
      credentialRole: 'break-glass-admin-token',
      ownerLogin: 'pocketid-passkey',
      adminTokenPosture: 'verified-break-glass',
      adminTokenStorage: 'argon2id-phc-runtime',
      appLocalSignups: 'disabled',
      plaintextAdminTokenEnv: 'absent',
      outerAuthBoundary: 'tinyauth-pocketid',
      appLocalOwner: 'pocketid-owner-preprovisioned',
      appLocalSessionHandoff: 'vaultwarden-invite-prepared',
      readyToUseContentStatus: 'owner-completes-vaultwarden-invite',
    },
    'immich-owner-bootstrap': {
      credentialRole: 'technical-admin-bootstrap',
      technicalAdmin: 'stackkit-admin-created',
      appLocalOwner: 'pocketid-owner-preprovisioned',
      demoData: 'seeded-when-enabled',
      outerAuthBoundary: 'tinyauth-pocketid',
      ownerLogin: 'pocketid-passkey',
      pocketidOAuth: 'enabled',
      oidcClientId: 'stackkit-immich',
      autoRegister: 'false',
      autoLaunch: 'true',
      appLocalSessionHandoff: 'oidc-email-link-prepared',
    },
  };
  for (const [key, want] of Object.entries(required[dropName] || {})) {
    if (String(evidence[key] || '').trim() !== want) {
      return `${label} evidence[${key}] is ${evidence[key] || 'missing'}, want ${want}`;
    }
  }
  if (dropName === 'immich-owner-bootstrap' || dropName === 'vaultwarden-admin-handoff') {
    if (!String(evidence.ownerEmail || '').trim() || !String(evidence.ownerProvisioning || '').trim()) {
      return `${label} must include Owner handoff evidence`;
    }
  }
  if (dropName === 'immich-owner-bootstrap') {
    if (!String(evidence.oidcIssuer || '').trim().startsWith('http')) {
      return `${label} evidence[oidcIssuer] is ${evidence.oidcIssuer || 'missing'}, want URL evidence`;
    }
  }
  if (dropName === 'vaultwarden-admin-handoff') {
    const provisioning = String(evidence.ownerProvisioning || '').trim();
    if (!['vaultwarden-admin-invite-created', 'vaultwarden-admin-invite-already-exists'].includes(provisioning)) {
      return `${label} evidence[ownerProvisioning] is ${evidence.ownerProvisioning || 'missing'}, want Vaultwarden admin invite evidence`;
    }
  }
  return '';
}

function parseSetupRunDrops(content) {
  const drops = new Map();
  const lines = String(content || '').split(/\r?\n/);
  let inSetupRuns = false;
  let setupIndent = 0;
  let current = null;
  let currentFieldIndent = null;
  let currentListKey = '';
  let currentMapKey = '';

  const flush = () => {
    if (current?.dropName) {
      drops.set(current.dropName, {
        runId: current.runId || '',
        status: current.status || '',
        phase: current.phase || '',
        serviceKey: current.serviceKey || '',
        failureClass: current.failureClass || '',
        attempts: current.attempts || '',
        lastRequested: current.lastRequested || '',
        lastStarted: current.lastStarted || '',
        lastFinished: current.lastFinished || '',
        logCount: current.logCount || '',
        rollbackNoteCount: current.rollbackNoteCount || '',
        evidence: current.evidence || {},
      });
    }
    current = null;
    currentFieldIndent = null;
    currentListKey = '';
    currentMapKey = '';
  };

  for (const rawLine of lines) {
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
      currentFieldIndent = null;
      const inline = trimmed.slice(2).trim();
      const inlinePair = parseYAMLScalarPair(inline);
      if (inlinePair) {
        assignSetupRunField(current, inlinePair.key, inlinePair.value);
      }
      continue;
    }
    if (!current) continue;
    if (currentFieldIndent === null) currentFieldIndent = indent;
    if (currentMapKey && indent > currentFieldIndent) {
      const pair = parseYAMLScalarPair(trimmed);
      if (pair) {
        current[currentMapKey] = current[currentMapKey] || {};
        current[currentMapKey][pair.key] = pair.value;
      }
      continue;
    }
    if (indent !== currentFieldIndent) continue;
    const pair = parseYAMLScalarPair(trimmed);
    if (pair) {
      assignSetupRunField(current, pair.key, pair.value);
      currentListKey = current._currentListKey || '';
      currentMapKey = current._currentMapKey || '';
      delete current._currentListKey;
      delete current._currentMapKey;
    }
  }
  flush();
  return drops;
}

function parseYAMLScalarPair(line) {
  const index = line.indexOf(':');
  if (index < 0) return null;
  const key = line.slice(0, index).trim();
  const value = cleanYAMLScalar(line.slice(index + 1));
  if (!key) return null;
  return { key, value };
}

function assignSetupRunField(target, key, value) {
  if (['logs', 'rollbackNotes'].includes(key) && value === '') {
    const countKey = key === 'logs' ? 'logCount' : 'rollbackNoteCount';
    target[countKey] = '0';
    target._currentListKey = key === 'logs' ? 'log' : 'rollbackNote';
    return;
  }
  if (key === 'evidence' && value === '') {
    target.evidence = {};
    target._currentMapKey = 'evidence';
    return;
  }
  if (['runId', 'dropName', 'status', 'phase', 'serviceKey', 'failureClass', 'attempts', 'lastRequested', 'lastStarted', 'lastFinished'].includes(key)) {
    target[key] = value;
  }
}

function cleanYAMLScalar(value) {
  return String(value || '').trim().replace(/^['"]|['"]$/g, '').trim();
}

function validateBrowserEvidenceCheckContent(check, ownerEmail = '') {
  const evidence = check && typeof check.evidence === 'object' && check.evidence ? check.evidence : {};
  if (check.name === 'pocketid-owner-passkey') {
    const count = Number(evidence.passkeyCredentials || 0);
    if (evidence.verification !== 'webauthn-virtual-authenticator' || !Number.isInteger(count) || count < 1) {
      return 'missing PocketID WebAuthn passkey credential evidence';
    }
    if (evidence.authenticatorProtocol !== 'ctap2') {
      return `PocketID authenticatorProtocol ${evidence.authenticatorProtocol || 'missing'} must be ctap2`;
    }
    if (!String(evidence.authenticatorTransport || '').trim()) {
      return 'missing PocketID authenticator transport evidence';
    }
  }
  if (check.name === 'tinyauth-owner-session') {
    const cookieCount = Number(evidence.sessionCookieCount || 0);
    const forwardAuthStatus = Number(evidence.forwardAuthStatus || 0);
    if (
      evidence.verification !== 'tinyauth-forwardauth-session' ||
      evidence.authBoundary !== 'tinyauth-pocketid'
    ) {
      return 'missing TinyAuth/PocketID ForwardAuth Owner-session evidence';
    }
    if (!Number.isInteger(cookieCount) || cookieCount < 1) {
      return `TinyAuth sessionCookieCount ${evidence.sessionCookieCount || 'missing'} must be >= 1`;
    }
    if (!Number.isInteger(forwardAuthStatus) || forwardAuthStatus < 200 || forwardAuthStatus > 299) {
      return `TinyAuth forwardAuthStatus ${evidence.forwardAuthStatus || 'missing'} must be 2xx`;
    }
    if (!String(evidence.sessionCookieNames || '').trim()) {
      return 'missing TinyAuth session cookie names';
    }
    if (!String(evidence.sessionCookieDomains || '').trim()) {
      return 'missing TinyAuth session cookie domains';
    }
    if (!['forwardauth-2xx', 'logout', 'signed-in', 'owner'].includes(String(evidence.ownerSessionSignal || '').trim())) {
      return `TinyAuth ownerSessionSignal ${evidence.ownerSessionSignal || 'missing'} is not an authenticated Owner-session signal`;
    }
    for (const [label, value] of [
      ['authUrl', evidence.authUrl],
      ['sessionUrl', evidence.sessionUrl],
      ['forwardAuthEndpoint', evidence.forwardAuthEndpoint],
    ]) {
      const issue = validateBrowserURL(value);
      if (issue) return `TinyAuth ${label} ${issue}`;
    }
  }
  if (check.name === 'photos-demo-content') {
    const demoModeIssue = browserDemoDataModeIssue(evidence);
    if (demoModeIssue) return demoModeIssue;
    if (String(evidence.demoData || '').trim() === 'disabled') {
      if (evidence.demoContent !== 'immich-owner-session' || evidence.verification !== 'immich-users-me') {
        return 'missing Immich Owner-session evidence for the demoData=disabled rollout';
      }
      if (evidence.ownerVerification !== 'immich-users-me') {
        return 'missing Immich Owner browser-session evidence from /api/users/me';
      }
      const sessionOwnerEmail = String(evidence.immichOwnerEmail || '').trim();
      if (!sessionOwnerEmail || sessionOwnerEmail.toLowerCase() !== String(ownerEmail || '').trim().toLowerCase()) {
        return `Immich immichOwnerEmail ${sessionOwnerEmail || 'missing'} must match Owner ${ownerEmail || 'missing'}`;
      }
      for (const field of ['immichDemoAssets', 'demoAssetDeviceId', 'demoAssetDeviceAssetId', 'demoAssetFile']) {
        if (String(evidence[field] || '').trim()) {
          return `demoData=disabled Photos evidence must not carry demo-asset field ${field}`;
        }
      }
    } else {
      const count = Number(evidence.immichDemoAssets || 0);
      if (evidence.demoContent !== 'immich-demo-assets' || !Number.isInteger(count) || count < 1) {
        return 'missing Immich seeded demo asset evidence';
      }
      if (evidence.verification !== 'immich-search-metadata') {
        return 'missing Immich StackKit demo asset metadata-search evidence';
      }
      if (evidence.ownerVerification !== 'immich-users-me') {
        return 'missing Immich Owner browser-session evidence from /api/users/me';
      }
      const sessionOwnerEmail = String(evidence.immichOwnerEmail || '').trim();
      if (!sessionOwnerEmail || sessionOwnerEmail.toLowerCase() !== String(ownerEmail || '').trim().toLowerCase()) {
        return `Immich immichOwnerEmail ${sessionOwnerEmail || 'missing'} must match Owner ${ownerEmail || 'missing'}`;
      }
      if (
        evidence.demoAssetDeviceId !== 'stackkit-demo' ||
        evidence.demoAssetDeviceAssetId !== 'stackkit-demo-photo-1' ||
        evidence.demoAssetFile !== 'stackkit-demo-photo.png'
      ) {
        return 'missing StackKit Immich demo photo identity evidence';
      }
    }
  }
  if (check.name === 'files-demo-content') {
    const demoModeIssue = browserDemoDataModeIssue(evidence);
    if (demoModeIssue) return demoModeIssue;
    if (String(evidence.demoData || '').trim() === 'disabled') {
      if (
        evidence.demoContent !== 'cloudreve-owner-session' ||
        evidence.verification !== 'cloudreve-browser-session-api' ||
        evidence.identityBridge !== 'stackkit-cloudreve-local-session' ||
        evidence.bridgeVerification !== 'stackkit-cloudreve-session-bridge'
      ) {
        return 'missing Cloudreve Owner-session StackKit session-bridge evidence for the demoData=disabled rollout';
      }
      const bridgeUser = String(evidence.bridgeCurrentUser || '').trim();
      const sessionUser = String(evidence.cloudreveSessionUser || '').trim();
      if (!bridgeUser || !sessionUser || bridgeUser !== sessionUser) {
        return `Cloudreve StackKit bridgeCurrentUser ${bridgeUser || 'missing'} and cloudreveSessionUser ${sessionUser || 'missing'} must match`;
      }
      for (const field of ['seededFolder', 'seededFile']) {
        if (String(evidence[field] || '').trim()) {
          return `demoData=disabled Files evidence must not carry seeded-content field ${field}`;
        }
      }
    } else {
      if (
        evidence.demoContent !== 'cloudreve-demo-file' ||
        evidence.seededFolder !== 'StackKit Demo' ||
        evidence.seededFile !== 'README.txt' ||
        evidence.verification !== 'cloudreve-browser-session-api' ||
        evidence.identityBridge !== 'stackkit-cloudreve-local-session' ||
        evidence.bridgeVerification !== 'stackkit-cloudreve-session-bridge'
      ) {
        return 'missing Cloudreve StackKit Demo/README.txt StackKit session-bridge evidence';
      }
      const bridgeUser = String(evidence.bridgeCurrentUser || '').trim();
      const sessionUser = String(evidence.cloudreveSessionUser || '').trim();
      if (!bridgeUser || !sessionUser || bridgeUser !== sessionUser) {
        return `Cloudreve StackKit bridgeCurrentUser ${bridgeUser || 'missing'} and cloudreveSessionUser ${sessionUser || 'missing'} must match`;
      }
    }
  }
  if (check.name === 'vault-auth-boundary') {
    if (
      evidence.verification !== 'anonymous-vault-route-check' ||
      evidence.authBoundary !== 'tinyauth-pocketid' ||
      evidence.anonymousAccess !== 'rejected'
    ) {
      return 'missing anonymous Vault TinyAuth/PocketID boundary evidence';
    }
    const signal = String(evidence.anonymousBoundarySignal || '').trim();
    if (!['http-401', 'http-403', 'tinyauth', 'pocketid', 'auth-host'].includes(signal)) {
      return `anonymous Vault boundary signal ${signal || 'missing'} is not a TinyAuth/PocketID or HTTP rejection signal`;
    }
    if (!String(evidence.anonymousStatus || '').trim()) {
      return 'missing anonymous Vault status evidence';
    }
    const anonymousURLIssue = validateBrowserURL(evidence.anonymousUrl);
    if (anonymousURLIssue) {
      return `anonymous Vault url ${anonymousURLIssue}`;
    }
  }
  return '';
}

// A missing demoData value keeps the original strict seeded-content contract
// so older manifests cannot silently downgrade to owner-session-only proof.
function browserDemoDataModeIssue(evidence) {
  const mode = String(evidence.demoData || '').trim();
  if (mode === '' || mode === 'enabled' || mode === 'disabled') return '';
  return `browser evidence demoData ${mode} must be enabled or disabled`;
}

async function validateBrowserEvidenceScreenshot(root, screenshotPath) {
  const raw = String(screenshotPath || '').trim();
  if (!raw) return 'empty path';
  const resolved = resolveEvidenceRelativePath(root, raw);
  if (resolved.issue) return resolved.issue;
  const clean = resolved.clean;
  const ext = path.extname(clean).toLowerCase();
  if (!['.png', '.jpg', '.jpeg', '.webp'].includes(ext)) {
    return 'path must end with .png, .jpg, .jpeg, or .webp';
  }
  let info;
  try {
    info = await stat(resolved.path);
  } catch (error) {
    return `file missing (${error.code || error.message})`;
  }
  if (!info.isFile()) return 'not a file';
  if (info.size === 0) return 'empty file';
  const file = await readFile(resolved.path);
  if (!hasSupportedScreenshotSignature(file.subarray(0, 12))) {
    return 'not a PNG, JPEG, or WebP screenshot';
  }
  const structureIssue = validateScreenshotStructure(file);
  if (structureIssue) {
    return structureIssue;
  }
  const dimensions = screenshotDimensions(file);
  if (dimensions.issue) {
    return dimensions.issue;
  }
  if (dimensions.width < MIN_BROWSER_SCREENSHOT_WIDTH || dimensions.height < MIN_BROWSER_SCREENSHOT_HEIGHT) {
    return `dimensions = ${dimensions.width}x${dimensions.height}, want at least ${MIN_BROWSER_SCREENSHOT_WIDTH}x${MIN_BROWSER_SCREENSHOT_HEIGHT}`;
  }
  return '';
}

function resolveEvidenceRelativePath(root, rawPath) {
  const raw = String(rawPath || '').trim();
  if (!raw) return { issue: 'empty path' };
  if (path.isAbsolute(raw) || raw.startsWith('/') || raw.startsWith('\\') || /^[A-Za-z]:[\\/]/.test(raw)) {
    return { issue: 'path must be relative' };
  }
  const clean = path.normalize(raw);
  if (clean === '..' || clean.startsWith(`..${path.sep}`)) {
    return { issue: 'path escapes evidence root' };
  }
  const rootDir = path.resolve(root || process.cwd());
  const full = path.resolve(rootDir, clean);
  const rel = path.relative(rootDir, full);
  if (rel === '..' || rel.startsWith(`..${path.sep}`) || path.isAbsolute(rel)) {
    return { issue: 'path escapes evidence root' };
  }
  return { path: full, clean };
}

function hasSupportedScreenshotSignature(header) {
  if (header.length >= 8 && header.subarray(0, 8).equals(Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]))) {
    return true;
  }
  if (header.length >= 3 && header[0] === 0xff && header[1] === 0xd8 && header[2] === 0xff) {
    return true;
  }
  return header.length >= 12 && header.subarray(0, 4).toString('ascii') === 'RIFF' && header.subarray(8, 12).toString('ascii') === 'WEBP';
}

function screenshotDimensions(buffer) {
  if (buffer.length >= 24 && buffer.subarray(0, 8).equals(Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]))) {
    return {
      width: buffer.readUInt32BE(16),
      height: buffer.readUInt32BE(20),
    };
  }
  if (buffer.length >= 3 && buffer[0] === 0xff && buffer[1] === 0xd8 && buffer[2] === 0xff) {
    return jpegDimensions(buffer);
  }
  if (buffer.length >= 12 && buffer.subarray(0, 4).toString('ascii') === 'RIFF' && buffer.subarray(8, 12).toString('ascii') === 'WEBP') {
    return webpDimensions(buffer);
  }
  return { issue: 'cannot read screenshot dimensions' };
}

function validateScreenshotStructure(buffer) {
  if (buffer.length >= 8 && buffer.subarray(0, 8).equals(Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]))) {
    return validatePNGStructure(buffer);
  }
  return '';
}

function validatePNGStructure(buffer) {
  let offset = 8;
  let sawIHDR = false;
  let sawIDAT = false;
  while (offset + 12 <= buffer.length) {
    const length = buffer.readUInt32BE(offset);
    const type = buffer.subarray(offset + 4, offset + 8).toString('ascii');
    const chunkEnd = offset + 12 + length;
    if (chunkEnd > buffer.length) {
      return `truncated PNG ${type || 'chunk'} chunk`;
    }
    if (!sawIHDR && type !== 'IHDR') {
      return 'PNG first chunk must be IHDR';
    }
    if (type === 'IHDR') {
      if (sawIHDR) return 'PNG contains duplicate IHDR chunk';
      if (length !== 13) return 'PNG IHDR chunk must be 13 bytes';
      sawIHDR = true;
    }
    if (type === 'IDAT') {
      sawIDAT = true;
    }
    if (type === 'IEND') {
      if (!sawIHDR) return 'PNG missing IHDR chunk';
      if (!sawIDAT) return 'PNG missing IDAT image data';
      if (chunkEnd !== buffer.length) return 'PNG contains trailing bytes after IEND';
      return '';
    }
    offset = chunkEnd;
  }
  if (!sawIHDR) return 'PNG missing IHDR chunk';
  if (!sawIDAT) return 'PNG missing IDAT image data';
  return 'PNG missing IEND chunk';
}

function jpegDimensions(buffer) {
  let offset = 2;
  while (offset < buffer.length) {
    while (offset < buffer.length && buffer[offset] !== 0xff) offset += 1;
    while (offset < buffer.length && buffer[offset] === 0xff) offset += 1;
    if (offset >= buffer.length) break;
    const marker = buffer[offset];
    offset += 1;
    if (marker === 0xd8 || marker === 0xd9) continue;
    if (offset + 2 > buffer.length) break;
    const length = buffer.readUInt16BE(offset);
    if (length < 2 || offset + length > buffer.length) break;
    if (marker >= 0xc0 && marker <= 0xcf && ![0xc4, 0xc8, 0xcc].includes(marker)) {
      if (length < 7) return { issue: 'short JPEG frame header' };
      return {
        height: buffer.readUInt16BE(offset + 3),
        width: buffer.readUInt16BE(offset + 5),
      };
    }
    offset += length;
  }
  return { issue: 'cannot read JPEG dimensions' };
}

function webpDimensions(buffer) {
  if (buffer.length < 16) return { issue: 'short WebP header' };
  const chunk = buffer.subarray(12, 16).toString('ascii');
  if (chunk === 'VP8X') {
    if (buffer.length < 30) return { issue: 'short VP8X header' };
    return {
      width: 1 + readUInt24LE(buffer, 24),
      height: 1 + readUInt24LE(buffer, 27),
    };
  }
  if (chunk === 'VP8L') {
    if (buffer.length < 25) return { issue: 'short VP8L header' };
    if (buffer[20] !== 0x2f) return { issue: 'invalid VP8L signature' };
    return {
      width: 1 + buffer[21] + ((buffer[22] & 0x3f) << 8),
      height: 1 + (buffer[22] >> 6) + (buffer[23] << 2) + ((buffer[24] & 0x0f) << 10),
    };
  }
  if (chunk === 'VP8 ') {
    if (buffer.length < 30) return { issue: 'short VP8 header' };
    if (buffer[23] !== 0x9d || buffer[24] !== 0x01 || buffer[25] !== 0x2a) {
      return { issue: 'invalid VP8 start code' };
    }
    return {
      width: buffer.readUInt16LE(26) & 0x3fff,
      height: buffer.readUInt16LE(28) & 0x3fff,
    };
  }
  return { issue: `unsupported WebP chunk ${chunk}` };
}

function readUInt24LE(buffer, offset) {
  return buffer[offset] | (buffer[offset + 1] << 8) | (buffer[offset + 2] << 16);
}

function browserEvidenceCheck(browserEvidence) {
  if (!browserEvidence) {
    return {
      status: 'pending',
      summary: 'SK-S1 browser evidence manifest has not been attached to release evidence.',
    };
  }
  return {
    status: browserEvidence.status,
    summary: browserEvidence.summary,
    url: browserEvidence.path,
  };
}

function browserPreflightCheck(browserPreflight) {
  if (!browserPreflight) {
    return {
      status: 'pending',
      summary: 'SK-S1 browser preflight report has not been attached to release evidence.',
    };
  }
  return {
    status: browserPreflight.status,
    summary: browserPreflight.summary,
    url: browserPreflight.path,
  };
}

async function main() {
  const opts = parseArgs(process.argv.slice(2));
  const outputPath = path.resolve(opts.output);
  const publicRelease = opts.visibility === 'public';
  // The existing browser inputs are Docker-development evidence without a
  // protected product-run producer identity. Public releases ignore them and
  // stay pending until a dedicated leased-Proxmox provenance contract exists.
  const browserPreflight = publicRelease ? null : await loadBrowserPreflight(opts.browserPreflight);
  const browserEvidence = publicRelease
    ? null
    : await loadBrowserEvidence(
        opts.browserEvidence,
        opts.browserEvidenceRoot,
        browserPreflight?.browserChannel || '',
        browserPreflight?.evidenceRoot || '',
        browserPreflight?.runId || '',
      );
  const scenarioEvidence = [...opts.scenarioEvidence];
  for (const artifactPath of opts.scenarioArtifacts) {
    scenarioEvidence.push(await loadScenarioArtifactEvidence(artifactPath));
  }
  if (browserPreflight) {
    scenarioEvidence.push({
      scenarioId: `${browserPreflight.scenarioId}-browser-preflight`,
      status: browserPreflight.status,
      summary: browserPreflight.summary,
      url: browserPreflight.path,
    });
  }
  if (browserEvidence) {
    scenarioEvidence.push({
      scenarioId: `${browserEvidence.scenarioId}-browser`,
      status: browserEvidence.status,
      summary: browserEvidence.summary,
      url: browserEvidence.path,
    });
  }
  const boundedScenarioEvidence = enforcePublicSKS1EvidenceBoundary(scenarioEvidence, opts.visibility);
  const mergedScenarioEvidence = mergeRequiredScenarioEvidence(boundedScenarioEvidence);
  const releaseDefaults = releaseEvidenceDefaults(opts.tag);
  const evidence = {
    schemaVersion: '1.0.0',
    generatedAt: new Date().toISOString(),
    release: {
      tag: required(opts, 'tag'),
      commit: required(opts, 'commit'),
      sourceRepository: required(opts, 'sourceRepository'),
      releaseRepository: required(opts, 'releaseRepository'),
      visibility: opts.visibility,
      ...(opts.releaseUrl ? { releaseUrl: opts.releaseUrl } : {}),
      ...(opts.workflowRunId ? { workflowRunId: opts.workflowRunId } : {}),
      ...(opts.workflowRunUrl ? { workflowRunUrl: opts.workflowRunUrl } : {}),
    },
    artifacts: await collectArtifacts(path.resolve(opts.dist), outputPath),
    checks: {
      publicExport: checkFromMap(opts.checks, 'publicExport', 'pending'),
      archiveValidation: checkFromMap(opts.checks, 'archiveValidation', 'pending'),
      candidateE2E: checkFromMap(
        opts.checks,
        'candidateE2E',
        'pending',
        'Exact-source device Candidate evidence has not been attached to this release.',
      ),
      securityScans: checkFromMap(opts.checks, 'securityScans', 'pending'),
      securityBaseline: securityBaselineReleaseCheck(mergedScenarioEvidence),
      liveInstallerSmoke: checkFromMap(opts.checks, 'liveInstallerSmoke', 'pending'),
      freshUbuntuBaseKit: publicRelease
        ? publicSKS1PendingCheck()
        : checkFromMap(opts.checks, 'freshUbuntuBaseKit', 'pending'),
      browserPreflight: publicRelease
        ? publicSKS1PendingCheck(PUBLIC_BROWSER_PENDING_SUMMARY)
        : opts.checks.has('browserPreflight')
          ? opts.checks.get('browserPreflight')
          : browserPreflightCheck(browserPreflight),
      browserEvidence: publicRelease
        ? publicSKS1PendingCheck(PUBLIC_BROWSER_PENDING_SUMMARY)
        : opts.checks.has('browserEvidence')
          ? opts.checks.get('browserEvidence')
          : browserEvidenceCheck(browserEvidence),
      upgradeRollbackVm: checkFromMap(opts.checks, 'upgradeRollbackVm', 'pending'),
      defaultL3PaaSDelivery: checkFromMap(opts.checks, 'defaultL3PaaSDelivery', 'pending'),
      osCompatMatrix: checkFromMap(opts.checks, 'osCompatMatrix', 'pending', 'Public OS compatibility is unverified while the controlled HostConformanceReceipt projector is unavailable.'),
      attestationVerification: checkFromMap(opts.checks, 'attestationVerification', 'pending'),
    },
    scenarioEvidence: mergedScenarioEvidence,
    pendingGates: mergePendingGates(opts.pendingGates, mergedScenarioEvidence),
    missingAlternatives: mergeRequiredMissingAlternatives(
      opts.missingAlternatives,
      releaseDefaults.missingAlternatives,
    ),
    knownLimitations: mergeRequiredKnownLimitations(
      opts.knownLimitations.length
        ? opts.knownLimitations
        : releaseDefaults.defaultKnownLimitations,
      releaseDefaults.requiredKnownLimitations,
      !publicRelease && browserEvidence?.status === 'pass',
    ),
  };

  await writeFile(outputPath, `${JSON.stringify(evidence, null, 2)}\n`);
}

main().catch((err) => {
  console.error(err.message);
  process.exit(1);
});
