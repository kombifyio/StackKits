#!/usr/bin/env node

import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { spawnSync } from 'node:child_process'
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

import { canonicalScope } from './compose-origin-scope.mjs'
import { validateStandaloneTraffic } from './validate-standalone-oss-e2e.mjs'
import { validateStandaloneRuntimeE2E } from './validate-standalone-runtime-e2e.mjs'

const digest = (value) => createHash('sha256').update(value).digest('hex')
const networkID = '1'.repeat(64)
const containerID = '2'.repeat(64)

function originScope() {
  return {
    schemaVersion: 'stackkit.compose-origin-scope/v2',
    project: 'stackkit-basement-core',
    networks: [{
      id: networkID,
      name: 'stackkit-basement-core_default',
      subnets: ['172.18.0.0/16', 'fd18::/64']
    }],
    localHostGateways: [{
      host: '172.17.0.1',
      sourceContainerId: containerID,
      sourceService: 'coolify'
    }],
    containers: [{
      id: containerID,
      service: 'coolify',
      networks: [{id: networkID, ips: ['172.18.0.11', 'fd18::11']}]
    }]
  }
}

function fixture() {
  const root = mkdtempSync(path.join(tmpdir(), 'stackkit-runtime-e2e-'))
  const apply = Buffer.from('apply success\n')
  const verify = Buffer.from('{"schemaVersion":"stackkit.command-result/v1","command":"stackkit verify","status":"success","data":{}}\n')
  const releaseBootstrap = Buffer.from(`${
    JSON.stringify({
      schemaVersion: 'stackkit.command-result/v1',
      command: 'stackkit kit verify',
      status: 'success',
      data: [{
        schemaVersion: 'stackkit.release-receipt/v1',
        kit: 'basement-kit',
        version: 'v0.8.0-beta.1',
        channel: 'beta',
        platform: {os: 'linux', arch: 'amd64'},
        archiveSha256: 'c'.repeat(64),
        sbomSha256: 'd'.repeat(64),
        attestationSha256: 'e'.repeat(64),
        attestationIssuer: 'https://token.actions.githubusercontent.com',
        attestationSubject: 'stackkits-basement-kit_0.8.0-beta.1_linux_amd64.tar.gz',
        trustedRootSha256: '1'.repeat(64),
        indexSha256: 'f'.repeat(64),
        indexAttestationSha256: '2'.repeat(64),
        verifiedAt: '2026-07-26T11:59:00Z',
        installDir: '/tmp/project/.stackkit/releases/basement-kit/v0.8.0-beta.1/linux-amd64'
      }]
    })
  }\n`)
  const traffic = Buffer.from('{"schemaVersion":"stackkit.network-event/v1","observedAt":"2026-07-26T12:00:00.000Z","kind":"tcp","host":"172.18.0.5","port":2375,"scope":"local"}\n')
  const scope = Buffer.from(canonicalScope(originScope()))
  writeFileSync(path.join(root, 'apply.log'), apply)
  writeFileSync(path.join(root, 'verify.json'), verify)
  writeFileSync(path.join(root, 'release-bootstrap.json'), releaseBootstrap)
  writeFileSync(path.join(root, 'network-events.jsonl'), traffic)
  writeFileSync(path.join(root, 'compose-origin-scope.json'), scope)
  const evidence = {
    schemaVersion: 'stackkit.oss-runtime-e2e-evidence/v3',
    source: {
      repository: 'kombifyio/stackKits',
      commit: 'a'.repeat(40),
      digest: `sha256:${'b'.repeat(64)}`
    },
    archive: {
      name: 'stackkits-basement-kit_0.8.0-beta.1_linux_amd64.tar.gz',
      sha256: 'c'.repeat(64),
      sbomSha256: 'd'.repeat(64),
      attestationSha256: 'e'.repeat(64),
      releaseIndexSha256: 'f'.repeat(64),
      releaseIndexAttestationSha256: '2'.repeat(64),
      trustedRootSha256: '1'.repeat(64),
      releaseBootstrapSha256: digest(releaseBootstrap)
    },
    network: {
      recorder: 'stackkit.hermetic-network-log/v2',
      captureMode: 'host-forbidden-dns+compose-origin-initial-syn/v1',
      eventsSha256: digest(traffic),
      originScopeSha256: digest(scope),
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
  return {
    root,
    evidence,
    evidencePath,
    trafficPath: path.join(root, 'network-events.jsonl'),
    scopePath: path.join(root, 'compose-origin-scope.json')
  }
}

function parseTraffic(lines, mutateScope) {
  const root = mkdtempSync(path.join(tmpdir(), 'stackkit-runtime-traffic-'))
  const input = path.join(root, 'tcpdump.log')
  const output = path.join(root, 'network-events.jsonl')
  const scopePath = path.join(root, 'compose-origin-scope.json')
  const scope = originScope()
  mutateScope?.(scope)
  writeFileSync(input, `${lines.join('\n')}\n`)
  writeFileSync(scopePath, canonicalScope(scope))
  const result = spawnSync(
    process.execPath,
    [fileURLToPath(new URL('./parse-standalone-traffic.mjs', import.meta.url)), input, scopePath, output],
    {encoding: 'utf8'}
  )
  return {
    result,
    events: result.status === 0
      ? readFileSync(output, 'utf8').trimEnd().split('\n').map((line) => JSON.parse(line))
      : []
  }
}

test('accepts exact v3 runtime evidence and detects traffic or scope tamper', () => {
  const item = fixture()
  assert.equal(
    validateStandaloneRuntimeE2E(item.evidencePath, item.trafficPath, item.scopePath).phase.status,
    'pass'
  )
  writeFileSync(item.scopePath, `${readFileSync(item.scopePath, 'utf8')} `)
  assert.throws(
    () => validateStandaloneRuntimeE2E(item.evidencePath, item.trafficPath, item.scopePath),
    /canonical|digest/u
  )
})

test('attested runtime evidence rejects release-bootstrap tamper', () => {
  const item = fixture()
  writeFileSync(path.join(item.root, 'release-bootstrap.json'), '{}\n')
  assert.throws(
    () => validateStandaloneRuntimeE2E(item.evidencePath, item.trafficPath, item.scopePath),
    /release bootstrap evidence differs/u
  )
})

test('v3 binds every release receipt identity and trust digest', () => {
  const mutations = [
    ['version', 'v0.8.0-edge.1'],
    ['channel', 'edge'],
    ['platform', {os: 'linux', arch: 'arm64'}],
    ['archiveSha256', '9'.repeat(64)],
    ['sbomSha256', '9'.repeat(64)],
    ['attestationSha256', '9'.repeat(64)],
    ['attestationIssuer', 'https://issuer.invalid'],
    ['attestationSubject', 'other.tar.gz'],
    ['trustedRootSha256', '9'.repeat(64)],
    ['indexSha256', '9'.repeat(64)],
    ['indexAttestationSha256', '9'.repeat(64)]
  ]
  for (const [field, value] of mutations) {
    const item = fixture()
    const bootstrapPath = path.join(item.root, 'release-bootstrap.json')
    const bootstrap = JSON.parse(readFileSync(bootstrapPath))
    bootstrap.data[0][field] = value
    const raw = Buffer.from(`${JSON.stringify(bootstrap)}\n`)
    writeFileSync(bootstrapPath, raw)
    const evidence = JSON.parse(readFileSync(item.evidencePath))
    evidence.archive.releaseBootstrapSha256 = digest(raw)
    writeFileSync(item.evidencePath, `${JSON.stringify(evidence)}\n`)
    assert.throws(
      () => validateStandaloneRuntimeE2E(item.evidencePath, item.trafficPath, item.scopePath),
      /init did not bind the exact release receipt/u,
      field
    )
  }
})

test('v3 rejects unknown receipt fields instead of widening the evidence contract', () => {
  const item = fixture()
  const bootstrapPath = path.join(item.root, 'release-bootstrap.json')
  const bootstrap = JSON.parse(readFileSync(bootstrapPath))
  bootstrap.data[0].unexpected = true
  const raw = Buffer.from(`${JSON.stringify(bootstrap)}\n`)
  writeFileSync(bootstrapPath, raw)
  const evidence = JSON.parse(readFileSync(item.evidencePath))
  evidence.archive.releaseBootstrapSha256 = digest(raw)
  writeFileSync(item.evidencePath, `${JSON.stringify(evidence)}\n`)
  assert.throws(
    () => validateStandaloneRuntimeE2E(item.evidencePath, item.trafficPath, item.scopePath),
    /release receipt fields must be exactly/u
  )
})

test('parser emits only exact Compose-origin P SYNs and ignores runner TCP and DNS noise', () => {
  const parsed = parseTraffic([
    '2026-07-26 12:00:00.000000 eth0 Out IP 10.1.0.158.49000 > 8.8.4.4.443: Flags [S], seq 1',
    '2026-07-26 12:00:00.010000 eth0 In IP 20.85.130.105.443 > 10.1.0.158.49001: Flags [S], seq 2',
    '2026-07-26 12:00:00.020000 eth0 Out IP 10.1.0.158.40000 > 168.63.129.16.53: 10+ A? packages.microsoft.com. (40)',
    '2026-07-26 12:00:00.030000 vethstack P IP 172.18.0.11.59288 > 172.18.0.5.2375: Flags [S], seq 3',
    '2026-07-26 12:00:00.031000 vethpeer Out IP 172.18.0.11.59288 > 172.18.0.5.2375: Flags [S], seq 3',
    '2026-07-26 12:00:00.040000 vethstack P IP6 fd18::11.59289 > fd18::5.2376: Flags [S], seq 4'
  ])
  assert.equal(parsed.result.status, 0, parsed.result.stderr)
  assert.deepEqual(parsed.events.map((event) => [event.host, event.port, event.scope]), [
    ['172.18.0.5', 2375, 'local'],
    ['fd18::5', 2376, 'local']
  ])
})

test('parser fails closed for unknown sources in the Compose subnet and invalid scope', () => {
  const unknown = parseTraffic([
    '2026-07-26 12:00:00.000000 vethstack P IP 172.18.0.12.49000 > 172.18.0.5.2375: Flags [S]'
  ])
  assert.notEqual(unknown.result.status, 0)
  assert.match(unknown.result.stderr, /unknown source 172\.18\.0\.12/u)

  const invalid = parseTraffic([
    '2026-07-26 12:00:00.000000 vethstack P IP 172.18.0.11.49000 > 172.18.0.5.2375: Flags [S]'
  ], (scope) => {
    scope.containers[0].networks[0].ips = ['172.19.0.11']
  })
  assert.notEqual(invalid.result.status, 0)
  assert.match(invalid.result.stderr, /outside its declared network subnets/u)
})

test('external Compose TCP remains visible and fails closed without DNS binding', () => {
  const parsed = parseTraffic([
    '2026-07-26 12:00:00.000000 vethstack P IP 172.18.0.11.49000 > 185.199.110.133.443: Flags [S]'
  ])
  assert.equal(parsed.result.status, 0, parsed.result.stderr)
  assert.equal(parsed.events[0].scope, 'external')
  assert.throws(() => validateStandaloneTraffic(parsed.events), /non-allowlisted host/u)
})

test('parser admits only the exact Coolify source and resolved host gateway pair as local', () => {
  const accepted = parseTraffic([
    '2026-07-26 12:00:00.000000 vethstack P IP 172.18.0.11.49000 > 172.17.0.1.8080: Flags [S]'
  ], (scope) => {
    scope.localHostGateways = [{
      host: '172.17.0.1',
      sourceContainerId: containerID,
      sourceService: 'coolify'
    }]
  })
  assert.equal(accepted.result.status, 0, accepted.result.stderr)
  assert.equal(accepted.events[0].scope, 'local')
  assert.doesNotThrow(() => validateStandaloneTraffic(accepted.events))

  const wrongSource = parseTraffic([
    '2026-07-26 12:00:00.000000 vethstack P IP 172.18.0.12.49000 > 172.17.0.1.8080: Flags [S]'
  ], (scope) => {
    scope.containers.push({
      id: '3'.repeat(64),
      service: 'pocket-id',
      networks: [{id: networkID, ips: ['172.18.0.12']}]
    })
    scope.localHostGateways = [{
      host: '172.17.0.1',
      sourceContainerId: containerID,
      sourceService: 'coolify'
    }]
  })
  assert.equal(wrongSource.result.status, 0, wrongSource.result.stderr)
  assert.equal(wrongSource.events[0].scope, 'external')
  assert.throws(() => validateStandaloneTraffic(wrongSource.events), /non-allowlisted host/u)

  for (const host of ['172.17.0.2', '192.168.50.10', '185.199.110.133']) {
    const wrongHost = parseTraffic([
      `2026-07-26 12:00:00.000000 vethstack P IP 172.18.0.11.49000 > ${host}.8080: Flags [S]`
    ], (scope) => {
      scope.localHostGateways = [{
        host: '172.17.0.1',
        sourceContainerId: containerID,
        sourceService: 'coolify'
      }]
    })
    assert.equal(wrongHost.result.status, 0, wrongHost.result.stderr)
    assert.equal(wrongHost.events[0].scope, 'external')
    assert.throws(() => validateStandaloneTraffic(wrongHost.events), /non-allowlisted host/u)
  }
})

test('parser rejects non-exact or tampered host gateway ownership before reading traffic', () => {
  const packet = [
    '2026-07-26 12:00:00.000000 vethstack P IP 172.18.0.11.49000 > 172.17.0.1.8080: Flags [S]'
  ]
  for (const localHostGateways of [
    [],
    [
      {host: '172.17.0.1', sourceContainerId: containerID, sourceService: 'coolify'},
      {host: '192.168.50.10', sourceContainerId: containerID, sourceService: 'coolify'}
    ]
  ]) {
    const nonExact = parseTraffic(packet, (scope) => {
      scope.localHostGateways = localHostGateways
    })
    assert.notEqual(nonExact.result.status, 0)
    assert.match(nonExact.result.stderr, /must contain exactly one entry/u)
  }

  const tampered = parseTraffic([
    ...packet
  ], (scope) => {
    scope.localHostGateways = [{
      host: '172.17.0.1',
      sourceContainerId: containerID,
      sourceService: 'pocket-id'
    }]
  })
  assert.notEqual(tampered.result.status, 0)
  assert.match(tampered.result.stderr, /source service does not match its container/u)
})

test('host DNS is a negative gate and malformed or TCP DNS fails closed', () => {
  for (const [line, pattern] of [
    [
      '2026-07-26 12:00:00.000000 eth0 Out IP 10.1.0.158.40000 > 168.63.129.16.53: 10+ A? api.kombify.io. (40)',
      /Kombify-controlled/u
    ],
    [
      '2026-07-26 12:00:00.000000 eth0 Out IP 10.1.0.158.40000 > 168.63.129.16.53: 10+ [1au] (28)',
      /unparsed captured DNS question/u
    ],
    [
      '2026-07-26 12:00:00.000000 eth0 Out IP 10.1.0.158.40000 > 168.63.129.16.53: Flags [S]',
      /DNS-over-TCP/u
    ]
  ]) {
    const parsed = parseTraffic([line])
    assert.notEqual(parsed.result.status, 0)
    assert.match(parsed.result.stderr, pattern)
  }
})

test('harness binds evidence to exact Compose project scope and the new capture contract', () => {
  const harness = readFileSync(new URL('./run-standalone-oss-runtime-e2e.sh', import.meta.url), 'utf8')
  for (const fragment of [
    'docker compose --project-name stackkit-basement-core',
    'host.docker.internal=host-gateway',
    'localHostGateways',
    'docker ps --all --quiet --no-trunc --filter label=com.docker.compose.project=stackkit-basement-core',
    'host, none, and container network modes are forbidden',
    'config --services',
    'compose-origin-scope.json',
    'stackkit.compose-origin-scope/v2',
    'process.exit(isIP(process.argv[1]) !== 0 && process.argv[1] === process.argv[1].toLowerCase() ? 0 : 1)',
    'originScopeSha256',
    'host-forbidden-dns+compose-origin-initial-syn/v1',
    'export STACKKIT_RELEASE_FIXTURE_URL="$fixture_url"',
    'lifecycle_stackkit="$candidate_stackkit"',
    'timeout 120 "$lifecycle_stackkit" backup configure --json',
    'timeout 120 "$lifecycle_stackkit" verify --json',
    'stackkit kit verify --json >"$output_dir/release-bootstrap.json"',
    'stackkit apply >"$output_dir/apply.log" 2>&1',
    'parse-standalone-traffic.mjs" "$raw_traffic" "$network_scope" "$traffic_events"'
  ]) {
    assert.match(harness, new RegExp(fragment.replaceAll(/[.*+?^${}()|[\]\\]/gu, '\\$&'), 'u'))
  }
  assert.ok(harness.indexOf('github-release-fixture.mjs') < harness.indexOf('stackkit init'))
  assert.ok(harness.indexOf('STACKKIT_RELEASE_FIXTURE_URL') < harness.indexOf('stackkit init'))
  const captureStarts = [...harness.matchAll(/tcpdump -tttt/gu)].map((match) => match.index)
  assert.equal(captureStarts.length, 2)
  const initIndex = harness.indexOf('stackkit init')
  const preloadIndex = harness.indexOf('docker pull "$image"')
  const applyIndex = harness.indexOf('stackkit apply')
  const authoringCaptureStop = harness.indexOf('kill -INT "$capture_pid"', captureStarts[0])
  assert.ok(captureStarts[0] < initIndex)
  assert.ok(initIndex < authoringCaptureStop)
  assert.ok(authoringCaptureStop < preloadIndex)
  assert.ok(preloadIndex < captureStarts[1])
  assert.ok(captureStarts[1] < applyIndex)
  assert.ok(harness.indexOf('stackkit init') < harness.indexOf('stackkit kit verify --json'))
  assert.doesNotMatch(harness, /stackkit upgrade --to "\$version"/u)
  assert.doesNotMatch(harness, /timeout \d+ stackkit backup/u)
  assert.doesNotMatch(harness, /release-install\.json/u)
  assert.ok(harness.indexOf('stackkit apply') < harness.indexOf('docker inspect "$container_id"'))
  assert.ok(harness.indexOf('docker inspect "$container_id"') < harness.indexOf('stackkit verify --json'))
})
