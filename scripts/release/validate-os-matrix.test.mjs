import assert from 'node:assert/strict';
import { test } from 'node:test';
import { summarizeOSMatrix, validateOSMatrix } from './validate-os-matrix.mjs';

function validMatrix(overrides = {}) {
  return {
    schemaVersion: 2,
    stackkitsVersion: 'unreleased',
    generatedAt: '2026-07-18T10:00:00Z',
    results: [{
      os: { family: 'linux', distribution: 'ubuntu', version: '24.04' },
      grade: 'unverified',
      reasonCodes: ['current-candidate-receipt-pending'],
    }],
    ...overrides,
  };
}

test('valid OS-only unverified projection passes', () => {
  assert.deepEqual(validateOSMatrix(validMatrix(), {
    maxAgeDays: 14,
    now: Date.parse('2026-07-19T00:00:00Z'),
  }), []);
});

test('positive grades remain disabled until the receipt projector exists', () => {
  for (const grade of ['supported', 'preview', 'unsupported']) {
    const matrix = validMatrix();
    matrix.results[0].grade = grade;
    assert.match(validateOSMatrix(matrix).join('\n'), /must remain unverified until the receipt projector exists/);
  }
});

test('reason codes are closed and free text is forbidden', () => {
  const unknown = validMatrix();
  unknown.results[0].reasonCodes = ['Docker diagnostic passed'];
  assert.match(validateOSMatrix(unknown).join('\n'), /closed public reason codes/);

  const freeText = validMatrix();
  freeText.results[0].limitations = ['anything'];
  assert.match(validateOSMatrix(freeText).join('\n'), /limitations is not allowed/);

  const duplicateReason = validMatrix();
  duplicateReason.results[0].reasonCodes.push('current-candidate-receipt-pending');
  assert.match(validateOSMatrix(duplicateReason).join('\n'), /reasonCodes must be unique/);
});

test('rejects legacy lane, stage, architecture, kernel, evidence and target fields', () => {
  for (const [key, value] of Object.entries({
    lane: 'vm', stages: [], arch: 'amd64', kernel: '6.6', evidence: {}, target: {}, virtType: 'kvm', runId: 'legacy-run',
  })) {
    const matrix = validMatrix();
    matrix.results[0][key] = value;
    assert.match(validateOSMatrix(matrix).join('\n'), /not allowed|forbidden/);
  }
});

test('rejects infrastructure identities, addresses and duplicate OS rows', () => {
  const infrastructure = validMatrix();
  infrastructure.results[0].os.distribution = 'docker';
  assert.match(validateOSMatrix(infrastructure).join('\n'), /infrastructure\/runtime terminology/);

  const leaked = validMatrix();
  leaked.results[0].os.version = '192.168.50.77';
  assert.match(validateOSMatrix(leaked).join('\n'), /RFC1918/);

  const duplicate = validMatrix();
  duplicate.results.push(structuredClone(duplicate.results[0]));
  assert.match(validateOSMatrix(duplicate).join('\n'), /duplicates compatibility identity/);
});

test('kit-specific claims remain disabled with the receipt projector', () => {
  assert.match(validateOSMatrix(validMatrix(), { expectKit: 'basement-kit' }).join('\n'), /kit-specific evidence is unavailable/);
});

test('summary is explicit about missing authority', () => {
  const matrix = validMatrix();
  matrix.results.push({
    os: { family: 'linux', distribution: 'debian', version: '12' },
    grade: 'unverified',
    reasonCodes: ['os-policy-not-yet-admitted'],
  });
  assert.match(summarizeOSMatrix(matrix), /2 rows \(all unverified; receipt projector pending\)/);
});
