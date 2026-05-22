import { mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import test from 'node:test';
import assert from 'node:assert/strict';

const execFileAsync = promisify(execFile);

test('render-release-evidence writes artifact hashes and checks', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  await writeFile(path.join(dist, 'stackkits_0.0.1_linux_amd64.tar.gz'), 'archive');
  await writeFile(path.join(dist, 'stackkits_0.0.1_linux_amd64.tar.gz.spdx.json'), '{"SPDXID":"SPDXRef-DOCUMENT"}');

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--visibility',
    'internal',
    '--dist',
    dist,
    '--output',
    output,
    '--check',
    'publicExport=pass,exported tree passed leak checks',
    '--check',
    'archiveValidation=pass',
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.schemaVersion, '1.0.0');
  assert.equal(evidence.release.tag, 'v0.0.1');
  assert.equal(evidence.checks.publicExport.status, 'pass');
  assert.equal(evidence.checks.archiveValidation.status, 'pass');
  assert.equal(evidence.checks.liveInstallerSmoke.status, 'pending');
  assert.equal(evidence.checks.defaultL3PaaSDelivery.status, 'pending');
  assert.equal(evidence.artifacts.length, 2);
  assert.ok(evidence.artifacts.some((artifact) => artifact.kind === 'archive' && artifact.sha256.length === 64));
  assert.ok(evidence.artifacts.some((artifact) => artifact.kind === 'sbom'));
});

test('update-release-evidence-check updates one check without rebuilding artifacts', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-update-'));
  const evidencePath = path.join(dir, 'release-evidence.json');
  await writeFile(
    evidencePath,
    JSON.stringify({
      schemaVersion: '1.0.0',
      generatedAt: '2026-05-18T00:00:00.000Z',
      release: {
        tag: 'v0.0.1',
        commit: 'abcdef123456',
        sourceRepository: 'kombifyio/stackKits',
        releaseRepository: 'kombifyio/stackKits',
        visibility: 'internal',
      },
      artifacts: [{ name: 'archive.tar.gz', kind: 'archive', sha256: 'a'.repeat(64), sizeBytes: 7 }],
      checks: { attestationVerification: { status: 'pending' } },
      knownLimitations: ['BaseKit only'],
    }),
  );

  await execFileAsync(process.execPath, [
    'scripts/release/update-release-evidence-check.mjs',
    '--file',
    evidencePath,
    '--name',
    'attestationVerification',
    '--status',
    'pass',
    '--summary',
    'verified',
  ]);

  const evidence = JSON.parse(await readFile(evidencePath, 'utf8'));
  assert.equal(evidence.checks.attestationVerification.status, 'pass');
  assert.equal(evidence.checks.attestationVerification.summary, 'verified');
  assert.equal(evidence.artifacts.length, 1);
});
