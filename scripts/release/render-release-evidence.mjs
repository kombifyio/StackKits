#!/usr/bin/env node
import { createHash } from 'node:crypto';
import { readdir, stat, writeFile } from 'node:fs/promises';
import { createReadStream } from 'node:fs';
import path from 'node:path';

const VALID_STATUS = new Set(['pass', 'fail', 'pending', 'not_applicable']);

function parseArgs(argv) {
  const opts = {
    checks: new Map(),
    knownLimitations: [],
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

async function main() {
  const opts = parseArgs(process.argv.slice(2));
  const outputPath = path.resolve(opts.output);
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
      securityScans: checkFromMap(opts.checks, 'securityScans', 'pending'),
      liveInstallerSmoke: checkFromMap(opts.checks, 'liveInstallerSmoke', 'pending'),
      freshUbuntuBaseKit: checkFromMap(opts.checks, 'freshUbuntuBaseKit', 'pending'),
      upgradeRollbackVm: checkFromMap(opts.checks, 'upgradeRollbackVm', 'pending'),
      defaultL3PaaSDelivery: checkFromMap(opts.checks, 'defaultL3PaaSDelivery', 'pending'),
      attestationVerification: checkFromMap(opts.checks, 'attestationVerification', 'pending'),
    },
    knownLimitations: opts.knownLimitations.length
      ? opts.knownLimitations
      : [
          'Enterprise production readiness is BaseKit-only until explicit release evidence says otherwise.',
          'Modern Homelab and HA Kit remain alpha/scaffolding.',
          'Complete Coolify-managed application-layer rollout for ready-to-use use cases is not verified until the tracked blocker is closed with evidence.',
        ],
  };

  await writeFile(outputPath, `${JSON.stringify(evidence, null, 2)}\n`);
}

main().catch((err) => {
  console.error(err.message);
  process.exit(1);
});
