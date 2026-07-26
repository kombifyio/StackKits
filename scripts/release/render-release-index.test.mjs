import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import test from 'node:test'

import { main, renderReleaseIndex } from './render-release-index.mjs'

function fixture() {
  const root = mkdtempSync(path.join(tmpdir(), 'stackkits-release-index-'))
  const dist = path.join(root, 'dist')
  mkdirSync(dist)
  for (const archive of [
    'stackkits-basement-kit_0.8.0-beta.1_linux_amd64.tar.gz',
    'stackkits-cloud-kit_0.8.0-beta.1_windows_amd64.zip'
  ]) {
    writeFileSync(path.join(dist, archive), `archive:${archive}`)
    writeFileSync(path.join(dist, `${archive}.spdx.json`), '{"spdxVersion":"SPDX-2.3"}\n')
  }
  const trustedRoot = path.join(dist, 'sigstore-trusted-root.jsonl')
  const attestation = path.join(dist, 'stackkits-release-assets.intoto.jsonl')
  writeFileSync(trustedRoot, '{"mediaType":"application/vnd.dev.sigstore.trustedroot+json;version=0.1"}\n')
  writeFileSync(attestation, '{"dsseEnvelope":{}}\n')
  return { root, dist, trustedRoot, attestation }
}

function render(overrides = {}) {
  const files = fixture()
  const tag = overrides.tag ?? 'v0.8.0-beta.1'
  return renderReleaseIndex({
    ...files,
    tag,
    publishedAt: '2026-07-26T12:00:00Z',
    baseURL: `https://github.com/kombifyio/stackKits/releases/download/${tag}`,
    certificateIdentity:
      `https://github.com/kombifyio/StackKits/.github/workflows/release.yml@refs/tags/${tag}`,
    ...overrides
  })
}

test('renders deterministic beta assets with exact archive, SBOM, and attestation digests', () => {
  const index = render()
  assert.equal(index.schemaVersion, 'stackkits-release-index/v1')
  assert.equal(index.release.repository, 'kombifyio/stackKits')
  assert.equal(index.release.version, 'v0.8.0-beta.1')
  assert.equal(index.assets.length, 2)
  assert.deepEqual(
    index.assets.map(({ kit, channel, platform }) => ({ kit, channel, platform })),
    [
      { kit: 'basement-kit', channel: 'beta', platform: { os: 'linux', arch: 'amd64' } },
      { kit: 'cloud-kit', channel: 'beta', platform: { os: 'windows', arch: 'amd64' } }
    ]
  )
  for (const asset of index.assets) {
    assert.match(asset.archive.sha256, /^[0-9a-f]{64}$/u)
    assert.match(asset.sbom.sha256, /^[0-9a-f]{64}$/u)
    assert.match(asset.attestation.sha256, /^[0-9a-f]{64}$/u)
    assert.equal(asset.attestation.subject, asset.archive.name)
    assert.equal(
      asset.attestation.certificateIdentity,
      'https://github.com/kombifyio/StackKits/.github/workflows/release.yml@refs/tags/v0.8.0-beta.1'
    )
  }
})

test('rejects foreign workflow identity, unsupported prerelease channels, and mixed versions', () => {
  assert.throws(() => render({ certificateIdentity: 'https://github.com/attacker/workflow.yml' }), /identity/u)
  assert.throws(() => render({ tag: 'v0.8.0-rc.1' }), /stable, -beta\.N, or -edge\.N/u)
  assert.throws(() => render({ tag: 'v0.8.0-beta.2' }), /version does not match/u)
})

test('CLI writes one newline-terminated canonical index', () => {
  const files = fixture()
  const output = path.join(files.root, 'stackkits-release-index-v1.json')
  main([
    '--dist',
    files.dist,
    '--tag',
    'v0.8.0-beta.1',
    '--published-at',
    '2026-07-26T12:00:00Z',
    '--base-url',
    'https://github.com/kombifyio/stackKits/releases/download/v0.8.0-beta.1',
    '--trusted-root',
    files.trustedRoot,
    '--attestation',
    files.attestation,
    '--certificate-identity',
    'https://github.com/kombifyio/StackKits/.github/workflows/release.yml@refs/tags/v0.8.0-beta.1',
    '--output',
    output
  ])
  const raw = readFileSync(output, 'utf8')
  assert.equal(raw.endsWith('\n'), true)
  assert.equal(JSON.parse(raw).assets.length, 2)
})
