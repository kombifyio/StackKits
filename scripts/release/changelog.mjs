function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

function normalizeVersion(version) {
  return version.startsWith('v') ? version.slice(1) : version
}

function stripFullChangelogLink(body) {
  return body.replace(/\n?\*\*Full Changelog\*\*:.*$/m, '').trim()
}

function rewritePublicRepositoryLinks(body, repoUrl) {
  if (!repoUrl) {
    return body
  }
  return body.replace(
    /https:\/\/github\.com\/[^/\s)]+\/kombify-StackKits/giu,
    repoUrl.replace(/\/$/u, ''),
  )
}

function getSubsectionBody(body, heading) {
  const headingExpression = new RegExp(`^### ${escapeRegExp(heading)}\\s*$`, 'm')
  const headingMatch = headingExpression.exec(body)
  if (!headingMatch) {
    return ''
  }

  const afterHeading = body.slice(headingMatch.index + headingMatch[0].length).trimStart()
  const nextHeadingMatch = /^### /m.exec(afterHeading)
  if (!nextHeadingMatch) {
    return afterHeading.trim()
  }

  return afterHeading.slice(0, nextHeadingMatch.index).trim()
}

function extractBulletBlocks(body) {
  const bullets = []
  let current = ''

  for (const line of body.split('\n')) {
    const trimmed = line.trim()
    if (/^[-*]\s/u.test(trimmed)) {
      if (current) {
        bullets.push(current)
      }
      current = trimmed
      continue
    }

    // A blank line ends the bullet, so a trailing section paragraph (such as
    // the release-please "Notes cover changes after ..." footer) never merges
    // into the last entry.
    if (!trimmed) {
      if (current) {
        bullets.push(current)
      }
      current = ''
      continue
    }

    if (!current || trimmed.startsWith('### ') || trimmed.startsWith('## ')) {
      continue
    }

    current = `${current} ${trimmed}`
  }

  if (current) {
    bullets.push(current)
  }

  return bullets
}

function toReleaseNote(rawLine, index) {
  const raw = rawLine.slice(2).trim()
  const scopedBoldMatch = raw.match(/^\*\*(.+?):\*\*\s*(.+)$/u)
  if (scopedBoldMatch) {
    return {
      title: scopedBoldMatch[1].trim(),
      body: scopedBoldMatch[2].trim(),
    }
  }
  const boldMatch = raw.match(/^\*\*(.+?)\*\*:\s*(.+)$/)
  if (boldMatch) {
    return {
      title: boldMatch[1].trim(),
      body: boldMatch[2].trim(),
    }
  }

  const colonIndex = raw.indexOf(':')
  if (colonIndex > 0) {
    return {
      title: raw.slice(0, colonIndex).trim(),
      body: raw.slice(colonIndex + 1).trim(),
    }
  }

  return {
    title: `Update ${index + 1}`,
    body: raw,
  }
}

export function parseChangelogSections(markdown) {
  markdown = markdown.replace(/^## Unreleased[ \t]*\r?$/gm, '## [Unreleased]')
  const headerExpression = /^## \[([^\]]+)\](?:\([^)]+\))?(?:[ \t]+(?:[-\u2014][ \t]+(.+)|\(([^)]+)\)))?[ \t]*\r?$/gm
  const matches = [...markdown.matchAll(headerExpression)]
  const sections = []

  for (let index = 0; index < matches.length; index += 1) {
    const match = matches[index]
    const version = match[1]?.trim()
    if (!version) {
      continue
    }

    const sectionStart = (match.index ?? 0) + match[0].length
    const sectionEnd = matches[index + 1]?.index ?? markdown.length
    sections.push({
      version,
      date: (match[2] ?? match[3])?.trim() ?? '',
      body: markdown.slice(sectionStart, sectionEnd).trim(),
    })
  }

  return sections
}

export function extractLatestReleaseNotes(markdown, options = {}) {
  const { limit = 3, fallbackVersion = 'Unreleased' } = options
  const sections = parseChangelogSections(markdown)
  const latest = sections.find((section) => section.version !== 'Unreleased') || sections[0]
  if (!latest) {
    return { version: fallbackVersion, notes: [] }
  }

  const highlightsBody = getSubsectionBody(latest.body, 'Highlights')
  const addedBody = getSubsectionBody(latest.body, 'Added')
  const sourceBody = highlightsBody || addedBody || latest.body
  const bulletLines = extractBulletBlocks(sourceBody).slice(0, limit)

  return {
    version: latest.version,
    notes: bulletLines.map(toReleaseNote),
  }
}

function minorKey(version) {
  const match = /^(\d+)\.(\d+)\.\d+(?:[-+].*)?$/u.exec(version)
  return match ? `${match[1]}.${match[2]}` : ''
}

export function toMinorVersionLabel(version) {
  return minorKey(normalizeVersion(version)) || normalizeVersion(version)
}

function compareMinorKeys(left, right) {
  const [leftMajor, leftMinor] = left.split('.').map(Number)
  const [rightMajor, rightMinor] = right.split('.').map(Number)
  return leftMajor - rightMajor || leftMinor - rightMinor
}

function subsectionHeadings(body) {
  return [...body.matchAll(/^### (.+?)\s*$/gmu)].map((match) => match[1].trim())
}

// A line entry keeps its conventional-commit scope as the title; unscoped
// entries get an empty title instead of a synthetic "Update N".
function toLineEntry(bullet) {
  const raw = bullet.slice(2).trim()
  const scoped = raw.match(/^\*\*(.+?):\*\*\s*(.+)$/u)
  return scoped ? { title: scoped[1].trim(), body: scoped[2].trim() } : { title: '', body: raw }
}

function normalizeEntry(bullet) {
  return bullet.replace(/\s+/gu, ' ').trim().toLowerCase()
}

// The website presents one release line (X.Y) at a time: curated highlights
// plus every entry that first shipped in that line. An X.Y.0 section restates
// fixes that patch releases of the previous line already shipped, so those
// are dropped. Without a curated `### Highlights` list the line's Added
// entries stand in, never its fixes.
// Every public release line (X.Y.0) needs a curated `### Highlights` list:
// CI-CD-PLATFORM-STANDARD §4.2 requires understandable notes and a concise
// summary per new line, and the website and GitHub releases render these
// highlights instead of raw commit subjects. Returns the X.Y.0 versions, at or
// after `sinceVersion`, whose section has no Highlights.
export function releaseLinesMissingHighlights(markdown, options = {}) {
  const { sinceVersion = '0.32.0' } = options
  const since = minorKey(sinceVersion)
  return parseChangelogSections(markdown)
    .filter((section) => /^\d+\.\d+\.0$/u.test(section.version))
    .filter((section) => compareMinorKeys(minorKey(section.version), since) >= 0)
    .filter((section) => !subsectionHeadings(section.body).includes('Highlights'))
    .map((section) => section.version)
}

export function extractReleaseLine(markdown, options = {}) {
  const { anchorVersion = '', highlightLimit = 4, fallbackVersion = '0.0' } = options
  const sections = parseChangelogSections(markdown)
  const currentMinor = minorKey(normalizeVersion(anchorVersion))
    || minorKey(sections.find((section) => minorKey(section.version))?.version || '')
  const lineSections = sections.filter((section) => minorKey(section.version) === currentMinor)
  if (!currentMinor || lineSections.length === 0) {
    return { version: fallbackVersion, latestVersion: '', previousVersion: '', highlights: [], groups: [] }
  }

  const earlierSections = sections.filter((section) => {
    const key = minorKey(section.version)
    return key && compareMinorKeys(key, currentMinor) < 0
  })
  const shippedEarlier = new Set(earlierSections.flatMap((section) => extractBulletBlocks(section.body).map(normalizeEntry)))
  const previousVersion = earlierSections.map((section) => minorKey(section.version))
    .sort((left, right) => compareMinorKeys(right, left))[0] ?? ''

  const groups = new Map()
  const seen = new Set()
  let curatedHighlights = ''
  // Oldest section first, so an entry keeps the release that introduced it.
  for (const section of [...lineSections].reverse()) {
    for (const heading of subsectionHeadings(section.body)) {
      const body = getSubsectionBody(section.body, heading)
      if (heading === 'Highlights') {
        curatedHighlights ||= body
        continue
      }
      for (const bullet of extractBulletBlocks(body)) {
        const key = normalizeEntry(bullet)
        if (shippedEarlier.has(key) || seen.has(key)) continue
        seen.add(key)
        if (!groups.has(heading)) groups.set(heading, [])
        groups.get(heading).push(toLineEntry(bullet))
      }
    }
  }

  const highlights = curatedHighlights
    ? extractBulletBlocks(curatedHighlights).map(toReleaseNote)
    : groups.get('Added') ?? []
  const order = ['Added', 'Changed', 'Fixed', 'Reverted']
  return {
    version: currentMinor,
    latestVersion: lineSections[0].version,
    previousVersion,
    highlights: highlights.slice(0, highlightLimit),
    groups: [...groups.entries()]
      .sort(([left], [right]) => (order.indexOf(left) + 1 || order.length + 1) - (order.indexOf(right) + 1 || order.length + 1))
      .map(([title, notes]) => ({ title, notes })),
  }
}

export function renderReleaseNotes({
  markdown,
  version,
  repoUrl,
  compareUrl,
  allowUnreleased = false,
}) {
  const sections = parseChangelogSections(markdown)
  const normalizedVersion = normalizeVersion(version)
  let sectionIndex = sections.findIndex((section) => section.version === normalizedVersion)

  if (sectionIndex === -1 && minorKey(normalizedVersion)) {
    sectionIndex = sections.findIndex((section) => section.version === `${minorKey(normalizedVersion)}.0`)
  }

  if (sectionIndex === -1 && allowUnreleased && !sections.some((section) => minorKey(section.version))) {
    sectionIndex = sections.findIndex((section) => section.version === 'Unreleased')
  }

  if (sectionIndex === -1) {
    throw new Error(`Release ${version} was not found in CHANGELOG.md.`)
  }

  const section = sections[sectionIndex]
  const previousSection = sections.slice(sectionIndex + 1).find((candidate) => candidate.version !== 'Unreleased')
  const notes = [rewritePublicRepositoryLinks(stripFullChangelogLink(section.body), repoUrl)]

  if (section.version === 'Unreleased') {
    notes.unshift(`Release notes for ${version} are rendered from the current Unreleased changelog section.`)
  } else if (section.version !== normalizedVersion) {
    notes.unshift(`Release ${normalizedVersion} belongs to the ${minorKey(normalizedVersion)} feature line. The highlights below describe that line; see the full changelog for patch changes.`)
  }

  if (compareUrl) {
    notes.push(`**Full Changelog**: ${compareUrl}`)
  } else if (repoUrl && previousSection) {
    notes.push(`**Full Changelog**: ${repoUrl}/compare/v${previousSection.version}...v${normalizedVersion}`)
  }

  return notes.filter(Boolean).join('\n\n').trim()
}
