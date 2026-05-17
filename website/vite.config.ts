import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'
import { readFileSync } from 'fs'
import { resolve } from 'path'
// @ts-expect-error Plain .mjs helper consumed at build time.
import { extractLatestReleaseNotes } from '../scripts/release/changelog.mjs'

const changelog = readFileSync(resolve(__dirname, '../CHANGELOG.md'), 'utf-8')
const latestRelease = extractLatestReleaseNotes(changelog, { fallbackVersion: '0.0.0' })
const appVersion = latestRelease.version || '0.0.0'
const versionMatch = /^v?(\d+)\.(\d+)\.(\d+)/.exec(appVersion)
const minorLabel = versionMatch ? `${versionMatch[1]}.${versionMatch[2]}` : appVersion
const displayVersion = `v${minorLabel}`

const ossRepo = 'https://github.com/kombifyio/stackKits'
const ossRepoReleases = `${ossRepo}/releases`
const installOneLiner = 'curl -sSL https://base.stackkit.cc | sh'
const cliInstallOneLiner = 'curl -sSL https://install.stackkit.cc | sh'

export default defineConfig({
  plugins: [
    tailwindcss(),
    svelte(),
  ],
  define: {
    __STACKKIT_VERSION__: JSON.stringify(appVersion),
    __STACKKIT_DISPLAY_VERSION__: JSON.stringify(displayVersion),
    __OSS_REPO__: JSON.stringify(ossRepo),
    __OSS_REPO_RELEASES__: JSON.stringify(ossRepoReleases),
    __STACKKIT_INSTALL_ONELINER__: JSON.stringify(installOneLiner),
    __STACKKIT_CLI_INSTALL_ONELINER__: JSON.stringify(cliInstallOneLiner),
    __STACKKIT_LATEST_RELEASE_VERSION__: JSON.stringify(latestRelease.version),
    __STACKKIT_LATEST_RELEASE_NOTES__: JSON.stringify(latestRelease.notes),
  },
})
