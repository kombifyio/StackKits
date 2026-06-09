// Package docs embeds public documentation for local agent surfaces.
package docs

import "embed"

// FS exposes public documentation to the StackKits MCP runtime and local agents.
//
//go:embed CLI.md API.md stack-spec-reference.md agent
var FS embed.FS
