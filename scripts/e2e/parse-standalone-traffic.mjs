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
      (isIP(host) === 0 && !host.includes('.')) || isLocalIP(host)) {
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

function addToSet(map, key, value) {
  const values = map.get(key) ?? new Set()
  values.add(value)
  map.set(key, values)
}

const lines = readFileSync(inputPath, 'utf8').split(/\r?\n/u)
const dnsBindingWindowMs = 60_000
const dnsQuestions = new Map()
const dnsAnswers = new Map()
const dnsQuestionPattern = /\b([A-Z][A-Z0-9-]*)\? ([a-zA-Z0-9._-]+)\.?(?:\s|$)/iu
const dnsIPv4AnswerPattern = /\bA ([0-9]{1,3}(?:\.[0-9]{1,3}){3})(?:[, ]|$)/gu
const dnsIPv6AnswerPattern = /\bAAAA ([0-9a-fA-F:]+)(?:[, ]|$)/gu
const packetTimestampPattern = /^(\d{4})-(\d{2})-(\d{2})\s+(\d{2}):(\d{2}):(\d{2})\.(\d{1,6})\s+/u
const packetHeaderPattern = /\b(In|Out)\s+(IP6?)\s+(\S+)\s+>\s+(\S+):\s*(.*)$/u
const dnsTransactionPattern = /^(\d+)[+*]?(?:\s|$)/u
const tcpFlagsPattern = /\bFlags \[([^\]]*)\]/u

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

function dnsFlowKey(packet, transactionID, response = false) {
  const transport = packet.payload.includes('Flags [') ? 'tcp' : 'udp'
  const client = response ? packet.destinationRaw : packet.sourceRaw
  const resolver = response ? packet.sourceRaw : packet.destinationRaw
  return `${packet.family}:${transport}:${client}>${resolver}:${transactionID}`
}

function activeResolvedHosts(host, observedAtMs) {
  const bindings = (dnsAnswers.get(host) ?? []).filter((binding) =>
    binding.observedAtMs <= observedAtMs &&
    observedAtMs - binding.observedAtMs <= dnsBindingWindowMs
  )
  return [...new Set(bindings.map((binding) => binding.host))].sort()
}

const events = []
for (const [index, line] of lines.entries()) {
  if (!line.includes(' IP ') && !line.includes(' IP6 ')) continue
  const packet = parsePacket(line, index)
  const transaction = dnsTransactionPattern.exec(packet.payload)
  const question = dnsQuestionPattern.exec(packet.payload)
  const dnsOverTCP = (
    packet.payload.includes('Flags [') &&
    (packet.source.port === 53 || packet.destination.port === 53)
  )
  if (dnsOverTCP) {
    throw new Error(`DNS-over-TCP is not admitted at captured line ${index + 1}`)
  }
  if (transaction && question && packet.destination.port === 53) {
    const questionType = question[1].toUpperCase()
    const host = question[2].toLowerCase().replace(/\.$/u, '')
    const key = dnsFlowKey(packet, transaction[1])
    const existing = dnsQuestions.get(key)
    dnsQuestions.set(key, {
      host,
      questionType,
      observedAtMs: packet.observedAtMs,
      ambiguous: existing !== undefined
    })
    const event = {
      schemaVersion: 'stackkit.network-event/v1',
      observedAt: packet.observedAt,
      kind: 'dns',
      host,
      scope: scopeFor(host)
    }
    events.push(event)
    continue
  }
  if (packet.destination.port === 53) {
    throw new Error(`unparsed captured DNS query at line ${index + 1}: ${line}`)
  }

  if (packet.source.port === 53) {
    if (!transaction) {
      throw new Error(`unparsed captured DNS response at line ${index + 1}: ${line}`)
    }
    const key = dnsFlowKey(packet, transaction[1], true)
    const matchingQuestion = dnsQuestions.get(key)
    dnsQuestions.delete(key)
    if (matchingQuestion &&
        !matchingQuestion.ambiguous &&
        packet.observedAtMs >= matchingQuestion.observedAtMs &&
        packet.observedAtMs - matchingQuestion.observedAtMs <= dnsBindingWindowMs) {
      const answers = matchingQuestion.questionType === 'A'
        ? [...packet.payload.matchAll(dnsIPv4AnswerPattern)]
        : matchingQuestion.questionType === 'AAAA'
          ? [...packet.payload.matchAll(dnsIPv6AnswerPattern)]
          : []
      for (const answer of answers) {
        const address = answer[1].toLowerCase()
        const bindings = dnsAnswers.get(address) ?? []
        bindings.push({host: matchingQuestion.host, observedAtMs: packet.observedAtMs})
        dnsAnswers.set(address, bindings)
      }
    }
    continue
  }

  const tcpFlags = tcpFlagsPattern.exec(packet.payload)?.[1]
  if (tcpFlags === undefined) {
    throw new Error(`unparsed captured non-DNS packet at line ${index + 1}: ${line}`)
  }
  const initialSYN = tcpFlags.includes('S') && !tcpFlags.includes('.')
  if (!initialSYN) {
    throw new Error(`captured TCP packet violates outbound initial SYN contract at line ${index + 1}`)
  }
  if (packet.direction !== 'Out') continue
  const host = packet.destination.host
  const resolvedHosts = activeResolvedHosts(host, packet.observedAtMs)
  const githubResolution = (
    resolvedHosts.length > 0 &&
    resolvedHosts.every((resolvedHost) => scopeFor(resolvedHost) === 'github')
  )
  const event = {
    schemaVersion: 'stackkit.network-event/v1',
    observedAt: packet.observedAt,
    kind: 'tcp',
    host,
    port: packet.destination.port,
    scope: githubResolution ? 'github' : scopeFor(host)
  }
  if (githubResolution) event.resolvedHost = resolvedHosts[0]
  events.push(event)
}
if (events.length === 0) throw new Error('traffic capture did not contain a parseable network event')
writeFileSync(outputPath, `${events.map((event) => JSON.stringify(event)).join('\n')}\n`)
