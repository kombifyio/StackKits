# WebMCP contest boundary and demo evidence

Live entry: <https://stackkit.cc/planner>

Official rules: <https://webmcp.devpost.com/rules>. The submission deadline is
3 September 2026 at 22:00 CEST. Acceptance requires a reachable live URL,
complete public source with a visible OSS license, and a public demo video
under three minutes.

Public source license: Apache-2.0 (`webmcp/LICENSE`; the exported repository
also keeps the root license and notices).

## Existing StackKits functionality

Before this WebMCP implementation, StackKits already owned the CUE kit
definitions, compute-tier graphs, use-case fits, module substitutions,
standalone CLI lifecycle, host preflight, and local MCP connector. The
implementation baseline is commit
`37ca8465067996eaec5e07519990c35b2182486b`; merged planning evidence is PR
#824 and Bead `kombify-StackKits-ohma`.

Those existing capabilities are inputs to the contest project. They are not
presented as newly built WebMCP functionality.

## Contest implementation

The implementation PR after that baseline adds:

- two deterministic OSS authority documents for compute-tier fits and
  standalone operation metadata;
- the `stackkits-webmcp/v1` JSON Schemas, schema-generated TypeScript types,
  fail-closed catalog projection, and four read-only page tools;
- the shared visible Planner session and Svelte component;
- the thin `stackkit.cc/planner` host and independently buildable OSS
  reference host;
- generator, contract, behavior, browser, export, and five agent-eval
  evidence lanes.

No WebMCP tool installs software, reads a host, executes a CLI, contacts a
provider, writes files, or applies a plan. The CLI handoff is reviewable data.

## Demo video outline (under three minutes)

1. Open `/planner` in a browser without WebMCP and complete an explicit
   Basement `low` capacity assessment.
2. Show the excluded Media explanation and the Core/Photo-Lite substitutions.
3. In a WebMCP-capable browser, discover all four tools and run
   list → profile → assess → handoff.
4. Show the same selection and result appearing immediately in the visible
   Planner.
5. Inspect the `argv` arrays and the separate, non-executable
   `stackkit.apply` follow-up.
6. Show the exact source SHA and both digests, then the public source and
   Apache-2.0 license.

## Submission evidence checklist

- Live URL reachable: pending exact-SHA production deployment.
- Public mirror builds on Node 24: verified locally; exact implementation SHA
  evidence is recorded on the implementation PR.
- Public demo video under three minutes: pending recording.
- Devpost submission: pending after live and video evidence.

Implemented, locally verified, merged, publicly reproduced, deployed, live
verified, and submitted remain separate statuses.
