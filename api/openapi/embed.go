// Package openapi provides embedded access to the kombify StackKits OpenAPI specification.
package openapi

import _ "embed"

//go:embed stackkits-v1.yaml
var Spec []byte
