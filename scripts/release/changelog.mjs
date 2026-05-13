function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

function normalizeVersion(version) {
  return version.startsWith('v') ? version.slice(1) : version
}

function stripFullChangelogLink(body) {
  return body.replace(/\n?\*\*Full Changelog\*\*:.*$/m, '').trim()
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
    if (trimmed.startsWith('- ')) {
      if (current) {
        bullets.push(current)
      }
      current = trimmed
      continue
    }

    if (!current || !trimmed || trimmed.startsWith('### ') || trimmed.startsWith('## ')) {
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
  const headerExpression = /^## \[([^\]]+)\](?:\s+[-\u2014]\s+(.+))?$/gm
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
      date: match[2]?.trim() ?? '',
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

export function renderReleaseNotes({ markdown, version, repoUrl, compareUrl, allowUnreleased = false }) {
  const sections = parseChangelogSections(markdown)
  const normalizedVersion = normalizeVersion(version)
  let sectionIndex = sections.findIndex((section) => section.version === normalizedVersion)

  if (sectionIndex === -1 && allowUnreleased) {
    sectionIndex = sections.findIndex((section) => section.version === 'Unreleased')
  }

  if (sectionIndex === -1) {
    throw new Error(`Release ${version} was not found in CHANGELOG.md.`)
  }

  const section = sections[sectionIndex]
  const previousSection = sections.slice(sectionIndex + 1).find((candidate) => candidate.version !== 'Unreleased')
  const notes = [stripFullChangelogLink(section.body)]

  if (section.version === 'Unreleased') {
    notes.unshift(`Release notes for ${version} are rendered from the current Unreleased changelog section.`)
  }

  if (compareUrl) {
    notes.push(`**Full Changelog**: ${compareUrl}`)
  } else if (repoUrl && previousSection) {
    notes.push(`**Full Changelog**: ${repoUrl}/compare/v${previousSection.version}...v${normalizedVersion}`)
  }

  return notes.filter(Boolean).join('\n\n').trim()
}
