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

test('traffic parser binds GitHub TCP IPs to observed DNS and never treats unknown public IPs as local', () => {
  const root = mkdtempSync(path.join(tmpdir(), 'stackkit-runtime-traffic-'))
  const input = path.join(root, 'tcpdump.log')
  const output = path.join(root, 'network-events.jsonl')
  writeFileSync(input, [
    '2026-07-26 12:00:00.000000 eth0 Out IP 172.18.0.2.40000 > 127.0.0.53.53: 4242+ A? api.github.com. (32)',
    '2026-07-26 12:00:00.010000 eth0 In IP 127.0.0.53.53 > 172.18.0.2.40000: 4242 1/0/0 A 140.82.113.21 (48)',
    '2026-07-26 12:00:00.020000 eth0 Out IP 172.18.0.2.12345 > 172.18.0.3.443: Flags [S]',
    '2026-07-26 12:00:00.500000 eth0 Out IP 172.18.0.2.12345 > 140.82.113.21.443: Flags [S]',
    '2026-07-26 12:00:01.000000 eth0 Out IP 172.18.0.2.12345 > 8.8.8.8.443: Flags [S]',
    '2026-07-26 12:00:01.500000 eth0 Out IP6 fd00::2.12345 > fd00::3.443: Flags [S]',
    '2026-07-26 12:00:02.000000 eth0 Out IP6 fd00::2.12345 > 2001:4860:4860::8888.443: Flags [S]',
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
  assert.deepEqual(
    events.find((event) => event.host === '140.82.113.21'),
    {
      schemaVersion: 'stackkit.network-event/v1',
      observedAt: events.find((event) => event.host === '140.82.113.21').observedAt,
      kind: 'tcp',
      host: '140.82.113.21',
      port: 443,
      scope: 'github',
      resolvedHost: 'api.github.com'
    }
  )
  assert.equal(events.find((event) => event.host === '8.8.8.8').scope, 'external')
  assert.equal(events.find((event) => event.host === 'fd00::3').scope, 'local')
  assert.equal(events.find((event) => event.host === '2001:4860:4860::8888').scope, 'external')
})

test('traffic parser rejects future, cross-flow, and ambiguous DNS bindings', () => {
  const root = mkdtempSync(path.join(tmpdir(), 'stackkit-runtime-traffic-ordering-'))
  const input = path.join(root, 'tcpdump.log')
  const output = path.join(root, 'network-events.jsonl')
  writeFileSync(input, [
    // A response cannot bind to a future query with the same transaction ID.
    '2026-07-26 12:00:00.000000 eth0 In IP 127.0.0.53.53 > 172.18.0.2.40000: 100 1/0/0 A 140.82.113.20 (48)',
    '2026-07-26 12:00:00.100000 eth0 Out IP 172.18.0.2.40000 > 127.0.0.53.53: 100+ A? api.github.com. (32)',
    '2026-07-26 12:00:00.200000 eth0 Out IP 172.18.0.2.12345 > 140.82.113.20.443: Flags [S]',
    // A response for another client cannot bind this client's question.
    '2026-07-26 12:00:01.000000 eth0 Out IP 172.18.0.3.40000 > 127.0.0.53.53: 200+ A? api.github.com. (32)',
    '2026-07-26 12:00:01.100000 eth0 In IP 127.0.0.53.53 > 172.18.0.4.40000: 200 1/0/0 A 140.82.113.22 (48)',
    '2026-07-26 12:00:01.200000 eth0 Out IP 172.18.0.3.12345 > 140.82.113.22.443: Flags [S]',
    // Reusing an in-flight ID on one flow is ambiguous and must not bind.
    '2026-07-26 12:00:02.000000 eth0 Out IP 172.18.0.2.40000 > 127.0.0.53.53: 300+ A? api.github.com. (32)',
    '2026-07-26 12:00:02.100000 eth0 Out IP 172.18.0.2.40000 > 127.0.0.53.53: 300+ A? example.com. (32)',
    '2026-07-26 12:00:02.200000 eth0 In IP 127.0.0.53.53 > 172.18.0.2.40000: 300 1/0/0 A 140.82.113.23 (48)',
    '2026-07-26 12:00:02.300000 eth0 Out IP 172.18.0.2.12345 > 140.82.113.23.443: Flags [S]',
    ''
  ].join('\n'))
  const result = spawnSync(
    process.execPath,
    [fileURLToPath(new URL('./parse-standalone-traffic.mjs', import.meta.url)), input, output],
    {encoding: 'utf8'}
  )
  assert.equal(result.status, 0, result.stderr)
  const events = readFileSync(output, 'utf8').trim().split('\n').map((line) => JSON.parse(line))
  for (const host of ['140.82.113.20', '140.82.113.22', '140.82.113.23']) {
    assert.equal(events.find((event) => event.host === host).scope, 'external')
  }
})

test('traffic parser preserves TCP-before-answer violations and expires DNS bindings', () => {
  const root = mkdtempSync(path.join(tmpdir(), 'stackkit-runtime-traffic-validity-'))
  const input = path.join(root, 'tcpdump.log')
  const output = path.join(root, 'network-events.jsonl')
  writeFileSync(input, [
    '2026-07-26 12:00:00.000000 eth0 Out IP 172.18.0.2.40000 > 127.0.0.53.53: 400+ A? api.github.com. (32)',
    '2026-07-26 12:00:00.010000 eth0 Out IP 172.18.0.2.12345 > 140.82.113.24.443: Flags [S]',
    '2026-07-26 12:00:00.020000 eth0 In IP 127.0.0.53.53 > 172.18.0.2.40000: 400 1/0/0 A 140.82.113.24 (48)',
    '2026-07-26 12:00:00.030000 eth0 Out IP 172.18.0.2.12345 > 140.82.113.24.443: Flags [S]',
    '2026-07-26 12:01:00.021000 eth0 Out IP 172.18.0.2.12345 > 140.82.113.24.443: Flags [S]',
    ''
  ].join('\n'))
  const result = spawnSync(
    process.execPath,
    [fileURLToPath(new URL('./parse-standalone-traffic.mjs', import.meta.url)), input, output],
    {encoding: 'utf8'}
  )
  assert.equal(result.status, 0, result.stderr)
  const tcpEvents = readFileSync(output, 'utf8').trim().split('\n')
    .map((line) => JSON.parse(line))
    .filter((event) => event.host === '140.82.113.24')
  assert.deepEqual(tcpEvents.map((event) => event.scope), ['external', 'github', 'external'])
})

test('traffic parser records every DNS question type and rejects undecoded port 53 traffic', () => {
  const root = mkdtempSync(path.join(tmpdir(), 'stackkit-runtime-traffic-dns-types-'))
  const input = path.join(root, 'tcpdump.log')
  const output = path.join(root, 'network-events.jsonl')
  writeFileSync(input, [
    '2026-07-26 12:00:00.000000 eth0 Out IP 172.18.0.2.40000 > 127.0.0.53.53: 500+ HTTPS? api.kombify.io. (36)',
    '2026-07-26 12:00:00.010000 eth0 In IP 127.0.0.53.53 > 172.18.0.2.40000: 500 0/1/0 (80)',
    ''
  ].join('\n'))
  const result = spawnSync(
    process.execPath,
    [fileURLToPath(new URL('./parse-standalone-traffic.mjs', import.meta.url)), input, output],
    {encoding: 'utf8'}
  )
  assert.equal(result.status, 0, result.stderr)
  const events = readFileSync(output, 'utf8').trim().split('\n').map((line) => JSON.parse(line))
  assert.deepEqual(events.map((event) => [event.kind, event.host, event.scope]), [
    ['dns', 'api.kombify.io', 'external']
  ])

  for (const line of [
    '2026-07-26 12:00:01.000000 eth0 Out IP 172.18.0.2.40000 > 127.0.0.53.53: 501+ [1au] (28)',
    '2026-07-26 12:00:02.000000 eth0 Out IP 172.18.0.2.40000 > 127.0.0.53.53: Flags [S]'
  ]) {
    writeFileSync(input, `${line}\n`)
    const denied = spawnSync(
      process.execPath,
      [fileURLToPath(new URL('./parse-standalone-traffic.mjs', import.meta.url)), input, output],
      {encoding: 'utf8'}
    )
    assert.notEqual(denied.status, 0)
    assert.match(denied.stderr, /DNS|port 53/u)
  }
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
    'release fixture must resolve to exactly one unique digest',
    "tcpdump -tttt -i any -nn -l '(udp port 53 or tcp)'"
  ]) {
    assert.match(harness, new RegExp(fragment.replaceAll(/[.*+?^${}()|[\]\\]/gu, '\\$&'), 'u'))
  }
  assert.ok(
    harness.indexOf('docker pull "$image"') < harness.indexOf('tcpdump -tttt -i any'),
    'optional preload must finish before recorded traffic starts'
  )
})
