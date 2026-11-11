// Package agent embeds the public local-agent prompts the CLI ships.
package agent

import "embed"

// FS exposes the public agent prompt bodies to local surfaces.
//
//go:embed *.md
var FS embed.FS
