package kitio

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// Import parses stackkit.yaml bytes into a KitDefinition.
//
// Two-pass approach:
//  1. yaml.Unmarshal into KitDefinition struct (typed sections).
//  2. Re-parse as generic map to preserve unknown keys (Outputs, Pattern etc.
//     that the struct surfaces as map[string]interface{}).
func Import(yamlBytes []byte) (KitDefinition, error) {
	var def KitDefinition
	if err := yaml.Unmarshal(yamlBytes, &def); err != nil {
		return KitDefinition{}, fmt.Errorf("yaml.Unmarshal kitdef: %w", err)
	}

	// Capture extra/free-form sections via a generic decode. yaml.v3 already
	// gave us map fields, but we double-decode here so unexpected sections
	// land in metadata extension when needed.
	var generic map[string]interface{}
	if err := yaml.Unmarshal(yamlBytes, &generic); err != nil {
		return KitDefinition{}, fmt.Errorf("yaml.Unmarshal generic: %w", err)
	}
	generic = normalizeYAMLMaps(generic)

	if def.Outputs == nil {
		if v, ok := generic["outputs"].(map[string]interface{}); ok {
			def.Outputs = v
		}
	}
	if def.Pattern == nil {
		if v, ok := generic["pattern"].(map[string]interface{}); ok {
			def.Pattern = v
		}
	}
	if def.PaaS == nil {
		if v, ok := generic["paas"].(map[string]interface{}); ok {
			def.PaaS = v
		}
	}
	if def.Secrets == nil {
		if v, ok := generic["secrets"].(map[string]interface{}); ok {
			def.Secrets = v
		}
	}
	if def.Swarm == nil {
		if v, ok := generic["swarm"].(map[string]interface{}); ok {
			def.Swarm = v
		}
	}
	if def.Identity == nil {
		if v, ok := generic["identity"].(map[string]interface{}); ok {
			def.Identity = v
		}
	}
	if def.Requirements == nil {
		if v, ok := generic["requirements"].(map[string]interface{}); ok {
			def.Requirements = v
		}
	}
	if def.AutoSelect == nil {
		if v, ok := generic["autoSelect"].(map[string]interface{}); ok {
			def.AutoSelect = v
		}
	}

	return def, nil
}

// CanonicalHash returns sha256(canonical-json(def)) — same algorithm as the
// kit-import endpoint server-side. Used for drift detection + golden tests.
func CanonicalHash(def KitDefinition) (string, error) {
	canonical, err := canonicalJSON(def)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// canonicalJSON marshals def in deterministic form: map keys sorted,
// no whitespace. Matches the TS endpoint's `canonicalize`.
func canonicalJSON(v interface{}) ([]byte, error) {
	// Trick: round-trip through json + sortDeep
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var generic interface{}
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, err
	}
	return json.Marshal(sortDeep(generic))
}

func sortDeep(v interface{}) interface{} {
	switch vv := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(vv))
		for k := range vv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]interface{}, len(vv))
		for _, k := range keys {
			out[k] = sortDeep(vv[k])
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(vv))
		for i, x := range vv {
			out[i] = sortDeep(x)
		}
		return out
	default:
		return v
	}
}

// normalizeYAMLMaps turns map[interface{}]interface{} (yaml.v2 legacy form
// occasionally produced for nested maps) into map[string]interface{}.
// yaml.v3 mostly returns string-keyed maps, but this guards array-of-maps
// edge cases.
func normalizeYAMLMaps(in interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	switch m := in.(type) {
	case map[string]interface{}:
		for k, v := range m {
			out[k] = normalizeValue(v)
		}
	case map[interface{}]interface{}:
		for k, v := range m {
			ks, ok := k.(string)
			if !ok {
				ks = fmt.Sprintf("%v", k)
			}
			out[ks] = normalizeValue(v)
		}
	}
	return out
}

func normalizeValue(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}, map[interface{}]interface{}:
		return normalizeYAMLMaps(t)
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, x := range t {
			out[i] = normalizeValue(x)
		}
		return out
	default:
		return v
	}
}
