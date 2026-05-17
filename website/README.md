# stackkit.cc website

Public-facing site for **kombify StackKits**, deployed to https://stackkit.cc via Render (with a Cloudflare CDN in front) and a Cloudflare Worker (`public/_worker.js`) that handles installer-subdomain routing.

## Stack

- Svelte 5 SPA + Vite 8 + Tailwind 4
- TypeScript with `svelte-check` for type safety
- Static output (`dist/`) compatible with Cloudflare Pages
- Cloudflare Worker (`public/_worker.js`) handles installer host routing
  (`install.stackkit.cc`, `base.stackkit.cc`, `modern.stackkit.cc`,
  `ha.stackkit.cc` → `/install`, `/base`, `/modern`, `/ha` shell scripts).

## Development

```bash
cd website
npm install
npm run dev        # regenerates changelog.json then starts Vite on 127.0.0.1:5173
npm run check      # svelte-check + tsc
npm run build      # production build into dist/
npm run preview    # serve dist/ locally on 127.0.0.1:4173
npm run test       # node --test on tests/*.test.mjs
```

`npm run build` automatically runs `scripts/prebuild.mjs`, which regenerates
`public/changelog.json` from `../CHANGELOG.md`. The latest release version and
highlights are also exposed to the Svelte code via Vite `define` constants
declared in `vite.config.ts`.

## Layout

```
website/
├── index.html                # Vite entrypoint with <link rel="alternate"> tags
├── src/
│   ├── main.ts               # Mounts App.svelte
│   ├── App.svelte            # Client-side router
│   ├── Layout.svelte         # Top-nav, dropdowns, footer
│   ├── app.css               # Dark M3 token system + orange accent
│   ├── pages/                # Route pages
│   ├── lib/                  # Reusable components
│   └── content/              # Page data (kits, prompts, CLI commands, works-with)
├── public/                   # Static, served as-is at the site root
│   ├── llms.txt, llms-full.txt, llms-snippets.txt
│   ├── api/openapi.v1.yaml
│   ├── schemas/*.json
│   ├── mcp/stackkit-mcp.md
│   ├── getting-started/agents.md + agents/*.md
│   ├── install, base, modern, ha       # POSIX install scripts
│   ├── _headers, _worker.js, CNAME
│   └── sitemap.xml
└── scripts/prebuild.mjs      # Regenerates public/changelog.json
```

## Agent surfaces

The `public/` tree intentionally carries first-class agent surfaces:
`llms.txt`, the OpenAPI YAML, JSON Schemas for run manifests and functional
results, the MCP connector guide, and prompt Markdown. The Svelte routes are
mirrors with links to the raw files — never the source of truth.

## Public mirror rules

Content on this site is published from the curated `kombifyio/stackKits` OSS
mirror. Do not introduce Doppler secret refs, internal-only paths, internal
Auth0 details, or private Beads issue IDs into this folder. See
`../CLAUDE.md` for the full handoff rules.
