import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const privatePublishPath = path.join(root, '.github/workflows/publish-oss.yml')
const publicTemplatePath = path.join(root, 'scripts/public/workflows/release.yml')
const publicReleasePath = existsSync(publicTemplatePath)
  ? publicTemplatePath
  : path.join(root, '.github/workflows/release.yml')
const privatePublish = existsSync(privatePublishPath) ? readFileSync(privatePublishPath, 'utf8') : null
const publicRelease = readFileSync(publicReleasePath, 'utf8')

function before(text, left, right) {
  const leftIndex = text.indexOf(left)
  const rightIndex = text.indexOf(right)
  assert.notEqual(leftIndex, -1, `missing ${left}`)
  assert.notEqual(rightIndex, -1, `missing ${right}`)
  assert.ok(leftIndex < rightIndex, `${left} must precede ${right}`)
}

test('private publisher creates an immutable draft and cannot publish it directly', () => {
  if (privatePublish === null) return
  assert.match(
    privatePublish,
    /gh release create "\$TAG"[\s\S]*?--draft[\s\S]*?"\$\{prerelease_args\[@\]\}"/u
  )
  assert.doesNotMatch(privatePublish, /gh release edit "\$TAG"[\s\S]*?--draft=false/u)
  assert.match(privatePublish, /public repository tag workflow owns archive attestation/u)
})

test('public tag workflow binds exact draft bytes before publishing a prerelease', () => {
  for (const fragment of [
    'contents: write',
    'jq -r \'.isDraft\'',
    '.checks.attestationVerification.status',
    'actions/attest-build-provenance',
    'gh attestation trusted-root',
    'render-release-index.mjs',
    'stackkits-release-index-v1.json.intoto.jsonl',
    'run-standalone-oss-runtime-e2e.sh',
    'STACKKIT_E2E_PRELOAD_IMAGES',
    '--draft=false'
  ]) {
    assert.match(publicRelease, new RegExp(fragment.replaceAll(/[.*+?^${}()|[\]\\]/gu, '\\$&'), 'u'))
  }
  assert.doesNotMatch(publicRelease, /gh release create/u)
  before(publicRelease, 'Attest exact per-kit release archives', 'render-release-index.mjs')
  before(publicRelease, 'render-release-index.mjs', 'Attest the release index')
  before(publicRelease, 'run-standalone-oss-runtime-e2e.sh', '--draft=false')
  assert.match(publicRelease, /Stable publication requires the S4 upgrade, rollback, backup, and drift receipt/u)
})
