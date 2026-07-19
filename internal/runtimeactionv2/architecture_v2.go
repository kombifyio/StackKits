package runtimeaction

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var architectureV2PlanHashPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

const (
	architectureV2FieldAPIVersion       = "api_version"
	architectureV2FieldAction           = "action"
	architectureV2FieldStackID          = "stack_id"
	architectureV2FieldTenantID         = "tenant_id"
	architectureV2FieldOwnerID          = "owner_id"
	architectureV2FieldStackSpec        = "stack_spec"
	architectureV2FieldInventory        = "inventory"
	architectureV2FieldExpectedPlanHash = "expected_plan_hash"
)

var architectureV2AllowedFields = map[string]struct{}{
	architectureV2FieldAPIVersion: {}, architectureV2FieldAction: {}, architectureV2FieldStackID: {},
	architectureV2FieldTenantID: {}, architectureV2FieldOwnerID: {}, architectureV2FieldStackSpec: {},
	architectureV2FieldInventory: {}, architectureV2FieldExpectedPlanHash: {},
}

// DecodeArchitectureV2Request accepts exactly one closed v2 JSON object. It
// rejects unknown or duplicate top-level fields and trailing JSON before
// validating the provider-free admission contract. Errors name contract fields
// only and never echo caller values.
func DecodeArchitectureV2Request(data []byte) (ArchitectureV2Request, error) {
	if err := rejectArchitectureV2DuplicateOrTrailingJSON(data); err != nil {
		return ArchitectureV2Request{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var request ArchitectureV2Request
	if err := decoder.Decode(&request); err != nil {
		return ArchitectureV2Request{}, fmt.Errorf("runtimeaction v2 envelope is invalid: %w", err)
	}
	if err := requireArchitectureV2EOF(decoder); err != nil {
		return ArchitectureV2Request{}, err
	}
	if err := ValidateArchitectureV2Request(request); err != nil {
		return ArchitectureV2Request{}, err
	}
	return request, nil
}

// ValidateArchitectureV2Request validates a constructed provider-free v2
// request before a consumer sends or admits it.
func ValidateArchitectureV2Request(request ArchitectureV2Request) error {
	if request.APIVersion != RuntimeActionAPIVersionV2Alpha1 {
		return fmt.Errorf("runtimeaction field %s is unsupported", architectureV2FieldAPIVersion)
	}
	switch request.Action {
	case ArchitectureV2OperationRollout, ArchitectureV2OperationVerify:
	default:
		return fmt.Errorf("runtimeaction field %s is unsupported for Architecture v2", architectureV2FieldAction)
	}
	if strings.TrimSpace(request.StackID) == "" {
		return fmt.Errorf("runtimeaction field %s is required", architectureV2FieldStackID)
	}
	if err := validateArchitectureV2JSONObject(request.StackSpec, architectureV2FieldStackSpec); err != nil {
		return err
	}
	if err := validateArchitectureV2JSONObject(request.Inventory, architectureV2FieldInventory); err != nil {
		return err
	}
	if !architectureV2PlanHashPattern.MatchString(request.ExpectedPlanHash) {
		return fmt.Errorf("runtimeaction field %s must be canonical sha256", architectureV2FieldExpectedPlanHash)
	}
	return nil
}

func rejectArchitectureV2DuplicateOrTrailingJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("runtimeaction v2 envelope is invalid JSON")
	}
	if delimiter, ok := opening.(json.Delim); !ok || delimiter != '{' {
		return fmt.Errorf("runtimeaction v2 envelope must be an object")
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("runtimeaction v2 envelope is invalid JSON")
		}
		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("runtimeaction v2 envelope has an invalid field name")
		}
		if _, allowed := architectureV2AllowedFields[key]; !allowed {
			return fmt.Errorf("runtimeaction v2 envelope contains an undeclared field")
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("runtimeaction v2 envelope contains a duplicate field")
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return fmt.Errorf("runtimeaction v2 envelope contains an invalid field value")
		}
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("runtimeaction v2 envelope is invalid JSON")
	}
	return requireArchitectureV2EOF(decoder)
}

func requireArchitectureV2EOF(decoder *json.Decoder) error {
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("runtimeaction v2 envelope contains trailing JSON")
	}
	return nil
}

func validateArchitectureV2JSONObject(raw json.RawMessage, field string) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return fmt.Errorf("runtimeaction field %s is required", field)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return fmt.Errorf("runtimeaction field %s must be a JSON object", field)
	}
	return nil
}
