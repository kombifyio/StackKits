#!/usr/bin/env node

import { createHash } from 'node:crypto'
import { readdirSync, readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const schemaVersion = 'stackkits-release-index/v1'
const githubOIDCIssuer = 'https://token.actions.githubusercontent.com'
const provenancePredicate = 'https://slsa.dev/provenance/v1'
const trustedRootMediaType = 'application/vnd.dev.sigstore.trustedroot+json;version=0.1'
const inTotoMediaType = 'application/vnd.in-toto+jsonl'
const spdxMediaType = 'application/spdx+json'
const releaseArchivePattern =
  /^stackkits-(basement-kit|cloud-kit|modern-homelab)_(.+)_(linux|darwin|windows)_(amd64|arm64)\.(tar\.gz|zip)$/u
const tagPattern =
  /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:beta|edge)\.(?:0|[1-9][0-9]*)))?$/u

function fail(message) {
  throw new Error(`release index rendering failed: ${message}`)
}

function digest(file) {
  return createHash('sha256').update(readFileSync(file)).digest('hex')
}

function releaseURL(baseURL, name) {
  return `${baseURL.replace(/\/+$/u, '')}/${encodeURIComponent(name)}`
}

function blob(file, baseURL, mediaType) {
  const name = path.basename(file)
  return {
    name,
    url: releaseURL(baseURL, name),
    sha256: digest(file),
    mediaType
  }
}

function parseTag(tag) {
  const match = tagPattern.exec(tag)
  if (!match) {
    fail('tag must be strict v-prefixed stable, -beta.N, or -edge.N SemVer')
  }
  return {
    version: tag,
    archiveVersion: tag.slice(1),
    channel: match[4]?.startsWith('beta.') ? 'beta' : match[4]?.startsWith('edge.') ? 'edge' : 'stable'
  }
}

export function renderReleaseIndex({
  dist,
  tag,
  publishedAt,
  baseURL,
  trustedRoot,
  attestation,
  certificateIdentity
}) {
  const release = parseTag(tag)
  const timestamp = new Date(publishedAt)
  if (!Number.isFinite(timestamp.valueOf()) || !String(publishedAt).endsWith('Z')) {
    fail('published-at must be an RFC3339 UTC instant')
  }
  const expectedBaseURL = `https://github.com/kombifyio/stackKits/releases/download/${tag}`
  if (baseURL !== expectedBaseURL) {
    fail(`base-url must be ${expectedBaseURL}`)
  }
  const expectedIdentity =
    `https://github.com/kombifyio/StackKits/.github/workflows/release.yml@refs/tags/${tag}`
  if (certificateIdentity !== expectedIdentity) {
    fail(`certificate identity must be ${expectedIdentity}`)
  }

  const rootPath = path.resolve(trustedRoot)
  const attestationPath = path.resolve(attestation)
  const assets = readdirSync(dist, { withFileTypes: true })
    .filter((entry) => entry.isFile())
    .map((entry) => entry.name)
    .filter((name) => releaseArchivePattern.test(name))
    .sort()
    .map((name) => {
      const match = releaseArchivePattern.exec(name)
      const [, kit, archiveVersion, os, arch, extension] = match
      if (archiveVersion !== release.archiveVersion) {
        fail(`${name} version does not match tag ${tag}`)
      }
      const archivePath = path.join(dist, name)
      const sbomPath = `${archivePath}.spdx.json`
      readFileSync(sbomPath)
      return {
        kit,
        version: release.version,
        channel: release.channel,
        platform: { os, arch },
        archive: blob(
          archivePath,
          baseURL,
          extension === 'zip' ? 'application/zip' : 'application/gzip'
        ),
        sbom: blob(sbomPath, baseURL, spdxMediaType),
        attestation: {
          ...blob(attestationPath, baseURL, inTotoMediaType),
          issuer: githubOIDCIssuer,
          certificateIdentity,
          subject: name,
          predicateType: provenancePredicate
        }
      }
    })
  if (assets.length === 0) {
    fail('no per-kit release archives were found')
  }
  return {
    schemaVersion,
    release: {
      repository: 'kombifyio/stackKits',
      version: release.version,
      publishedAt: timestamp.toISOString(),
      trustedRoot: blob(rootPath, baseURL, trustedRootMediaType)
    },
    assets
  }
}

function parseArgs(argv) {
  const options = {}
  for (let index = 0; index < argv.length; index += 2) {
    const key = argv[index]
    const value = argv[index + 1]
    if (!key?.startsWith('--') || value === undefined) fail(`invalid argument ${key ?? '<missing>'}`)
    options[key.slice(2)] = value
  }
  const required = [
    'dist',
    'tag',
    'published-at',
    'base-url',
    'trusted-root',
    'attestation',
    'certificate-identity',
    'output'
  ]
  for (const key of required) {
    if (!options[key]) fail(`--${key} is required`)
  }
  return options
}

export function main(argv = process.argv.slice(2)) {
  const options = parseArgs(argv)
  const index = renderReleaseIndex({
    dist: path.resolve(options.dist),
    tag: options.tag,
    publishedAt: options['published-at'],
    baseURL: options['base-url'],
    trustedRoot: options['trusted-root'],
    attestation: options.attestation,
    certificateIdentity: options['certificate-identity']
  })
  writeFileSync(path.resolve(options.output), `${JSON.stringify(index, null, 2)}\n`, { mode: 0o600 })
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  try {
    main()
  } catch (error) {
    console.error(error.message)
    process.exitCode = 1
  }
}
