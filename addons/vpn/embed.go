// Package vpn embeds the tool catalog of the optional VPN add-on so the
// stackkit CLI runs exactly the components tools.cue pins.
package vpn

import _ "embed"

// ToolsCUE is tools.cue: the four tools, their components, pinned images and
// licences. It imports nothing and evaluates on its own.
//
//go:embed tools.cue
var ToolsCUE string
