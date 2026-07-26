#!/usr/bin/env node

import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { validateStandaloneTraffic } from './validate-standalone-oss-e2e.mjs'

const schema = 'stackkit.oss-runtime-e2e-evidence/v1'
const sha256Pattern = /^[0-9a-f]{64}$/u
const sourceDigestPattern = /^sha256:[0-9a-f]{64}$/u
const commitPattern = /^[0-9a-f]{40}$/u
const forbiddenKey = /(?:credential|password|private.?key|secret|token)/iu

function fail(message) {
  throw new Error(`standalone OSS runtime E2E validation failed: ${message}`)
}

function exactKeys(value, keys, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) fail(`${label} must be an object`)
  const actual = Object.keys(value).sort()
  const expected = [...keys].sort()
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) {
    fail(`${label} fields must be exactly ${expected.join(', ')}`)
  }
}

function rejectSecrets(value, trail = 'evidence') {
  if (Array.isArray(value)) {
    value.forEach((item, index) => rejectSecrets(item, `${trail}[${index}]`))
    return
  }
  if (!value || typeof value !== 'object') return
  for (const [key, item] of Object.entries(value)) {
    if (forbiddenKey.test(key)) fail(`forbidden evidence field ${trail}.${key}`)
    rejectSecrets(item, `${trail}.${key}`)
  }
}

function digest(data) {
  return createHash('sha256').update(data).digest('hex')
}

function requireDigest(value, label) {
  if (typeof value !== 'string' || !sha256Pattern.test(value)) fail(`${label} must be a lowercase SHA-256`)
}

function parseTime(value, label) {
  if (typeof value !== 'string' || !value.endsWith('Z')) fail(`${label} must be an RFC3339 UTC instant`)
  const parsed = Date.parse(value)
  if (!Number.isFinite(parsed)) fail(`${label} must be an RFC3339 UTC instant`)
  return parsed
}

export function validateStandaloneRuntimeE2E(evidencePath, trafficPath) {
  const evidenceRaw = readFileSync(evidencePath)
  const evidence = JSON.parse(evidenceRaw)
  rejectSecrets(evidence)
  exactKeys(evidence, ['schemaVersion', 'source', 'archive', 'network', 'phase'], 'evidence')
  if (evidence.schemaVersion !== schema) fail('unsupported schemaVersion')

  exactKeys(evidence.source, ['repository', 'commit', 'digest'], 'source')
  if (evidence.source.repository !== 'kombifyio/stackKits' ||
      !commitPattern.test(evidence.source.commit) ||
      !sourceDigestPattern.test(evidence.source.digest)) {
    fail('source does not identify one exact public StackKits tree')
  }

  exactKeys(evidence.archive, ['name', 'sha256', 'sbomSha256', 'attestationSha256', 'releaseIndexSha256'], 'archive')
  if (typeof evidence.archive.name !== 'string' ||
      path.basename(evidence.archive.name) !== evidence.archive.name ||
      !/^stackkits-basement-kit_.+_linux_(?:amd64|arm64)\.tar\.gz$/u.test(evidence.archive.name)) {
    fail('archive.name is not a safe Basement Linux release archive')
  }
  for (const field of ['sha256', 'sbomSha256', 'attestationSha256', 'releaseIndexSha256']) {
    requireDigest(evidence.archive[field], `archive.${field}`)
  }

  exactKeys(evidence.network, ['recorder', 'captureMode', 'eventsSha256', 'eventCount'], 'network')
  if (evidence.network.recorder !== 'stackkit.hermetic-network-log/v2') fail('unsupported network recorder')
  if (evidence.network.captureMode !== 'bidirectional-dns+outbound-initial-syn/v1') {
    fail('unsupported network capture mode')
  }
  requireDigest(evidence.network.eventsSha256, 'network.eventsSha256')
  const trafficRaw = readFileSync(trafficPath)
  if (trafficRaw.length === 0 || trafficRaw[trafficRaw.length - 1] !== 0x0a) fail('traffic log must be newline-terminated')
  const traffic = trafficRaw.toString('utf8').trimEnd().split(/\r?\n/u).map((line) => JSON.parse(line))
  validateStandaloneTraffic(traffic)
  if (digest(trafficRaw) !== evidence.network.eventsSha256 ||
      traffic.length !== evidence.network.eventCount) {
    fail('network evidence digest or event count differs from the capture')
  }

  const phase = evidence.phase
  exactKeys(phase, ['id', 'status', 'startedAt', 'finishedAt', 'durationSeconds', 'commands', 'evidence'], 'phase')
  if (phase.id !== 'runtime' || phase.status !== 'pass') fail('runtime phase did not pass')
  if (JSON.stringify(phase.commands) !== JSON.stringify(['stackkit apply', 'stackkit verify --json'])) {
    fail('runtime commands differ from the public workflow')
  }
  const started = parseTime(phase.startedAt, 'phase.startedAt')
  const finished = parseTime(phase.finishedAt, 'phase.finishedAt')
  if (!Number.isInteger(phase.durationSeconds) || phase.durationSeconds < 0 ||
      phase.durationSeconds > 600 || Math.round((finished - started) / 1000) !== phase.durationSeconds) {
    fail('runtime phase duration is inconsistent or exceeds 600 seconds')
  }
  if (!Array.isArray(phase.evidence) || phase.evidence.length !== 2) fail('runtime phase requires Apply and Verify evidence')
  const evidenceDirectory = path.dirname(path.resolve(evidencePath))
  const expectedNames = ['apply.log', 'verify.json']
  phase.evidence.forEach((item, index) => {
    exactKeys(item, ['name', 'sha256'], `phase.evidence[${index}]`)
    if (item.name !== expectedNames[index]) fail('runtime evidence order or name is not canonical')
    requireDigest(item.sha256, `phase.evidence[${index}].sha256`)
    if (digest(readFileSync(path.join(evidenceDirectory, item.name))) !== item.sha256) {
      fail(`runtime evidence ${item.name} was changed`)
    }
  })
  const verify = JSON.parse(readFileSync(path.join(evidenceDirectory, 'verify.json')))
  if (verify.schemaVersion !== 'stackkit.command-result/v1' || verify.status !== 'success') {
    fail('stackkit verify did not emit a successful command result')
  }
  return evidence
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  if (process.argv.length !== 4) {
    console.error('usage: validate-standalone-runtime-e2e.mjs <runtime-evidence.json> <network-events.jsonl>')
    process.exitCode = 2
  } else {
    validateStandaloneRuntimeE2E(process.argv[2], process.argv[3])
    console.log('standalone OSS runtime E2E evidence passed')
  }
}
