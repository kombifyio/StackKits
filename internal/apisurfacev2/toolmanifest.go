package apisurface

import (
	"sort"
	"strings"
)

const typeObject = "object"

// ToolManifestSchemaVersion identifies the product-native MCP tool manifest.
const ToolManifestSchemaVersion = "kombify.tool-manifest/v1"

// ToolManifest is the product-level MCP discovery document consumed by the
// product's native MCP server, discovery and Gateway validation.
type ToolManifest struct {
	SchemaVersion string           `json:"schema_version"`
	Product       string           `json:"product"`
	Capability    string           `json:"capability,omitempty"`
	MCP           ToolManifestMCP  `json:"mcp"`
	Tools         []ToolDefinition `json:"tools"`
}

// ToolManifestMCP identifies the transport endpoint and protocol version.
type ToolManifestMCP struct {
	Transport       string `json:"transport"`
	Endpoint        string `json:"endpoint"`
	ProtocolVersion string `json:"protocol_version"`
}

// ToolDefinition binds one operation to HTTP and MCP.
type ToolDefinition struct {
	Name               string         `json:"name"`
	Title              string         `json:"title,omitempty"`
	Description        string         `json:"description,omitempty"`
	RequiredCapability string         `json:"requiredCapability"`
	OperationID        string         `json:"operationId"`
	HTTP               HTTPBinding    `json:"http"`
	InputSchema        map[string]any `json:"inputSchema"`
	OutputSchema       map[string]any `json:"outputSchema,omitempty"`
	Annotations        Annotations    `json:"annotations"`
}

// HTTPBinding names the REST operation backing a tool.
type HTTPBinding struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// ToolManifest projects every MCP-exposed operation into the tool manifest,
// tools sorted by name. Output schemas are kept only for object payloads.
func (s *Surface) ToolManifest() *ToolManifest {
	m := &ToolManifest{
		SchemaVersion: ToolManifestSchemaVersion,
		Product:       s.Product,
		Capability:    s.Capability,
		Tools:         []ToolDefinition{},
	}
	if s.MCP != nil {
		m.MCP = ToolManifestMCP{Transport: s.MCP.Transport, Endpoint: s.MCP.Endpoint, ProtocolVersion: s.MCP.ProtocolVersion}
	}
	for _, op := range s.Operations {
		if op.MCP == nil {
			continue
		}
		tool := ToolDefinition{
			Name:               op.MCP.ToolName,
			Title:              op.MCP.Title,
			Description:        strings.Join(strings.Fields(op.Description), " "),
			RequiredCapability: op.MCP.RequiredCapability,
			OperationID:        op.OperationID,
			HTTP:               HTTPBinding{Method: op.Method, Path: op.Path},
			InputSchema:        inputSchema(op.Arguments),
			Annotations:        op.MCP.Annotations,
		}
		if tool.Title == "" {
			tool.Title = op.Summary
		}
		if tool.Description == "" {
			tool.Description = op.Summary
		}
		if op.Output != nil {
			tool.OutputSchema = objectSchema(op.Output.Schema)
		}
		m.Tools = append(m.Tools, tool)
	}
	sort.Slice(m.Tools, func(i, j int) bool { return m.Tools[i].Name < m.Tools[j].Name })
	return m
}

// Marshal encodes the manifest deterministically: 2-space indent, trailing
// newline, no HTML escaping.
func (m *ToolManifest) Marshal() ([]byte, error) { return encode(m) }

func inputSchema(args []Argument) map[string]any {
	props := map[string]any{}
	defs := map[string]any{}
	var required []any
	for _, a := range args {
		prop := map[string]any{}
		for k, v := range a.Schema {
			prop[k] = v
		}
		// Recursive definitions resolve against the document root, so they
		// move from the argument schema to the input schema.
		if d, ok := prop["$defs"].(map[string]any); ok {
			for name, def := range d {
				defs[name] = def
			}
			delete(prop, "$defs")
		}
		if _, has := prop["description"]; !has && a.Description != "" {
			prop["description"] = a.Description
		}
		props[a.Name] = prop
		if a.Required {
			required = append(required, a.Name)
		}
	}
	schema := map[string]any{"type": typeObject, "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	if len(defs) > 0 {
		schema["$defs"] = defs
	}
	return schema
}

// objectSchema returns schema when it describes an object, as MCP structured
// content requires; an allOf of objects gains an explicit object type.
func objectSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	if t, has := schema["type"]; has {
		if t == typeObject {
			return schema
		}
		return nil
	}
	members, _ := schema["allOf"].([]any)
	if len(members) == 0 {
		return nil
	}
	for _, m := range members {
		if sub, _ := m.(map[string]any); sub["type"] != typeObject {
			return nil
		}
	}
	out := make(map[string]any, len(schema)+1)
	for k, v := range schema {
		out[k] = v
	}
	out["type"] = typeObject
	return out
}
