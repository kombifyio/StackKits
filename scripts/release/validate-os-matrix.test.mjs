import assert from 'node:assert/strict';
import { test } from 'node:test';
import { summarizeOSMatrix, validateOSMatrix } from './validate-os-matrix.mjs';

function validMatrix(overrides = {}) {
  return {
    schemaVersion: 1,
    kit: 'basement-kit',
    stackkitVersion: 'v0.6.0',
    runId: 'osmx-1',
    generatedAt: '2026-07-12T10:00:00Z',
    results: [
      {
        os: { id: 'ubuntu-24.04-amd64', distroFamily: 'ubuntu', version: '24.04', arch: 'amd64' },
        runId: 'osmx-1',
        lane: 'vm',
        overall: 'supported',
        stages: [
          { stage: 'cpu-baseline', status: 'supported' },
          { stage: 'install', status: 'supported', exitCode: 0, durationMs: 12000 },
          { stage: 'apply', status: 'supported' },
          { stage: 'service-up', status: 'supported' },
          { stage: 'cleanup', status: 'supported' },
        ],
        target: {},
        generatedAt: '2026-07-12T09:55:00Z',
      },
    ],
    ...overrides,
  };
}

const NOW = Date.parse('2026-07-13T00:00:00Z');

test('valid matrix passes with freshness policy', () => {
  assert.deepEqual(validateOSMatrix(validMatrix(), { maxAgeDays: 14, now: NOW, expectKit: 'basement-kit', expectVersionPrefix: 'v0.6' }), []);
});

test('rejects wrong schema version, empty results, bad grades', () => {
  const errors = validateOSMatrix({ schemaVersion: 2, runId: '', generatedAt: 'nope', results: [] });
  assert.match(errors.join('\n'), /schemaVersion must be 1/);
  assert.match(errors.join('\n'), /runId/);
  assert.match(errors.join('\n'), /generatedAt/);
  assert.match(errors.join('\n'), /non-empty array/);

  const bad = validMatrix();
  bad.results[0].overall = 'green';
  bad.results[0].lane = 'cloud';
  bad.results[0].stages[0].stage = 'compile';
  assert.match(validateOSMatrix(bad).join('\n'), /overall invalid/);
  assert.match(validateOSMatrix(bad).join('\n'), /lane invalid/);
  assert.match(validateOSMatrix(bad).join('\n'), /stage unknown/);
});

test('redaction gate rejects private fields and populated target', () => {
  const withTarget = validMatrix();
  withTarget.results[0].target = { host: '198.51.100.7' };
  assert.match(validateOSMatrix(withTarget).join('\n'), /redaction leak/);

  const withEvidence = validMatrix();
  withEvidence.results[0].stages[1].evidencePath = 'artifacts/os-matrix/run/x.log';
  assert.match(validateOSMatrix(withEvidence).join('\n'), /evidencePath.*redaction leak/);

  const withOSRelease = validMatrix();
  withOSRelease.results[0].osReleaseRaw = 'ID=ubuntu';
  assert.match(validateOSMatrix(withOSRelease).join('\n'), /osReleaseRaw.*redaction leak/);

  const withMDNS = validMatrix();
  withMDNS.results[0].mdnsHost = 'osm-run-ubuntu';
  assert.match(validateOSMatrix(withMDNS).join('\n'), /mdnsHost.*redaction leak/);
});

test('redaction gate rejects RFC1918 addresses anywhere', () => {
  const leaked = validMatrix();
  leaked.results[0].stages[1].detail = 'connected to 192.168.178.155 as root';
  assert.match(validateOSMatrix(leaked).join('\n'), /RFC1918/);
});

test('freshness policy rejects stale and future matrices', () => {
  const stale = validateOSMatrix(validMatrix(), { maxAgeDays: 14, now: Date.parse('2026-08-15T00:00:00Z') });
  assert.match(stale.join('\n'), /days old/);
  const future = validateOSMatrix(validMatrix({ generatedAt: '2027-01-01T00:00:00Z' }), { maxAgeDays: 14, now: NOW });
  assert.match(future.join('\n'), /future/);
});

test('version prefix and kit expectations', () => {
  const wrongKit = validateOSMatrix(validMatrix(), { expectKit: 'cloud-kit' });
  assert.match(wrongKit.join('\n'), /kit is "basement-kit"/);
  const wrongVersion = validateOSMatrix(validMatrix(), { expectVersionPrefix: 'v0.7' });
  assert.match(wrongVersion.join('\n'), /does not match release prefix/);
  // Unstamped matrices skip the version check (container-only runs).
  assert.deepEqual(validateOSMatrix(validMatrix({ stackkitVersion: '' }), { expectVersionPrefix: 'v0.7' }), []);
});

test('summary counts grades', () => {
  const matrix = validMatrix();
  matrix.results.push({
    ...matrix.results[0],
    os: { ...matrix.results[0].os, id: 'debian-12-amd64' },
    overall: 'partial',
  });
  const summary = summarizeOSMatrix(matrix);
  assert.match(summary, /2 targets \(1 supported \/ 1 partial \/ 0 unsupported\)/);
  assert.match(summary, /osmx-1/);
});
