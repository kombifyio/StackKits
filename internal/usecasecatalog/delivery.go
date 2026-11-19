package usecasecatalog

import (
	"fmt"
	"sort"
)

type deliveryRowClaim struct {
	status         string
	deployment     bool
	routeTLS       bool
	statusEvidence bool
	backupRestore  bool
}

// committedCell reports whether a package commits any kit/tier cell for the
// alternative and adapter (docs/USE_CASE_STANDARD.md D5). Only committed
// cells may be published as supported.
func committedCell(pkg map[string]any, alternativeRef, adapterRef string) bool {
	cells, _ := pkg["committed"].([]any)
	for _, raw := range cells {
		cell, _ := raw.(map[string]any)
		if stringField(cell, "alternative") == alternativeRef && stringField(cell, "adapter") == adapterRef {
			return true
		}
	}
	return false
}

// claimsBackupRestore reports whether any delivery row of an alternative
// claims the backup/restore capability.
func claimsBackupRestore(alternative map[string]any) bool {
	runtime, _ := alternative["runtime"].(map[string]any)
	compatibility, _ := runtime["compatibility"].([]any)
	for _, raw := range compatibility {
		row, _ := raw.(map[string]any)
		capabilities, _ := row["capabilities"].(map[string]any)
		if boolField(capabilities, "backupRestore") {
			return true
		}
	}
	return false
}

func hasDeliveryRow(alternative map[string]any, adapterRef string) bool {
	runtime, _ := alternative["runtime"].(map[string]any)
	compatibility, _ := runtime["compatibility"].([]any)
	for _, raw := range compatibility {
		row, _ := raw.(map[string]any)
		if stringField(row, "adapterRef") == adapterRef {
			return true
		}
	}
	return false
}

// claimsStandaloneBackupRestore reports whether the standalone Compose row of
// an alternative claims the backup/restore capability.
func claimsStandaloneBackupRestore(alternative map[string]any) bool {
	runtime, _ := alternative["runtime"].(map[string]any)
	compatibility, _ := runtime["compatibility"].([]any)
	for _, raw := range compatibility {
		row, _ := raw.(map[string]any)
		capabilities, _ := row["capabilities"].(map[string]any)
		if stringField(row, "adapterRef") == "standalone-compose" && boolField(capabilities, "backupRestore") {
			return true
		}
	}
	return false
}

func deliveryRows(source sourceCatalog) ([]ApplicationDelivery, error) {
	var rows []ApplicationDelivery
	seen := map[string]deliveryRowClaim{}
	for useCaseRef, workload := range source.Workloads {
		workloadRef := metadataID(workload)
		alternatives, _ := workload["alternatives"].([]any)
		defaultAlternative := stringField(workload, "defaultAlternative")
		defaultModule := ""
		for _, raw := range alternatives {
			alternative, _ := raw.(map[string]any)
			if stringField(alternative, "id") == defaultAlternative {
				defaultModule = stringField(alternative, "moduleRef")
				break
			}
		}
		if defaultAlternative != "" && defaultModule == "" {
			return nil, fmt.Errorf("workload %s has no module for its default alternative", workloadRef)
		}
		for _, rawAlternative := range alternatives {
			alternative, _ := rawAlternative.(map[string]any)
			// A row states what the default alternative delivers through an
			// adapter; other alternatives keep their own catalog maturity.
			if defaultAlternative != "" && stringField(alternative, "id") != defaultAlternative {
				continue
			}
			runtime, _ := alternative["runtime"].(map[string]any)
			compatibility, _ := runtime["compatibility"].([]any)
			for _, rawRow := range compatibility {
				row, _ := rawRow.(map[string]any)
				adapter := stringField(row, "adapterRef")
				status := stringField(row, "maturity")
				if status == "contract-only" {
					status = "preview"
				}
				if status != "supported" && status != "beta" && status != "preview" && status != "unsupported" {
					return nil, fmt.Errorf("invalid delivery status %q", status)
				}
				if status == "supported" && !committedCell(source.Packages[useCaseRef], stringField(alternative, "id"), adapter) {
					return nil, fmt.Errorf("use case %s claims supported delivery for %s through %s without a committed cell", useCaseRef, stringField(alternative, "id"), adapter)
				}
				capMap, _ := row["capabilities"].(map[string]any)
				claim := deliveryRowClaim{
					status:         status,
					deployment:     boolField(capMap, "deployment"),
					routeTLS:       boolField(capMap, "routeTLS"),
					statusEvidence: boolField(capMap, "statusEvidence"),
					backupRestore:  boolField(capMap, "backupRestore"),
				}
				key := useCaseRef + "/" + workloadRef + "/" + adapter
				if previous, ok := seen[key]; ok {
					if previous != claim {
						return nil, fmt.Errorf("conflicting application delivery row %s", key)
					}
					continue
				}
				seen[key] = claim
				rows = append(rows, ApplicationDelivery{DefaultAlternativeRef: defaultAlternative, DefaultModuleRef: defaultModule, UseCaseRef: useCaseRef, WorkloadRef: workloadRef, AdapterRef: adapter, AdapterName: adapterName(adapter), Status: status, Capabilities: DeliveryCapabilities{Deployment: claim.deployment, RouteTLS: claim.routeTLS, StatusEvidence: claim.statusEvidence, BackupRestore: claim.backupRestore}})
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.UseCaseRef != b.UseCaseRef {
			return a.UseCaseRef < b.UseCaseRef
		}
		return a.AdapterRef < b.AdapterRef
	})
	return rows, nil
}

func adapterName(ref string) string {
	switch ref {
	case "coolify":
		return "Coolify"
	case "dokploy":
		return "Dokploy"
	case "komodo":
		return "Komodo"
	case "runtipi":
		return "Runtipi"
	case "standalone-compose":
		return "Standalone Compose"
	default:
		return ref
	}
}
