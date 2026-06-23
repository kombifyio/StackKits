#!/usr/bin/env node
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const STATUS_VALUES = new Set(['pass', 'fail', 'pending', 'not_applicable']);
const VISIBILITY_VALUES = new Set(['public', 'internal', 'private', 'unknown']);
const ARTIFACT_KINDS = new Set(['archive', 'checksum', 'sbom', 'evidence', 'package', 'other']);
const REQUIRED_CHECKS = [
  'publicExport',
  'archiveValidation',
  'securityScans',
  'securityBaseline',
  'liveInstallerSmoke',
  'freshUbuntuBaseKit',
  'browserPreflight',
  'browserEvidence',
  'upgradeRollbackVm',
  'defaultL3PaaSDelivery',
  'attestationVerification',
];
const REQUIRED_SCENARIOS = ['SK-S1', 'SK-S2', 'SK-S3', 'SK-S5'];
const SECURITY_BASELINE_SCENARIOS = ['SK-S1', 'SK-S2', 'SK-S3'];
const REQUIRED_MISSING_ALTERNATIVE_PREFIXES = ['Photos ', 'Vault '];
const RELEASE_TAG_PATTERN = /^v[0-9]+\.[0-9]+\.[0-9]+([-.].+)?$/;
const SHA256_PATTERN = /^[a-f0-9]{64}$/;

export async function validateReleaseEvidenceFile(filePath) {
  const raw = await readFile(filePath, 'utf8');
  let evidence;
  try {
    evidence = JSON.parse(raw);
  } catch (err) {
    return [`${filePath}: invalid JSON: ${err.message}`];
  }
  return validateReleaseEvidence(evidence);
}

export function validateReleaseEvidence(evidence) {
  const errors = [];
  if (!isObject(evidence)) {
    return ['release evidence must be a JSON object'];
  }

  if (evidence.schemaVersion !== '1.0.0') {
    errors.push('schemaVersion must be 1.0.0');
  }
  if (!isNonEmptyString(evidence.generatedAt) || Number.isNaN(Date.parse(evidence.generatedAt))) {
    errors.push('generatedAt must be an RFC3339 timestamp string');
  }

  validateRelease(errors, evidence.release);
  validateArtifacts(errors, evidence.artifacts);
  validateChecks(errors, evidence.checks);
  validateStringArray(errors, 'pendingGates', evidence.pendingGates);
  validateStringArray(errors, 'knownLimitations', evidence.knownLimitations);
  validateStringArray(errors, 'missingAlternatives', evidence.missingAlternatives);
  validateRequiredMissingAlternatives(errors, evidence.missingAlternatives);
  validateScenarioEvidence(errors, evidence.scenarioEvidence, evidence.pendingGates);
  validateSecurityBaselineCheck(errors, evidence.checks, evidence.scenarioEvidence);

  return errors;
}

function validateRelease(errors, release) {
  if (!isObject(release)) {
    errors.push('release must be an object');
    return;
  }
  if (!isNonEmptyString(release.tag) || !RELEASE_TAG_PATTERN.test(release.tag)) {
    errors.push('release.tag must look like v0.0.0');
  }
  if (!isNonEmptyString(release.commit) || release.commit.length < 7) {
    errors.push('release.commit must be at least 7 characters');
  }
  for (const field of ['sourceRepository', 'releaseRepository']) {
    if (!isNonEmptyString(release[field])) {
      errors.push(`release.${field} must be a non-empty string`);
    }
  }
  if (!VISIBILITY_VALUES.has(release.visibility)) {
    errors.push(`release.visibility must be one of ${Array.from(VISIBILITY_VALUES).join(', ')}`);
  }
}

function validateArtifacts(errors, artifacts) {
  if (!Array.isArray(artifacts)) {
    errors.push('artifacts must be an array');
    return;
  }
  for (const [index, artifact] of artifacts.entries()) {
    if (!isObject(artifact)) {
      errors.push(`artifacts[${index}] must be an object`);
      continue;
    }
    if (!isNonEmptyString(artifact.name)) {
      errors.push(`artifacts[${index}].name must be a non-empty string`);
    }
    if (!ARTIFACT_KINDS.has(artifact.kind)) {
      errors.push(`artifacts[${index}].kind must be a known release artifact kind`);
    }
    if (!isNonEmptyString(artifact.sha256) || !SHA256_PATTERN.test(artifact.sha256)) {
      errors.push(`artifacts[${index}].sha256 must be a lowercase SHA-256 hex digest`);
    }
    if (!Number.isInteger(artifact.sizeBytes) || artifact.sizeBytes < 0) {
      errors.push(`artifacts[${index}].sizeBytes must be a non-negative integer`);
    }
  }
}

function validateChecks(errors, checks) {
  if (!isObject(checks)) {
    errors.push('checks must be an object');
    return;
  }
  for (const name of REQUIRED_CHECKS) {
    const check = checks[name];
    if (!isObject(check)) {
      errors.push(`checks.${name} must be present`);
      continue;
    }
    if (!STATUS_VALUES.has(check.status)) {
      errors.push(`checks.${name}.status must be a known status`);
    }
  }
}

function validateScenarioEvidence(errors, scenarioEvidence, pendingGates) {
  if (!Array.isArray(scenarioEvidence)) {
    errors.push('scenarioEvidence must be an array');
    return;
  }
  if (scenarioEvidence.length === 0) {
    errors.push('scenarioEvidence must contain canonical SK-S1/SK-S2/SK-S3/SK-S5 rows');
    return;
  }

  const byID = new Map();
  for (const [index, scenario] of scenarioEvidence.entries()) {
    if (!isObject(scenario)) {
      errors.push(`scenarioEvidence[${index}] must be an object`);
      continue;
    }
    if (!isNonEmptyString(scenario.scenarioId)) {
      errors.push(`scenarioEvidence[${index}].scenarioId must be a non-empty string`);
      continue;
    }
    if (!STATUS_VALUES.has(scenario.status)) {
      errors.push(`scenarioEvidence[${index}].status must be a known status`);
    }
    byID.set(scenario.scenarioId, scenario);
  }

  for (const scenarioId of REQUIRED_SCENARIOS) {
    const scenario = byID.get(scenarioId);
    if (!scenario) {
      errors.push(`scenarioEvidence must include ${scenarioId}`);
      continue;
    }
    if (scenario.status === 'pass') {
      if (scenario.source !== 'homelab-artifact') {
        errors.push(`scenarioEvidence[${scenarioId}].source must be homelab-artifact for passing canonical evidence`);
      }
      if (!isNonEmptyString(scenario.url)) {
        errors.push(`scenarioEvidence[${scenarioId}].url must reference the passing homelab artifact`);
      }
    }
    if (scenario.status !== 'pass' && !pendingGateMentions(pendingGates, scenarioId)) {
      errors.push(`pendingGates must mention ${scenarioId} while its scenario evidence is ${scenario.status}`);
    }
  }
}

function validateSecurityBaselineCheck(errors, checks, scenarioEvidence) {
  if (!isObject(checks) || !isObject(checks.securityBaseline) || !Array.isArray(scenarioEvidence)) return;

  const byID = new Map(
    scenarioEvidence
      .filter((scenario) => isObject(scenario) && isNonEmptyString(scenario.scenarioId))
      .map((scenario) => [scenario.scenarioId, scenario]),
  );
  const allBaselineScenariosPassed = SECURITY_BASELINE_SCENARIOS.every((scenarioId) => {
    const scenario = byID.get(scenarioId);
    return scenario?.status === 'pass' && scenario?.source === 'homelab-artifact';
  });

  if (checks.securityBaseline.status === 'pass') {
    for (const scenarioId of SECURITY_BASELINE_SCENARIOS) {
      const scenario = byID.get(scenarioId);
      if (scenario?.status !== 'pass' || scenario?.source !== 'homelab-artifact') {
        errors.push(`checks.securityBaseline.status cannot be pass until ${scenarioId} has passing homelab-artifact evidence`);
      }
    }
    return;
  }

  if (allBaselineScenariosPassed) {
    errors.push('checks.securityBaseline.status must be pass when SK-S1/SK-S2/SK-S3 have passing homelab-artifact evidence');
  }
}

function validateRequiredMissingAlternatives(errors, missingAlternatives) {
  if (!Array.isArray(missingAlternatives)) {
    return;
  }
  for (const prefix of REQUIRED_MISSING_ALTERNATIVE_PREFIXES) {
    if (!missingAlternatives.some((item) => typeof item === 'string' && item.startsWith(prefix))) {
      errors.push(`missingAlternatives must include a ${prefix.trim()} v0.4 limitation entry`);
    }
  }
}

function pendingGateMentions(pendingGates, scenarioId) {
  return Array.isArray(pendingGates) && pendingGates.some((gate) => {
    return typeof gate === 'string' && gate.toLowerCase().includes(scenarioId.toLowerCase());
  });
}

function validateStringArray(errors, field, value) {
  if (!Array.isArray(value)) {
    errors.push(`${field} must be an array`);
    return;
  }
  for (const [index, item] of value.entries()) {
    if (!isNonEmptyString(item)) {
      errors.push(`${field}[${index}] must be a non-empty string`);
    }
  }
}

function isObject(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function isNonEmptyString(value) {
  return typeof value === 'string' && value.trim().length > 0;
}

async function main() {
  const filePath = process.argv[2];
  if (!filePath) {
    throw new Error('usage: validate-release-evidence.mjs <release-evidence.json>');
  }
  const errors = await validateReleaseEvidenceFile(filePath);
  if (errors.length > 0) {
    throw new Error(errors.join('\n'));
  }
  console.log(`release evidence valid: ${filePath}`);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  main().catch((err) => {
    console.error(err.message);
    process.exit(1);
  });
}
