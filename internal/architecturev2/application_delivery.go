package architecturev2

import (
	"fmt"
	"sort"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// ApplicationDeliveryCapabilities states what one exact workload/adapter row
// currently implements. False remains an explicit unsupported capability; it
// must never be inferred from catalog presence or adapter maturity.
type ApplicationDeliveryCapabilities struct {
	Deployment     bool `json:"deployment"`
	RouteTLS       bool `json:"routeTLS"`
	StatusEvidence bool `json:"statusEvidence"`
	BackupRestore  bool `json:"backupRestore"`
}

// ApplicationDeliveryCompatibilityEntry is the CUE-owned, read-only support
// row for one StackKits application and one delivery adapter.
type ApplicationDeliveryCompatibilityEntry struct {
	WorkloadRef    string                          `json:"workloadRef"`
	AlternativeRef string                          `json:"alternativeRef"`
	AdapterRef     string                          `json:"adapterRef"`
	Maturity       string                          `json:"maturity"`
	Capabilities   ApplicationDeliveryCapabilities `json:"capabilities"`
}

// ListApplicationDeliveryCompatibility returns the complete application
// delivery matrix from the immutable embedded CUE catalog. It reports product
// support, not selected plan state, runtime availability, or live evidence.
func (s *Service) ListApplicationDeliveryCompatibility() ([]ApplicationDeliveryCompatibilityEntry, error) {
	if s == nil || s.authority == nil {
		return nil, resolveError(ErrResolveFailed, "service is not initialized", nil)
	}
	if len(s.authority.contractSources) == 0 ||
		s.authority.planAuthority.Class != "product" ||
		s.authority.planAuthority.Document != "catalog" {
		return nil, resolveError(ErrAuthorityLoad, "embedded Architecture v2 product catalog authority is required", nil)
	}
	entries, err := applicationDeliveryCompatibilityEntries(s.authority.catalog.Workloads)
	if err != nil {
		return nil, resolveError(ErrAuthorityLoad, "decode application delivery compatibility: "+err.Error(), err)
	}
	return entries, nil
}

func applicationDeliveryCompatibilityEntries(
	workloads []resolvedplan.WorkloadContract,
) ([]ApplicationDeliveryCompatibilityEntry, error) {
	entries := make([]ApplicationDeliveryCompatibilityEntry, 0, 9)
	seen := map[string]struct{}{}
	for workloadIndex, workload := range workloads {
		metadata, ok := workload["metadata"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("workloads[%d].metadata must be an object", workloadIndex)
		}
		workloadRef, ok := metadata["id"].(string)
		if !ok || workloadRef == "" {
			return nil, fmt.Errorf("workloads[%d].metadata.id must be a non-empty string", workloadIndex)
		}
		kind, ok := workload["kind"].(string)
		if !ok {
			return nil, fmt.Errorf("workload %q kind must be a string", workloadRef)
		}
		if kind != "application" {
			continue
		}
		alternatives, ok := workload["alternatives"].([]any)
		if !ok || len(alternatives) == 0 {
			return nil, fmt.Errorf("application workload %q alternatives must be a non-empty list", workloadRef)
		}
		for alternativeIndex, rawAlternative := range alternatives {
			alternative, ok := rawAlternative.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("workload %q alternatives[%d] must be an object", workloadRef, alternativeIndex)
			}
			alternativeRef, ok := alternative["id"].(string)
			if !ok || alternativeRef == "" {
				return nil, fmt.Errorf("workload %q alternative id must be a non-empty string", workloadRef)
			}
			runtime, ok := alternative["runtime"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("workload %q alternative %q runtime must be an object", workloadRef, alternativeRef)
			}
			compatibility, ok := runtime["compatibility"].([]any)
			if !ok || len(compatibility) == 0 {
				return nil, fmt.Errorf("application workload %q has no delivery compatibility rows", workloadRef)
			}
			for rowIndex, rawRow := range compatibility {
				row, ok := rawRow.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("workload %q compatibility[%d] must be an object", workloadRef, rowIndex)
				}
				entry, err := decodeApplicationDeliveryCompatibilityRow(workloadRef, alternativeRef, row)
				if err != nil {
					return nil, err
				}
				key := entry.WorkloadRef + "/" + entry.AlternativeRef + "/" + entry.AdapterRef
				if _, duplicate := seen[key]; duplicate {
					return nil, fmt.Errorf("application delivery compatibility row %q is duplicated", key)
				}
				seen[key] = struct{}{}
				entries = append(entries, entry)
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		left, right := entries[i], entries[j]
		if left.WorkloadRef != right.WorkloadRef {
			return left.WorkloadRef < right.WorkloadRef
		}
		if left.AlternativeRef != right.AlternativeRef {
			return left.AlternativeRef < right.AlternativeRef
		}
		return left.AdapterRef < right.AdapterRef
	})
	return entries, nil
}

func decodeApplicationDeliveryCompatibilityRow(
	workloadRef, alternativeRef string,
	row map[string]any,
) (ApplicationDeliveryCompatibilityEntry, error) {
	adapterRef, ok := row["adapterRef"].(string)
	if !ok || adapterRef == "" {
		return ApplicationDeliveryCompatibilityEntry{}, fmt.Errorf("workload %q compatibility adapterRef must be a non-empty string", workloadRef)
	}
	maturity, ok := row["maturity"].(string)
	if !ok || (maturity != "supported" && maturity != "beta" &&
		maturity != "contract-only" && maturity != "unsupported") {
		return ApplicationDeliveryCompatibilityEntry{}, fmt.Errorf("workload %q adapter %q maturity is invalid", workloadRef, adapterRef)
	}
	capabilities, ok := row["capabilities"].(map[string]any)
	if !ok {
		return ApplicationDeliveryCompatibilityEntry{}, fmt.Errorf("workload %q adapter %q capabilities must be an object", workloadRef, adapterRef)
	}
	read := func(field string) (bool, error) {
		value, exists := capabilities[field]
		if !exists {
			return false, fmt.Errorf("workload %q adapter %q capability %q is absent", workloadRef, adapterRef, field)
		}
		result, ok := value.(bool)
		if !ok {
			return false, fmt.Errorf("workload %q adapter %q capability %q must be boolean", workloadRef, adapterRef, field)
		}
		return result, nil
	}
	deployment, err := read("deployment")
	if err != nil {
		return ApplicationDeliveryCompatibilityEntry{}, err
	}
	routeTLS, err := read("routeTLS")
	if err != nil {
		return ApplicationDeliveryCompatibilityEntry{}, err
	}
	statusEvidence, err := read("statusEvidence")
	if err != nil {
		return ApplicationDeliveryCompatibilityEntry{}, err
	}
	backupRestore, err := read("backupRestore")
	if err != nil {
		return ApplicationDeliveryCompatibilityEntry{}, err
	}
	return ApplicationDeliveryCompatibilityEntry{
		WorkloadRef: workloadRef, AlternativeRef: alternativeRef,
		AdapterRef: adapterRef, Maturity: maturity,
		Capabilities: ApplicationDeliveryCapabilities{
			Deployment: deployment, RouteTLS: routeTLS,
			StatusEvidence: statusEvidence, BackupRestore: backupRestore,
		},
	}, nil
}
