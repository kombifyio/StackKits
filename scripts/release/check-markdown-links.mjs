#!/usr/bin/env node
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import path from 'node:path'

const root = path.resolve(process.argv[2] || process.cwd())
const markdownFiles = []
const excludedDirs = new Set([
  '.git',
  '.roadmap-sync',
  '.stackkit',
  '.stackkits-vm-workspace',
  'build',
  'coverage',
  'dist',
  'node_modules',
])
const excludedRelativeDirs = new Set([
  'scripts/public/templates',
])

function toRelativePath(fullPath) {
  return path.relative(root, fullPath).replaceAll(path.sep, '/')
}

function isExcludedDirectory(fullPath, name) {
  if (excludedDirs.has(name)) {
    return true
  }
  return excludedRelativeDirs.has(toRelativePath(fullPath))
}

function walk(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const fullPath = path.join(dir, entry.name)
    if (entry.isDirectory()) {
      if (isExcludedDirectory(fullPath, entry.name)) {
        continue
      }
      walk(fullPath)
    } else if (entry.isFile() && entry.name.endsWith('.md')) {
      markdownFiles.push(fullPath)
    }
  }
}

function isExternalLink(link) {
  return /^(https?:|mailto:|#)/i.test(link) || /^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(link)
}

function resolveTarget(baseDir, target) {
  const decoded = decodeURIComponent(target)
  if (decoded.startsWith('/')) {
    const websitePublicTarget = path.resolve(root, 'website/public', decoded.slice(1))
    if (existsSync(websitePublicTarget)) {
      return websitePublicTarget
    }
    return path.resolve(root, decoded.slice(1))
  }
  return path.resolve(baseDir, decoded)
}

// Optional file paths scope a consumer check without walking unrelated docs.
const selectedFiles = process.argv.slice(3)
if (selectedFiles.length > 0) {
  markdownFiles.push(...selectedFiles.map((file) => path.resolve(root, file)))
} else {
  walk(root)
}

const broken = []
const linkPattern = /\[[^\]]+\]\(([^)]+)\)/g

for (const file of markdownFiles) {
  const text = readFileSync(file, 'utf8')
  const baseDir = path.dirname(file)
  for (const match of text.matchAll(linkPattern)) {
    const raw = match[1].trim()
    if (!raw || raw.startsWith('<') || raw.startsWith('>') || isExternalLink(raw)) {
      continue
    }

    const target = raw.split('#')[0]
    if (!target) {
      continue
    }

    const fullTarget = resolveTarget(baseDir, target)
    if (!existsSync(fullTarget)) {
      broken.push({
        file: toRelativePath(file),
        link: raw,
      })
      continue
    }

    statSync(fullTarget)
  }
}

if (broken.length > 0) {
  console.error('Public markdown link check failed:')
  for (const item of broken) {
    console.error(`  ${item.file}: ${item.link}`)
  }
  process.exit(1)
}

console.log('Public markdown link check passed.')
