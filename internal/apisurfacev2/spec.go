package apisurface

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// document is a loaded OpenAPI document as generic JSON-compatible values.
type document struct {
	root map[string]any
}

// loadDocument decodes YAML or JSON (JSON is valid YAML) into generic values
// with string map keys.
func loadDocument(data []byte) (*document, error) {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("apisurface: parse spec: %w", err)
	}
	root, ok := normalize(raw).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("apisurface: spec root is not an object")
	}
	if v, _ := root["openapi"].(string); !strings.HasPrefix(v, "3.1") {
		return nil, fmt.Errorf("apisurface: spec declares openapi %q; only 3.1 is supported", v)
	}
	return &document{root: root}, nil
}

// normalize converts YAML decoding artifacts (map[any]any, non-string keys)
// into JSON-compatible values.
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			t[k] = normalize(e)
		}
		return t
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[fmt.Sprint(k)] = normalize(e)
		}
		return out
	case []any:
		for i, e := range t {
			t[i] = normalize(e)
		}
		return t
	default:
		return v
	}
}

// pointer resolves a local JSON pointer ("#/components/schemas/X").
func (d *document) pointer(ref string) (any, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("unsupported $ref %q: only local references are resolved", ref)
	}
	var cur any = d.root
	for _, seg := range strings.Split(ref[2:], "/") {
		seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
		switch c := cur.(type) {
		case map[string]any:
			next, ok := c[seg]
			if !ok {
				return nil, fmt.Errorf("unresolved $ref %q", ref)
			}
			cur = next
		case []any:
			var idx int
			if _, err := fmt.Sscanf(seg, "%d", &idx); err != nil || idx < 0 || idx >= len(c) {
				return nil, fmt.Errorf("unresolved $ref %q", ref)
			}
			cur = c[idx]
		default:
			return nil, fmt.Errorf("unresolved $ref %q", ref)
		}
	}
	return cur, nil
}

// deref follows a chain of $ref objects (parameters, request bodies,
// responses, schemas) until it reaches a non-reference object.
func (d *document) deref(v any) (map[string]any, error) {
	seen := map[string]bool{}
	for {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, nil
		}
		ref, ok := m["$ref"].(string)
		if !ok {
			return m, nil
		}
		if seen[ref] {
			return nil, fmt.Errorf("circular $ref %q", ref)
		}
		seen[ref] = true
		next, err := d.pointer(ref)
		if err != nil {
			return nil, err
		}
		v = next
	}
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func obj(m map[string]any, key string) map[string]any {
	o, _ := m[key].(map[string]any)
	return o
}
