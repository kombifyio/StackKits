#!/usr/bin/env node
import { readFile, readdir } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const CANONICAL_RELEASE_SCENARIOS = new Set(['SK-S1', 'SK-S2', 'SK-S3', 'SK-S5']);
const VALID_ARTIFACT_STATUS = new Set(['pass', 'passed', 'success', 'succeeded']);
const SYSTEM_PLATFORM_APPS_BY_SERVICE_KEY = new Map([
  ['base', 'stackkit-hub'],
  ['kuma', 'uptime-kuma'],
  ['whoami', 'whoami'],
]);
const L3_PLATFORM_APPS_BY_SERVICE_KEY = new Map([
  ['vault', { appName: 'vaultwarden', policyKey: 'vaultwarden' }],
  ['photos', { appName: 'immich', policyKey: 'immich' }],
  ['files', { appName: 'cloudreve', policyKey: 'files' }],
]);

export async function validateScenarioArtifactFile(filePath, options = {}) {
  const errors = [];
  let artifact;
  try {
    artifact = JSON.parse(await readFile(filePath, 'utf8'));
  } catch (error) {
    return [`${filePath}: ${error.code || error.message}`];
  }

  const scenariosDir = options.scenariosDir || path.join(repoRoot(), 'tests', 'scenarios');
  const scenario = await loadScenarioByID(String(artifact?.scenarioId || '').trim(), scenariosDir);
  validateScenarioArtifact(errors, artifact, scenario);
  return errors;
}

export function validateScenarioArtifact(errors, artifact, scenario) {
  if (!artifact || typeof artifact !== 'object' || Array.isArray(artifact)) {
    errors.push('artifact must be an object');
    return;
  }

  const scenarioId = stringValue(artifact.scenarioId);
  if (!scenarioId) {
    errors.push('scenarioId must be present');
  } else if (!CANONICAL_RELEASE_SCENARIOS.has(scenarioId)) {
    errors.push(`scenarioId ${scenarioId} is not a canonical v0.4 release scenario`);
  }
  if (!scenario) {
    errors.push(`canonical scenario ${scenarioId || '<missing>'} was not found`);
  }
  if (!stringValue(artifact.runId)) {
    errors.push('runId must be present');
  }
  if (!VALID_ARTIFACT_STATUS.has(stringValue(artifact.status).toLowerCase())) {
    errors.push(`status must be passing, got ${artifact.status || '<missing>'}`);
  }
  if (!Number.isFinite(Date.parse(stringValue(artifact.generatedAt)))) {
    errors.push('generatedAt must be RFC3339');
  }

  validateProfile(errors, artifact.profile, scenario?.expected?.profile || {}, scenarioId);
  validateTarget(errors, artifact.target, scenario?.expected?.target || {});
  validateSimulation(errors, artifact.simulation, scenario?.expected?.simulation || {});
  validateSimulationStatus(errors, artifact.simulationStatus, scenario?.expected?.simulation || {}, artifact);
  validateReachability(errors, artifact, scenario);
  validatePlatformAppEvidence(errors, artifact, scenario);
}

function validateProfile(errors, profile, expectedProfile, scenarioId) {
  if (!profile || typeof profile !== 'object' || Array.isArray(profile)) {
    errors.push('profile must be an object');
    return;
  }
  for (const field of [
    'adminProfileKey',
    'domain',
    'mailMode',
    'ownerMode',
    'ownerSource',
    'paas',
    'bootstrapMode',
  ]) {
    const expected = stringValue(expectedProfile[field]);
    if (!expected) continue;
    const got = stringValue(profile[field]);
    if (!profileFieldMatchesScenario(field, got, expected, scenarioId)) {
      errors.push(`profile.${field} = ${got || '<missing>'}, want ${expected}`);
    }
  }
  if (typeof expectedProfile.demoDataEnabled === 'boolean' && profile.demoDataEnabled !== expectedProfile.demoDataEnabled) {
    errors.push(`profile.demoDataEnabled = ${profile.demoDataEnabled}, want ${expectedProfile.demoDataEnabled}`);
  }
}

function profileFieldMatchesScenario(field, got, expected, scenarioId) {
  if (field === 'domain' && scenarioId === 'SK-S3') {
    return domainMatchesExpectedZone(got, expected);
  }
  return got === expected;
}

function validateTarget(errors, target, expectedTarget) {
  if (!expectedTarget || Object.keys(expectedTarget).length === 0) return;
  if (!target || typeof target !== 'object' || Array.isArray(target)) {
    errors.push('target must be an object');
    return;
  }

  const lane = stringValue(expectedTarget.lane);
  const runtime = stringValue(expectedTarget.runtime);
  if (lane === 'docker-desktop-local') {
    if (!stringValue(target.host) && !stringValue(target.containerName)) {
      errors.push('target.host or target.containerName must be present for docker-desktop-local scenarios');
    }
    return;
  }
  if (runtime === 'managed-lease' || runtime === 'linux-server') {
    if (!stringValue(target.publicIp)) {
      errors.push(`target.publicIp must be present for ${runtime} scenarios`);
    }
  }
}

function validateSimulation(errors, simulation, expectedSimulation) {
  if (!simulation || typeof simulation !== 'object' || Array.isArray(simulation)) {
    errors.push('simulation must be an object');
    return;
  }
  compareStringArrays(errors, 'simulation.setupActions', simulation.setupActions, expectedSimulation.setupActions);
  compareStringArrays(errors, 'simulation.seededContent', simulation.seededContent, expectedSimulation.seededContent);
  compareStringArrays(errors, 'simulation.healthChecks', simulation.healthChecks, expectedSimulation.healthChecks);
}

function validateSimulationStatus(errors, simulationStatus, expectedSimulation, artifact = {}) {
  const expectedHealthChecks = normalizeStringArray(expectedSimulation.healthChecks);
  const expectedSetupActions = normalizeStringArray(expectedSimulation.setupActions);
  if (expectedHealthChecks.length === 0 && expectedSetupActions.length === 0) {
    if (simulationStatus && Object.keys(simulationStatus).length > 0) {
      errors.push('simulationStatus must be omitted or empty when the canonical scenario has no health checks or setup actions');
    }
    return;
  }
  if (!simulationStatus || typeof simulationStatus !== 'object' || Array.isArray(simulationStatus)) {
    errors.push('simulationStatus must be present for scenarios with health checks or setup actions');
    return;
  }
  const onDemandSetupActions = onDemandSetupActionEvidence(artifact);
  const observedSetupActions = normalizeStringArray(simulationStatus.observedSetupActions);
  const missingSetupActions = normalizeStringArray(simulationStatus.missingSetupActions);
  const blockingMissingSetupActions = missingSetupActions.filter((action) => !onDemandSetupActions.has(action));
  const observed = normalizeStringArray(simulationStatus.observedHealthChecks);
  const missing = normalizeStringArray(simulationStatus.missingHealthChecks);
  const unobservedSetupActions = expectedSetupActions.filter(
    (action) => !observedSetupActions.includes(action) && !onDemandSetupActions.has(action),
  );
  const unobservedHealthChecks = expectedHealthChecks.filter((check) => !observed.includes(check));
  const simulationStatusValue = stringValue(simulationStatus.status);
  const incompleteOnlyForOnDemandSetup =
    simulationStatusValue === 'incomplete' &&
    blockingMissingSetupActions.length === 0 &&
    missing.length === 0 &&
    unobservedSetupActions.length === 0 &&
    unobservedHealthChecks.length === 0;
  if (simulationStatusValue !== 'pass' && !incompleteOnlyForOnDemandSetup) {
    errors.push(`simulationStatus.status = ${simulationStatus.status || '<missing>'}, want pass`);
  }
  for (const action of unobservedSetupActions) {
    errors.push(`simulationStatus.observedSetupActions missing ${action}`);
  }
  if (blockingMissingSetupActions.length > 0) {
    errors.push(`simulationStatus.missingSetupActions must be empty, got ${blockingMissingSetupActions.join(',')}`);
  }

  for (const check of unobservedHealthChecks) {
    errors.push(`simulationStatus.observedHealthChecks missing ${check}`);
  }
  if (missing.length > 0) {
    errors.push(`simulationStatus.missingHealthChecks must be empty, got ${missing.join(',')}`);
  }
}

function validateReachability(errors, artifact, scenario) {
  const expectedHealthChecks = normalizeStringArray(scenario?.expected?.simulation?.healthChecks);
  if (expectedHealthChecks.length === 0) return;
  const expectedAccess = scenario?.expected?.access || {};
  const expectedHubURL = stringValue(expectedAccess.hubUrl);
  const reachabilityContext = reachabilityContextForArtifact(artifact, scenario);
  if (expectedHubURL && !accessURLMatchesScenario(stringValue(artifact.hubUrl), expectedHubURL, 'base', reachabilityContext)) {
    errors.push(`hubUrl = ${stringValue(artifact.hubUrl) || '<missing>'}, want ${expectedHubURL}`);
  }

  if (!stringValue(artifact.browserUrl)) {
    errors.push('browserUrl must be present for reachable scenarios');
  } else if (expectedHubURL && !accessURLMatchesScenario(stringValue(artifact.browserUrl), expectedHubURL, 'base', reachabilityContext)) {
    errors.push(`browserUrl = ${stringValue(artifact.browserUrl)}, want ${expectedHubURL}`);
  }
  const services = Array.isArray(artifact.services) ? artifact.services : [];
  if (services.length === 0) {
    errors.push('services must include observed service URLs');
    return;
  }
  const observedKeys = new Set(
    services
      .map((service) => stringValue(service?.key))
      .filter(Boolean),
  );
  const observedByKey = new Map(
    services
      .map((service) => [stringValue(service?.key), service])
      .filter(([key]) => Boolean(key)),
  );
  const expectedByKey = new Map(
    (Array.isArray(expectedAccess.services) ? expectedAccess.services : [])
      .map((service) => [stringValue(service?.key), service])
      .filter(([key]) => Boolean(key)),
  );
  if (stringValue(artifact.hubUrl)) {
    observedKeys.add('base');
  }
  for (const check of expectedHealthChecks) {
    const key = serviceKeyForHealthCheck(check);
    if (key && !observedKeys.has(key)) {
      errors.push(`services missing observed key ${key} for ${check}`);
      continue;
    }
    if (key) {
      validateObservedServiceAccess(errors, observedByKey.get(key), expectedByKey.get(key), key, {
        ...reachabilityContext,
      });
    }
  }
}

function validateObservedServiceAccess(errors, observed, expected, key, context = {}) {
  if (!observed || !expected) return;
  const expectedHost = expectedHostForObservedService(expected, key, context);
  const expectedScheme = stringValue(expected.scheme);
  const expectedPath = stringValue(expected.path);
  const observedHost = stringValue(observed.host);
  const observedURL = stringValue(observed.url);
  if (expectedHost && observedHost !== expectedHost) {
    errors.push(`services[${key}].host = ${observedHost || '<missing>'}, want ${expectedHost}`);
  }
  if (!observedURL) {
    errors.push(`services[${key}].url must be present`);
    return;
  }
  let parsed;
  try {
    parsed = new URL(observedURL);
  } catch {
    errors.push(`services[${key}].url is not a valid URL: ${observedURL}`);
    return;
  }
  const observedScheme = parsed.protocol.replace(/:$/, '');
  if (expectedScheme && observedScheme !== expectedScheme) {
    errors.push(`services[${key}].url scheme = ${observedScheme || '<missing>'}, want ${expectedScheme}`);
  }
  if (expectedHost && parsed.hostname !== expectedHost) {
    errors.push(`services[${key}].url host = ${parsed.hostname || '<missing>'}, want ${expectedHost}`);
  }
  if (expectedPath && parsed.pathname !== expectedPath) {
    errors.push(`services[${key}].url path = ${parsed.pathname || '<missing>'}, want ${expectedPath}`);
  }
}

function expectedHostForObservedService(expected, key, context) {
  const expectedHost = stringValue(expected.host);
  if (context?.scenarioId === 'SK-S2' && context?.kombifyMePrefix) {
    return `${context.kombifyMePrefix}-${key}.kombify.me`;
  }
  if (
    context?.scenarioId === 'SK-S3' &&
    domainMatchesExpectedZone(stringValue(context.profileDomain), stringValue(context.expectedProfileDomain))
  ) {
    const label = expectedHost ? expectedHost.split('.')[0] : key;
    if (label) return `${label}.${stringValue(context.profileDomain)}`;
  }
  return expectedHost;
}

function reachabilityContextForArtifact(artifact, scenario) {
  const scenarioId = stringValue(artifact.scenarioId);
  const context = {
    scenarioId,
    profileDomain: stringValue(artifact.profile?.domain),
    expectedProfileDomain: stringValue(scenario?.expected?.profile?.domain),
  };
  if (scenarioId === 'SK-S2') {
    const prefix = observedKombifyMePrefix(artifact);
    if (prefix) context.kombifyMePrefix = prefix;
  }
  return context;
}

function observedKombifyMePrefix(artifact) {
  const services = Array.isArray(artifact?.services) ? artifact.services : [];
  const base = services.find((service) => stringValue(service?.key) === 'base');
  const basePrefix = kombifyMePrefixForHost(stringValue(base?.host), 'base');
  if (basePrefix) return basePrefix;

  const hubPrefix = kombifyMePrefixForHost(hostnameFromURL(stringValue(artifact?.hubUrl)), 'base');
  if (hubPrefix) return hubPrefix;

  for (const service of services) {
    const key = stringValue(service?.key);
    const prefix = kombifyMePrefixForHost(stringValue(service?.host), key);
    if (prefix) return prefix;
  }
  return '';
}

function kombifyMePrefixForHost(host, key) {
  if (!host || !key) return '';
  const suffix = `-${key}.kombify.me`;
  if (!host.endsWith(suffix)) return '';
  return host.slice(0, -suffix.length);
}

function accessURLMatchesScenario(gotURL, expectedURL, key, context) {
  if (gotURL === expectedURL) return true;
  const expectedForRun = expectedURLForDynamicService(expectedURL, key, context);
  return Boolean(expectedForRun && gotURL === expectedForRun);
}

function expectedURLForDynamicService(expectedURL, key, context) {
  if (context?.scenarioId !== 'SK-S2' || !context?.kombifyMePrefix) return '';
  let parsed;
  try {
    parsed = new URL(expectedURL);
  } catch {
    return '';
  }
  parsed.hostname = `${context.kombifyMePrefix}-${key}.kombify.me`;
  const path = parsed.pathname === '/' ? '' : parsed.pathname;
  return `${parsed.protocol}//${parsed.host}${path}${parsed.search}${parsed.hash}`;
}

function hostnameFromURL(rawURL) {
  if (!rawURL) return '';
  try {
    return new URL(rawURL).hostname;
  } catch {
    return '';
  }
}

function domainMatchesExpectedZone(domain, expectedZone) {
  if (!domain || !expectedZone) return false;
  return domain === expectedZone || domain.endsWith(`.${expectedZone}`);
}

function onDemandSetupActionEvidence(artifact) {
  const actions = new Set();
  const apps = [
    ...(Array.isArray(artifact?.platformSystemApps) ? artifact.platformSystemApps : []),
    ...(Array.isArray(artifact?.platformApps) ? artifact.platformApps : []),
  ];
  for (const app of apps) {
    if (normalizedPolicy(app?.setupPolicy) !== 'on_demand') continue;
    if (!stringValue(app?.externalId)) continue;
    if (!platformAppEvidenceAcceptable(stringValue(app?.observedStatus), 'on_demand')) continue;
    for (const drop of Array.isArray(app?.setupDrops) ? app.setupDrops : []) {
      const name = stringValue(drop?.name);
      if (name) actions.add(name);
    }
  }
  return actions;
}

function validatePlatformAppEvidence(errors, artifact, scenario) {
  const expected = scenario?.expected || {};
  const expectedPlatform = stringValue(expected.profile?.paas) || stringValue(expected.generation?.paas);
  if (!expectedPlatform || expectedPlatform === 'none') return;
  const setupPolicies = expected.generation?.setupPolicies || {};
  const expectedServiceKeys = new Set(
    (Array.isArray(expected.access?.services) ? expected.access.services : [])
      .map((service) => stringValue(service?.key))
      .filter(Boolean),
  );

  const platformPolicy = normalizedPolicy(setupPolicies.platform);
  if (platformPolicy && platformPolicy !== 'manual') {
    const requiredSystemApps = new Set();
    if (stringValue(expected.access?.hubUrl)) {
      requiredSystemApps.add('stackkit-hub');
      requiredSystemApps.add('stackkit-server');
    }
    for (const [serviceKey, appName] of SYSTEM_PLATFORM_APPS_BY_SERVICE_KEY.entries()) {
      if (expectedServiceKeys.has(serviceKey)) {
        requiredSystemApps.add(appName);
      }
    }
    for (const appName of requiredSystemApps) {
      validatePlatformAppRef(errors, 'platformSystemApps', artifact.platformSystemApps, appName, expectedPlatform, platformPolicy);
    }
  }

  for (const [serviceKey, app] of L3_PLATFORM_APPS_BY_SERVICE_KEY.entries()) {
    if (!expectedServiceKeys.has(serviceKey)) continue;
    const policy = normalizedPolicy(setupPolicies[app.policyKey]) || normalizedPolicy(setupPolicies.applicationDefault);
    if (!policy || policy === 'manual') continue;
    validatePlatformAppRef(errors, 'platformApps', artifact.platformApps, app.appName, expectedPlatform, policy);
  }
}

function validatePlatformAppRef(errors, label, refs, appName, expectedPlatform, expectedPolicy) {
  if (!Array.isArray(refs)) {
    errors.push(`${label} must include managed platform app evidence for ${appName}`);
    return;
  }
  const ref = refs.find((item) => stringValue(item?.name) === appName);
  if (!ref) {
    errors.push(`${label} missing managed platform app ${appName}`);
    return;
  }
  const platform = stringValue(ref.platform);
  if (platform !== expectedPlatform) {
    errors.push(`${label}[${appName}].platform = ${platform || '<missing>'}, want ${expectedPlatform}`);
  }
  const management = stringValue(ref.management);
  if (management !== 'managed') {
    errors.push(`${label}[${appName}].management = ${management || '<missing>'}, want managed`);
  }
  const externalID = stringValue(ref.externalId);
  if (!externalID) {
    errors.push(`${label}[${appName}].externalId must be present`);
  } else if (externalID.startsWith('local-compose:')) {
    errors.push(`${label}[${appName}].externalId must be a selected-PaaS id, got ${externalID}`);
  }
  const observedStatus = stringValue(ref.observedStatus);
  if (!platformAppEvidenceAcceptable(observedStatus, expectedPolicy)) {
    errors.push(`${label}[${appName}].observedStatus = ${observedStatus || '<missing>'}, want running/docker:running${expectedPolicy === 'on_demand' ? ' or deploy:accepted' : ''}`);
  }
  if (!Number.isFinite(Date.parse(stringValue(ref.observedAt)))) {
    errors.push(`${label}[${appName}].observedAt must be RFC3339`);
  }
}

function platformAppEvidenceAcceptable(status, setupPolicy) {
  const normalizedStatus = stringValue(status).toLowerCase();
  if (normalizedStatus.startsWith('running') || normalizedStatus === 'docker:running') {
    return true;
  }
  return setupPolicy === 'on_demand' && normalizedStatus === 'deploy:accepted';
}

function normalizedPolicy(value) {
  return stringValue(value).toLowerCase();
}

async function loadScenarioByID(id, scenariosDir) {
  if (!id) return null;
  const entries = await readdir(scenariosDir, { withFileTypes: true });
  for (const entry of entries) {
    if (entry.isDirectory() || path.extname(entry.name) !== '.json') continue;
    const filePath = path.join(scenariosDir, entry.name);
    const scenario = JSON.parse(await readFile(filePath, 'utf8'));
    if (scenario.id === id) return scenario;
  }
  return null;
}

function compareStringArrays(errors, label, gotValue, expectedValue) {
  const got = normalizeStringArray(gotValue).sort();
  const expected = normalizeStringArray(expectedValue).sort();
  if (got.length !== expected.length || got.some((value, index) => value !== expected[index])) {
    errors.push(`${label} = [${got.join(',')}], want [${expected.join(',')}]`);
  }
}

function normalizeStringArray(value) {
  if (!Array.isArray(value)) return [];
  return value.map((item) => stringValue(item)).filter(Boolean);
}

function serviceKeyForHealthCheck(check) {
  return stringValue(check)
    .replace(/-protected-route$/, '')
    .replace(/-route$/, '');
}

function stringValue(value) {
  return String(value ?? '').trim();
}

function repoRoot() {
  return path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
}

async function main() {
  const filePath = process.argv[2];
  if (!filePath) {
    throw new Error('usage: validate-scenario-artifact.mjs <artifacts/scenarios/<id>/homelab.json>');
  }
  const errors = await validateScenarioArtifactFile(filePath);
  if (errors.length > 0) {
    console.error(errors.join('\n'));
    process.exitCode = 1;
    return;
  }
  console.log(`Scenario artifact contract passed: ${filePath}`);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  main().catch((error) => {
    console.error(error.message);
    process.exitCode = 1;
  });
}
