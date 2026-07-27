#!/usr/bin/env node

import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { readComposeOriginScope } from './compose-origin-scope.mjs'
import { validateStandaloneTraffic } from './validate-standalone-oss-e2e.mjs'

const schema = 'stackkit.oss-runtime-e2e-evidence/v3'
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

export function validateStandaloneRuntimeE2E(evidencePath, trafficPath, originScopePath) {
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

  exactKeys(
    evidence.archive,
    [
      'name',
      'sha256',
      'sbomSha256',
      'attestationSha256',
      'releaseIndexSha256',
      'releaseIndexAttestationSha256',
      'trustedRootSha256',
      'releaseBootstrapSha256'
    ],
    'archive'
  )
  if (typeof evidence.archive.name !== 'string' ||
      path.basename(evidence.archive.name) !== evidence.archive.name ||
      !/^stackkits-basement-kit_.+_linux_(?:amd64|arm64)\.tar\.gz$/u.test(evidence.archive.name)) {
    fail('archive.name is not a safe Basement Linux release archive')
  }
  for (const field of [
    'sha256',
    'sbomSha256',
    'attestationSha256',
    'releaseIndexSha256',
    'releaseIndexAttestationSha256',
    'trustedRootSha256',
    'releaseBootstrapSha256'
  ]) {
    requireDigest(evidence.archive[field], `archive.${field}`)
  }
  const evidenceDirectory = path.dirname(path.resolve(evidencePath))
  const releaseBootstrapRaw = readFileSync(path.join(evidenceDirectory, 'release-bootstrap.json'))
  if (digest(releaseBootstrapRaw) !== evidence.archive.releaseBootstrapSha256) {
    fail('release bootstrap evidence differs from the attested runtime evidence')
  }
  const releaseBootstrap = JSON.parse(releaseBootstrapRaw)
  rejectSecrets(releaseBootstrap, 'releaseBootstrap')
  exactKeys(
    releaseBootstrap,
    ['schemaVersion', 'command', 'status', 'data'],
    'release bootstrap'
  )
  if (releaseBootstrap.schemaVersion !== 'stackkit.command-result/v1' ||
      releaseBootstrap.command !== 'stackkit kit verify' ||
      releaseBootstrap.status !== 'success' ||
      !Array.isArray(releaseBootstrap.data) ||
      releaseBootstrap.data.length !== 1) {
    fail('init did not bind the exact release receipt to the runtime archive')
  }
  const receipt = releaseBootstrap.data[0]
  exactKeys(
    receipt,
    [
      'schemaVersion',
      'kit',
      'version',
      'channel',
      'platform',
      'archiveSha256',
      'sbomSha256',
      'attestationSha256',
      'attestationIssuer',
      'attestationSubject',
      'trustedRootSha256',
      'indexSha256',
      'indexAttestationSha256',
      'verifiedAt',
      'installDir'
    ],
    'release receipt'
  )
  exactKeys(receipt.platform, ['os', 'arch'], 'release receipt platform')
  const versionMatch = /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(beta|edge)\.(0|[1-9][0-9]*))?$/u.exec(receipt.version)
  const expectedChannel = versionMatch?.[4] ?? 'stable'
  const expectedArchiveName = versionMatch === null ||
      !['amd64', 'arm64'].includes(receipt.platform.arch)
    ? ''
    : `stackkits-basement-kit_${receipt.version.slice(1)}_linux_${receipt.platform.arch}.tar.gz`
  const expectedInstallSuffix = versionMatch === null
    ? ''
    : `/.stackkit/releases/basement-kit/${receipt.version}/linux-${receipt.platform.arch}`
  if (receipt.schemaVersion !== 'stackkit.release-receipt/v1' ||
      receipt.kit !== 'basement-kit' ||
      versionMatch === null ||
      receipt.channel !== expectedChannel ||
      receipt.platform.os !== 'linux' ||
      !['amd64', 'arm64'].includes(receipt.platform.arch) ||
      evidence.archive.name !== expectedArchiveName ||
      receipt.archiveSha256 !== evidence.archive.sha256 ||
      receipt.sbomSha256 !== evidence.archive.sbomSha256 ||
      receipt.attestationSha256 !== evidence.archive.attestationSha256 ||
      receipt.attestationIssuer !== 'https://token.actions.githubusercontent.com' ||
      receipt.attestationSubject !== evidence.archive.name ||
      receipt.trustedRootSha256 !== evidence.archive.trustedRootSha256 ||
      receipt.indexSha256 !== evidence.archive.releaseIndexSha256 ||
      receipt.indexAttestationSha256 !== evidence.archive.releaseIndexAttestationSha256 ||
      typeof receipt.installDir !== 'string' ||
      !receipt.installDir.replaceAll('\\', '/').endsWith(expectedInstallSuffix) ||
      !Number.isFinite(Date.parse(receipt.verifiedAt)) ||
      typeof receipt.verifiedAt !== 'string' ||
      !receipt.verifiedAt.endsWith('Z')) {
    fail('init did not bind the exact release receipt to the runtime archive')
  }

  exactKeys(
    evidence.network,
    ['recorder', 'captureMode', 'eventsSha256', 'originScopeSha256', 'eventCount'],
    'network'
  )
  if (evidence.network.recorder !== 'stackkit.hermetic-network-log/v2') fail('unsupported network recorder')
  if (evidence.network.captureMode !== 'host-forbidden-dns+compose-origin-initial-syn/v1') {
    fail('unsupported network capture mode')
  }
  requireDigest(evidence.network.eventsSha256, 'network.eventsSha256')
  requireDigest(evidence.network.originScopeSha256, 'network.originScopeSha256')
  if (!Number.isInteger(evidence.network.eventCount) || evidence.network.eventCount < 1) {
    fail('network.eventCount must be positive')
  }
  const trafficRaw = readFileSync(trafficPath)
  if (trafficRaw.length === 0 || trafficRaw[trafficRaw.length - 1] !== 0x0a) fail('traffic log must be newline-terminated')
  const traffic = trafficRaw.toString('utf8').trimEnd().split(/\r?\n/u).map((line) => JSON.parse(line))
  validateStandaloneTraffic(traffic)
  if (digest(trafficRaw) !== evidence.network.eventsSha256 ||
      traffic.length !== evidence.network.eventCount) {
    fail('network evidence digest or event count differs from the capture')
  }
  const originScope = readComposeOriginScope(originScopePath)
  if (digest(originScope.raw) !== evidence.network.originScopeSha256) {
    fail('Compose origin scope digest differs from the evidence')
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
  if (process.argv.length !== 5) {
    console.error('usage: validate-standalone-runtime-e2e.mjs <runtime-evidence.json> <network-events.jsonl> <compose-origin-scope.json>')
    process.exitCode = 2
  } else {
    validateStandaloneRuntimeE2E(process.argv[2], process.argv[3], process.argv[4])
    console.log('standalone OSS runtime E2E evidence passed')
  }
}
