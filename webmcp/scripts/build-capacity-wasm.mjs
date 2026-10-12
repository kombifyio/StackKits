import { execFileSync } from 'node:child_process'
import { mkdir, readdir, readFile, writeFile } from 'node:fs/promises'
import { copyStaticAsset } from './copy-static-asset.mjs'
import { createHash } from 'node:crypto'
import { existsSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const packageRoot = dirname(dirname(fileURLToPath(import.meta.url)))
const repositoryRoot = dirname(packageRoot)
const assets = join(packageRoot, 'assets')
await mkdir(assets, { recursive: true })
const manifestPath = join(assets, 'manifest.json')
const digest = (bytes) => createHash('sha256').update(bytes).digest('hex')
if (!existsSync(join(repositoryRoot, 'cmd', 'stackkit-capacity-wasm', 'main_js_wasm.go'))) {
  // Published npm consumers receive these artifacts; no Go toolchain or
  // surrounding StackKits source checkout is required to use or rebuild TS.
  const manifest = JSON.parse(await readFile(manifestPath, 'utf8'))
  if (manifest.schemaVersion !== 'stackkit.capacity-browser-assets/v1') throw new Error('capacity asset manifest is invalid')
  for (const file of ['capacityfit.wasm', 'wasm_exec.js', 'GO-LICENSE']) {
    if (digest(await readFile(join(assets, file))) !== manifest.files[file]) throw new Error(`capacity artifact integrity failed: ${file}`)
  }
  process.stdout.write('[capacity] verified packaged Go assets; source/toolchain not required\n')
  process.exit(0)
}
const goRoot = execFileSync('go', ['env', 'GOROOT'], { cwd: repositoryRoot, encoding: 'utf8' }).trim()
const goVersion = execFileSync('go', ['env', 'GOVERSION'], { cwd: repositoryRoot, encoding: 'utf8' }).trim()
execFileSync('go', ['build', '-trimpath', '-ldflags=-s -w', '-o', join(assets, 'capacityfit.wasm'), './cmd/stackkit-capacity-wasm'], {
  cwd: repositoryRoot,
  env: { ...process.env, GOWORK: 'off', GOOS: 'js', GOARCH: 'wasm', GOMAXPROCS: '2', GOFLAGS: '-mod=readonly -p=2' },
  stdio: 'inherit',
})
// The runtime must come from the exact toolchain that built this module.
await copyStaticAsset(join(goRoot, 'lib', 'wasm', 'wasm_exec.js'), join(assets, 'wasm_exec.js'))
await copyStaticAsset(join(goRoot, 'LICENSE'), join(assets, 'GO-LICENSE'))
const source = createHash('sha256')
for (const directory of ['internal/capacityfit', 'cmd/stackkit-capacity-wasm']) {
  for (const file of (await readdir(join(repositoryRoot, directory))).filter((file) => file.endsWith('.go') && !file.endsWith('_test.go')).sort()) {
    source.update(`${directory}/${file}\n`).update(await readFile(join(repositoryRoot, directory, file)))
  }
}
const files = {}
for (const file of ['capacityfit.wasm', 'wasm_exec.js', 'GO-LICENSE']) files[file] = digest(await readFile(join(assets, file)))
await writeFile(manifestPath, `${JSON.stringify({ schemaVersion: 'stackkit.capacity-browser-assets/v1', goVersion, engineSourceSha256: source.digest('hex'), files }, null, 2)}\n`)
process.stdout.write('[capacity] shared Go authority built as static browser assets\n')
