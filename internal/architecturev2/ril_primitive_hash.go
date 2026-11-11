package architecturev2

import (
	"fmt"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// The executor-bound RIL primitive contract hashes embedded in the signed
// binary as one capability marker per primitive. The compiler-boundary check
// requires each to equal the normalized CUE catalog, and the catalog's
// executor-bound set to equal exactly this set.
const (
	ProductRILCheckBackupHash          = "sha256:798902cba9b7483de4ad1ac95a2a2a8942a3cd699abce6dbf31e945647e57cbf"
	ProductRILRestartHostContainerHash = "sha256:9a398fa9171765e6c2676a2409d4c2660b180c08a862fda24aa1e2fab0161081"
	ProductRILRestartHostUnitHash      = "sha256:f0c233ba59b9f640f0389249db3a7a06a49637d8669b3e76a143f07b7c790da5"
	ProductRILRestartServiceHash       = "sha256:ca3d0a38a48b908262c3bb83edd4fda11dd1da320e885ad0a7e948b6d7589e55"
	ProductRILRunBackupHash            = "sha256:84fa1fdecf6c0b0031e119274b12d91eeaafd807ab1aa719c780a3fa87a9430c"
	ProductRILStartHostContainerHash   = "sha256:91f2ee82035573d89800b05f8b93146a323f55a79923ed299260e37082dd8675"
	ProductRILStartHostUnitHash        = "sha256:867a6b8955c6fd2a7878a309b1d1a567fcda2b771498e52b1cc22f4c610bbe2e"
	ProductRILStartServiceHash         = "sha256:e07611a953a97430e034f4d81a287c3a8705653827530beeca1e5c8900098084"
	ProductRILStopHostContainerHash    = "sha256:e25c920858d49d2a9b3663162fd28455d63863ff62285c4567b4e654e00e844d"
	ProductRILStopHostUnitHash         = "sha256:6a91ca8f0fff176134a5943ad00a3f17bd81fb7623227cfae416f229b546d9d8"
	ProductRILStopServiceHash          = "sha256:1aeca1fdf39612f9f60335bf93554efb6892ce740a6f1f05db0dda7fb01ef348"
	ProductRILUpdateServiceHash        = "sha256:9ba98dc91a647a74135fc660ba4684ed398ac3b74a44e3f9409648f5bbdaa351"
	ProductRILVerifyStackKitStateHash  = "sha256:8d38857c71ceec1b2558aa842dd04f31ec116e3dd0e7dd48eec3e592bccefabe"
)

const (
	rilPrimitiveMarkerPrefix = "stackkit.ril-primitive-contract/v2:"
	rilPrimitiveMarkerSuffix = ":end-stackkit.ril-primitive-contract"
)

// ProductRILPrimitiveMarkersV2 are compile-time constants, so each marker is
// present verbatim in the executable bytes a consumer authenticates.
var ProductRILPrimitiveMarkersV2 = []string{
	rilPrimitiveMarkerPrefix + "check-backup:" + ProductRILCheckBackupHash + rilPrimitiveMarkerSuffix,
	rilPrimitiveMarkerPrefix + "restart-host-container:" + ProductRILRestartHostContainerHash + rilPrimitiveMarkerSuffix,
	rilPrimitiveMarkerPrefix + "restart-host-unit:" + ProductRILRestartHostUnitHash + rilPrimitiveMarkerSuffix,
	rilPrimitiveMarkerPrefix + "restart-service:" + ProductRILRestartServiceHash + rilPrimitiveMarkerSuffix,
	rilPrimitiveMarkerPrefix + "run-backup:" + ProductRILRunBackupHash + rilPrimitiveMarkerSuffix,
	rilPrimitiveMarkerPrefix + "start-host-container:" + ProductRILStartHostContainerHash + rilPrimitiveMarkerSuffix,
	rilPrimitiveMarkerPrefix + "start-host-unit:" + ProductRILStartHostUnitHash + rilPrimitiveMarkerSuffix,
	rilPrimitiveMarkerPrefix + "start-service:" + ProductRILStartServiceHash + rilPrimitiveMarkerSuffix,
	rilPrimitiveMarkerPrefix + "stop-host-container:" + ProductRILStopHostContainerHash + rilPrimitiveMarkerSuffix,
	rilPrimitiveMarkerPrefix + "stop-host-unit:" + ProductRILStopHostUnitHash + rilPrimitiveMarkerSuffix,
	rilPrimitiveMarkerPrefix + "stop-service:" + ProductRILStopServiceHash + rilPrimitiveMarkerSuffix,
	rilPrimitiveMarkerPrefix + "update-service:" + ProductRILUpdateServiceHash + rilPrimitiveMarkerSuffix,
	rilPrimitiveMarkerPrefix + "verify-stackkit-state:" + ProductRILVerifyStackKitStateHash + rilPrimitiveMarkerSuffix,
}

// ProductRILPrimitiveIDs is the executor-bound set of this executable.
var ProductRILPrimitiveIDs = []string{"check-backup", "restart-host-container", "restart-host-unit", "restart-service", "run-backup", "start-host-container", "start-host-unit", "start-service", "stop-host-container", "stop-host-unit", "stop-service", "update-service", "verify-stackkit-state"}

// EmbeddedRILPrimitiveHash returns the hash this executable declares for an
// executor-bound primitive; there is no wildcard.
func EmbeddedRILPrimitiveHash(id string) (string, bool) {
	switch id {
	case "check-backup":
		return ProductRILCheckBackupHash, true
	case "restart-host-container":
		return ProductRILRestartHostContainerHash, true
	case "restart-host-unit":
		return ProductRILRestartHostUnitHash, true
	case "restart-service":
		return ProductRILRestartServiceHash, true
	case "run-backup":
		return ProductRILRunBackupHash, true
	case "start-host-container":
		return ProductRILStartHostContainerHash, true
	case "start-host-unit":
		return ProductRILStartHostUnitHash, true
	case "start-service":
		return ProductRILStartServiceHash, true
	case "stop-host-container":
		return ProductRILStopHostContainerHash, true
	case "stop-host-unit":
		return ProductRILStopHostUnitHash, true
	case "stop-service":
		return ProductRILStopServiceHash, true
	case "update-service":
		return ProductRILUpdateServiceHash, true
	case "verify-stackkit-state":
		return ProductRILVerifyStackKitStateHash, true
	}
	return "", false
}

// RILPrimitiveHash returns only a supported product primitive from this
// service's verified embedded catalog, not a filesystem-supplied catalog.
func (s *Service) RILPrimitiveHash(id string) (string, error) {
	if s == nil || s.authority == nil || !s.authority.catalogIsNormalizedExport || s.authority.planAuthority.Class != "product" {
		return "", fmt.Errorf("verified product catalog required")
	}
	var found resolvedplan.RILActionPrimitiveContract
	for _, p := range s.authority.catalog.RILActionPrimitives {
		if p["id"] == id {
			if found != nil {
				return "", fmt.Errorf("duplicate RIL primitive")
			}
			found = p
		}
	}
	if found == nil || found["support"] != "executor-bound" {
		return "", fmt.Errorf("RIL primitive unavailable")
	}
	return resolvedplan.RILPrimitiveContractHashV1(found)
}

// ExecutorBoundRILPrimitiveIDs lists the executor-bound primitive ids of this
// service's verified embedded catalog, in catalog order.
func (s *Service) ExecutorBoundRILPrimitiveIDs() ([]string, error) {
	if s == nil || s.authority == nil || !s.authority.catalogIsNormalizedExport || s.authority.planAuthority.Class != "product" {
		return nil, fmt.Errorf("verified product catalog required")
	}
	var ids []string
	for _, p := range s.authority.catalog.RILActionPrimitives {
		if p["support"] == "executor-bound" {
			id, _ := p["id"].(string)
			ids = append(ids, id)
		}
	}
	return ids, nil
}
