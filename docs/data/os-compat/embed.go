// Package oscompat embeds only the public OS compatibility evidence consumed
// by the standalone CLI.
package oscompat

import "embed"

// FS contains the current public OS compatibility matrix.
//
//go:embed latest.json
var FS embed.FS
