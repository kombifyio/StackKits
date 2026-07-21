package resolvedplan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

const redactedValue = "<redacted>"

var (
	secretRefPattern       = regexp.MustCompile(`^(secret|vault|doppler|techstack)://.+$`)
	canonicalSHA256Pattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	compactKeyReplacer     = strings.NewReplacer("_", "", "-", "")
	setSemanticKeys        = map[string]struct{}{
		"access": {}, "actionAllowlist": {}, "addons": {}, "allowedAuthorityKinds": {},
		"allowedExposures": {}, "allowedFlows": {}, "allowedMethods": {}, "allowedModes": {}, "allowedOriginKinds": {}, "allowedSiteKinds": {},
		"allowedStrategies": {}, "allowedTargets": {}, "artifactRefs": {}, "artifacts": {},
		"authorityKinds": {}, "blockers": {}, "capabilities": {}, "compatibleRendererRefs": {}, "components": {}, "conflicts": {}, "dataClasses": {},
		"backendPools": {}, "defaults": {}, "defaultForSiteKinds": {}, "disable": {}, "edgeKinds": {},
		"enable": {}, "evidence": {}, "evidenceGateRefs": {}, "evidenceScenarios": {},
		"daemonBindings": {}, "providesInterfaces": {}, "requiresInterfaces": {}, "providerBindings": {}, "privilegedInterfaceApprovals": {}, "scopes": {},
		"expectedStatuses": {}, "forbidden": {}, "health": {}, "healthGateRefs": {},
		"dependsOn": {}, "members": {}, "methods": {}, "moduleRefs": {}, "modules": {}, "networkFlows": {}, "networkRefs": {},
		"nodeRefs": {}, "nodes": {}, "optional": {}, "outputBindings": {}, "outputs": {}, "peerSiteRefs": {},
		"placement": {}, "privileges": {}, "providers": {}, "provides": {}, "publications": {},
		"refs": {}, "required": {}, "requiredCapabilities": {}, "requiredRealizations": {}, "requiredRefs": {}, "requiredSiteKinds": {}, "requires": {}, "roles": {}, "routes": {},
		"inputBindings": {}, "planInputRefs": {}, "publicInputRefs": {}, "renderUnits": {}, "secretInputRefs": {}, "secretInputs": {}, "secretRefs": {}, "serviceEndpoints": {}, "siteRefs": {}, "sites": {}, "sourceKinds": {},
		"supportedKits": {}, "supportedSiteKinds": {}, "volumes": {}, "warnings": {},
	}
)

func bytesReader(data []byte) io.Reader { return bytes.NewReader(data) }

func canonicalHash(value any, redactSecrets bool) (string, error) {
	data, err := canonicalJSON(value, redactSecrets)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func canonicalJSON(value any, redactSecrets bool) ([]byte, error) {
	normalized, err := normalizeJSON(value, redactSecrets, "")
	if err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

func normalizeJSON(value any, redactSecrets bool, key string) (any, error) {
	if redactSecrets && isSecretReferenceContainerKey(key) {
		return normalizeSecretReferenceContainer(value)
	}
	if redactSecrets && isSecretDeclarationKey(key) {
		// Declaration containers carry slot/source identity, not credential
		// material. Preserve their complete structure in the contract hash even
		// when a target key such as DB_PASSWORD is intentionally secret-shaped.
		return normalizeJSON(value, false, key)
	}
	if redactSecrets && isSecretKey(key) && !isSecretDeclarationKey(key) && !isNonSecretPolicyMetadataKey(key) {
		return normalizeSecretValue(value)
	}

	switch typed := value.(type) {
	case nil, bool, string, json.Number, float64, float32,
		int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return typed, nil
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for childKey := range typed {
			keys = append(keys, childKey)
		}
		sort.Strings(keys)
		result := make(map[string]any, len(typed))
		for _, childKey := range keys {
			child, err := normalizeJSON(typed[childKey], redactSecrets, childKey)
			if err != nil {
				return nil, err
			}
			result[childKey] = child
		}
		return result, nil
	case []any:
		result := make([]any, len(typed))
		for i := range typed {
			child, err := normalizeJSON(typed[i], redactSecrets, key)
			if err != nil {
				return nil, err
			}
			result[i] = child
		}
		if _, isSet := setSemanticKeys[key]; isSet {
			sort.SliceStable(result, func(i, j int) bool {
				left, _ := json.Marshal(result[i])
				right, _ := json.Marshal(result[j])
				return bytes.Compare(left, right) < 0
			})
		}
		return result, nil
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("canonical JSON marshal: %w", err)
		}
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.UseNumber()
		var generic any
		if err := decoder.Decode(&generic); err != nil {
			return nil, fmt.Errorf("canonical JSON decode: %w", err)
		}
		return normalizeJSON(generic, redactSecrets, key)
	}
}

func isSecretKey(key string) bool {
	compact := compactKey(key)
	if strings.Contains(compact, "secret") || strings.Contains(compact, "password") ||
		strings.Contains(compact, "passphrase") || strings.Contains(compact, "token") {
		return true
	}
	switch compact {
	case "apikey", "privatekey", "credential", "credentials", "credentialref", "credentialsref":
		return true
	default:
		return false
	}
}

func compactKey(key string) string {
	return compactKeyReplacer.Replace(strings.ToLower(key))
}

// Secret input declarations and component secretEnvironment maps contain
// governed slot/source identifiers, not secret material.
func isSecretDeclarationKey(key string) bool {
	switch compactKey(key) {
	case "secretinputs", "secretinputrefs", "secretinputbindings", "secretenvironment":
		return true
	default:
		return false
	}
}

// These security policy fields are not credentials. Their concrete values are
// part of intent and plan identity and must never be erased by the conservative
// key-name secret heuristic.
func isNonSecretPolicyMetadataKey(key string) bool {
	switch compactKey(key) {
	case "requireresolvedsecrets", "requiresresolvedsecrets", "credentialttlseconds", "cloudmayissuedevicecredentials", "implicitsecretsharing":
		return true
	default:
		return false
	}
}

// Explicit reference containers map caller-chosen input names to opaque
// SecretReference URIs. Their keys are declarations, not reference values.
func isSecretReferenceContainerKey(key string) bool {
	switch compactKey(key) {
	case "secretrefs", "credentialrefs":
		return true
	default:
		return false
	}
}

func normalizeSecretValue(value any) (any, error) {
	switch typed := value.(type) {
	case string:
		if secretRefPattern.MatchString(typed) {
			return typed, nil
		}
		return redactedValue, nil
	case []any:
		result := make([]any, len(typed))
		for i, child := range typed {
			normalized, err := normalizeSecretValue(child)
			if err != nil {
				return nil, err
			}
			result[i] = normalized
		}
		return result, nil
	case nil:
		return nil, nil
	default:
		// Objects and non-string scalar credentials cannot be safe references.
		return redactedValue, nil
	}
}

func normalizeSecretReferenceContainer(value any) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		result := make(map[string]any, len(typed))
		for _, key := range keys {
			normalized, err := normalizeSecretValue(typed[key])
			if err != nil {
				return nil, err
			}
			result[key] = normalized
		}
		return result, nil
	case []any:
		result := make([]any, len(typed))
		for i, child := range typed {
			normalized, err := normalizeSecretValue(child)
			if err != nil {
				return nil, err
			}
			result[i] = normalized
		}
		return result, nil
	default:
		return normalizeSecretValue(value)
	}
}

func validateSecretReferences(value any, path, key string) error {
	if isSecretDeclarationKey(key) || isNonSecretPolicyMetadataKey(key) {
		return nil
	}
	if isSecretReferenceContainerKey(key) {
		return validateSecretReferenceContainer(value, path)
	}
	if isSecretKey(key) {
		return validateSecretReferenceValue(value, path)
	}
	switch typed := value.(type) {
	case map[string]any:
		for childKey, child := range typed {
			if err := validateSecretReferences(child, joinPath(path, childKey), childKey); err != nil {
				return err
			}
		}
	case []any:
		for i, child := range typed {
			if err := validateSecretReferences(child, fmt.Sprintf("%s[%d]", path, i), key); err != nil {
				return err
			}
		}
	default:
		// CUE-exported aliases are normalized before reaching nested values.
		normalized, err := normalizeJSON(value, false, "")
		if err != nil {
			return err
		}
		if normalizedMap, ok := normalized.(map[string]any); ok {
			return validateSecretReferences(normalizedMap, path, key)
		}
	}
	return nil
}

func validateSecretReferenceContainer(value any, path string) error {
	switch typed := value.(type) {
	case map[string]any:
		for name, reference := range typed {
			if err := validateSecretReferenceValue(reference, joinPath(path, name)); err != nil {
				return err
			}
		}
		return nil
	case []any:
		for i, reference := range typed {
			if err := validateSecretReferenceValue(reference, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	default:
		return validateSecretReferenceValue(value, path)
	}
}

func validateSecretReferenceValue(value any, path string) error {
	switch typed := value.(type) {
	case string:
		if secretRefPattern.MatchString(typed) {
			return nil
		}
	case []any:
		for i, child := range typed {
			if err := validateSecretReferenceValue(child, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	}
	return fail(ErrUnsafeSecretReference, path, "plaintext or embedded secret material is forbidden; use a secret://, vault://, doppler://, or techstack:// reference")
}

func sortStringsUnique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
