package apisurface

import (
	"strings"
)

// Keywords whose values are schemas, maps of schemas or arrays of schemas.
// Everything else is copied verbatim (enum, const, default, examples...).
var (
	schemaKeywords = map[string]bool{
		"items": true, "additionalProperties": true, "not": true, "if": true,
		"then": true, "else": true, "contains": true, "propertyNames": true,
		"unevaluatedItems": true, "unevaluatedProperties": true,
		"additionalItems": true, "contentSchema": true,
	}
	schemaMapKeywords = map[string]bool{
		"properties": true, "patternProperties": true, "$defs": true,
		"definitions": true, "dependentSchemas": true,
	}
	schemaListKeywords = map[string]bool{
		"allOf": true, "anyOf": true, "oneOf": true, "prefixItems": true,
	}
	// Non-JSON-Schema keys stripped from emitted schemas; all x-* keys too.
	strippedKeywords = map[string]bool{
		"example": true, "xml": true, "discriminator": true, "externalDocs": true,
	}
)

// schemaEmitter turns one OpenAPI schema into a self-contained JSON Schema:
// non-recursive $refs are inlined, recursive ones point into a root $defs map.
type schemaEmitter struct {
	doc   *document
	stack []string
	defs  map[string]any
	err   error
}

// emitSchema returns a self-contained copy of schema, or nil when schema is
// not an object.
func (d *document) emitSchema(schema any) (map[string]any, error) {
	e := &schemaEmitter{doc: d, defs: map[string]any{}}
	out, ok := e.emit(schema).(map[string]any)
	if e.err != nil {
		return nil, e.err
	}
	if !ok {
		return nil, nil
	}
	if len(e.defs) > 0 {
		out["$defs"] = e.defs
	}
	return out, nil
}

func (e *schemaEmitter) emit(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v // boolean schemas
	}
	if ref, ok := m["$ref"].(string); ok {
		return e.emitRef(ref, m)
	}
	out := make(map[string]any, len(m))
	for k, val := range m {
		if strings.HasPrefix(k, "x-") || strippedKeywords[k] {
			continue
		}
		out[k] = e.emitKeyword(k, val)
	}
	return out
}

func (e *schemaEmitter) emitKeyword(k string, val any) any {
	switch {
	case schemaKeywords[k]:
		if list, ok := val.([]any); ok { // legacy tuple items
			return e.emitList(list)
		}
		return e.emit(val)
	case schemaMapKeywords[k]:
		src, ok := val.(map[string]any)
		if !ok {
			return val
		}
		dst := make(map[string]any, len(src))
		for name, sub := range src {
			dst[name] = e.emit(sub)
		}
		return dst
	case schemaListKeywords[k]:
		if list, ok := val.([]any); ok {
			return e.emitList(list)
		}
		return val
	default:
		return deepCopy(val)
	}
}

func (e *schemaEmitter) emitList(list []any) []any {
	out := make([]any, len(list))
	for i, sub := range list {
		out[i] = e.emit(sub)
	}
	return out
}

// emitRef inlines ref unless it is already being expanded (recursion), in
// which case it becomes a #/$defs pointer. Sibling keywords override the
// referenced schema's keywords.
func (e *schemaEmitter) emitRef(ref string, m map[string]any) any {
	name := ref[strings.LastIndex(ref, "/")+1:]
	siblings := make(map[string]any, len(m))
	for k, v := range m {
		if k != "$ref" {
			siblings[k] = v
		}
	}
	for _, active := range e.stack {
		if active == ref {
			e.define(ref, name)
			ptr := e.emit(siblings).(map[string]any)
			ptr["$ref"] = "#/$defs/" + name
			return ptr
		}
	}
	target, err := e.doc.pointer(ref)
	if err != nil {
		if e.err == nil {
			e.err = err
		}
		return map[string]any{}
	}
	e.stack = append(e.stack, ref)
	defer func() { e.stack = e.stack[:len(e.stack)-1] }()
	out := e.emit(target)
	if len(siblings) == 0 {
		return out
	}
	merged, ok := out.(map[string]any)
	if !ok {
		return out
	}
	for k, v := range e.emit(siblings).(map[string]any) {
		merged[k] = v
	}
	return merged
}

// define emits the recursive target once into $defs.
func (e *schemaEmitter) define(ref, name string) {
	if _, done := e.defs[name]; done {
		return
	}
	e.defs[name] = map[string]any{} // placeholder breaks the cycle
	target, err := e.doc.pointer(ref)
	if err != nil {
		if e.err == nil {
			e.err = err
		}
		return
	}
	e.defs[name] = e.emit(target)
}

func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = deepCopy(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopy(e)
		}
		return out
	default:
		return v
	}
}
