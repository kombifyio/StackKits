import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { cp, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import { projectAuthorityBundle } from '../scripts/generate-catalog.mjs'

const authorityRoot = new URL('../../internal/architecturev2/authority_bundle/', import.meta.url)
const sourceSha = '1111111111111111111111111111111111111111'

test('authority projection is deterministic and source-bound', async () => {
  const first = await projectAuthorityBundle(fileURLToPath(authorityRoot), sourceSha)
  const second = await projectAuthorityBundle(fileURLToPath(authorityRoot), sourceSha)
  assert.deepEqual(first, second)
  assert.equal(first.source_sha, sourceSha)
  assert.match(first.catalog_sha256, /^[a-f0-9]{64}$/)
  assert.deepEqual(first.kits.map(({ stackkit_id }) => stackkit_id), [...first.kits.map(({ stackkit_id }) => stackkit_id)].sort())
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
