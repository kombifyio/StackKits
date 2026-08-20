// Package schemas - public StackKit document envelope schemas
package schemas

// #KitMaturity is the public readiness vocabulary used by every kit manifest.
#KitMaturity: "supported" | "preview" | "alpha"

// #KitMetadata is the single public stackkit.yaml metadata schema.
#KitMetadata: {
	name:        =~"^[a-z][a-z0-9-]+$"
	displayName: string
	version:     =~"^[0-9]+\\.[0-9]+\\.[0-9]+(-[a-z0-9.]+)?$"
	description: string
	summary?:    string
	author?:     string
	license:     string
	homepage?:   string
	repository?: string
	maturity:    #KitMaturity
	status?:     string
	tags?: [...string]
}

// #KitMetadataDocument validates the common public envelope while preserving
// the two intentional root document kinds from ADR-0033. Rich StackKit/v1
// fields remain open; catalog documents are kept metadata-only by the separate
// document-shape boundary test.
#KitMetadataDocument: ({
	apiVersion: "stackkit/v1"
	kind:       "StackKit"
} | {
	apiVersion: "stackkit.catalog/v1alpha1"
	kind:       "KitCatalogMetadata"
}) & {
	metadata: #KitMetadata
	supportedOS: [...string]
	...
}
