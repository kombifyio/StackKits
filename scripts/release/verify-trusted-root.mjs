#!/usr/bin/env node

import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'

function option(name) {
  const index = process.argv.indexOf(name)
  if (index < 0 || index + 1 >= process.argv.length) {
    throw new Error(`missing ${name}`)
  }
  return process.argv[index + 1]
}

const policyPath = option('--policy')
const trustedRootPath = option('--trusted-root')
const purpose = process.argv.includes('--purpose') ? option('--purpose') : 'release'
if (!['release', 'preview-candidate'].includes(purpose)) {
  throw new Error('unknown trusted-root verification purpose')
}
const policy = JSON.parse(readFileSync(policyPath, 'utf8'))
const policyKeys = Object.keys(policy).sort()

function validDigests(digests) {
  return Array.isArray(digests) && digests.length > 0 &&
    digests.every((digest) => typeof digest === 'string' && /^[a-f0-9]{64}$/u.test(digest)) &&
    new Set(digests).size === digests.length
}

function validCandidate(candidate) {
  if (!candidate || typeof candidate !== 'object' || Array.isArray(candidate)) return false
  const fields = ['schemaVersion', 'sigstoreTrustedRootDocumentSha256', 'verificationMode',
    'sourceRepositoryIdentifier', 'sourceRepositoryOwnerIdentifier', 'sourceRepositoryVisibilityAtSigning']
  return Object.keys(candidate).length === fields.length &&
    Object.keys(candidate).every((key) => fields.includes(key)) &&
    candidate.schemaVersion === 'stackkit.preview-candidate-trust/v1' &&
    candidate.verificationMode === 'github-rfc3161' &&
    candidate.sourceRepositoryIdentifier === '1129956899' &&
    candidate.sourceRepositoryOwnerIdentifier === '249091777' &&
    candidate.sourceRepositoryVisibilityAtSigning === 'internal' &&
    validDigests(candidate.sigstoreTrustedRootDocumentSha256)
}

if (
  policyKeys.length !== (Object.hasOwn(policy, 'previewCandidate') ? 3 : 2) ||
  policyKeys.some((key) => !['schemaVersion', 'sigstoreTrustedRootDocumentSha256', 'previewCandidate'].includes(key)) ||
  policy.schemaVersion !== 'stackkit.release-trust-policy/v1' ||
  !validDigests(policy.sigstoreTrustedRootDocumentSha256) ||
  (Object.hasOwn(policy, 'previewCandidate') && (
    !validCandidate(policy.previewCandidate) ||
    !policy.previewCandidate.sigstoreTrustedRootDocumentSha256.every((digest) =>
      policy.sigstoreTrustedRootDocumentSha256.includes(digest))
  ))
) {
  throw new Error('release trust policy is invalid')
}

if (purpose === 'preview-candidate' && !validCandidate(policy.previewCandidate)) {
  throw new Error('explicit reviewed Preview candidate trust policy is required')
}
const allowed = purpose === 'preview-candidate'
  ? policy.previewCandidate.sigstoreTrustedRootDocumentSha256
  : policy.sigstoreTrustedRootDocumentSha256

const documentDigests = readFileSync(trustedRootPath, 'utf8')
  .split(/\r?\n/u)
  .map((document) => document.trim())
  .filter(Boolean)
  .map((document) => createHash('sha256').update(document).digest('hex'))

const accepted = documentDigests.filter((digest) =>
  allowed.includes(digest)
)
if (accepted.length === 0) {
  throw new Error(
    'fetched Sigstore trusted-root collection contains no document allowed by the pinned trust policy'
  )
}

process.stdout.write(`${accepted.join('\n')}\n`)
