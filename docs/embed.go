// Package docs embeds public documentation for local agent surfaces.
package docs

import "embed"

// FS exposes public documentation to the StackKits MCP runtime and local agents.
//
//go:embed CLI.md API.md OWN_APPLICATIONS.md OS_COMPATIBILITY.md TROUBLESHOOTING.md stack-spec-reference.md data/os-compat/latest.json agent/*.md
var FS embed.FS
