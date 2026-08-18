// Package foundation provides shared CUE schemas for all Product StackKits.
//
// This package defines the core building blocks that every StackKit extends:
//   - #FoundationStackKit: The root composition schema
//   - #ServiceDefinition: How services are defined
//   - #NodeDefinition: How nodes are specified
//   - Configuration schemas for system, network, security, observability
//
// StackKits MUST import and extend #FoundationStackKit.
//
// Example:
//
//   import "github.com/kombifyio/stackkits/foundation"
//
//   #MyStackKit: foundation.#FoundationStackKit & {
//       metadata: { name: "my-stackkit", ... }
//       ...
//   }
package foundation
