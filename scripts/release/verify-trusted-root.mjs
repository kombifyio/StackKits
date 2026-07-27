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
const policy = JSON.parse(readFileSync(policyPath, 'utf8'))
const policyKeys = Object.keys(policy).sort()

if (
  policyKeys.length !== 2 ||
  policyKeys[0] !== 'schemaVersion' ||
  policyKeys[1] !== 'sigstoreTrustedRootDocumentSha256' ||
  policy.schemaVersion !== 'stackkit.release-trust-policy/v1' ||
  !Array.isArray(policy.sigstoreTrustedRootDocumentSha256) ||
  policy.sigstoreTrustedRootDocumentSha256.length === 0 ||
  policy.sigstoreTrustedRootDocumentSha256.some((digest) => !/^[a-f0-9]{64}$/u.test(digest)) ||
  new Set(policy.sigstoreTrustedRootDocumentSha256).size !== policy.sigstoreTrustedRootDocumentSha256.length
) {
  throw new Error('release trust policy is invalid')
}

const documentDigests = readFileSync(trustedRootPath, 'utf8')
  .split(/\r?\n/u)
  .map((document) => document.trim())
  .filter(Boolean)
  .map((document) => createHash('sha256').update(document).digest('hex'))

const accepted = documentDigests.filter((digest) =>
  policy.sigstoreTrustedRootDocumentSha256.includes(digest)
)
if (accepted.length === 0) {
  throw new Error(
    'fetched Sigstore trusted-root collection contains no document allowed by the pinned trust policy'
  )
}

process.stdout.write(`${accepted.join('\n')}\n`)
