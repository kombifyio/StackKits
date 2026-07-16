import assert from 'node:assert/strict';
import { mkdtemp, readFile, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import {
  renderCloudProviderReceipt,
  renderCloudProviderReceiptFile,
  validateCloudProviderReceipt,
} from './cloud-provider-receipt.mjs';

const source = {
  repository: 'kombify/kombify-StackKits',
  sha: 'a'.repeat(40),
  treeSha: 'c'.repeat(40),
  workflow: 'Production Tests',
  runId: '123456',
  runAttempt: '1',
};

function completedCleanup(scenarioId, provider, overrides = {}) {
  return {
    schemaVersion: 'stackkit.provider-cleanup/v1',
    scenarioId,
    provider,
    status: 'completed',
    testedSource: {
      mode: 'candidate-bundle',
      sha: source.sha,
      bundleSha256: 'b'.repeat(64),
      releaseVersion: '',
    },
    allocationIntent: {
      schemaVersion: 'stackkit.provider-allocation-intent/v1',
      scenarioId,
      provider,
      testName: 'e2e-provider-smoke-1',
      ownershipKey: `${scenarioId}/${provider}/e2e-provider-smoke-1`,
      lifecycle: 'ephemeral-release-smoke',
      protectedPersistent: false,
      createdAt: '2026-07-16T11:00:00.000Z',
      expiresAt: '2026-07-16T13:00:00.000Z',
      testedSource: {
        mode: 'candidate-bundle',
        sha: source.sha,
        bundleSha256: 'b'.repeat(64),
        releaseVersion: '',
      },
    },
    observedResources: [{ kind: 'simulation', id: 'sim-1', provider }],
    deletedResources: [{ kind: 'simulation', id: 'sim-1', provider }],
    remainingResources: [],
    errors: [],
    ...overrides,
  };
}

test('provider receipt passes only for the exact scenario and provider', () => {
  const receipt = renderCloudProviderReceipt({
    scenarioId: 'SK-S2',
    provider: 'centron-managed',
    artifactPath: 'artifacts/scenarios/SK-S2/homelab.json',
    artifact: {
      scenarioId: 'SK-S2',
      status: 'passed',
      browserUrl: 'https://demo.example.test/',
      target: { provider: 'centron-managed' },
    },
    cleanup: completedCleanup('SK-S2', 'centron-managed'),
    cleanupPath: 'artifacts/scenarios/SK-S2/cleanup-ledger.json',
    source,
    generatedAt: '2026-07-16T12:00:00.000Z',
  });

  assert.equal(receipt.status, 'pass');
  assert.equal(receipt.schema_version, 1);
  assert.equal(receipt.kind, 'provider-preview');
  assert.equal(receipt.run_id, source.runId);
  assert.equal(receipt.head_sha, source.sha);
  assert.equal(receipt.tree_sha, source.treeSha);
  assert.equal(receipt.scenario_result, 'PASS');
  assert.equal(receipt.cleanup.result, 'PASS');
  assert.deepEqual(receipt.cleanup.remaining_resources, []);
  assert.equal(receipt.provider, 'centron-managed');
  assert.equal(receipt.evidence.observedProvider, 'centron-managed');
  assert.equal(receipt.source.sha, source.sha);
  assert.equal(receipt.source.mode, 'candidate-bundle');
  assert.equal(receipt.source.bundleSha256, 'b'.repeat(64));
  assert.deepEqual(receipt.cleanup.remainingResources, []);
  assert.equal('failures' in receipt, false);
});

test('provider receipt blocks a fallback artifact from another provider', () => {
  const receipt = renderCloudProviderReceipt({
    scenarioId: 'SK-S3',
    provider: 'ionos-managed',
    artifact: {
      scenarioId: 'SK-S3',
      status: 'passed',
      browserUrl: 'https://demo.example.test/',
      target: { provider: 'centron-managed' },
    },
    cleanup: completedCleanup('SK-S3', 'ionos-managed'),
    source,
  });

  assert.equal(receipt.status, 'blocked');
  assert.match(receipt.failures.join('\n'), /centron-managed.*ionos-managed/);
});

test('provider receipt blocks missing and non-passing evidence but still writes diagnostics', async () => {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'stackkits-provider-receipt-'));
  const artifactPath = path.join(dir, 'homelab.json');
  const outputPath = path.join(dir, 'provider-receipt.json');
  const cleanupPath = path.join(dir, 'cleanup-ledger.json');
  await writeFile(artifactPath, JSON.stringify({
    scenarioId: 'SK-S2',
    status: 'failed',
    browserUrl: 'https://demo.example.test/',
    target: { provider: 'ionos-managed' },
  }));
  await writeFile(cleanupPath, JSON.stringify(completedCleanup('SK-S2', 'ionos-managed')));

  const receipt = await renderCloudProviderReceiptFile({
    scenarioId: 'SK-S2',
    provider: 'ionos-managed',
    artifactPath,
    cleanupPath,
    outputPath,
    source,
  });

  assert.equal(receipt.status, 'blocked');
  assert.match(receipt.failures.join('\n'), /status/);
  assert.deepEqual(JSON.parse(await readFile(outputPath, 'utf8')), receipt);
});

test('provider receipt blocks when the scenario artifact is absent', async () => {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'stackkits-provider-receipt-missing-'));
  const outputPath = path.join(dir, 'provider-receipt.json');
  const cleanupPath = path.join(dir, 'cleanup-ledger.json');
  await writeFile(cleanupPath, JSON.stringify(completedCleanup('SK-S3', 'centron-managed')));
  const receipt = await renderCloudProviderReceiptFile({
    scenarioId: 'SK-S3',
    provider: 'centron-managed',
    artifactPath: path.join(dir, 'missing.json'),
    cleanupPath,
    outputPath,
    source,
  });

  assert.equal(receipt.status, 'blocked');
  assert.match(receipt.failures.join('\n'), /cannot read scenario artifact/);
  assert.equal(JSON.parse(await readFile(outputPath, 'utf8')).status, 'blocked');
});

test('release validation binds a provider receipt to scenario, provider, source SHA, run, and artifact', () => {
  const artifact = {
    scenarioId: 'SK-S3',
    status: 'passed',
    browserUrl: 'https://demo.example.test/',
    target: { provider: 'ionos-managed' },
  };
  const receipt = renderCloudProviderReceipt({
    scenarioId: 'SK-S3',
    provider: 'ionos-managed',
    artifact,
    cleanup: completedCleanup('SK-S3', 'ionos-managed'),
    source,
  });

  assert.deepEqual(validateCloudProviderReceipt({
    receipt,
    scenarioId: 'SK-S3',
    provider: 'ionos-managed',
    sourceSha: source.sha,
    runId: source.runId,
    artifact,
  }), []);

  const errors = validateCloudProviderReceipt({
    receipt,
    scenarioId: 'SK-S3',
    provider: 'centron-managed',
    sourceSha: 'b'.repeat(40),
    runId: '9999',
    artifact,
  });
  assert.match(errors.join('\n'), /instead of centron-managed/);
  assert.match(errors.join('\n'), /source SHA/);
  assert.match(errors.join('\n'), /run ID/);
});

test('provider receipt blocks skipped cleanup, residue, and released-tag evidence', () => {
  const artifact = {
    scenarioId: 'SK-S2',
    status: 'passed',
    browserUrl: 'https://demo.example.test/',
    target: { provider: 'centron-managed' },
  };
  const cleanup = completedCleanup('SK-S2', 'centron-managed', {
    status: 'skipped',
    remainingResources: [{ kind: 'simulation', id: 'sim-residue', provider: 'centron-managed' }],
    testedSource: {
      mode: 'released-tag',
      sha: source.sha,
      bundleSha256: '',
      releaseVersion: 'v0.7.0-beta.1',
    },
  });
  const receipt = renderCloudProviderReceipt({
    scenarioId: 'SK-S2',
    provider: 'centron-managed',
    artifact,
    cleanup,
    source,
  });

  assert.equal(receipt.status, 'blocked');
  assert.match(receipt.failures.join('\n'), /status.*skipped/);
  assert.match(receipt.failures.join('\n'), /remaining resource/);
  assert.match(receipt.failures.join('\n'), /released-tag/);
  assert.match(receipt.failures.join('\n'), /releaseVersion must be empty/);
});

test('provider receipt blocks when cleanup ledger is absent', async () => {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'stackkits-provider-cleanup-missing-'));
  const artifactPath = path.join(dir, 'homelab.json');
  const outputPath = path.join(dir, 'provider-receipt.json');
  await writeFile(artifactPath, JSON.stringify({
    scenarioId: 'SK-S3', status: 'passed', browserUrl: 'https://demo.example.test/', target: { provider: 'ionos-managed' },
  }));

  const receipt = await renderCloudProviderReceiptFile({
    scenarioId: 'SK-S3',
    provider: 'ionos-managed',
    artifactPath,
    cleanupPath: path.join(dir, 'missing-cleanup.json'),
    outputPath,
    source,
  });

  assert.equal(receipt.status, 'blocked');
  assert.match(receipt.failures.join('\n'), /cannot read cleanup ledger/);
});

test('provider receipt rejects protected persistent demo infrastructure', () => {
  const cleanup = completedCleanup('SK-S3', 'ionos-managed');
  cleanup.allocationIntent = {
    ...cleanup.allocationIntent,
    lifecycle: 'persistent-demo',
    protectedPersistent: true,
  };
  const receipt = renderCloudProviderReceipt({
    scenarioId: 'SK-S3',
    provider: 'ionos-managed',
    artifact: { scenarioId: 'SK-S3', status: 'passed', browserUrl: 'https://demo.example.test/', target: { provider: 'ionos-managed' } },
    cleanup,
    source,
  });
  assert.equal(receipt.status, 'blocked');
  assert.match(receipt.failures.join('\n'), /not an unprotected ephemeral release-smoke resource/);
});
