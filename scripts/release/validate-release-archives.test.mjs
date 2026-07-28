import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import test from 'node:test'

const root = path.resolve(import.meta.dirname, '../..')
const validator = readFileSync(
  path.join(root, 'scripts/release/validate-release-archives.sh'),
  'utf8'
)
const goreleaser = readFileSync(path.join(root, '.goreleaser.yaml'), 'utf8')

test('snapshot archives embed a compatibility-admissible semantic version', () => {
  assert.match(
    goreleaser,
    /snapshot:[\s\S]*?version_template: "\{\{ incpatch \.Version \}\}-devel"/u
  )
})

test('pre-trust archive validation is structural only', () => {
  assert.match(validator, /require_archive_matrix/u)
  assert.match(validator, /check_archive_contents "\$basement_archive"/u)
  assert.match(validator, /check_archive_contents "\$cloud_archive"/u)
  assert.match(validator, /check_archive_contents "\$modern_archive"/u)
  assert.doesNotMatch(validator, /smoke_v2_authoring|stackkit"\s+init|stackkit"\s+apply/u)
  assert.match(validator, /smoke_public_archive_cli "\$full_extract"/u)
  assert.match(validator, /validate-architecture-contract-fixture\.mjs/u)
  assert.match(validator, /backup emergency-export/u)
  assert.match(validator, /pre-trust release archive structural validation passed/u)
})

test('post-trust runtime proof owns the exact standalone lifecycle', () => {
  const workflow = readFileSync(
    path.join(root, 'scripts/public/workflows/release.yml'),
    'utf8'
  )
  const runner = readFileSync(
    path.join(root, 'scripts/e2e/run-standalone-oss-runtime-e2e.sh'),
    'utf8'
  )
  assert.match(workflow, /runtime-e2e:\n[\s\S]*?needs: release-trust/u)
  const trust = workflow.indexOf('Materialize offline bundles, trusted roots, and release index')
  const runtime = workflow.indexOf('Run bounded standalone Basement archive Apply and Verify')
  assert(trust >= 0 && runtime > trust)
  assert.match(workflow.slice(runtime), /run-standalone-oss-runtime-e2e\.sh/u)
  const init = runner.indexOf('stackkit init --owner-source=local')
  const receipt = runner.indexOf('stackkit kit verify --json')
  const validate = runner.indexOf('stackkit validate')
  const generate = runner.indexOf('stackkit generate')
  const apply = runner.indexOf('stackkit apply')
  const verify = runner.indexOf('stackkit verify --json')
  assert(init >= 0 && receipt > init && validate > receipt && generate > validate)
  assert(apply > generate && verify > apply)
})

test('Modern live proof separates the tagged internal helper from the release binary', () => {
  const runner = readFileSync(
    path.join(root, 'scripts/e2e/run-modern-live-phase.sh'),
    'utf8'
  )
  assert.match(runner, /test "\$\(git -C "\$source_root" rev-parse --verify HEAD\)" = "\$commit"/u)
  assert.match(runner, /go build -trimpath -tags stackkit_e2e[\s\S]*?-X main\.Version=\$\{tag#v\} -X main\.GitCommit=\$commit/u)
  assert.match(runner, /"\$internal_stackkit" internal proof federation-binding issue/u)
  assert.match(runner, /"\$internal_stackkit" federation binding import/u)
  assert.match(runner, /admission="\$project\/\.stackkit\/evidence\/federation-binding\/proof\.json"/u)
  assert.match(runner, /inventory="\$project\/deploy\/\.stackkit\/inventory\.json"/u)
  for (const command of ['init modern-homelab', 'kit verify', 'validate', 'generate', 'apply', 'verify']) {
    assert.ok(runner.includes(`"$release_stackkit" ${command}`))
  }
  assert.doesNotMatch(runner, /"\$release_stackkit" (?:internal proof|federation binding import)/u)
})
