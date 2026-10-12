import test from 'node:test'
import assert from 'node:assert/strict'
import { chmod, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { copyStaticAsset } from '../scripts/copy-static-asset.mjs'

test('a rebuilt static asset replaces a read-only prior output', async () => {
  const root = await mkdtemp(join(tmpdir(), 'stackkits-capacity-copy-'))
  const source = join(root, 'source.js')
  const output = join(root, 'runtime.js')
  try {
    await writeFile(source, 'first toolchain runtime')
    await chmod(source, 0o444)
    await copyStaticAsset(source, output)
    // Simulate an output left by the old Windows copyFile implementation.
    await chmod(output, 0o444)
    await chmod(source, 0o644)
    await writeFile(source, 'updated toolchain runtime')
    await chmod(source, 0o444)
    await copyStaticAsset(source, output)
    assert.deepEqual(await readFile(output), await readFile(source))
  } finally {
    await chmod(source, 0o644).catch(() => {})
    await chmod(output, 0o644).catch(() => {})
    await rm(root, { recursive: true, force: true })
  }
})
