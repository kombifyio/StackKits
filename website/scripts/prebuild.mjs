import { readFile, writeFile, mkdir } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { extractLatestReleaseNotes } from '../../scripts/release/changelog.mjs'

const here = path.dirname(fileURLToPath(import.meta.url))
const websiteRoot = path.resolve(here, '..')
const repoRoot = path.resolve(websiteRoot, '..')
const publicDir = path.join(websiteRoot, 'public')
const changelogPath = path.join(repoRoot, 'CHANGELOG.md')
const outputPath = path.join(publicDir, 'changelog.json')

const changelog = await readFile(changelogPath, 'utf8')
const latest = extractLatestReleaseNotes(changelog, { fallbackVersion: 'Unreleased' })

await mkdir(publicDir, { recursive: true })
await writeFile(outputPath, `${JSON.stringify(latest, null, 2)}\n`, 'utf8')

console.log(`[prebuild] Wrote ${path.relative(websiteRoot, outputPath)} (release ${latest.version}, ${latest.notes.length} notes)`)
