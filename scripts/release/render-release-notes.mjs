#!/usr/bin/env node
import { readFile, writeFile } from 'node:fs/promises'
import { renderReleaseNotes } from './changelog.mjs'

function readArg(name, fallback = '') {
  const index = process.argv.indexOf(`--${name}`)
  if (index === -1) {
    return fallback
  }
  return process.argv[index + 1] ?? fallback
}

const version = readArg('version')
const repoUrl = readArg('repo-url')
const compareUrl = readArg('compare-url')
const output = readArg('output')
const changelogPath = readArg('changelog', 'CHANGELOG.md')
const allowUnreleased = process.argv.includes('--allow-unreleased')

if (!version) {
  throw new Error('--version is required')
}

const markdown = await readFile(changelogPath, 'utf8')
const notes = renderReleaseNotes({
  markdown,
  version,
  repoUrl,
  compareUrl,
  allowUnreleased,
})

if (output) {
  await writeFile(output, `${notes}\n`, 'utf8')
} else {
  process.stdout.write(`${notes}\n`)
}
