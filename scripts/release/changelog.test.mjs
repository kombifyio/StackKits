import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import {
  extractLatestReleaseNotes,
  parseChangelogSections,
  renderReleaseNotes,
} from './changelog.mjs'

const sampleChangelog = `# Changelog

## [Unreleased]

### Added

- **Draft**: Future release item.

## [0.2.0] - 2026-05-12

### Highlights

- **Public export**: Release publishing now uses an allowlisted OSS tree.
  The public mirror receives only sanitized files.
- BaseKit live gate: Fresh Ubuntu validation is the first live test before tag publish.

### Fixed

- **Website notes**: Release notes are rendered from CHANGELOG.md.

## [0.1.0] - 2026-05-01

### Added

- Initial public release candidate.
`

test('parseChangelogSections reads versions and dates', () => {
  const sections = parseChangelogSections(sampleChangelog)

  assert.deepEqual(
    sections.map((section) => [section.version, section.date]),
    [
      ['Unreleased', ''],
      ['0.2.0', '2026-05-12'],
      ['0.1.0', '2026-05-01'],
    ],
  )
})

test('extractLatestReleaseNotes skips Unreleased and folds continuation lines', () => {
  const latest = extractLatestReleaseNotes(sampleChangelog, { limit: 2 })

  assert.equal(latest.version, '0.2.0')
  assert.deepEqual(latest.notes, [
    {
      title: 'Public export',
      body: 'Release publishing now uses an allowlisted OSS tree. The public mirror receives only sanitized files.',
    },
    {
      title: 'BaseKit live gate',
      body: 'Fresh Ubuntu validation is the first live test before tag publish.',
    },
  ])
})

test('renderReleaseNotes renders exact release notes and compare link', () => {
  const notes = renderReleaseNotes({
    markdown: sampleChangelog,
    version: 'v0.2.0',
    repoUrl: 'https://github.com/kombifyio/stackKits',
  })

  assert.match(notes, /Public export/)
  assert.match(notes, /Website notes/)
  assert.match(
    notes,
    /https:\/\/github\.com\/kombifyio\/stackKits\/compare\/v0\.1\.0\.\.\.v0\.2\.0/,
  )
})

test('renderReleaseNotes can render an Unreleased release candidate on demand', () => {
  const notes = renderReleaseNotes({
    markdown: sampleChangelog,
    version: 'v0.3.0',
    repoUrl: 'https://github.com/kombifyio/stackKits',
    allowUnreleased: true,
  })

  assert.match(notes, /Release notes for v0\.3\.0 are rendered from the current Unreleased/)
  assert.match(notes, /Future release item/)
})

test('published v0.14 and v0.15 history has exact changelog-backed notes', async () => {
  const markdown = await readFile(new URL('../../CHANGELOG.md', import.meta.url), 'utf8')
  const publishedVersions = [
    'v0.14.4',
    'v0.14.7',
    'v0.14.8',
    'v0.14.9',
    'v0.14.12',
    'v0.14.13',
    'v0.14.14',
    'v0.14.15',
    'v0.14.17',
    'v0.14.18',
    'v0.14.19',
    'v0.14.28',
    'v0.15.0',
    'v0.15.1',
    'v0.15.3',
  ]

  for (const version of publishedVersions) {
    const notes = renderReleaseNotes({
      markdown,
      version,
      repoUrl: 'https://github.com/kombifyio/stackKits',
    })
    assert.ok(notes.length > 120, `${version} release notes are unexpectedly thin`)
    assert.doesNotMatch(notes, /Pre-1\.0 development release from/)
  }
})

test('fast pre-1.0 publication cannot bypass the exact changelog renderer', async () => {
  let workflow
  try {
    workflow = await readFile(
      new URL('../../.github/workflows/publish-oss.yml', import.meta.url),
      'utf8',
    )
  } catch (error) {
    // The curated public export intentionally excludes the private publisher.
    // Source-repo test runs still exercise the assertions below.
    assert.equal(error.code, 'ENOENT')
    return
  }

  assert.doesNotMatch(workflow, /Pre-1\.0 development release from/)
  assert.match(workflow, /render-release-notes\.mjs/)
})
