// Package game defines the Game Server use case package.
//
// Game is explicitly post-1.0 and has no selected default tool. SK-M1 records
// catalog intent only; tool selection, module, game-specific configuration,
// runtime, and lifecycle decisions remain deferred to the normal integration
// path.
package game

import "github.com/kombifyio/stackkits/foundation"

Package: foundation.#UseCasePackage & {
	metadata: {
		name:        "game"
		useCaseRef:  "game"
		displayName: "Game Server"
		version:     "0.1.0"
		layer:       "application"
		category:    "game"
		lifecycle:   "draft"
		description: "Post-1.0 self-hosted multiplayer game-server intent with implementation and operational authority deliberately unresolved."
	}

	selection: {
		role: "optional"
		alternatives: []
	}

	defaultRuntimeProfile: "post-1-0-game"
	runtimeProfiles: "post-1-0-game": {
		displayName: "Post-1.0 Game Runtime"
		description: "A future use-case integration must select the game-server implementation, image and license policy, ports, persistence, secrets, resources, backup, and placement."
		realization: "oss"
		placementModes: []
		contexts: []
		managedServerlessEligible: false
		requiresControlPlane:      false
		requiresLocalBridge:       false
		notes: ["Catalog-only placeholder: no default tool, placement, or context is selected for the 1.0 product contract."]
	}

	tools: {}
}
