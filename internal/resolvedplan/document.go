package resolvedplan

import (
	"encoding/json"
	"fmt"
	"strconv"
)

func asObject(value any, path string) (map[string]any, error) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, nil
	case KitDefinition:
		return map[string]any(typed), nil
	case StackSpecV2:
		return map[string]any(typed), nil
	case InventoryFacts:
		return map[string]any(typed), nil
	case CapabilityContract:
		return map[string]any(typed), nil
	case CapabilityProvider:
		return map[string]any(typed), nil
	case AddOnContract:
		return map[string]any(typed), nil
	case ModuleContract:
		return map[string]any(typed), nil
	case PlanArtifactContract:
		return map[string]any(typed), nil
	case ResolvedPlan:
		return map[string]any(typed), nil
	default:
		return nil, fail(ErrInvalidInput, path, "expected object, got %T", value)
	}
}

func objectField(object map[string]any, path, name string) (map[string]any, error) {
	value, ok := object[name]
	if !ok {
		return nil, fail(ErrInvalidInput, joinPath(path, name), "required object is missing")
	}
	return asObject(value, joinPath(path, name))
}

func optionalObjectField(object map[string]any, path, name string) (map[string]any, bool, error) {
	value, ok := object[name]
	if !ok || value == nil {
		return nil, false, nil
	}
	result, err := asObject(value, joinPath(path, name))
	return result, true, err
}

func objectListField(object map[string]any, path, name string) ([]map[string]any, error) {
	value, ok := object[name]
	if !ok {
		return nil, fail(ErrInvalidInput, joinPath(path, name), "required list is missing")
	}
	values, ok := value.([]any)
	if !ok {
		return nil, fail(ErrInvalidInput, joinPath(path, name), "expected list, got %T", value)
	}
	result := make([]map[string]any, len(values))
	for i := range values {
		item, err := asObject(values[i], fmt.Sprintf("%s[%d]", joinPath(path, name), i))
		if err != nil {
			return nil, err
		}
		result[i] = item
	}
	return result, nil
}

func stringField(object map[string]any, path, name string) (string, error) {
	value, ok := object[name]
	if !ok {
		return "", fail(ErrInvalidInput, joinPath(path, name), "required string is missing")
	}
	text, ok := value.(string)
	if !ok || text == "" {
		return "", fail(ErrInvalidInput, joinPath(path, name), "expected non-empty string")
	}
	return text, nil
}

func optionalStringField(object map[string]any, path, name string) (string, bool, error) {
	value, ok := object[name]
	if !ok || value == nil {
		return "", false, nil
	}
	text, ok := value.(string)
	if !ok || text == "" {
		return "", false, fail(ErrInvalidInput, joinPath(path, name), "expected non-empty string")
	}
	return text, true, nil
}

func stringListField(object map[string]any, path, name string, required bool) ([]string, error) {
	value, ok := object[name]
	if !ok {
		if required {
			return nil, fail(ErrInvalidInput, joinPath(path, name), "required list is missing")
		}
		return nil, nil
	}
	values, ok := value.([]any)
	if !ok {
		return nil, fail(ErrInvalidInput, joinPath(path, name), "expected list, got %T", value)
	}
	result := make([]string, len(values))
	for i, value := range values {
		text, ok := value.(string)
		if !ok || text == "" {
			return nil, fail(ErrInvalidInput, fmt.Sprintf("%s[%d]", joinPath(path, name), i), "expected non-empty string")
		}
		result[i] = text
	}
	return result, nil
}

func boolFieldDefault(object map[string]any, path, name string, fallback bool) (bool, error) {
	value, ok := object[name]
	if !ok {
		return fallback, nil
	}
	boolean, ok := value.(bool)
	if !ok {
		return false, fail(ErrInvalidInput, joinPath(path, name), "expected boolean")
	}
	return boolean, nil
}

func intField(object map[string]any, path, name string) (int, error) {
	value, ok := object[name]
	if !ok {
		return 0, fail(ErrInvalidInput, joinPath(path, name), "required integer is missing")
	}
	switch number := value.(type) {
	case json.Number:
		parsed, err := strconv.Atoi(number.String())
		if err != nil {
			return 0, fail(ErrInvalidInput, joinPath(path, name), "expected integer")
		}
		return parsed, nil
	case float64:
		parsed := int(number)
		if float64(parsed) != number {
			return 0, fail(ErrInvalidInput, joinPath(path, name), "expected integer")
		}
		return parsed, nil
	case int:
		return number, nil
	default:
		return 0, fail(ErrInvalidInput, joinPath(path, name), "expected integer, got %T", value)
	}
}

func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func cloneObject(object map[string]any, redactSecrets bool) (map[string]any, error) {
	normalized, err := normalizeJSON(object, redactSecrets, "")
	if err != nil {
		return nil, err
	}
	return asObject(normalized, "clone")
}
