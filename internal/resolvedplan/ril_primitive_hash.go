package resolvedplan

import "fmt"

// RILPrimitiveContractHashV1 hashes the canonical, normalized catalog object
// under a versioned domain; caller-supplied objects are never product authority.
func RILPrimitiveContractHashV1(contract RILActionPrimitiveContract) (string, error) {
	if contract == nil {
		return "", fmt.Errorf("RIL primitive contract required")
	}
	return canonicalHash(map[string]any{"schemaVersion": "stackkit.ril-primitive-contract-hash/v1", "primitive": map[string]any(contract)}, true)
}
