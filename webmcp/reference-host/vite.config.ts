import { readFile } from 'node:fs/promises'
import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

export default defineConfig(async () => {
  const catalogPath = fileURLToPath(new URL('../data/stackkits-catalog.json', import.meta.url))
  const catalog = JSON.parse(await readFile(catalogPath, 'utf8')) as { source_sha?: string }
  if (!/^[a-f0-9]{40}$/.test(catalog.source_sha ?? '')) throw new Error('generated catalog source_sha is invalid')
  return {
    plugins: [svelte()],
    define: { __STACKKITS_SOURCE_SHA__: JSON.stringify(catalog.source_sha) },
    server: { fs: { allow: ['..'] } },
  }
})
