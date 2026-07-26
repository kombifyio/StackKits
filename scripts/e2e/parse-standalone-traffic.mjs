#!/usr/bin/env node

import { readFileSync, writeFileSync } from 'node:fs'
import { isIP } from 'node:net'

const [inputPath, outputPath] = process.argv.slice(2)
if (!inputPath || !outputPath) {
  console.error('usage: parse-standalone-traffic.mjs <tcpdump.log> <network-events.jsonl>')
  process.exit(2)
}

function scopeFor(host) {
  if (host === 'localhost' || host.endsWith('.localhost') || host.endsWith('.home.test') ||
      !host.includes('.') || isLocalIP(host)) {
    return 'local'
  }
  if (host === 'github.com' || host.endsWith('.github.com') ||
      host.endsWith('.githubassets.com') || host.endsWith('.githubusercontent.com')) {
    return 'github'
  }
  return 'external'
}

function isLocalIP(host) {
  if (isIP(host) === 0) return false
  if (host === '::1' || /^f[cd][0-9a-f]*:/u.test(host)) return true
  if (/^127(?:\.\d{1,3}){3}$/u.test(host) || /^10(?:\.\d{1,3}){3}$/u.test(host) ||
      /^192\.168(?:\.\d{1,3}){2}$/u.test(host) || /^169\.254(?:\.\d{1,3}){2}$/u.test(host)) {
    return true
  }
  const private172 = /^172\.(\d{1,3})(?:\.\d{1,3}){2}$/u.exec(host)
  return private172 !== null && Number(private172[1]) >= 16 && Number(private172[1]) <= 31
}

const events = new Map()
for (const line of readFileSync(inputPath, 'utf8').split(/\r?\n/u)) {
  if (!line.includes(' IP ')) continue
  const dns = /\b(?:A|AAAA)\? ([a-zA-Z0-9._-]+)\.?(?:\s|$)/u.exec(line)
  if (dns) {
    const host = dns[1].toLowerCase().replace(/\.$/u, '')
    const event = {
      schemaVersion: 'stackkit.network-event/v1',
      observedAt: new Date().toISOString(),
      kind: 'dns',
      host,
      scope: scopeFor(host)
    }
    events.set(`dns:${host}`, event)
    continue
  }
  const tcp = /> ([0-9]{1,3}(?:\.[0-9]{1,3}){3})\.([0-9]+): Flags/u.exec(line)
  if (tcp) {
    const host = tcp[1]
    const port = Number(tcp[2])
    const event = {
      schemaVersion: 'stackkit.network-event/v1',
      observedAt: new Date().toISOString(),
      kind: 'tcp',
      host,
      port,
      scope: scopeFor(host)
    }
    events.set(`tcp:${host}:${port}`, event)
    continue
  }
  if (line.includes('Flags [')) {
    throw new Error(`unparsed captured TCP packet: ${line}`)
  }
}
if (events.size === 0) throw new Error('traffic capture did not contain a parseable network event')
writeFileSync(outputPath, `${[...events.values()].map((event) => JSON.stringify(event)).join('\n')}\n`)
