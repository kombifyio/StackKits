import { mkdir, mkdtemp, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import test from 'node:test';
import assert from 'node:assert/strict';
import { validateReleaseEvidence } from './validate-release-evidence.mjs';

const execFileAsync = promisify(execFile);

test('validate-release-evidence accepts canonical pending scenario rows', () => {
  assert.deepEqual(validateReleaseEvidence(validEvidence()), []);
});

test('validate-release-evidence rejects empty scenario evidence', () => {
  const evidence = validEvidence();
  evidence.scenarioEvidence = [];

  assert.match(validateReleaseEvidence(evidence).join('\n'), /scenarioEvidence must contain canonical/);
});

test('validate-release-evidence rejects missing canonical scenario row', () => {
  const evidence = validEvidence();
  evidence.scenarioEvidence = evidence.scenarioEvidence.filter((item) => item.scenarioId !== 'SK-S3');

  assert.match(validateReleaseEvidence(evidence).join('\n'), /scenarioEvidence must include SK-S3/);
});

test('validate-release-evidence requires pending gate for non-pass canonical scenario', () => {
  const evidence = validEvidence();
  evidence.pendingGates = evidence.pendingGates.filter((gate) => !gate.includes('SK-S2'));

  assert.match(validateReleaseEvidence(evidence).join('\n'), /pendingGates must mention SK-S2/);
});

test('validate-release-evidence requires artifact URL for passing canonical scenario evidence', () => {
  const evidence = validEvidence();
  evidence.scenarioEvidence = evidence.scenarioEvidence.map((scenario) =>
    scenario.scenarioId === 'SK-S2'
      ? { scenarioId: 'SK-S2', status: 'pass', summary: 'manual pass without artifact' }
      : scenario,
  );

  assert.match(validateReleaseEvidence(evidence).join('\n'), /scenarioEvidence\[SK-S2\]\.url/);
  assert.match(validateReleaseEvidence(evidence).join('\n'), /scenarioEvidence\[SK-S2\]\.source/);

  evidence.scenarioEvidence = evidence.scenarioEvidence.map((scenario) =>
    scenario.scenarioId === 'SK-S2'
      ? {
          ...scenario,
          source: 'homelab-artifact',
          url: 'artifacts/scenarios/SK-S2/homelab.json',
        }
      : scenario,
  );
  assert.deepEqual(validateReleaseEvidence(evidence), []);
});

test('validate-release-evidence requires the first-class security baseline check', () => {
  const evidence = validEvidence();
  delete evidence.checks.securityBaseline;

  assert.match(validateReleaseEvidence(evidence).join('\n'), /checks\.securityBaseline must be present/);
});

test('validate-release-evidence ties security baseline pass to SK-S1/SK-S2/SK-S3 artifacts', () => {
  const evidence = validEvidence();
  evidence.checks.securityBaseline = { status: 'pass' };
  assert.match(validateReleaseEvidence(evidence).join('\n'), /cannot be pass until SK-S1/);

  evidence.scenarioEvidence = evidence.scenarioEvidence.map((scenario) =>
    ['SK-S1', 'SK-S2', 'SK-S3'].includes(scenario.scenarioId)
      ? {
          scenarioId: scenario.scenarioId,
          source: 'homelab-artifact',
          status: 'pass',
          url: `artifacts/scenarios/${scenario.scenarioId}/homelab.json`,
        }
      : scenario,
  );
  evidence.checks.securityBaseline = { status: 'pending' };
  assert.match(validateReleaseEvidence(evidence).join('\n'), /checks\.securityBaseline\.status must be pass/);

  evidence.checks.securityBaseline = { status: 'pass' };
  assert.deepEqual(validateReleaseEvidence(evidence), []);
});

test('validate-release-evidence requires Photos and Vault missing alternatives', () => {
  const evidence = validEvidence();
  evidence.missingAlternatives = ['Photos alternative is not accepted for v0.4 beta'];

  const errors = validateReleaseEvidence(evidence).join('\n');
  assert.doesNotMatch(errors, /Photos alternative-status entry/);
  assert.match(errors, /Vault alternative-status entry/);
});

test('validate-release-evidence CLI exits nonzero for invalid evidence', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-validate-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const file = path.join(dist, 'release-evidence.json');
  await writeFile(file, JSON.stringify({ ...validEvidence(), scenarioEvidence: [] }, null, 2));

  await assert.rejects(
    execFileAsync(process.execPath, ['scripts/release/validate-release-evidence.mjs', file]),
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
        'securityBaseline',
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
