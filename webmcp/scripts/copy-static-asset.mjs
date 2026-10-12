import { chmod, readFile, writeFile } from 'node:fs/promises'

// Node's Windows copyFile preserves the Go module cache's read-only bit.
// Write bytes instead so a second build can replace its own generated asset.
export async function copyStaticAsset(source, destination) {
  const bytes = await readFile(source)
  await chmod(destination, 0o644).catch((error) => {
    if (error.code !== 'ENOENT') throw error
  })
  await writeFile(destination, bytes, { mode: 0o644 })
}
