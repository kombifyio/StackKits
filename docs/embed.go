package docs

import "embed"

// FS exposes public documentation to local agent surfaces such as stackkit-mcp.
//
//go:embed CLI.md API.md stack-spec-reference.md agent
var FS embed.FS
