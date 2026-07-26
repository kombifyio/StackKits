#!/usr/bin/env node

import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { isIP } from 'node:net'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const receiptSchema = 'stackkit.oss-e2e-receipt/v1'
const eventSchema = 'stackkit.network-event/v1'
const recorderSchema = 'stackkit.hermetic-network-log/v2'
const captureMode = 'bidirectional-dns+outbound-initial-syn/v1'
const sha256Pattern = /^[0-9a-f]{64}$/u
const sourceDigestPattern = /^sha256:[0-9a-f]{64}$/u
const commitPattern = /^[0-9a-f]{40}$/u
const forbiddenEvidenceKey = /(?:credential|password|private.?key|secret|token)/iu
const forbiddenHostSuffixes = [
  'kombify.io',
  'kombify.me',
  'stackkit.cc'
]
const githubHostSuffixes = [
  'github.com',
  'githubassets.com',
  'githubusercontent.com'
]
const phaseContract = [
  {
    id: 'authoring',
    commands: ['stackkit init --owner-source=local', 'stackkit validate', 'stackkit generate']
  },
  {
    id: 'runtime',
    commands: ['stackkit apply', 'stackkit verify --json']
  },
  {
    id: 'day2',
    commands: [
      'stackkit upgrade --to latest',
      'stackkit drift detect --json',
      'stackkit rollback',
      'stackkit verify --json'
    ]
  }
]

function fail(message) {
  throw new Error(`standalone OSS E2E validation failed: ${message}`)
}

function exactKeys(value, keys, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    fail(`${label} must be an object`)
  }
  const actual = Object.keys(value).sort()
  const expected = [...keys].sort()
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) {
    fail(`${label} fields must be exactly ${expected.join(', ')}`)
  }
}

function rejectSecretFields(value, trail = 'receipt') {
  if (Array.isArray(value)) {
    value.forEach((item, index) => rejectSecretFields(item, `${trail}[${index}]`))
    return
  }
  if (!value || typeof value !== 'object') return
  for (const [key, item] of Object.entries(value)) {
    if (forbiddenEvidenceKey.test(key)) {
      fail(`forbidden evidence field ${trail}.${key}`)
    }
    rejectSecretFields(item, `${trail}.${key}`)
  }
}

function requireSHA256(value, label) {
  if (typeof value !== 'string' || !sha256Pattern.test(value)) {
    fail(`${label} must be a lowercase SHA-256 digest`)
  }
}

function parseInstant(value, label) {
  if (typeof value !== 'string' || !/Z$/u.test(value)) fail(`${label} must be an RFC3339 UTC instant`)
  const instant = Date.parse(value)
  if (!Number.isFinite(instant)) fail(`${label} must be an RFC3339 UTC instant`)
  return instant
}

function isSuffix(host, suffix) {
  return host === suffix || host.endsWith(`.${suffix}`)
}

function isLocalHost(host) {
  if (host === 'localhost' || host.endsWith('.localhost') || host.endsWith('.home.test') || host === '::1') return true
  if (isIP(host) === 0) return false
  if (/^127(?:\.\d{1,3}){3}$/u.test(host)) return true
  if (/^10(?:\.\d{1,3}){3}$/u.test(host)) return true
  if (/^192\.168(?:\.\d{1,3}){2}$/u.test(host)) return true
  const match = /^172\.(\d{1,3})(?:\.\d{1,3}){2}$/u.exec(host)
  if (match && Number(match[1]) >= 16 && Number(match[1]) <= 31) return true
  return /^f[cd][0-9a-f]*:/u.test(host)
}

function validateNetworkEvent(event, index) {
  const label = `network event ${index}`
  const allowedKeys = ['schemaVersion', 'observedAt', 'kind', 'host', 'scope']
  if (Object.hasOwn(event, 'port')) allowedKeys.push('port')
  if (Object.hasOwn(event, 'resolvedHost')) allowedKeys.push('resolvedHost')
  exactKeys(event, allowedKeys, label)
  if (event.schemaVersion !== eventSchema) fail(`${label} has unsupported schemaVersion`)
  parseInstant(event.observedAt, `${label}.observedAt`)
  if (!['dns', 'http', 'tcp'].includes(event.kind)) fail(`${label}.kind is unsupported`)
  if (typeof event.host !== 'string' || event.host !== event.host.trim().toLowerCase() ||
      event.host.endsWith('.') || event.host.length === 0) {
    fail(`${label}.host is not canonical`)
  }
  if (event.port !== undefined &&
      (!Number.isInteger(event.port) || event.port < 1 || event.port > 65535)) {
    fail(`${label}.port is invalid`)
  }
  if (event.resolvedHost !== undefined &&
      (event.kind !== 'tcp' || isIP(event.host) === 0 ||
       typeof event.resolvedHost !== 'string' ||
       event.resolvedHost !== event.resolvedHost.trim().toLowerCase() ||
       event.resolvedHost.endsWith('.') || event.resolvedHost.length === 0 ||
       isIP(event.resolvedHost) !== 0)) {
    fail(`${label}.resolvedHost is not a canonical TCP DNS binding`)
  }
  const observedHosts = [event.host]
  if (event.resolvedHost !== undefined) observedHosts.push(event.resolvedHost)
  const forbiddenHost = observedHosts.find((host) =>
    forbiddenHostSuffixes.some((suffix) => isSuffix(host, suffix))
  )
  if (forbiddenHost !== undefined) {
    fail(`${label} reaches Kombify-controlled host ${forbiddenHost}`)
  }
  const directGitHubHost = githubHostSuffixes.some((suffix) => isSuffix(event.host, suffix))
  const dnsBoundGitHubIP = (
    event.kind === 'tcp' &&
    isIP(event.host) !== 0 &&
    event.resolvedHost !== undefined &&
    githubHostSuffixes.some((suffix) => isSuffix(event.resolvedHost, suffix))
  )
  const allowed = (
    event.scope === 'github' &&
      (directGitHubHost || dnsBoundGitHubIP)
  ) || (
    event.scope === 'fixture' &&
      event.host === 'github-release-fixture.localhost'
  ) || (
    event.scope === 'local' &&
      isLocalHost(event.host)
  )
  if (!allowed) fail(`${label} reaches non-allowlisted host ${event.host}`)
  return event
}

export function validateStandaloneTraffic(events) {
  if (!Array.isArray(events) || events.length === 0) {
    fail('network event log must contain at least one observed event')
  }
  return events.map(validateNetworkEvent)
}

function readTraffic(trafficPath) {
  const raw = readFileSync(trafficPath)
  if (raw.length === 0 || raw[raw.length - 1] !== 0x0a) {
    fail('traffic log must be non-empty JSONL ending in a newline')
  }
  const lines = raw.toString('utf8').trimEnd().split(/\r?\n/u)
  const events = lines.map((line, index) => {
    try {
      return JSON.parse(line)
    } catch (error) {
      fail(`network event ${index} is not JSON: ${error.message}`)
    }
  })
  return { raw, events: validateStandaloneTraffic(events) }
}

function validatePhase(phase, index) {
  exactKeys(phase, ['id', 'status', 'startedAt', 'finishedAt', 'durationSeconds', 'commands', 'evidence'], `phases[${index}]`)
  const expected = phaseContract[index]
  if (!expected || phase.id !== expected.id) fail(`phase order must be authoring, runtime, day2`)
  if (phase.status !== 'pass') fail(`phases[${index}].status must be pass`)
  const startedAt = parseInstant(phase.startedAt, `phases[${index}].startedAt`)
  const finishedAt = parseInstant(phase.finishedAt, `phases[${index}].finishedAt`)
  if (!Number.isInteger(phase.durationSeconds) || phase.durationSeconds < 0 ||
      phase.durationSeconds > 600) {
    fail(`phases[${index}].durationSeconds must be within 0-600`)
  }
  const observedDuration = Math.round((finishedAt - startedAt) / 1000)
  if (observedDuration !== phase.durationSeconds) {
    fail(`phases[${index}].durationSeconds differs from its timestamps`)
  }
  if (!Array.isArray(phase.commands) ||
      phase.commands.length !== expected.commands.length ||
      phase.commands.some((command, commandIndex) => command !== expected.commands[commandIndex])) {
    fail(`phases[${index}].commands differ from the canonical ${phase.id} workflow`)
  }
  if (!Array.isArray(phase.evidence) || phase.evidence.length === 0) {
    fail(`phases[${index}].evidence must not be empty`)
  }
  phase.evidence.forEach((evidence, evidenceIndex) => {
    exactKeys(evidence, ['name', 'sha256'], `phases[${index}].evidence[${evidenceIndex}]`)
    if (typeof evidence.name !== 'string' || path.basename(evidence.name) !== evidence.name) {
      fail(`phases[${index}].evidence[${evidenceIndex}].name must be a local filename`)
    }
    requireSHA256(evidence.sha256, `phases[${index}].evidence[${evidenceIndex}].sha256`)
  })
  return phase
}

function parseReceipt(input) {
  if (typeof input !== 'string') return structuredClone(input)
  try {
    return JSON.parse(readFileSync(input, 'utf8'))
  } catch (error) {
    fail(`read receipt: ${error.message}`)
  }
}

export function validateStandaloneOSSE2E(input, trafficPath) {
  const receipt = parseReceipt(input)
  rejectSecretFields(receipt)
  exactKeys(receipt, ['schemaVersion', 'source', 'archive', 'network', 'phases'], 'receipt')
  if (receipt.schemaVersion !== receiptSchema) fail('unsupported receipt schemaVersion')
  exactKeys(receipt.source, ['repository', 'commit', 'digest'], 'source')
  if (receipt.source.repository !== 'kombifyio/stackKits' ||
      !commitPattern.test(receipt.source.commit) ||
      !sourceDigestPattern.test(receipt.source.digest)) {
    fail('source identity is not an exact public StackKits source digest')
  }
  exactKeys(
    receipt.archive,
    ['name', 'sha256', 'sbomSha256', 'attestationSha256', 'releaseIndexSha256'],
    'archive'
  )
  if (typeof receipt.archive.name !== 'string' ||
      path.basename(receipt.archive.name) !== receipt.archive.name ||
      !/^stackkits-basement-kit_.+_linux_(?:amd64|arm64)\.tar\.gz$/u.test(receipt.archive.name)) {
    fail('archive.name is not a Basement Linux release archive')
  }
  for (const field of ['sha256', 'sbomSha256', 'attestationSha256', 'releaseIndexSha256']) {
    requireSHA256(receipt.archive[field], `archive.${field}`)
  }
  exactKeys(receipt.network, ['recorder', 'captureMode', 'eventsSha256', 'eventCount'], 'network')
  if (receipt.network.recorder !== recorderSchema) fail('network.recorder is unsupported')
  if (receipt.network.captureMode !== captureMode) fail('network.captureMode is unsupported')
  requireSHA256(receipt.network.eventsSha256, 'network.eventsSha256')
  if (!Number.isInteger(receipt.network.eventCount) || receipt.network.eventCount < 1) {
    fail('network.eventCount must be positive')
  }
  const traffic = readTraffic(trafficPath)
  const trafficDigest = createHash('sha256').update(traffic.raw).digest('hex')
  if (trafficDigest !== receipt.network.eventsSha256) fail('traffic log digest does not match receipt')
  if (traffic.events.length !== receipt.network.eventCount) fail('traffic log event count does not match receipt')
  if (!Array.isArray(receipt.phases) || receipt.phases.length !== phaseContract.length) {
    fail('receipt must contain exactly three phases')
  }
  receipt.phases.forEach(validatePhase)
  return receipt
}

function main(argv) {
  if (argv.length !== 2) {
    console.error('usage: validate-standalone-oss-e2e.mjs <receipt.json> <network-events.jsonl>')
    process.exitCode = 2
    return
  }
  validateStandaloneOSSE2E(argv[0], argv[1])
  console.log('standalone OSS E2E receipt passed')
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  main(process.argv.slice(2))
}
