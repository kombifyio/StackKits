#!/usr/bin/env node
// Validates the public OS-only compatibility projection. Producer-side host,
// runtime and infrastructure diagnostics are intentionally a different
// artifact and cannot pass this gate.
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const MATRIX_KEYS = new Set(['schemaVersion', 'stackkitsVersion', 'results', 'generatedAt']);
const RESULT_KEYS = new Set(['os', 'grade', 'reasonCodes']);
const OS_KEYS = new Set(['family', 'distribution', 'version']);
const REASON_CODES = new Set(['current-candidate-receipt-pending', 'os-policy-not-yet-admitted']);
const FORBIDDEN_KEYS = new Set([
  'runId', 'lane', 'stages', 'stage', 'overall', 'target', 'arch', 'architecture',
  'kernel', 'packageMgr', 'initSystem', 'virtType', 'virtTier', 'virtualization',
  'runtime', 'engine', 'osReleaseRaw', 'evidencePath', 'mdnsHost', 'host', 'hostname',
  'provider', 'device', 'resourceId', 'lease', 'cleanupState',
]);
const INFRASTRUCTURE_TEXT = /\b(docker|container|wsl2?|proxmox|pico\s*kvm|kvm|hypervisor|bare[ -]?metal|virtual(?:ization| machine)|ionos|centron)\b/i;
const RFC1918 = /(^|[^0-9.])(10\.\d{1,3}\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3}|172\.(1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3})([^0-9.]|$)/;
const SLUG = /^[a-z0-9][a-z0-9.-]*$/;
const VERSION = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;
const STACKKITS_VERSION = /^(unreleased|v(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?)$/;

export function validateOSMatrix(matrix, options = {}) {
  const errors = [];
  if (!matrix || typeof matrix !== 'object' || Array.isArray(matrix)) {
    return ['matrix must be a JSON object'];
  }
  rejectUnknownKeys(matrix, MATRIX_KEYS, '$', errors);
  if (matrix.schemaVersion !== 2) errors.push(`schemaVersion must be 2, got ${JSON.stringify(matrix.schemaVersion)}`);
  if (typeof matrix.stackkitsVersion !== 'string' || !STACKKITS_VERSION.test(matrix.stackkitsVersion)) {
    errors.push(`stackkitsVersion invalid: ${JSON.stringify(matrix.stackkitsVersion)}`);
  }
  const generatedAt = parseDate(matrix.generatedAt, '$.generatedAt', errors);
  if (!Array.isArray(matrix.results) || matrix.results.length === 0) {
    errors.push('results must be a non-empty array');
    return errors;
  }

  const identities = new Set();
  matrix.results.forEach((result, i) => {
    const at = `results[${i}]`;
    if (!result || typeof result !== 'object' || Array.isArray(result)) {
      errors.push(`${at} must be an object`);
      return;
    }
    rejectUnknownKeys(result, RESULT_KEYS, at, errors);
    validateOS(result.os, `${at}.os`, errors);
    if (result.grade !== 'unverified') errors.push(`${at}.grade must remain unverified until the receipt projector exists`);
    if (!Array.isArray(result.reasonCodes) || result.reasonCodes.length === 0 || result.reasonCodes.some((code) => !REASON_CODES.has(code))) {
      errors.push(`${at}.reasonCodes must contain only closed public reason codes`);
    } else if (new Set(result.reasonCodes).size !== result.reasonCodes.length) {
      errors.push(`${at}.reasonCodes must be unique`);
    }

    const identity = [result.os?.family, result.os?.distribution, result.os?.version].join('/');
    if (identities.has(identity)) errors.push(`${at} duplicates compatibility identity ${identity}`);
    identities.add(identity);
  });

  scanForbidden(matrix, '$', errors);
  const serialized = JSON.stringify(matrix);
  if (RFC1918.test(serialized)) errors.push('matrix contains RFC1918 addresses (public projection leak)');
  if (INFRASTRUCTURE_TEXT.test(serialized)) errors.push('matrix contains infrastructure/runtime terminology (public projection leak)');

  if (options.maxAgeDays !== undefined && !Number.isNaN(generatedAt)) {
    const now = options.now ?? Date.now();
    const ageDays = (now - generatedAt) / 86_400_000;
    if (ageDays > options.maxAgeDays) errors.push(`matrix is ${ageDays.toFixed(1)} days old; max is ${options.maxAgeDays}`);
    if (ageDays < -0.1) errors.push('generatedAt lies in the future');
  }
  if (options.expectKit) {
    errors.push('kit-specific evidence is unavailable until the receipt projector exists');
  }
  if (options.expectVersionPrefix && matrix.stackkitsVersion !== 'unreleased' && !matrix.stackkitsVersion.startsWith(options.expectVersionPrefix)) {
    errors.push(`stackkitsVersion ${matrix.stackkitsVersion} does not match release prefix ${options.expectVersionPrefix}`);
  }
  return errors;
}

function validateOS(os, at, errors) {
  if (!os || typeof os !== 'object' || Array.isArray(os)) {
    errors.push(`${at} missing`);
    return;
  }
  rejectUnknownKeys(os, OS_KEYS, at, errors);
  for (const key of ['family', 'distribution']) {
    if (typeof os[key] !== 'string' || !SLUG.test(os[key])) errors.push(`${at}.${key} invalid: ${JSON.stringify(os[key])}`);
  }
  if (typeof os.version !== 'string' || !VERSION.test(os.version)) errors.push(`${at}.version invalid: ${JSON.stringify(os.version)}`);
}

function parseDate(value, at, errors) {
  const parsed = Date.parse(value ?? '');
  if (Number.isNaN(parsed)) errors.push(`${at} must be an ISO date-time`);
  return parsed;
}

function rejectUnknownKeys(value, allowed, at, errors) {
  for (const key of Object.keys(value)) {
    if (!allowed.has(key)) errors.push(`${at}.${key} is not allowed in the OS-only public projection`);
  }
}

function scanForbidden(value, at, errors) {
  if (Array.isArray(value)) {
    value.forEach((entry, i) => scanForbidden(entry, `${at}[${i}]`, errors));
    return;
  }
  if (value && typeof value === 'object') {
    for (const [key, entry] of Object.entries(value)) {
      if (FORBIDDEN_KEYS.has(key)) errors.push(`${at}.${key} is forbidden in the OS-only public projection`);
      scanForbidden(entry, `${at}.${key}`, errors);
    }
  }
}

export function summarizeOSMatrix(matrix) {
  const total = (matrix.results ?? []).length;
  return `OS compatibility ${matrix.stackkitsVersion}: ${total} rows (all unverified; receipt projector pending), generated ${matrix.generatedAt}`;
}

function parseArgs(argv) {
  const args = { files: [] };
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === '--max-age-days') { args.maxAgeDays = Number(argv[++i]); }
    else if (arg === '--expect-kit') { args.expectKit = argv[++i]; }
    else if (arg === '--expect-version-prefix') { args.expectVersionPrefix = argv[++i]; }
    else if (arg === '--summary') { args.summary = true; }
    else args.files.push(arg);
  }
  return args;
}

async function main() {
  const args = parseArgs(process.argv.slice(2));
  if (args.files.length === 0) {
    console.error('usage: validate-os-matrix.mjs <public-v2.json> [--max-age-days N] [--expect-kit KIT] [--expect-version-prefix vX.Y] [--summary]');
    process.exit(2);
  }
  let failed = false;
  for (const file of args.files) {
    try {
      const matrix = JSON.parse(await readFile(file, 'utf8'));
      const errors = validateOSMatrix(matrix, args);
      if (errors.length > 0) {
        errors.forEach((error) => console.error(`${file}: ${error}`));
        failed = true;
      } else console.log(args.summary ? summarizeOSMatrix(matrix) : `${file}: OS compatibility projection valid`);
    } catch (error) {
      console.error(`${file}: ${error.message}`);
      failed = true;
    }
  }
  process.exit(failed ? 1 : 0);
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) await main();
