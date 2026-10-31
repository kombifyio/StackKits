import { readFile } from 'node:fs/promises'
import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite-plus'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// Vite+ (D-2026-10-30-06): `vp check` reads the `lint` and `fmt` blocks; `vp dev` and `vp build`
// ignore them. Svelte components stay on svelte-check (`.svelte` is not linted here) and the
// repository was never oxfmt-formatted, so the scripts run `vp check --no-fmt`.
const checkIgnorePatterns = ['dist/**', 'node_modules/**', 'public/**', '**/*.svelte']

export default defineConfig(async () => {
  const catalogPath = fileURLToPath(new URL('../data/stackkits-webmcp/v2alpha1/catalog.json', import.meta.url))
  const catalog = JSON.parse(await readFile(catalogPath, 'utf8')) as { source_sha?: string }
  if (!/^[a-f0-9]{40}$/.test(catalog.source_sha ?? '')) throw new Error('generated catalog source_sha is invalid')
  return {
    lint: {
      ignorePatterns: checkIgnorePatterns,
      options: { typeAware: true, typeCheck: true },
    },
    fmt: { ignorePatterns: checkIgnorePatterns },
    plugins: [svelte()],
    define: { __STACKKITS_SOURCE_SHA__: JSON.stringify(catalog.source_sha) },
    server: { fs: { allow: ['..'] } },
  }
})
