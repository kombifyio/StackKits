package apisurface

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// PublicOpenAPI projects a spec for published API reference documentation:
// operations marked x-kombify-internal: true are removed (path items left
// without operations are dropped, as are aliases of dropped paths) and every
// x-kombify-* key is stripped at every level, as are YAML comments.
// Everything else is kept; YAML output keeps the source key order, JSON
// output sorts keys.
func PublicOpenAPI(spec []byte, asYAML bool) ([]byte, error) {
	if _, err := loadDocument(spec); err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(spec, &doc); err != nil {
		return nil, fmt.Errorf("apisurface: parse spec: %w", err)
	}
	root := doc.Content[0]
	if paths := mappingValue(root, "paths"); paths != nil {
		dropInternalOperations(paths)
	}
	stripKombifyKeys(&doc)
	if asYAML {
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(&doc); err != nil {
			return nil, fmt.Errorf("apisurface: encode public spec: %w", err)
		}
		if err := enc.Close(); err != nil {
			return nil, fmt.Errorf("apisurface: encode public spec: %w", err)
		}
		return buf.Bytes(), nil
	}
	var v any
	if err := doc.Decode(&v); err != nil {
		return nil, fmt.Errorf("apisurface: decode public spec: %w", err)
	}
	return encode(normalize(v))
}

// dropInternalOperations removes internal operations from the paths mapping.
func dropInternalOperations(paths *yaml.Node) {
	dropped := map[string]bool{}
	var kept []*yaml.Node
	for i := 0; i+1 < len(paths.Content); i += 2 {
		key, item := paths.Content[i], paths.Content[i+1]
		if item.Kind == yaml.MappingNode && removeInternal(item) {
			dropped[key.Value] = true
			continue
		}
		kept = append(kept, key, item)
	}
	// A path item that $refs a dropped path would dangle.
	paths.Content = kept[:0:0]
	for i := 0; i+1 < len(kept); i += 2 {
		if ref := mappingValue(kept[i+1], "$ref"); ref != nil && strings.HasPrefix(ref.Value, "#/paths/") {
			target := strings.ReplaceAll(strings.ReplaceAll(strings.TrimPrefix(ref.Value, "#/paths/"), "~1", "/"), "~0", "~")
			if dropped[target] {
				continue
			}
		}
		paths.Content = append(paths.Content, kept[i], kept[i+1])
	}
}

// removeInternal deletes internal operations from a path item and reports
// whether the item lost all its operations to that.
func removeInternal(item *yaml.Node) bool {
	removed, remaining := false, false
	content := item.Content[:0:0]
	for i := 0; i+1 < len(item.Content); i += 2 {
		key, val := item.Content[i], item.Content[i+1]
		if slices.Contains(methods, key.Value) {
			if isTrue(mappingValue(val, "x-kombify-internal")) {
				removed = true
				continue
			}
			remaining = true
		}
		content = append(content, key, val)
	}
	item.Content = content
	return removed && !remaining
}

// stripKombifyKeys removes x-kombify-* keys and source comments; comments
// are authoring notes (JSON output has none either).
func stripKombifyKeys(n *yaml.Node) {
	n.HeadComment, n.LineComment, n.FootComment = "", "", ""
	switch n.Kind {
	case yaml.MappingNode:
		content := n.Content[:0:0]
		for i := 0; i+1 < len(n.Content); i += 2 {
			if strings.HasPrefix(n.Content[i].Value, "x-kombify-") {
				continue
			}
			stripKombifyKeys(n.Content[i])
			stripKombifyKeys(n.Content[i+1])
			content = append(content, n.Content[i], n.Content[i+1])
		}
		n.Content = content
	case yaml.SequenceNode, yaml.DocumentNode:
		for _, c := range n.Content {
			stripKombifyKeys(c)
		}
	}
}

func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func isTrue(n *yaml.Node) bool {
	var b bool
	return n != nil && n.Decode(&b) == nil && b
}
