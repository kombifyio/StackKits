import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { execFile } from 'node:child_process'
import { cp, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { promisify } from 'node:util'
import test from 'node:test'
import { projectAuthorityBundle, projectAuthorityBundleV2 } from '../scripts/generate-catalog.mjs'

const authorityRoot = new URL('../../internal/architecturev2/authority_bundle/', import.meta.url)
const sourceSha = '1111111111111111111111111111111111111111'

test('catalog CLI defaults to the native contract, with v1 only by explicit selection', async (context) => {
  const root = await mkdtemp(join(tmpdir(), 'stackkits-webmcp-cli-'))
  context.after(() => rm(root, { recursive: true, force: true }))
  const output = join(root, 'catalog.json')
  await promisify(execFile)(process.execPath, [
    fileURLToPath(new URL('../scripts/generate-catalog.mjs', import.meta.url)),
    '--authority-bundle', fileURLToPath(authorityRoot), '--source-sha', sourceSha, '--out', output,
  ])
  assert.equal(JSON.parse(await readFile(output, 'utf8')).schema_version, 'stackkits-webmcp/v2alpha1')
})

test('authority projection is deterministic and source-bound', async () => {
  const first = await projectAuthorityBundle(fileURLToPath(authorityRoot), sourceSha)
  const second = await projectAuthorityBundle(fileURLToPath(authorityRoot), sourceSha)
  assert.deepEqual(first, second)
  assert.equal(first.source_sha, sourceSha)
  assert.match(first.catalog_sha256, /^[a-f0-9]{64}$/)
  assert.deepEqual(first.kits.map(({ stackkit_id }) => stackkit_id), [...first.kits.map(({ stackkit_id }) => stackkit_id)].sort())
})

test('native v2 projection exposes explicit alternatives and local profiles', async () => {
  const first = await projectAuthorityBundleV2(fileURLToPath(authorityRoot), sourceSha)
  const second = await projectAuthorityBundleV2(fileURLToPath(authorityRoot), sourceSha)
  assert.deepEqual(first, second)
  assert.equal(first.schema_version, 'stackkits-webmcp/v2alpha1')
  assert.match(first.catalog_sha256, /^[a-f0-9]{64}$/)

  const basement = first.kits.find(({ stackkit_id }) => stackkit_id === 'basement-kit')
  assert.ok(basement)
  const core = basement.use_cases.find(({ use_case_id }) => use_case_id === 'basement-core')
  assert.deepEqual(core.alternatives.map(({ alternative_id }) => alternative_id), ['standalone', 'standalone-lite'])
  assert.equal(core.default_alternative_id, 'standalone')
  assert.equal(core.selected_by_default, true)
  assert.equal(core.required, true)

  const coreModules = basement.modules.filter(({ module_id }) => module_id.includes('basement-core'))
  assert.equal(basement.modules.find(({ module_id }) => module_id === 'stackkits-basement-core-runtime')?.default_compute_profile, 'standard')
  assert.equal(basement.modules.find(({ module_id }) => module_id === 'stackkits-basement-core-lite-runtime')?.default_compute_profile, 'low')
  assert.ok(coreModules.some(({ module_id, compute_profiles }) => module_id.endsWith('lite-runtime') && compute_profiles.some(({ id }) => id === 'low')))
  assert.ok(coreModules.some(({ module_id, compute_profiles }) => module_id.endsWith('runtime') && !module_id.endsWith('lite-runtime') && compute_profiles.some(({ id }) => id === 'standard')))
  assert.ok(basement.modules.some(({ compute_profiles }) => compute_profiles.length === 0))
  assert.equal(Object.hasOwn(basement, 'compute_tiers'), false)

  const photos = basement.modules.find(({ module_id }) => module_id === 'stackkits-immich-runtime')
  assert.ok(photos.compute_profiles.every(({ host_requirements }) =>
    host_requirements?.min_amd64_microarchitecture_level === 2 && host_requirements.storage_filesystem?.required_class === 'local-posix'))

  const modern = first.kits.find(({ stackkit_id }) => stackkit_id === 'modern-homelab')
  assert.ok(modern)
  const initialWorkload = modern.use_cases.find(({ use_case_id }) => use_case_id === 'photos')
  assert.ok(initialWorkload)
  assert.equal(initialWorkload.required, true)
  assert.equal(initialWorkload.selected_by_default, true)
  assert.equal(initialWorkload.default_alternative_id, 'immich')
  assert.ok(initialWorkload.alternatives.some(({ alternative_id }) => alternative_id === 'immich'))
})

test('unknown authority versions and sensitive public strings fail closed', async (context) => {
  const root = await mkdtemp(join(tmpdir(), 'stackkits-webmcp-authority-'))
  context.after(() => rm(root, { recursive: true, force: true }))
  await cp(authorityRoot, root, { recursive: true })
  const manifestPath = join(root, 'manifest.json')
  const manifest = JSON.parse(await readFile(manifestPath, 'utf8'))
  manifest.schemaVersion = 'unknown/v99'
  await writeFile(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`)
  await assert.rejects(projectAuthorityBundle(root, sourceSha), /unknown authority bundle schema version/)

  manifest.schemaVersion = 'stackkit.architecture-authority-bundle/v2'
  const definitionRelative = manifest.profiles['basement-kit']
  const definitionPath = join(root, definitionRelative)
  const definition = JSON.parse(await readFile(definitionPath, 'utf8'))
  definition.metadata.description = 'Read a private https://provider.example.invalid/token endpoint.'
  const bytes = Buffer.from(`${JSON.stringify(definition, null, 2)}\n`)
  await writeFile(definitionPath, bytes)
  manifest.documentHashes[definitionRelative] = `sha256:${createHash('sha256').update(bytes).digest('hex')}`
  await writeFile(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`)
  await assert.rejects(projectAuthorityBundle(root, sourceSha), /public (?:text|string)|sensitive|URL/i)
})
