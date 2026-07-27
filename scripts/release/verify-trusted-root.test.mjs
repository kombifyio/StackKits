import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { spawnSync } from 'node:child_process'
import test from 'node:test'

const script = path.resolve('scripts/release/verify-trusted-root.mjs')

function run(policy, trustedRoot) {
  const root = mkdtempSync(path.join(tmpdir(), 'stackkit-trust-policy-'))
  const policyPath = path.join(root, 'policy.json')
  const trustedRootPath = path.join(root, 'trusted-root.jsonl')
  writeFileSync(policyPath, JSON.stringify(policy))
  writeFileSync(trustedRootPath, trustedRoot)
  const result = spawnSync(process.execPath, [
    script,
    '--policy', policyPath,
    '--trusted-root', trustedRootPath
  ], { encoding: 'utf8' })
  rmSync(root, { recursive: true, force: true })
  return result
}

test('accepts only the exact out-of-band pinned root bytes', () => {
  const trustedRoot = '{"trusted":"root"}\n'
  const expected = createHash('sha256').update(trustedRoot.trim()).digest('hex')
  const policy = {
    schemaVersion: 'stackkit.release-trust-policy/v1',
    sigstoreTrustedRootDocumentSha256: [
      '0000000000000000000000000000000000000000000000000000000000000000',
      expected
    ]
  }

  const accepted = run(policy, trustedRoot)
  assert.equal(accepted.status, 0, accepted.stderr)
  assert.equal(accepted.stdout.trim(), expected)

  const rejected = run(policy, `${trustedRoot.trim()}tampered\n`)
  assert.notEqual(rejected.status, 0)
  assert.match(rejected.stderr, /contains no document allowed/u)
})

test('rejects malformed, duplicate, and extended trust policies', () => {
  const digest = 'a'.repeat(64)
  for (const policy of [
    { schemaVersion: 'stackkit.release-trust-policy/v1', sigstoreTrustedRootDocumentSha256: [] },
    { schemaVersion: 'stackkit.release-trust-policy/v1', sigstoreTrustedRootDocumentSha256: ['ABC'] },
    { schemaVersion: 'stackkit.release-trust-policy/v1', sigstoreTrustedRootDocumentSha256: [digest, digest] },
    {
      schemaVersion: 'stackkit.release-trust-policy/v1',
      sigstoreTrustedRootDocumentSha256: [digest],
      override: true
    }
  ]) {
    const result = run(policy, 'root\n')
    assert.notEqual(result.status, 0)
    assert.match(result.stderr, /release trust policy is invalid/u)
  }
})
