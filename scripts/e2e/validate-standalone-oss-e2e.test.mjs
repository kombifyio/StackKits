import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import test from 'node:test'

import {
  validateStandaloneOSSE2E,
  validateStandaloneTraffic
} from './validate-standalone-oss-e2e.mjs'

const phaseCommands = {
  authoring: ['stackkit init --owner-source=local', 'stackkit validate', 'stackkit generate'],
  runtime: ['stackkit apply', 'stackkit verify --json'],
  day2: [
    'stackkit upgrade --to latest',
    'stackkit drift detect --json',
    'stackkit rollback',
    'stackkit verify --json'
  ]
}

function sha256(data) {
  return createHash('sha256').update(data).digest('hex')
}

function fixture(t) {
  const root = mkdtempSync(path.join(tmpdir(), 'stackkit-oss-e2e-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  const events = [
    {
      schemaVersion: 'stackkit.network-event/v1',
      observedAt: '2026-07-26T10:00:01Z',
      kind: 'dns',
      host: 'api.github.com',
      scope: 'github'
    },
    {
      schemaVersion: 'stackkit.network-event/v1',
      observedAt: '2026-07-26T10:00:02Z',
      kind: 'http',
      host: 'github-release-fixture.localhost',
      port: 443,
      scope: 'fixture'
    },
    {
      schemaVersion: 'stackkit.network-event/v1',
      observedAt: '2026-07-26T10:00:03Z',
      kind: 'tcp',
      host: '127.0.0.1',
      port: 8080,
      scope: 'local'
    }
  ]
  const traffic = `${events.map((event) => JSON.stringify(event)).join('\n')}\n`
  const trafficPath = path.join(root, 'network-events.jsonl')
  writeFileSync(trafficPath, traffic)
  const startedAt = Date.parse('2026-07-26T10:00:00Z')
  const phases = Object.entries(phaseCommands).map(([id, commands], index) => ({
    id,
    status: 'pass',
    startedAt: new Date(startedAt + index * 120_000).toISOString(),
    finishedAt: new Date(startedAt + index * 120_000 + 90_000).toISOString(),
    durationSeconds: 90,
    commands,
    evidence: [{
      name: `${id}.json`,
      sha256: sha256(Buffer.from(`${id}-evidence`))
    }]
  }))
  const receipt = {
    schemaVersion: 'stackkit.oss-e2e-receipt/v1',
    source: {
      repository: 'kombifyio/stackKits',
      commit: 'a'.repeat(40),
      digest: `sha256:${'b'.repeat(64)}`
    },
    archive: {
      name: 'stackkits-basement-kit_v0.8.0-beta.1_linux_amd64.tar.gz',
      sha256: sha256(Buffer.from('archive')),
      sbomSha256: sha256(Buffer.from('sbom')),
      attestationSha256: sha256(Buffer.from('attestation')),
      releaseIndexSha256: sha256(Buffer.from('index'))
    },
    network: {
      recorder: 'stackkit.hermetic-network-log/v1',
      eventsSha256: sha256(Buffer.from(traffic)),
      eventCount: events.length
    },
    phases
  }
  const receiptPath = path.join(root, 'receipt.json')
  writeFileSync(receiptPath, `${JSON.stringify(receipt, null, 2)}\n`)
  return { events, receipt, receiptPath, trafficPath }
}

test('accepts the exact bounded three-phase OSS receipt and GitHub/local traffic', (t) => {
  const candidate = fixture(t)
  assert.equal(validateStandaloneTraffic(candidate.events).length, 3)
  const verified = validateStandaloneOSSE2E(candidate.receiptPath, candidate.trafficPath)
  assert.equal(verified.phases.length, 3)
  assert.equal(verified.network.eventCount, 3)
})

test('rejects over-budget, reordered, or incomplete phases', (t) => {
  const candidate = fixture(t)
  candidate.receipt.phases[0].durationSeconds = 601
  assert.throws(
    () => validateStandaloneOSSE2E(candidate.receipt, candidate.trafficPath),
    /durationSeconds/
  )
  candidate.receipt.phases[0].durationSeconds = 90
  candidate.receipt.phases.reverse()
  assert.throws(
    () => validateStandaloneOSSE2E(candidate.receipt, candidate.trafficPath),
    /phase order/
  )
})

test('rejects Kombify-controlled and unknown public hosts including DNS lookups', (t) => {
  const candidate = fixture(t)
  for (const host of ['api.kombify.io', 'stackkit.cc', 'example.com']) {
    assert.throws(
      () => validateStandaloneTraffic([{
        schemaVersion: 'stackkit.network-event/v1',
        observedAt: '2026-07-26T10:00:01Z',
        kind: 'dns',
        host,
        scope: 'github'
      }]),
      /network event/
    )
  }
  assert.throws(
    () => validateStandaloneTraffic([{
      schemaVersion: 'stackkit.network-event/v1',
      observedAt: '2026-07-26T10:00:01Z',
      kind: 'tcp',
      host: '127.999.0.1',
      port: 443,
      scope: 'local'
    }]),
    /non-allowlisted host/
  )
  assert.equal(validateStandaloneTraffic(candidate.events).length, 3)
})

test('rejects traffic-log tamper and secret-bearing receipt fields', (t) => {
  const candidate = fixture(t)
  const tamperedEvents = structuredClone(candidate.events)
  tamperedEvents[2].port = 8081
  writeFileSync(
    candidate.trafficPath,
    `${tamperedEvents.map((event) => JSON.stringify(event)).join('\n')}\n`
  )
  assert.throws(
    () => validateStandaloneOSSE2E(candidate.receipt, candidate.trafficPath),
    /traffic log digest/
  )
  candidate.receipt.network.eventsSha256 = sha256(Buffer.from('different'))
  candidate.receipt.operatorToken = 'must-never-enter-evidence'
  assert.throws(
    () => validateStandaloneOSSE2E(candidate.receipt, candidate.trafficPath),
    /forbidden evidence field/
  )
})

test('JSON Schema phase base admits the exact id and commands refined by each phase', () => {
  const schema = JSON.parse(readFileSync(
    new URL('../../schemas/standalone-oss-e2e-receipt.schema.json', import.meta.url),
    'utf8'
  ))
  assert.deepEqual(schema.$defs.phaseBase.properties.id, { type: 'string' })
  assert.deepEqual(schema.$defs.phaseBase.properties.commands, {
    type: 'array',
    items: { type: 'string' }
  })
})
