// Package surface embeds the generated agent-native API surface of the
// StackKits HTTP contract (API-FIRST-STANDARD section 9). The artifacts are
// generated from api/openapi/stackkits-v1.yaml by `mise run api:surface`;
// never edit them by hand.
package surface

import _ "embed"

// Raw is the generated kombify.api-surface/v1 document.
//
//go:embed api-surface.json
var Raw []byte

// ToolManifest is the generated kombify.tool-manifest/v1 document.
//
//go:embed tool-manifest.json
var ToolManifest []byte
