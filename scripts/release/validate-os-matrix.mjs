#!/usr/bin/env node
// Validates a redacted OS-compatibility matrix (matrix.public.json) before it
// becomes release evidence or a published doc. Structural twin of
// schemas/os-compat-matrix.schema.json (hand-rolled like the other release
// validators — no schema-lib dependency) plus the redaction-leak gate and the
// freshness policy: the matrix comes from an async lab/container run, so it
// can never be same-commit — instead it must be recent (--max-age-days) and,
// when stamped, share the release's major.minor (--expect-version-prefix).
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const GRADES = new Set(['supported', 'partial', 'unsupported']);
const LANES = new Set(['vm', 'container', 'bare-metal']);
const STAGES = new Set([
  'cpu-baseline', 'install', 'serverprep', 'binary-boot', 'prepare', 'init',
  'generate', 'apply', 'service-up', 'mdns-verify', 'kit-upgrade', 'rollback',
  'cleanup',
]);
const ARCHES = new Set(['amd64', 'arm64']);
// Private producer-side fields that must never survive RedactForPublish.
const FORBIDDEN_KEYS = new Set(['osReleaseRaw', 'evidencePath', 'mdnsHost']);
const RFC1918 = /(^|[^0-9.])(10\.\d{1,3}\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3}|172\.(1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3})([^0-9.]|$)/;
const OS_ID = /^[a-z0-9][a-z0-9.-]*$/;

export function validateOSMatrix(matrix, options = {}) {
  const errors = [];
  if (!matrix || typeof matrix !== 'object' || Array.isArray(matrix)) {
    return ['matrix must be a JSON object'];
  }
  if (matrix.schemaVersion !== 1) {
    errors.push(`schemaVersion must be 1, got ${JSON.stringify(matrix.schemaVersion)}`);
  }
  if (typeof matrix.runId !== 'string' || matrix.runId.trim() === '') {
    errors.push('runId must be a non-empty string');
  }
  const generatedAt = Date.parse(matrix.generatedAt ?? '');
  if (Number.isNaN(generatedAt)) {
    errors.push('generatedAt must be an ISO date-time');
  }
  if (!Array.isArray(matrix.results) || matrix.results.length === 0) {
    errors.push('results must be a non-empty array');
    return errors;
  }

  matrix.results.forEach((result, i) => {
    const at = `results[${i}]`;
    const os = result?.os;
    if (!os || typeof os !== 'object') {
      errors.push(`${at}.os missing`);
      return;
    }
    if (typeof os.id !== 'string' || !OS_ID.test(os.id)) {
      errors.push(`${at}.os.id invalid: ${JSON.stringify(os.id)}`);
    }
    if (os.arch !== undefined && !ARCHES.has(os.arch)) {
      errors.push(`${at}.os.arch invalid: ${JSON.stringify(os.arch)}`);
    }
    if (!GRADES.has(result.overall)) {
      errors.push(`${at}.overall invalid: ${JSON.stringify(result.overall)}`);
    }
    if (result.lane !== undefined && !LANES.has(result.lane)) {
      errors.push(`${at}.lane invalid: ${JSON.stringify(result.lane)}`);
    }
    if (!Array.isArray(result.stages) || result.stages.length === 0) {
      errors.push(`${at}.stages must be a non-empty array`);
    } else {
      result.stages.forEach((stage, j) => {
        if (!STAGES.has(stage?.stage)) {
          errors.push(`${at}.stages[${j}].stage unknown: ${JSON.stringify(stage?.stage)}`);
        }
        if (!GRADES.has(stage?.status)) {
          errors.push(`${at}.stages[${j}].status invalid: ${JSON.stringify(stage?.status)}`);
        }
      });
    }
    // Redaction gate: target may only be an empty object in the public form.
    if (result.target !== undefined) {
      if (typeof result.target !== 'object' || result.target === null || Object.keys(result.target).length > 0) {
        errors.push(`${at}.target must be absent or an empty object in the public matrix (redaction leak)`);
      }
    }
  });

  scanForbidden(matrix, '$', errors);
  const serialized = JSON.stringify(matrix);
  if (RFC1918.test(serialized)) {
    errors.push('matrix contains RFC1918 addresses (redaction leak)');
  }

  if (options.maxAgeDays !== undefined && !Number.isNaN(generatedAt)) {
    const now = options.now ?? Date.now();
    const ageDays = (now - generatedAt) / 86_400_000;
    if (ageDays > options.maxAgeDays) {
      errors.push(`matrix is ${ageDays.toFixed(1)} days old; max is ${options.maxAgeDays} (re-run the os-matrix workflow)`);
    }
    if (ageDays < -0.1) {
      errors.push('generatedAt lies in the future');
    }
  }
  if (options.expectKit && matrix.kit !== options.expectKit) {
    errors.push(`kit is ${JSON.stringify(matrix.kit)}, expected ${JSON.stringify(options.expectKit)}`);
  }
  if (options.expectVersionPrefix && matrix.stackkitVersion) {
    if (!String(matrix.stackkitVersion).startsWith(options.expectVersionPrefix)) {
      errors.push(`stackkitVersion ${matrix.stackkitVersion} does not match release prefix ${options.expectVersionPrefix}`);
    }
  }
  return errors;
}

function scanForbidden(value, at, errors) {
  if (Array.isArray(value)) {
    value.forEach((v, i) => scanForbidden(v, `${at}[${i}]`, errors));
    return;
  }
  if (value && typeof value === 'object') {
    for (const [key, v] of Object.entries(value)) {
      if (FORBIDDEN_KEYS.has(key)) {
        errors.push(`${at}.${key} is a private field and must not appear in the public matrix (redaction leak)`);
      }
      scanForbidden(v, `${at}.${key}`, errors);
    }
  }
}

export function summarizeOSMatrix(matrix) {
  const counts = { supported: 0, partial: 0, unsupported: 0 };
  for (const result of matrix.results ?? []) {
    if (counts[result.overall] !== undefined) counts[result.overall] += 1;
  }
  const total = (matrix.results ?? []).length;
  const version = matrix.stackkitVersion ? `, version ${matrix.stackkitVersion}` : '';
  return `OS compat matrix run ${matrix.runId}${version}: ${total} targets (${counts.supported} supported / ${counts.partial} partial / ${counts.unsupported} unsupported), generated ${matrix.generatedAt}`;
}

function parseArgs(argv) {
  const args = { files: [] };
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === '--max-age-days') {
      args.maxAgeDays = Number(argv[i + 1]);
      i += 1;
    } else if (arg === '--expect-kit') {
      args.expectKit = argv[i + 1];
      i += 1;
    } else if (arg === '--expect-version-prefix') {
      args.expectVersionPrefix = argv[i + 1];
      i += 1;
    } else if (arg === '--summary') {
      args.summary = true;
    } else {
      args.files.push(arg);
    }
  }
  return args;
}

async function main() {
  const args = parseArgs(process.argv.slice(2));
  if (args.files.length === 0) {
    console.error('usage: validate-os-matrix.mjs <matrix.public.json> [--max-age-days N] [--expect-kit KIT] [--expect-version-prefix vX.Y] [--summary]');
    process.exit(2);
  }
  let failed = false;
  for (const file of args.files) {
    let matrix;
    try {
      matrix = JSON.parse(await readFile(file, 'utf8'));
    } catch (error) {
      console.error(`${file}: ${error.message}`);
      failed = true;
      continue;
    }
    const errors = validateOSMatrix(matrix, args);
    if (errors.length > 0) {
      for (const err of errors) console.error(`${file}: ${err}`);
      failed = true;
    } else if (args.summary) {
      console.log(summarizeOSMatrix(matrix));
    } else {
      console.log(`${file}: OS compat matrix valid`);
    }
  }
  process.exit(failed ? 1 : 0);
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  await main();
}
