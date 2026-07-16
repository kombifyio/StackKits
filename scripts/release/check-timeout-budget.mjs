#!/usr/bin/env node
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const MAX_MINUTES = 15;

// Workflows may opt out of the budget with an explicit, justified marker line:
//   # timeout-budget: exempt -- <reason>
// Reserved for lanes that structurally cannot fit 15 minutes (e.g. the
// os-matrix VM lane running full rollouts on the self-hosted lab runner).
// The reason is mandatory; a bare marker still fails the check.
const EXEMPT_MARKER = /^\s*#\s*timeout-budget:\s*exempt\s*--\s*(\S.*)$/m;

function parseArgs(argv) {
  const args = { repoRoot: process.cwd() };
  for (let i = 0; i < argv.length; i += 1) {
    if (argv[i] === '--repo-root') {
      args.repoRoot = argv[i + 1];
      i += 1;
    }
  }
  return args;
}

function walk(dir, predicate) {
  if (!existsSync(dir)) return [];
  const entries = [];
  for (const entry of readdirSync(dir)) {
    const full = path.join(dir, entry);
    const stat = statSync(full);
    if (stat.isDirectory()) {
      entries.push(...walk(full, predicate));
    } else if (predicate(full)) {
      entries.push(full);
    }
  }
  return entries;
}

function minutesFromDuration(value, unit) {
  const amount = Number(value);
  if (unit === 'h') return amount * 60;
  if (unit === 'm') return amount;
  return amount / 60;
}

function relative(repoRoot, file) {
  return path.relative(repoRoot, file).replaceAll(path.sep, '/');
}

function validate(repoRoot) {
  const failures = [];
  const workflowFiles = [
    ...walk(path.join(repoRoot, '.github', 'workflows'), (file) => /\.(ya?ml)$/i.test(file)),
    ...walk(path.join(repoRoot, '.depot', 'workflows'), (file) => /\.(ya?ml)$/i.test(file)),
    ...walk(path.join(repoRoot, 'scripts', 'public', 'workflows'), (file) => /\.(ya?ml)$/i.test(file)),
  ];
  const commandTimeoutFiles = [
    ...workflowFiles,
    path.join(repoRoot, 'tests', 'production', 'README.md'),
  ].filter((file) => existsSync(file));
  const generatedWaitFiles = [
    path.join(repoRoot, 'basement-kit', 'templates', 'simple', 'main.tf'),
    path.join(repoRoot, 'cloud-kit', 'templates', 'simple', 'main.tf'),
  ].filter((file) => existsSync(file));

  const exemptFiles = new Set();
  for (const file of workflowFiles) {
    const text = readFileSync(file, 'utf8');
    if (/^\s*#\s*timeout-budget:\s*exempt\b/m.test(text)) {
      if (EXEMPT_MARKER.test(text)) {
        exemptFiles.add(file);
      } else {
        failures.push(`${relative(repoRoot, file)} carries a timeout-budget exempt marker without a reason (use "# timeout-budget: exempt -- <reason>")`);
      }
    }
  }

  for (const file of workflowFiles) {
    if (exemptFiles.has(file)) continue;
    const text = readFileSync(file, 'utf8');
    for (const match of text.matchAll(/timeout-minutes:\s*([0-9]+)/g)) {
      const minutes = Number(match[1]);
      if (minutes > MAX_MINUTES) {
        failures.push(`${relative(repoRoot, file)} sets timeout-minutes: ${minutes}; max is ${MAX_MINUTES}`);
      }
    }
  }

  for (const file of commandTimeoutFiles) {
    if (exemptFiles.has(file)) continue;
    const text = readFileSync(file, 'utf8');
    for (const match of text.matchAll(/-timeout\s+([0-9]+)([smh])\b/g)) {
      const minutes = minutesFromDuration(match[1], match[2]);
      if (minutes > MAX_MINUTES) {
        failures.push(`${relative(repoRoot, file)} uses ${match[0]}; max is ${MAX_MINUTES}m`);
      }
    }
  }

  for (const file of generatedWaitFiles) {
    const text = readFileSync(file, 'utf8');
    const rel = relative(repoRoot, file);
    if (/seq\s+1\s+360\b/.test(text)) {
      failures.push(`${rel} contains seq 1 360; generated wait loops must stay below ${MAX_MINUTES} minutes`);
    }
    if (/after\s+30\s+minutes/i.test(text)) {
      failures.push(`${rel} contains a 30 minute wait message; fail fast and emit diagnostics instead`);
    }
  }

  return failures;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  const { repoRoot } = parseArgs(process.argv.slice(2));
  const resolved = path.resolve(repoRoot);
  const failures = validate(resolved);
  if (failures.length > 0) {
    console.error(`Timeout budget check failed. No workflow job, test command, or generated readiness wait may exceed ${MAX_MINUTES} minutes.`);
    for (const failure of failures) {
      console.error(`- ${failure}`);
    }
    process.exit(1);
  }
  console.log(`Timeout budget check passed (max ${MAX_MINUTES} minutes).`);
}

export { validate };
