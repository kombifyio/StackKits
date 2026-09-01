import { execFile } from 'node:child_process'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { promisify } from 'node:util'

const root = dirname(dirname(fileURLToPath(import.meta.url)))
const source = join(root, '..', 'data', 'stackkits-catalog.json')
const target = join(root, 'public', 'data', 'stackkits-webmcp', 'catalog.json')
const execFileAsync = promisify(execFile)
const repositoryRoot = join(root, '..', '..')

let bytes
try {
  const marker = JSON.parse(await readFile(join(repositoryRoot, '.stackkits-public-export.json'), 'utf8'))
  if (marker.schemaVersion !== 'stackkits.public-export-destination/v1' || marker.state !== 'complete') {
    throw new Error('public export marker is invalid')
  }
  bytes = await readFile(source)
} catch (error) {
  if (error?.code !== 'ENOENT') throw error
  const { stdout } = await execFileAsync('git', ['-C', repositoryRoot, 'rev-parse', 'HEAD'])
  const sourceSha = stdout.trim().toLowerCase()
  if (!/^[a-f0-9]{40}$/.test(sourceSha)) throw new Error('repository source SHA is invalid')
  await execFileAsync(process.execPath, [
    join(root, '..', 'scripts', 'generate-catalog.mjs'),
    '--authority-bundle', join(repositoryRoot, 'internal', 'architecturev2', 'authority_bundle'),
    '--out', source,
    '--source-sha', sourceSha,
  ])
  bytes = await readFile(source)
}
const catalog = JSON.parse(bytes.toString('utf8'))
if (!/^[a-f0-9]{40}$/.test(catalog.source_sha ?? '')) throw new Error('generated catalog source_sha is invalid')
await mkdir(dirname(target), { recursive: true })
await writeFile(target, bytes)
process.stdout.write(`[prebuild] reference catalog source ${catalog.source_sha}\n`)
