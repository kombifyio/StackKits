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

test('Basement archive smoke proves the complete standalone authoring phase', () => {
  const init = validator.indexOf('init "${init_args[@]}"')
  const validate = validator.indexOf('validate >"$tmp/${label}-validate.log"')
  const generate = validator.indexOf('generate >"$tmp/${label}-generate.log"')
  assert(init >= 0 && validate > init && generate > validate)
  assert.match(
    validator,
    /if \[ "\$kit" = "basement-kit" \]; then\s+init_args\+=\(--owner-source=local\)\s+fi/u
  )
  assert.doesNotMatch(
    validator,
    /local init_args=\("\$kit" --owner-source=local/u
  )
  assert.match(validator, /cmp "\$before_validate" "\$after_validate"/u)
  assert.match(validator, /generation-manifest\.json/u)
  assert.match(validator, /generation-receipt\.json/u)
  assert.match(validator, /stackkits-basement-core-runtime/u)
  assert.match(validator, /stat -c '%a'/u)
})

test('archive smoke requires every default Basement core service', () => {
  for (const service of ['socket-proxy', 'router', 'pocketid', 'tinyauth', 'step-ca', 'coolify', 'hub']) {
    assert.match(validator, new RegExp(`(?:^| )${service}(?: |; do)`, 'u'))
  }
})
