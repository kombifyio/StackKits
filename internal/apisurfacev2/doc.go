// Package apisurface generates the kombify API surface from one OpenAPI 3.1
// contract. A product annotates its spec with x-kombify-* extensions;
// Generate validates the contract and builds a language-neutral Surface that
// marshals to api-surface.json and projects the product-native MCP
// tool-manifest.json, the Gateway MCP catalog fragment and the public OpenAPI
// document. Package apisurface/cli mounts the same Surface as cobra commands
// and package apisurface/mcpbind registers its tools on an MCP go-sdk server,
// so API, CLI and MCP stay one contract.
//
// Root extension (required):
//
//	x-kombify-surface:
//	  product: techstack
//	  capability: techstack.inventory.read
//	  envelope: data
//	  cli: { command: techstack }
//	  mcp: { transport: streamable-http, endpoint: /api/v1/mcp, protocolVersion: "2026-07-28" }
//
// x-kombify-surface.mcp may add defaults (tools derived for operations
// without an MCP decision) and catalog (the Gateway catalog fragment).
//
// Operation extensions: x-kombify-mcp, x-kombify-cli, x-kombify-confirmation,
// x-kombify-availability, x-kombify-internal and x-kombify-envelope.
// Parameters and body properties accept x-kombify-name. See README.md.
package apisurface
