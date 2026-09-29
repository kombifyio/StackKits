package apisurface

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// SchemaVersion identifies the api-surface.json artifact format.
const SchemaVersion = "kombify.api-surface/v1"

// Surface is the language-neutral projection of one product's OpenAPI
// contract. It is the single input for generated CLI commands and the MCP tool
// manifest.
type Surface struct {
	SchemaVersion string      `json:"schemaVersion"`
	Product       string      `json:"product"`
	Source        Source      `json:"source"`
	Envelope      string      `json:"envelope,omitempty"`
	CLI           *RootCLI    `json:"cli,omitempty"`
	Capability    string      `json:"capability,omitempty"`
	MCP           *RootMCP    `json:"mcp,omitempty"`
	Operations    []Operation `json:"operations"`
}

// Source binds the surface to the exact spec bytes it was generated from.
type Source struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// RootCLI names the product binary.
type RootCLI struct {
	Command string `json:"command,omitempty"`
}

// RootMCP identifies the product-native MCP endpoint.
type RootMCP struct {
	Transport       string `json:"transport,omitempty"`
	Endpoint        string `json:"endpoint,omitempty"`
	ProtocolVersion string `json:"protocolVersion,omitempty"`
}

// Operation is one exposed OpenAPI operation.
type Operation struct {
	OperationID  string     `json:"operationId"`
	Method       string     `json:"method"`
	Path         string     `json:"path"`
	Summary      string     `json:"summary,omitempty"`
	Description  string     `json:"description,omitempty"`
	Tags         []string   `json:"tags,omitempty"`
	Availability string     `json:"availability,omitempty"`
	Deprecated   bool       `json:"deprecated,omitempty"`
	Confirmation string     `json:"confirmation,omitempty"`
	Mutating     bool       `json:"mutating,omitempty"`
	Arguments    []Argument `json:"arguments,omitempty"`
	Body         *Body      `json:"body,omitempty"`
	Output       *Output    `json:"output,omitempty"`
	CLI          CLI        `json:"cli"`
	MCP          *MCP       `json:"mcp,omitempty"`
	// MCPExcluded records the reviewed decision (x-kombify-mcp: false) that
	// the operation is not an agent tool.
	MCPExcluded bool `json:"mcpExcluded,omitempty"`
}

// Argument locations.
const (
	InPath   = "path"
	InQuery  = "query"
	InHeader = "header"
	InBody   = "body"
)

// Body modes.
const (
	BodyProperties = "properties"
	BodyJSON       = "json"
	BodyFile       = "file"
)

// Argument is one caller-supplied input. Name is the surface name shared by
// CLI flags and MCP tool properties; WireName is the HTTP name.
type Argument struct {
	Name        string         `json:"name"`
	In          string         `json:"in"`
	WireName    string         `json:"wireName,omitempty"`
	Required    bool           `json:"required,omitempty"`
	Description string         `json:"description,omitempty"`
	Schema      map[string]any `json:"schema,omitempty"`
}

// Body describes the request body.
type Body struct {
	ContentType string `json:"contentType"`
	Required    bool   `json:"required,omitempty"`
	Mode        string `json:"mode"`
}

// Output describes the first successful JSON response. Schema is the payload
// schema after Envelope has been unwrapped.
type Output struct {
	ContentType string         `json:"contentType"`
	Envelope    string         `json:"envelope,omitempty"`
	Schema      map[string]any `json:"schema,omitempty"`
}

// CLI binds the operation to a command path.
type CLI struct {
	Command []string `json:"command"`
	Aliases []string `json:"aliases,omitempty"`
	Args    []string `json:"args,omitempty"`
	Hidden  bool     `json:"hidden,omitempty"`
}

// MCP binds the operation to an MCP tool.
type MCP struct {
	ToolName           string      `json:"toolName"`
	Title              string      `json:"title,omitempty"`
	RequiredCapability string      `json:"requiredCapability"`
	Annotations        Annotations `json:"annotations"`
	// CostBearing marks a tool whose call charges the user.
	CostBearing bool `json:"costBearing,omitempty"`
	// ActionClass is an explicit Gateway action class; empty means derived
	// (see ActionClassFor).
	ActionClass string `json:"actionClass,omitempty"`
	// ResourceBinding scopes authorization to the resource one argument
	// names (explicit tools only).
	ResourceBinding *ResourceBinding `json:"resourceBinding,omitempty"`
	// Derived marks a tool built from x-kombify-surface.mcp.defaults rather
	// than an explicit x-kombify-mcp object.
	Derived bool `json:"derived,omitempty"`
}

// ResourceBinding names the surface argument that identifies the resource a
// call acts on and the authorization dimension it belongs to.
type ResourceBinding struct {
	Argument  string `json:"argument"`
	Dimension string `json:"dimension"`
}

// Annotations are the MCP tool behavior hints.
type Annotations struct {
	ReadOnlyHint    bool `json:"readOnlyHint"`
	DestructiveHint bool `json:"destructiveHint"`
	IdempotentHint  bool `json:"idempotentHint"`
	OpenWorldHint   bool `json:"openWorldHint"`
}

// Marshal encodes the surface deterministically: 2-space indent, trailing
// newline, no HTML escaping.
func (s *Surface) Marshal() ([]byte, error) { return encode(s) }

// Parse decodes an api-surface.json artifact, typically embedded by a product
// binary.
func Parse(data []byte) (*Surface, error) {
	var s Surface
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("apisurface: decode surface: %w", err)
	}
	if s.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("apisurface: unsupported schemaVersion %q (want %q)", s.SchemaVersion, SchemaVersion)
	}
	return &s, nil
}

func encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
