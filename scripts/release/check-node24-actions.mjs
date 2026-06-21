#!/usr/bin/env node
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const ACTION_POLICIES = new Map([
  ['actions/checkout', { minMajor: 6, reason: 'checkout v6+ is the StackKits Node 24-compatible baseline' }],
  ['actions/setup-go', { minMajor: 6, reason: 'setup-go v6+ is the StackKits Node 24-compatible baseline' }],
  ['actions/setup-node', { minMajor: 6, reason: 'setup-node v6+ is the StackKits Node 24-compatible baseline' }],
  ['actions/setup-python', { minMajor: 6, reason: 'setup-python v6+ is the StackKits Node 24-compatible baseline' }],
  ['actions/upload-artifact', { minMajor: 7, reason: 'upload-artifact v7+ is the StackKits Node 24-compatible baseline' }],
  ['actions/download-artifact', { minMajor: 8, reason: 'download-artifact v8+ is the StackKits Node 24-compatible baseline' }],
  ['actions/attest-build-provenance', { minMajor: 4, reason: 'attest-build-provenance v4+ is the StackKits Node 24-compatible baseline' }],
  ['actions/attest-sbom', { minMajor: 4, reason: 'attest-sbom v4+ is the StackKits Node 24-compatible baseline' }],
  ['goreleaser/goreleaser-action', { minMajor: 7, reason: 'goreleaser-action v7+ is the StackKits Node 24-compatible baseline' }],
]);

const WORKFLOW_ROOTS = [
  path.join('.github', 'workflows'),
  path.join('scripts', 'public', 'workflows'),
];

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

function gitTrackedWorkflowFiles(repoRoot) {
  if (!existsSync(path.join(repoRoot, '.git'))) return [];
  const result = spawnSync('git', ['-C', repoRoot, 'ls-files', ...WORKFLOW_ROOTS], {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  if (result.status !== 0) return [];
  return result.stdout
    .split(/\r?\n/)
    .filter((file) => /\.(ya?ml)$/i.test(file))
    .map((file) => path.join(repoRoot, file));
}

function workflowFiles(repoRoot) {
  const tracked = gitTrackedWorkflowFiles(repoRoot);
  if (tracked.length > 0) return tracked;
  return WORKFLOW_ROOTS.flatMap((root) => walk(path.join(repoRoot, root), (file) => /\.(ya?ml)$/i.test(file)));
}

function relative(repoRoot, file) {
  return path.relative(repoRoot, file).replaceAll(path.sep, '/');
}

function versionMajor(value) {
  const match = String(value || '').match(/\bv([0-9]+)(?:[.\w-]*)?/i);
  if (!match) return null;
  return Number(match[1]);
}

function validateActionUse(repoRoot, file, lineNumber, line) {
  const match = line.match(/^\s*(?:-\s*)?uses:\s*([^@\s#]+)@([^\s#]+)(?:\s*#\s*(.+))?$/i);
  if (!match) return [];
  const actionName = match[1].toLowerCase();
  const ref = match[2].trim();
  const versionComment = (match[3] || '').trim();
  const policy = ACTION_POLICIES.get(actionName);
  if (!policy) return [];

  const major = versionMajor(ref) ?? versionMajor(versionComment);
  const location = `${relative(repoRoot, file)}:${lineNumber}`;
  if (major === null) {
    return [`${location} uses ${actionName}@${ref} without a trailing # vX.Y.Z comment; SHA pins for Node-runtime actions must document the audited action major`];
  }
  if (major < policy.minMajor) {
    return [`${location} uses ${actionName}@${ref}${versionComment ? ` # ${versionComment}` : ''}; need v${policy.minMajor}+ (${policy.reason})`];
  }
  return [];
}

function validateNodeVersion(repoRoot, file, lineNumber, line) {
  const location = `${relative(repoRoot, file)}:${lineNumber}`;
  if (/^\s*NODE_VERSION:\s*['"]?20(?:\b|[.'"])/.test(line)) {
    return [`${location} sets NODE_VERSION to 20; release workflows must use Node 24`];
  }
  if (/^\s*node-version:\s*['"]?20(?:\b|[.'"])/.test(line)) {
    return [`${location} sets node-version to 20; release workflows must use Node 24`];
  }
  return [];
}

function validate(repoRoot) {
  const failures = [];
  for (const file of workflowFiles(repoRoot)) {
    const lines = readFileSync(file, 'utf8').split(/\r?\n/);
    for (const [index, line] of lines.entries()) {
      failures.push(...validateActionUse(repoRoot, file, index + 1, line));
      failures.push(...validateNodeVersion(repoRoot, file, index + 1, line));
    }
  }
  return failures;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  const { repoRoot } = parseArgs(process.argv.slice(2));
  const resolved = path.resolve(repoRoot);
  const failures = validate(resolved);
  if (failures.length > 0) {
    console.error('Node 24 action compatibility check failed.');
    for (const failure of failures) {
      console.error(`- ${failure}`);
    }
    process.exit(1);
  }
  console.log('Node 24 action compatibility check passed.');
}

export { ACTION_POLICIES, validate };
