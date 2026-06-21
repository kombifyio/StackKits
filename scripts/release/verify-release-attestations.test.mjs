import { mkdir, mkdtemp, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import test from 'node:test';
import assert from 'node:assert/strict';

const execFileAsync = promisify(execFile);

test('verify-release-attestations dry-run lists release subjects', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-attest-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  await writeFile(path.join(dist, 'stackkits_0.0.1_linux_amd64.tar.gz'), 'archive');
  await writeFile(path.join(dist, 'kombify-stackkits_0.0.1_linux_amd64.deb'), 'package');
  await writeFile(path.join(dist, 'checksums.txt'), 'checksum');
  await writeFile(path.join(dist, 'stackkits_0.0.1_linux_amd64.tar.gz.spdx.json'), '{}');
  await writeFile(path.join(dist, 'release-evidence.json'), JSON.stringify(validEvidence(), null, 2));
  await writeFile(path.join(dist, 'notes.txt'), 'ignore');

  const { stdout } = await execFileAsync(process.execPath, [
    'scripts/release/verify-release-attestations.mjs',
    '--repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--include-evidence',
    '--image',
    'ghcr.io/kombifyio/stackkits:v0.0.1',
    '--dry-run',
  ]);

  assert.match(stdout, /file:.*stackkits_0\.0\.1_linux_amd64\.tar\.gz/);
  assert.match(stdout, /file:.*kombify-stackkits_0\.0\.1_linux_amd64\.deb/);
  assert.match(stdout, /file:.*checksums\.txt/);
  assert.match(stdout, /file:.*release-evidence\.json/);
  assert.match(stdout, /file:.*\.spdx\.json/);
  assert.match(stdout, /image:oci:\/\/ghcr\.io\/kombifyio\/stackkits:v0\.0\.1/);
  assert.doesNotMatch(stdout, /notes\.txt/);
});

test('verify-release-attestations rejects invalid release evidence before listing subjects', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-attest-invalid-evidence-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  await writeFile(path.join(dist, 'release-evidence.json'), JSON.stringify({
    ...validEvidence(),
    scenarioEvidence: [],
  }, null, 2));

  await assert.rejects(
    execFileAsync(process.execPath, [
      'scripts/release/verify-release-attestations.mjs',
      '--repo',
      'kombifyio/stackKits',
      '--dist',
      dist,
      '--include-evidence',
      '--dry-run',
    ]),
    /scenarioEvidence must contain canonical/,
  );
});

function validEvidence() {
  return {
    schemaVersion: '1.0.0',
    generatedAt: '2026-06-13T12:00:00.000Z',
    release: {
      tag: 'v0.4.0',
      commit: 'abcdef123456',
      sourceRepository: 'kombifyio/stackKits',
      releaseRepository: 'kombifyio/stackKits',
      visibility: 'public',
    },
    artifacts: [
      {
        name: 'stackkits_0.4.0_linux_amd64.tar.gz',
        kind: 'archive',
        sha256: 'a'.repeat(64),
        sizeBytes: 123,
      },
    ],
    checks: Object.fromEntries(
      [
        'publicExport',
        'archiveValidation',
        'securityScans',
        'liveInstallerSmoke',
        'freshUbuntuBaseKit',
        'browserPreflight',
        'browserEvidence',
        'upgradeRollbackVm',
        'defaultL3PaaSDelivery',
        'attestationVerification',
      ].map((name) => [name, { status: name === 'publicExport' ? 'pass' : 'pending' }]),
    ),
    scenarioEvidence: ['SK-S1', 'SK-S2', 'SK-S3', 'SK-S5'].map((scenarioId) => ({
      scenarioId,
      status: 'pending',
      summary: `${scenarioId} pending`,
    })),
    pendingGates: ['SK-S1 pending', 'SK-S2 pending', 'SK-S3 pending', 'SK-S5 pending'],
    knownLimitations: ['BaseKit beta scope only'],
    missingAlternatives: [
      'Photos has no accepted v0.4 alternative yet; Immich remains the beta default.',
      'Vault has no accepted v0.4 alternative yet; Vaultwarden remains the beta default.',
    ],
  };
}
