#!/usr/bin/env node

import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { spawnSync } from 'node:child_process'
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

import { validateStandaloneRuntimeE2E } from './validate-standalone-runtime-e2e.mjs'

const digest = (value) => createHash('sha256').update(value).digest('hex')

function fixture() {
  const root = mkdtempSync(path.join(tmpdir(), 'stackkit-runtime-e2e-'))
  const apply = Buffer.from('apply success\n')
  const verify = Buffer.from('{"schemaVersion":"stackkit.command-result/v1","command":"stackkit verify","status":"success","data":{}}\n')
  const traffic = Buffer.from('{"schemaVersion":"stackkit.network-event/v1","observedAt":"2026-07-26T12:00:00Z","kind":"tcp","host":"127.0.0.1","port":18080,"scope":"local"}\n')
  writeFileSync(path.join(root, 'apply.log'), apply)
  writeFileSync(path.join(root, 'verify.json'), verify)
  writeFileSync(path.join(root, 'network-events.jsonl'), traffic)
  const evidence = {
    schemaVersion: 'stackkit.oss-runtime-e2e-evidence/v1',
    source: {
      repository: 'kombifyio/stackKits',
      commit: 'a'.repeat(40),
      digest: `sha256:${'b'.repeat(64)}`
    },
    archive: {
      name: 'stackkits-basement-kit_v0.8.0-beta.1_linux_amd64.tar.gz',
      sha256: 'c'.repeat(64),
      sbomSha256: 'd'.repeat(64),
      attestationSha256: 'e'.repeat(64),
      releaseIndexSha256: 'f'.repeat(64)
    },
    network: {
      recorder: 'stackkit.hermetic-network-log/v1',
      eventsSha256: digest(traffic),
      eventCount: 1
    },
    phase: {
      id: 'runtime',
      status: 'pass',
      startedAt: '2026-07-26T12:00:00Z',
      finishedAt: '2026-07-26T12:00:30Z',
      durationSeconds: 30,
      commands: ['stackkit apply', 'stackkit verify --json'],
      evidence: [
        {name: 'apply.log', sha256: digest(apply)},
        {name: 'verify.json', sha256: digest(verify)}
      ]
    }
  }
  const evidencePath = path.join(root, 'runtime-evidence.json')
  writeFileSync(evidencePath, `${JSON.stringify(evidence, null, 2)}\n`)
  return {root, evidence, evidencePath, trafficPath: path.join(root, 'network-events.jsonl')}
}

test('accepts exact runtime evidence', () => {
  const item = fixture()
  assert.equal(validateStandaloneRuntimeE2E(item.evidencePath, item.trafficPath).phase.status, 'pass')
})

test('rejects evidence and traffic tamper', () => {
  const item = fixture()
  writeFileSync(path.join(item.root, 'apply.log'), 'changed\n')
  assert.throws(() => validateStandaloneRuntimeE2E(item.evidencePath, item.trafficPath), /was changed/u)

  const next = fixture()
  writeFileSync(next.trafficPath, `${readFileSync(next.trafficPath, 'utf8')}{"schemaVersion":"stackkit.network-event/v1","observedAt":"2026-07-26T12:00:01Z","kind":"dns","host":"api.kombify.io","scope":"external"}\n`)
  assert.throws(() => validateStandaloneRuntimeE2E(next.evidencePath, next.trafficPath), /Kombify-controlled/u)
})

test('traffic parser never classifies a public IP as local', () => {
  const root = mkdtempSync(path.join(tmpdir(), 'stackkit-runtime-traffic-'))
  const input = path.join(root, 'tcpdump.log')
  const output = path.join(root, 'network-events.jsonl')
  writeFileSync(input, [
    '12:00:00.000000 IP 172.18.0.2.12345 > 172.18.0.3.443: Flags [S]',
    '12:00:01.000000 IP 172.18.0.2.12345 > 8.8.8.8.443: Flags [S]',
    ''
  ].join('\n'))
  const result = spawnSync(
    process.execPath,
    [fileURLToPath(new URL('./parse-standalone-traffic.mjs', import.meta.url)), input, output],
    {encoding: 'utf8'}
  )
  assert.equal(result.status, 0, result.stderr)
  const events = readFileSync(output, 'utf8').trim().split('\n').map((line) => JSON.parse(line))
  assert.equal(events.find((event) => event.host === '172.18.0.3').scope, 'local')
  assert.equal(events.find((event) => event.host === '8.8.8.8').scope, 'external')
})

test('harness uses the extracted public binary and bounded public workflow', () => {
  const harness = readFileSync(new URL('./run-standalone-oss-runtime-e2e.sh', import.meta.url), 'utf8')
  for (const fragment of [
    'PATH="$extract_dir:$PATH"',
    'timeout 600 stackkit init --owner-source=local',
    'timeout 600 stackkit validate',
    'timeout 600 stackkit generate',
    'STACKKIT_E2E_PRELOAD_IMAGES',
    'docker pull "$image"',
    'stackkit apply',
    'stackkit verify --json',
    'STACKKIT_RELEASE_FIXTURE_URL',
    'release fixture must resolve to exactly one unique digest'
  ]) {
    assert.match(harness, new RegExp(fragment.replaceAll(/[.*+?^${}()|[\]\\]/gu, '\\$&'), 'u'))
  }
  assert.ok(
    harness.indexOf('docker pull "$image"') < harness.indexOf('tcpdump -i any'),
    'optional preload must finish before recorded traffic starts'
  )
})
