import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import process from 'node:process'
import { fileURLToPath } from 'node:url'

export function canonicalDigest(document) {
  const clone = structuredClone(document)
  delete clone.contentDigest
  const normalize = value => Array.isArray(value)
    ? value.map(normalize)
    : value && typeof value === 'object'
      ? Object.fromEntries(Object.keys(value).sort().map(key => [key, normalize(value[key])]))
      : value
  return `sha256:${createHash('sha256').update(JSON.stringify(normalize(clone)), 'utf8').digest('hex')}`
}

export function validateManifest(document, expectedSchema, expectedTag) {
  if (document.schemaVersion !== expectedSchema) throw new Error(`schemaVersion=${document.schemaVersion}, want ${expectedSchema}`)
  if (document.release?.tag !== expectedTag || document.release?.version !== expectedTag.slice(1)) throw new Error('release tag/version mismatch')
  if (!/^[0-9a-f]{40}$/.test(document.release?.sourceSha ?? '') || !/^[0-9a-f]{40}$/.test(document.release?.publicSourceSha ?? '')) throw new Error('release SHAs must be full lowercase values')
  const expectedURL = `https://github.com/kombifyio/StackKits/releases/tag/${expectedTag}`
  if (document.release?.releaseUrl !== expectedURL) throw new Error(`releaseUrl must be ${expectedURL}`)
  const digest = canonicalDigest(document)
  if (document.contentDigest !== digest) throw new Error(`contentDigest mismatch: ${document.contentDigest} != ${digest}`)
  if (expectedSchema === 'stackkits-compatibility/v1') {
    for (const row of document.compatibility?.os ?? []) {
      if (['supported', 'preview'].includes(row.status) && !/^https:\/\//.test(row.evidenceRef ?? '')) throw new Error(`positive OS row ${row.id} has no public evidenceRef`)
    }
  }
  return document
}

function main() {
  const [directory, tag] = process.argv.slice(2)
  if (!directory || !/^v\d+\.\d+\.\d+$/.test(tag ?? '')) throw new Error('usage: node validate-docs-manifests.mjs <directory> <vX.Y.Z>')
  validateManifest(JSON.parse(readFileSync(path.join(directory, 'stackkits-use-case-catalog-v1.json'), 'utf8')), 'stackkits-use-case-catalog/v1', tag)
  validateManifest(JSON.parse(readFileSync(path.join(directory, 'stackkits-compatibility-v1.json'), 'utf8')), 'stackkits-compatibility/v1', tag)
  process.stdout.write('stackkits_docs_manifests: ok\n')
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main()
