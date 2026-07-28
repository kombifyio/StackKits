import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const privatePublishPath = path.join(root, '.github/workflows/publish-oss.yml')
const publicTemplatePath = path.join(root, 'scripts/public/workflows/release.yml')
const publicImagePath = path.join(root, 'scripts/public/workflows/publish-image.yml')
const publicReleasePath = existsSync(publicTemplatePath)
  ? publicTemplatePath
  : path.join(root, '.github/workflows/release.yml')
const privatePublish = existsSync(privatePublishPath) ? readFileSync(privatePublishPath, 'utf8') : null
const publicRelease = readFileSync(publicReleasePath, 'utf8')
const publicImage = readFileSync(publicImagePath, 'utf8')
const runtimeHarness = readFileSync(
  path.join(root, 'scripts/e2e/run-standalone-oss-runtime-e2e.sh'),
  'utf8',
)
const releaseEvidenceSchema = JSON.parse(readFileSync(
  path.join(root, 'schemas/release-evidence.schema.json'),
  'utf8',
))

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
  assert.match(privatePublish, /--title "StackKits \$TAG"/u)
  assert.doesNotMatch(privatePublish, /gh release edit "\$TAG"[\s\S]*?--draft=false/u)
  assert.match(privatePublish, /public repository tag workflow owns archive attestation/u)
})

test('publisher dispatches public trust only after final immutable evidence', () => {
  if (privatePublish === null) return
  before(privatePublish, 'Verify final release evidence attestation', 'Signal public trust after immutable evidence is ready')
  before(privatePublish, 'Apply changelog release notes with release app', 'Signal public trust after immutable evidence is ready')
  assert.doesNotMatch(privatePublish, /permission-actions: write/u)
  assert.match(
    privatePublish,
    /event_type:"stackkits-release-ready"[\s\S]*?client_payload:\{tag:\$tag,source_commit:\$sourceCommit,stable_previous_tag:\$stablePreviousTag\}[\s\S]*?repos\/\$\{OSS_REPO\}\/dispatches/u
  )
  assert.match(privatePublish, /stable_previous_tag: \$\{\{ steps\.plan\.outputs\.stable_previous_tag \}\}/u)
})

test('public manual workflow binds exact ready draft bytes before publishing a release', () => {
  assert.ok(releaseEvidenceSchema.properties.release.required.includes('commit'))
  assert.equal(releaseEvidenceSchema.properties.source, undefined)
  for (const fragment of [
    'repository_dispatch:',
    'stackkits-release-ready',
    'workflow_dispatch:',
    'source_commit:',
    'contents: write',
    'jq -r \'.isDraft\'',
    '.checks.attestationVerification.status',
    '.release.commit == $expectedSourceCommit',
    'EXPECTED_SOURCE_COMMIT: ${{ inputs.source_commit }}',
    'Release dispatch mismatch',
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
  assert.doesNotMatch(publicRelease, /^\s*push:\s*$/mu)
  assert.doesNotMatch(publicRelease, /seq 1 60|sleep 5|within 300 seconds/u)
  before(publicRelease, 'Dispatch exact publisher-ready tag', 'Attest exact release and prove standalone runtime')
  assert.match(
    publicRelease,
    /permissions:\n\s+actions: write\n\s+contents: read[\s\S]*?gh workflow run release\.yml[\s\S]*?--ref "\$TAG"/u
  )
  assert.match(
    publicRelease,
    /release-trust:[\s\S]*?github\.event_name == 'workflow_dispatch'/u
  )
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
  // Both the certificate identity and the asset base URL must come from
  // GITHUB_REPOSITORY. Hardcoding the repository here let the two drift: the
  // identity used the canonical casing while the base URL spelled it
  // "stackKits", so every published index recorded URLs that could never equal
  // the browser_download_url GitHub returns, and release resolution failed with
  // "release index trusted root does not match the attested GitHub release
  // asset" on every 0.8 beta.
  assert.match(
    publicRelease,
    /identity="https:\/\/github\.com\/\$\{GITHUB_REPOSITORY\}\/\.github\/workflows\/release\.yml@refs\/tags\/\$\{TAG\}"[\s\S]*?--base-url "https:\/\/github\.com\/\$\{GITHUB_REPOSITORY\}\/releases\/download\/\$\{TAG\}"/u
  )
  assert.doesNotMatch(
    publicRelease,
    /--base-url "https:\/\/github\.com\/kombifyio\//u,
    'the release base URL must be derived from GITHUB_REPOSITORY, not hardcoded'
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
  for (const stableFragment of [
    'Download and verify exact previous stable release for stable Day-2',
    'stable_previous_tag:',
    '-f "stable_previous_tag=$STABLE_PREVIOUS_TAG"',
    'Expected the exact frozen published stable SemVer',
    'STACKKIT_E2E_STABLE_DAY2_PROOF: "1"',
    'STACKKIT_E2E_PREVIOUS_TAG',
    'STACKKIT_E2E_PREVIOUS_ARCHIVE',
    'stackkit.stable-day2-live-e2e-evidence/v1',
    'Attest exact stable Day-2 evidence',
    'Retain exact stable Day-2 evidence',
    'stable-restore-e2e:',
    'Run bounded exact-archive stable restore proof',
    'Attest exact stable restore evidence',
    'modern-runtime-e2e:',
    'Modern exact-archive runtime and partition proof',
    'modern-ha-e2e:',
    'Modern measured warm-standby proof',
    'modern-terminal-receipt:',
    'Compose exact Modern terminal receipt',
    'modern-terminal-live-receipt.json',
    'finalize-stable-release:',
    'Publish stable from exact Basement and Modern receipts',
    '--prerelease=false',
    '--latest'
  ]) {
    assert.match(publicRelease, new RegExp(stableFragment.replaceAll(/[.*+?^${}()|[\]\\]/gu, '\\$&'), 'u'))
  }
  assert.doesNotMatch(publicRelease, /Stable release gate incomplete/u)
  before(publicRelease, 'Download and verify exact previous stable release for stable Day-2', 'Run bounded stable Day-2 proof from exact stable baseline to candidate')
  before(publicRelease, 'Run bounded stable Day-2 proof from exact stable baseline to candidate', 'Attest exact stable Day-2 evidence')
  before(publicRelease, 'Run bounded exact-archive stable restore proof', 'Attest exact stable restore evidence')
  before(publicRelease, 'Attest exact stable Day-2 evidence', 'Publish stable from exact Basement and Modern receipts')
  before(publicRelease, 'Attest exact stable restore evidence', 'Publish stable from exact Basement and Modern receipts')
  before(publicRelease, 'Compose exact Modern terminal receipt', 'Publish stable from exact Basement and Modern receipts')
  const stableDay2Step = publicRelease.slice(
    publicRelease.indexOf('- name: Run bounded stable Day-2 proof from exact stable baseline to candidate'),
    publicRelease.indexOf('- name: Retain sanitized standalone failure diagnostics')
  )
  assert.doesNotMatch(stableDay2Step, /STACKKIT_E2E_RESTORE_PROOF/u)
  assert.match(
    publicRelease,
    /finalize-stable-release:[\s\S]*?needs: \[runtime-e2e, stable-restore-e2e, modern-terminal-receipt\][\s\S]*?\.source == \{commit: \$sourceCommit, digest: \$sourceDigest\}[\s\S]*?\.archive\.sha256 == \$candidateArchiveSha256[\s\S]*?validate-modern-terminal-receipt\.mjs[\s\S]*?gh release edit "\$TAG"/u
  )
})

test('restore observation covers the governed stop and copy path with sanitized diagnostics', () => {
  assert.match(runtimeHarness, /setsid timeout 600 "\$lifecycle_stackkit" backup restore activate/u)
  assert.match(runtimeHarness, /for _ in \$\(seq 1 5400\); do/u)
  assert.match(runtimeHarness, /stackkit\.restore-activation-observation\/v1/u)
  assert.match(runtimeHarness, /activation-observation-failure\.json/u)

  const start = runtimeHarness.indexOf('[ "$activation_ready" = "1" ] || {')
  const end = runtimeHarness.indexOf('  docker pause "$restore_helper_name"', start)
  assert.notEqual(start, -1)
  assert.ok(end > start)
  const diagnosticBlock = runtimeHarness.slice(start, end)
  assert.doesNotMatch(diagnosticBlock, /\.ownerRef|\.signature|workspaceHash/u)
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

test('public release workflow can replay an exact live restore proof without changing publisher flow', () => {
  for (const fragment of [
    'mode:',
    'restore-proof',
    "inputs.mode != 'restore-proof'",
    "inputs.mode == 'restore-proof'",
    'Checkout the current public proof harness',
    'EXPECTED_SOURCE_COMMIT: ${{ inputs.source_commit }}',
    'stackkits-release-assets.intoto.jsonl',
    'stackkits-release-index-v1.json.intoto.jsonl',
    'gh attestation verify "$archive"',
    'STACKKIT_E2E_RESTORE_PROOF: "1"',
    'stackkit.restore-live-e2e-evidence/v1',
    'artifacts/standalone-runtime/restore-live-evidence.json',
    'retention-days: 90'
  ]) {
    assert.match(publicRelease, new RegExp(fragment.replaceAll(/[.*+?^${}()|[\]\\]/gu, '\\$&'), 'u'))
  }
  before(publicRelease, 'Download and verify the exact live release trust set', 'Run bounded exact-archive restore proof')
  const restoreJob = publicRelease.slice(publicRelease.indexOf('\n  restore-proof:'))
  before(restoreJob, 'Run bounded exact-archive restore proof', 'Attest exact restore evidence')
  assert.match(
    publicRelease,
    /release-trust:[\s\S]*?inputs\.mode != 'restore-proof'[\s\S]*?restore-proof:[\s\S]*?inputs\.mode == 'restore-proof'/u
  )
})

test('image publication is asynchronous and outside the release gate', () => {
  assert.doesNotMatch(publicRelease, /^  publish-image:/mu)
  assert.match(publicImage, /^\s*release:\s*$/mu)
  assert.match(publicImage, /^\s*types:\s*\[published\]\s*$/mu)
  assert.match(publicImage, /^\s*workflow_dispatch:\s*$/mu)
  assert.doesNotMatch(publicImage, /^\s*needs:\s*(?:release-trust|runtime-e2e)\s*$/mu)
})
