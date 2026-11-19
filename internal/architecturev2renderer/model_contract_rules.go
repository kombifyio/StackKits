package architecturev2renderer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
)

func validateUniqueEnumList(values, allowed []string, valuePath string) error {
	if len(values) == 0 {
		return fail(ErrInvalidPlan, valuePath, "must contain at least one value")
	}
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		itemPath := fmt.Sprintf("%s[%d]", valuePath, index)
		if !oneOf(value, allowed...) {
			return fail(ErrInvalidPlan, itemPath, "unsupported value %q", value)
		}
		if _, duplicate := seen[value]; duplicate {
			return fail(ErrDuplicate, itemPath, "duplicate value %q", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func requireObjectKeysSubset(values map[string]json.RawMessage, expected map[string]struct{}, valuePath string) error {
	return requireObjectKeysSubsetAllowingNull(values, expected, nil, valuePath)
}

func requireObjectKeysSubsetAllowingNull(
	values map[string]json.RawMessage,
	expected map[string]struct{},
	nullable map[string]struct{},
	valuePath string,
) error {
	for key, raw := range values {
		if _, exists := expected[key]; !exists {
			return fail(ErrInvalidPlan, valuePath+"."+key, "input is not declared")
		}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 {
			return fail(ErrInvalidPlan, valuePath+"."+key, "input value must not be null")
		}
		if bytes.Equal(trimmed, []byte("null")) {
			if _, allowed := nullable[key]; !allowed {
				return fail(ErrInvalidPlan, valuePath+"."+key, "input value must not be null")
			}
		}
	}
	return nil
}

func requireOpaqueSecretReferences(values map[string]json.RawMessage, valuePath string) error {
	for key, raw := range values {
		var reference string
		if err := decodeJSON(raw, &reference, false); err != nil || !validSecretReference(reference) {
			if err == nil {
				err = fmt.Errorf("must be an opaque secret reference URI")
			}
			return wrap(ErrInvalidPlan, valuePath+"."+key, "secret material must remain an opaque reference", err)
		}
	}
	return nil
}

func validSecretReference(value string) bool {
	for _, prefix := range []string{"secret://", "vault://", "doppler://", "homelab://"} {
		if strings.HasPrefix(value, prefix) && len(value) > len(prefix) && !strings.ContainsAny(value, " \t\r\n") {
			return true
		}
	}
	return false
}

func uniqueContractIDSet(values []string, valuePath string) (map[string]struct{}, error) {
	result := make(map[string]struct{}, len(values))
	for index, value := range values {
		key := value
		if _, exists := result[key]; exists {
			return nil, fail(ErrDuplicate, fmt.Sprintf("%s[%d]", valuePath, index), "duplicate reference %q", value)
		}
		result[key] = struct{}{}
	}
	return result, nil
}

func decodeRawObject(data []byte, valuePath string) (map[string]json.RawMessage, error) {
	var result map[string]json.RawMessage
	if err := decodeJSON(data, &result, false); err != nil {
		return nil, wrap(ErrInvalidPlan, valuePath, "decode object", err)
	}
	if result == nil {
		return nil, fail(ErrInvalidPlan, valuePath, "must be an object")
	}
	return result, nil
}

func requiredRawObject(object map[string]json.RawMessage, name, valuePath string) (map[string]json.RawMessage, error) {
	raw, exists := object[name]
	if !exists {
		return nil, fail(ErrInvalidPlan, valuePath, "is required")
	}
	return decodeRawObject(raw, valuePath)
}

func requiredRawArray(object map[string]json.RawMessage, name, valuePath string) ([]json.RawMessage, error) {
	raw, exists := object[name]
	if !exists {
		return nil, fail(ErrInvalidPlan, valuePath, "is required")
	}
	var result []json.RawMessage
	if err := decodeJSON(raw, &result, false); err != nil || result == nil {
		if err == nil {
			err = fmt.Errorf("must be an array")
		}
		return nil, wrap(ErrInvalidPlan, valuePath, "decode array", err)
	}
	return result, nil
}

func requiredRawString(object map[string]json.RawMessage, name, valuePath string) (string, error) {
	raw, exists := object[name]
	if !exists {
		return "", fail(ErrInvalidPlan, valuePath, "is required")
	}
	var result string
	if err := decodeJSON(raw, &result, false); err != nil || strings.TrimSpace(result) == "" {
		if err == nil {
			err = fmt.Errorf("must be a non-empty string")
		}
		return "", wrap(ErrInvalidPlan, valuePath, "decode string", err)
	}
	return result, nil
}

func optionalRawBool(object map[string]json.RawMessage, name, valuePath string) (bool, error) {
	raw, exists := object[name]
	if !exists {
		return false, nil
	}
	var result bool
	if err := decodeJSON(raw, &result, false); err != nil {
		return false, wrap(ErrInvalidPlan, valuePath, "decode boolean", err)
	}
	return result, nil
}

func requiredStringArray(object map[string]json.RawMessage, name, valuePath string) ([]string, error) {
	raw, exists := object[name]
	if !exists {
		return nil, fail(ErrInvalidPlan, valuePath, "is required")
	}
	var result []string
	if err := decodeJSON(raw, &result, false); err != nil || result == nil {
		if err == nil {
			err = fmt.Errorf("must be an array")
		}
		return nil, wrap(ErrInvalidPlan, valuePath, "decode string array", err)
	}
	return result, nil
}

func decodeStrict(data []byte, target any) error { return decodeJSON(data, target, true) }

func decodeJSON(data []byte, target any, strict bool) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return fmt.Errorf("multiple JSON values")
	} else if err != io.EOF {
		return fmt.Errorf("invalid trailing JSON: %w", err)
	}
	return nil
}

func canonicalObject(value map[string]json.RawMessage) ([]byte, error) {
	// encoding/json sorts string map keys and RawMessage preserves the already
	// canonical scalar/object representation carried by VerifiedPlan.
	return json.Marshal(value)
}

func requireContractID(value, valuePath string) error {
	if !contractIDPattern.MatchString(value) {
		return fail(ErrInvalidPlan, valuePath, "must be a non-empty portable contract ID")
	}
	return nil
}

func joinOutputPath(outputRoot, output string) string {
	if outputRoot == "." {
		return output
	}
	return path.Join(outputRoot, output)
}

func validatePortablePath(value string) (string, error) {
	if value == "" || strings.ContainsRune(value, '\x00') || strings.Contains(value, `\`) || strings.ContainsAny(value, `<>:"|?*`) {
		return "", fmt.Errorf("must be a non-empty portable slash-separated relative path")
	}
	if strings.HasPrefix(value, "/") || (len(value) >= 2 && value[1] == ':') {
		return "", fmt.Errorf("absolute, drive-relative, and UNC paths are forbidden")
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != value {
		return "", fmt.Errorf("path must be canonical and remain beneath its root")
	}
	for _, segment := range strings.Split(clean, "/") {
		if strings.TrimRight(segment, ". ") != segment || windowsReservedSegment(segment) {
			return "", fmt.Errorf("path is not portable to Windows")
		}
	}
	return clean, nil
}

func windowsReservedSegment(segment string) bool {
	base := strings.ToUpper(strings.SplitN(segment, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
}

func portableKey(value string) string {
	// Architecture v2 output paths are portable contracts, not host-native
	// paths. Fold case on every host so one canonical plan cannot be accepted
	// on a case-sensitive builder and rejected later on Windows.
	return strings.ToLower(value)
}

func validSHA256(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}
