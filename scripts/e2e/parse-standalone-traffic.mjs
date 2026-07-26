#!/usr/bin/env node

import { readFileSync, writeFileSync } from 'node:fs'
import { isIP } from 'node:net'

import { addressInSubnet, readComposeOriginScope } from './compose-origin-scope.mjs'

const [inputPath, scopePath, outputPath] = process.argv.slice(2)
if (!inputPath || !scopePath || !outputPath) {
  console.error('usage: parse-standalone-traffic.mjs <tcpdump.log> <compose-origin-scope.json> <network-events.jsonl>')
  process.exit(2)
}

function scopeFor(source, host) {
  if (isIP(host) !== 0 &&
      networkScope.subnets.some((subnet) => addressInSubnet(host, subnet))) {
    return 'local'
  }
  if (networkScope.localHostGatewayPairs.has(JSON.stringify([source, host]))) {
    return 'local'
  }
  return 'external'
}

const networkScope = readComposeOriginScope(scopePath)
const lines = readFileSync(inputPath, 'utf8').split(/\r?\n/u)
const dnsQuestionPattern = /\b([A-Z][A-Z0-9-]*)\? ([a-zA-Z0-9._-]+)\.?(?:\s|$)/iu
const packetTimestampPattern = /^(\d{4})-(\d{2})-(\d{2})\s+(\d{2}):(\d{2}):(\d{2})\.(\d{1,6})\s+/u
const packetHeaderPattern = /\b(In|Out|P)\s+(IP6?)\s+(\S+)\s+>\s+(\S+):\s*(.*)$/u
const tcpFlagsPattern = /\bFlags \[([^\]]*)\]/u
const forbiddenHostSuffixes = ['kombify.io', 'kombify.me', 'stackkit.cc']

function parseEndpoint(value) {
  const match = /^(.+)\.([0-9]+)$/u.exec(value)
  if (!match) return null
  const port = Number(match[2])
  if (!Number.isInteger(port) || port < 1 || port > 65535) return null
  const host = match[1].toLowerCase()
  if (isIP(host) === 0) return null
  return {host, port}
}

function parsePacket(line, index) {
  const timestamp = packetTimestampPattern.exec(line)
  const header = packetHeaderPattern.exec(line)
  if (!timestamp || !header) {
    throw new Error(`unparsed captured IP packet at line ${index + 1}: ${line}`)
  }
  const fractionMs = Number(timestamp[7].padEnd(3, '0').slice(0, 3))
  const observedAtMs = Date.UTC(
    Number(timestamp[1]),
    Number(timestamp[2]) - 1,
    Number(timestamp[3]),
    Number(timestamp[4]),
    Number(timestamp[5]),
    Number(timestamp[6]),
    fractionMs
  )
  if (!Number.isFinite(observedAtMs)) {
    throw new Error(`invalid captured packet timestamp at line ${index + 1}: ${line}`)
  }
  const source = parseEndpoint(header[3])
  const destination = parseEndpoint(header[4])
  if (!source || !destination) {
    throw new Error(`unparsed captured IP endpoint at line ${index + 1}: ${line}`)
  }
  return {
    direction: header[1],
    family: header[2],
    source,
    sourceRaw: header[3].toLowerCase(),
    destination,
    destinationRaw: header[4].toLowerCase(),
    payload: header[5],
    observedAt: new Date(observedAtMs).toISOString(),
    observedAtMs,
    index
  }
}

const events = []
for (const [index, line] of lines.entries()) {
  if (!line.includes(' IP ') && !line.includes(' IP6 ')) continue
  const packet = parsePacket(line, index)
  const question = dnsQuestionPattern.exec(packet.payload)
  const dnsOverTCP = (
    packet.payload.includes('Flags [') &&
    (packet.source.port === 53 || packet.destination.port === 53)
  )
  if (dnsOverTCP) {
    throw new Error(`DNS-over-TCP is not admitted at captured line ${index + 1}`)
  }
  if (packet.destination.port === 53) {
    if (!question) throw new Error(`unparsed captured DNS question at line ${index + 1}: ${line}`)
    const host = question[2].toLowerCase().replace(/\.$/u, '')
    if (forbiddenHostSuffixes.some((suffix) => host === suffix || host.endsWith(`.${suffix}`))) {
      throw new Error(`captured DNS question reaches Kombify-controlled host ${host} at line ${index + 1}`)
    }
    continue
  }
  if (packet.source.port === 53) continue

  const tcpFlags = tcpFlagsPattern.exec(packet.payload)?.[1]
  if (tcpFlags === undefined) {
    throw new Error(`unparsed captured non-DNS packet at line ${index + 1}: ${line}`)
  }
  const initialSYN = tcpFlags.includes('S') && !tcpFlags.includes('.')
  if (!initialSYN) {
    throw new Error(`captured TCP packet violates outbound initial SYN contract at line ${index + 1}`)
  }
  if (packet.direction !== 'P') continue
  if (!networkScope.containerIPs.has(packet.source.host)) {
    if (networkScope.subnets.some((subnet) => addressInSubnet(packet.source.host, subnet))) {
      throw new Error(`unknown source ${packet.source.host} inside Compose network scope at line ${index + 1}`)
    }
    continue
  }
  const host = packet.destination.host
  const event = {
    schemaVersion: 'stackkit.network-event/v1',
    observedAt: packet.observedAt,
    kind: 'tcp',
    host,
    port: packet.destination.port,
    scope: scopeFor(packet.source.host, host)
  }
  events.push(event)
}
if (events.length === 0) throw new Error('traffic capture did not contain a Compose-origin network event')
writeFileSync(outputPath, `${events.map((event) => JSON.stringify(event)).join('\n')}\n`)
