#!/usr/bin/env node

import { readFileSync } from 'node:fs'
import { createServer } from 'node:http'
import path from 'node:path'

function fail(message) {
  console.error(`GitHub release fixture: ${message}`)
  process.exit(2)
}

const [rootArgument, portArgument = '18080'] = process.argv.slice(2)
if (!rootArgument) fail('usage: github-release-fixture.mjs <asset-directory> [port]')

const root = path.resolve(rootArgument)
const port = Number(portArgument)
if (!Number.isInteger(port) || port < 1024 || port > 65535) fail('port must be within 1024-65535')

const rawIndex = readFileSync(path.join(root, 'stackkits-release-index-v1.json'))
const index = JSON.parse(rawIndex)
const assetNames = new Set([
  'stackkits-release-index-v1.json',
  'stackkits-release-index-v1.json.intoto.jsonl',
  index.release.trustedRoot.name,
  ...index.assets.flatMap((asset) => [
    asset.archive.name,
    asset.sbom.name,
    asset.attestation.name
  ])
])

const server = createServer((request, response) => {
  if (request.method !== 'GET') {
    response.writeHead(405).end()
    return
  }
  const origin = `http://${request.headers.host}`
  const url = new URL(request.url, origin)
  if (url.pathname === '/repos/kombifyio/stackKits/releases') {
    const prerelease = /-(?:beta|edge)\./u.test(index.release.version)
    const releaseBase = `https://github.com/kombifyio/stackKits/releases/download/${index.release.version}`
    const body = JSON.stringify([{
      tag_name: index.release.version,
      prerelease: prerelease,
      draft: false,
      published_at: index.release.publishedAt,
      assets: [
        {
          name: 'stackkits-release-index-v1.json',
          browser_download_url: `${releaseBase}/stackkits-release-index-v1.json`
        },
        {
          name: 'stackkits-release-index-v1.json.intoto.jsonl',
          browser_download_url: `${releaseBase}/stackkits-release-index-v1.json.intoto.jsonl`
        },
        {
          name: 'sigstore-trusted-root.jsonl',
          browser_download_url: `${releaseBase}/sigstore-trusted-root.jsonl`
        }
      ]
    }])
    response.writeHead(200, {'content-type': 'application/json'}).end(body)
    return
  }
  if (url.pathname.startsWith('/assets/')) {
    const name = decodeURIComponent(url.pathname.slice('/assets/'.length))
    if (!assetNames.has(name) || path.basename(name) !== name) {
      response.writeHead(404).end()
      return
    }
    response.writeHead(200, {'content-type': 'application/octet-stream'})
    response.end(readFileSync(path.join(root, name)))
    return
  }
  response.writeHead(404).end()
})

server.listen(port, '127.0.0.1', () => {
  process.stdout.write(`http://127.0.0.1:${port}\n`)
})

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => server.close(() => process.exit(0)))
}
