package architecturev2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"gopkg.in/yaml.v3"
)

// decodeYAMLObject retains every field in a JSON-shaped map; it never decodes
// through a partial Go StackSpec struct. CUE is therefore the only authority
// that may accept, default, or reject v2 fields.
func decodeYAMLObject(data []byte, documentName string) (map[string]any, error) {
	var root yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("decode %s: %w", documentName, err)
	}
	if len(root.Content) != 1 {
		return nil, fmt.Errorf("%s must contain exactly one root value", documentName)
	}
	if err := rejectDuplicateOrNonStringKeys(root.Content[0], documentName); err != nil {
		return nil, err
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("%s contains multiple YAML documents", documentName)
		}
		return nil, fmt.Errorf("decode trailing %s data: %w", documentName, err)
	}

	var generic any
	if err := root.Content[0].Decode(&generic); err != nil {
		return nil, fmt.Errorf("decode %s values: %w", documentName, err)
	}
	jsonData, err := json.Marshal(generic)
	if err != nil {
		return nil, fmt.Errorf("normalize %s as JSON: %w", documentName, err)
	}
	return resolvedplan.DecodeDocument[map[string]any](jsonData)
}

func rejectDuplicateOrNonStringKeys(node *yaml.Node, path string) error {
	if node == nil {
		return fmt.Errorf("%s is empty", path)
	}
	switch node.Kind {
	case yaml.MappingNode:
		seen := make(map[string]struct{}, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return fmt.Errorf("%s contains a non-string mapping key", path)
			}
			if _, exists := seen[key.Value]; exists {
				return fmt.Errorf("%s contains duplicate field %q", path, key.Value)
			}
			seen[key.Value] = struct{}{}
			if err := rejectDuplicateOrNonStringKeys(value, path+"."+key.Value); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for index, child := range node.Content {
			if err := rejectDuplicateOrNonStringKeys(child, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	case yaml.AliasNode:
		// Aliases can make duplicate-field and source-lineage diagnostics depend
		// on parser expansion. Canonical StackSpec/Inventory documents are
		// deliberately explicit instead.
		return fmt.Errorf("%s uses a YAML alias; canonical architecture documents must be explicit", path)
	case yaml.ScalarNode:
		return nil
	default:
		return fmt.Errorf("%s has unsupported YAML node kind %d", path, node.Kind)
	}
	return nil
}

func bytesReader(data []byte) *bytes.Reader {
	return bytes.NewReader(data)
}
