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
