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
    'GH_CLI_LINUX_AMD64_SHA256',
    'Install pinned GitHub CLI',
    'gh attestation trusted-root',
    'verify-trusted-root.mjs',
    'release-trust-policy.json',
    'render-release-index.mjs',
    'stackkits-release-index-v1.json.intoto.jsonl',
    'Hand exact release trust set to the runtime proof',
    'Download exact release trust set from the producing job',
    'run-standalone-oss-runtime-e2e.sh',
    'STACKKIT_E2E_PRELOAD_IMAGES',
    '--draft=false'
  ]) {
    assert.match(publicRelease, new RegExp(fragment.replaceAll(/[.*+?^${}()|[\]\\]/gu, '\\$&'), 'u'))
  }
  assert.doesNotMatch(publicRelease, /gh release create/u)
  before(publicRelease, 'Attest exact per-kit release archives', 'render-release-index.mjs')
  before(publicRelease, 'Install pinned GitHub CLI', 'gh attestation trusted-root')
  before(publicRelease, 'render-release-index.mjs', 'Attest the release index')
  before(publicRelease, 'Publish and verify the release-index attestation', 'Hand exact release trust set to the runtime proof')
  before(publicRelease, 'Hand exact release trust set to the runtime proof', 'Download exact release trust set from the producing job')
  before(publicRelease, 'Download exact release trust set from the producing job', 'run-standalone-oss-runtime-e2e.sh')
  before(publicRelease, 'run-standalone-oss-runtime-e2e.sh', '--draft=false')
  assert.doesNotMatch(
    publicRelease,
    /gh release download "\$TAG" --dir dist --pattern "\$name" --clobber/u
  )
  assert.match(
    publicRelease,
    /identity="https:\/\/github\.com\/\$\{GITHUB_REPOSITORY\}\/\.github\/workflows\/release\.yml@refs\/tags\/\$\{TAG\}"[\s\S]*?--base-url "https:\/\/github\.com\/kombifyio\/stackKits\/releases\/download\/\$\{TAG\}"/u
  )
  const attachStart = publicRelease.indexOf('- name: Attach runtime evidence and publish the prerelease')
  const attachEnd = publicRelease.indexOf('--clobber', attachStart)
  assert.notEqual(attachStart, -1)
  assert.ok(attachEnd > attachStart)
  assert.deepEqual(
    [...publicRelease.slice(attachStart, attachEnd).matchAll(
      /artifacts\/standalone-runtime\/([a-z0-9.-]+)/gu
    )].map((match) => match[1]),
    [
      'runtime-evidence.json',
      'release-bootstrap.json',
      'network-events.jsonl',
      'compose-origin-scope.json',
      'apply.log',
      'verify.json'
    ]
  )
  assert.match(publicRelease, /Stable publication requires the S4 upgrade, rollback, backup, and drift receipt/u)
})

test('public runtime failure diagnostics are explicit, sanitized, and short-lived', () => {
  const start = publicRelease.indexOf('- name: Retain sanitized standalone failure diagnostics')
  const end = publicRelease.indexOf('- name: Attest standalone runtime evidence', start)
  assert.notEqual(start, -1)
  assert.ok(end > start)
  const block = publicRelease.slice(start, end)

  assert.match(block, /if: \$\{\{ failure\(\) \}\}/u)
  assert.match(block, /actions\/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a/u)
  for (const name of [
    'tcpdump.log',
    'network-events.jsonl',
    'compose-origin-scope.json',
    'tcpdump-init.stderr.log',
    'tcpdump.stderr.log',
    'fixture.log',
    'init.log',
    'validate.log',
    'generate.log',
    'release-bootstrap.json',
    'apply.log',
    'verify.json',
    'runtime-evidence.json'
  ]) {
    assert.match(block, new RegExp(`artifacts/standalone-runtime/${name.replaceAll('.', '\\.')}`, 'u'))
  }
  assert.match(block, /if-no-files-found: warn/u)
  assert.match(block, /retention-days: 1/u)
  assert.doesNotMatch(block, /\.stackkit|custody|work_root|project_dir|home_dir/u)
})
