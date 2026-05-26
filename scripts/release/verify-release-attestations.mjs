#!/usr/bin/env node
import { readdir } from 'node:fs/promises';
import path from 'node:path';
import { spawnSync } from 'node:child_process';

function parseArgs(argv) {
  const opts = {
    dist: '',
    includeEvidence: false,
    evidenceOnly: false,
    dryRun: false,
    images: [],
  };

  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    const next = argv[i + 1];
    switch (arg) {
      case '--repo':
        opts.repo = next;
        i += 1;
        break;
      case '--dist':
        opts.dist = next;
        i += 1;
        break;
      case '--image':
        opts.images.push(next);
        i += 1;
        break;
      case '--include-evidence':
        opts.includeEvidence = true;
        break;
      case '--evidence-only':
        opts.evidenceOnly = true;
        break;
      case '--dry-run':
        opts.dryRun = true;
        break;
      default:
        throw new Error(`unknown argument: ${arg}`);
    }
  }

  if (!opts.repo) {
    throw new Error('missing required --repo');
  }
  return opts;
}

function isReleaseArtifact(name, opts) {
  if (opts.evidenceOnly) return name === 'release-evidence.json';
  if (name === 'release-evidence.json') return opts.includeEvidence;
  if (name === 'checksums.txt') return true;
  if (name.endsWith('.spdx.json') || name.endsWith('.cdx.json')) return true;
  if (name.endsWith('.tar.gz') || name.endsWith('.zip')) return true;
  if (name.endsWith('.deb') || name.endsWith('.rpm') || name.endsWith('.apk')) return true;
  return false;
}

async function collectSubjects(opts) {
  const subjects = [];
  if (opts.dist) {
    const entries = await readdir(opts.dist, { withFileTypes: true }).catch((err) => {
      if (err.code === 'ENOENT') {
        throw new Error(`dist directory does not exist: ${opts.dist}`);
      }
      throw err;
    });
    for (const entry of entries) {
      if (!entry.isFile() || !isReleaseArtifact(entry.name, opts)) continue;
      subjects.push({ type: 'file', value: path.join(opts.dist, entry.name) });
    }
  }

  for (const image of opts.images) {
    if (!image) continue;
    subjects.push({ type: 'image', value: image.startsWith('oci://') ? image : `oci://${image}` });
  }

  subjects.sort((a, b) => a.value.localeCompare(b.value));
  return subjects;
}

function sleep(ms) {
  Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);
}

function verifySubject(repo, subject) {
  const args = ['attestation', 'verify', subject.value, '-R', repo];
  const attempts = 6;
  for (let attempt = 1; attempt <= attempts; attempt += 1) {
    const result = spawnSync('gh', args, { stdio: 'inherit', shell: process.platform === 'win32' });
    if (result.error) {
      throw result.error;
    }
    if (result.status === 0) {
      return;
    }
    if (attempt === attempts) {
      throw new Error(`gh ${args.join(' ')} failed with exit code ${result.status}`);
    }
    console.error(`attestation for ${subject.value} not ready yet; retrying (${attempt}/${attempts})`);
    sleep(5000);
  }
}

async function main() {
  const opts = parseArgs(process.argv.slice(2));
  const subjects = await collectSubjects(opts);
  if (subjects.length === 0) {
    throw new Error('no release attestation subjects found');
  }

  for (const subject of subjects) {
    if (opts.dryRun) {
      console.log(`${subject.type}:${subject.value}`);
    } else {
      verifySubject(opts.repo, subject);
    }
  }
}

main().catch((err) => {
  console.error(err.message);
  process.exit(1);
});
