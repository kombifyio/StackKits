// Package base provides foundational CUE schemas for all StackKits.
//
// This package defines the core building blocks that every StackKit extends:
//   - #BaseStackKit: The root composition schema
//   - #ServiceDefinition: How services are defined
//   - #NodeDefinition: How nodes are specified
//   - Configuration schemas for system, network, security, observability
//
// StackKits MUST import and extend #BaseStackKit to ensure compatibility
// with the KombiStack Unifier engine.
//
// Example:
//
//   import "github.com/kombifyio/stackkits/base"
//
//   #MyStackKit: base.#BaseStackKit & {
//       metadata: { name: "my-stackkit", ... }
//       ...
//   }
package base
