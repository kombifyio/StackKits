#!/usr/bin/env node

import { readFileSync } from 'node:fs'
import { isIP } from 'node:net'

const scopeSchema = 'stackkit.compose-origin-scope/v1'
const projectName = 'stackkit-basement-core'
const objectIDPattern = /^[0-9a-f]{64}$/u
const namePattern = /^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/u

function fail(message) {
  throw new Error(`invalid Compose origin scope: ${message}`)
}

function exactKeys(value, keys, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) fail(`${label} must be an object`)
  const actual = Object.keys(value).sort()
  const expected = [...keys].sort()
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) {
    fail(`${label} fields must be exactly ${expected.join(', ')}`)
  }
}

function ipValue(value) {
  const family = isIP(value)
  if (family === 4) {
    return {
      bits: 32n,
      value: value.split('.').reduce((result, part) => (result << 8n) | BigInt(part), 0n)
    }
  }
  if (family !== 6) fail(`invalid IP address ${value}`)
  const [head, tail, ...extra] = value.toLowerCase().split('::')
  if (extra.length > 0) fail(`invalid IP address ${value}`)
  const left = head === '' ? [] : head.split(':')
  const right = tail === undefined || tail === '' ? [] : tail.split(':')
  const missing = 8 - left.length - right.length
  if ((tail === undefined && missing !== 0) || (tail !== undefined && missing < 1)) {
    fail(`invalid IP address ${value}`)
  }
  const groups = [...left, ...Array(Math.max(0, missing)).fill('0'), ...right]
  let result = 0n
  for (const group of groups) result = (result << 16n) | BigInt(`0x${group || '0'}`)
  return {bits: 128n, value: result}
}

function parseSubnet(subnet) {
  if (typeof subnet !== 'string' || subnet !== subnet.trim().toLowerCase()) {
    fail(`subnet ${String(subnet)} is not canonical`)
  }
  const parts = subnet.split('/')
  if (parts.length !== 2 || !/^(?:0|[1-9][0-9]*)$/u.test(parts[1])) fail(`invalid subnet ${subnet}`)
  const address = ipValue(parts[0])
  const prefix = BigInt(parts[1])
  if (prefix < 0n || prefix > address.bits) fail(`invalid subnet ${subnet}`)
  const hostBits = address.bits - prefix
  const mask = hostBits === address.bits ? 0n : ((1n << address.bits) - 1n) ^ ((1n << hostBits) - 1n)
  if ((address.value & mask) !== address.value) fail(`subnet ${subnet} has host bits set`)
  return {...address, mask}
}

function sortedUniqueStrings(values, label, validate) {
  if (!Array.isArray(values) || values.length === 0) fail(`${label} must be a non-empty array`)
  if (values.some((value) => typeof value !== 'string') ||
      values.some((value, index) => index > 0 && value <= values[index - 1])) {
    fail(`${label} must contain sorted unique strings`)
  }
  values.forEach(validate)
}

export function addressInSubnet(address, subnet) {
  const candidate = ipValue(address)
  const network = parseSubnet(subnet)
  return candidate.bits === network.bits && (candidate.value & network.mask) === network.value
}

export function canonicalScope(scope) {
  return `${JSON.stringify(scope, null, 2)}\n`
}

export function validateComposeOriginScope(scope, raw) {
  exactKeys(scope, ['schemaVersion', 'project', 'networks', 'containers'], 'scope')
  if (scope.schemaVersion !== scopeSchema) fail('unsupported schemaVersion')
  if (scope.project !== projectName) fail(`project must be ${projectName}`)
  if (!Array.isArray(scope.networks) || scope.networks.length < 1 || scope.networks.length > 32) {
    fail('networks must contain 1-32 entries')
  }
  if (!Array.isArray(scope.containers) || scope.containers.length < 1 || scope.containers.length > 128) {
    fail('containers must contain 1-128 entries')
  }

  const networks = new Map()
  let previousNetworkID = ''
  scope.networks.forEach((network, index) => {
    exactKeys(network, ['id', 'name', 'subnets'], `networks[${index}]`)
    if (!objectIDPattern.test(network.id) || network.id <= previousNetworkID) {
      fail('networks must have sorted unique full Docker IDs')
    }
    previousNetworkID = network.id
    if (!namePattern.test(network.name)) fail(`networks[${index}].name is invalid`)
    sortedUniqueStrings(network.subnets, `networks[${index}].subnets`, parseSubnet)
    if (network.subnets.length > 16) fail(`networks[${index}].subnets exceeds 16 entries`)
    networks.set(network.id, network)
  })

  const containerIPs = new Set()
  let previousContainerID = ''
  scope.containers.forEach((container, containerIndex) => {
    exactKeys(container, ['id', 'service', 'networks'], `containers[${containerIndex}]`)
    if (!objectIDPattern.test(container.id) || container.id <= previousContainerID) {
      fail('containers must have sorted unique full Docker IDs')
    }
    previousContainerID = container.id
    if (!namePattern.test(container.service)) fail(`containers[${containerIndex}].service is invalid`)
    if (!Array.isArray(container.networks) ||
        container.networks.length < 1 || container.networks.length > 32) {
      fail(`containers[${containerIndex}] has no bridge network; host networking is forbidden`)
    }
    let previousID = ''
    container.networks.forEach((binding, bindingIndex) => {
      exactKeys(binding, ['id', 'ips'], `containers[${containerIndex}].networks[${bindingIndex}]`)
      if (!objectIDPattern.test(binding.id) || binding.id <= previousID || !networks.has(binding.id)) {
        fail(`containers[${containerIndex}] has an unknown or unsorted network ID`)
      }
      previousID = binding.id
      sortedUniqueStrings(binding.ips, `containers[${containerIndex}].networks[${bindingIndex}].ips`, (ip) => {
        if (isIP(ip) === 0 || ip !== ip.toLowerCase()) fail(`container IP ${ip} is invalid`)
        if (!networks.get(binding.id).subnets.some((subnet) => addressInSubnet(ip, subnet))) {
          fail(`container IP ${ip} is outside its declared network subnets`)
        }
        if (containerIPs.has(ip)) fail(`container IP ${ip} is assigned more than once`)
        containerIPs.add(ip)
      })
      if (binding.ips.length > 2) fail(`containers[${containerIndex}].networks[${bindingIndex}].ips exceeds 2 entries`)
    })
  })
  if (raw !== undefined && raw !== canonicalScope(scope)) fail('scope manifest is not canonical newline-terminated JSON')
  return {
    scope,
    containerIPs,
    subnets: scope.networks.flatMap((network) => network.subnets)
  }
}

export function readComposeOriginScope(scopePath) {
  const raw = readFileSync(scopePath, 'utf8')
  let scope
  try {
    scope = JSON.parse(raw)
  } catch (error) {
    fail(`JSON parse failed: ${error.message}`)
  }
  return {...validateComposeOriginScope(scope, raw), raw: Buffer.from(raw)}
}
