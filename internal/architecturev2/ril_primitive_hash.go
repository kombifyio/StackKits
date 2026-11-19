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
	ProductRILCheckBackupHash          = "sha256:0bc1dac961a60c086487af80ebbe38106240bc7c1bc6cdabe6d9e97b5516c72a"
	ProductRILRestartHostContainerHash = "sha256:56fb634dc6aeb5e934b95656987eb5f47fb7708b120b7065a6551168d7da1660"
	ProductRILRestartHostUnitHash      = "sha256:e4e885a4711a66268c4b11294f4a5161be4d4cd55183caf98aee7f599e020f7f"
	ProductRILRestartServiceHash       = "sha256:4def0809246531f46d44a752ec32f479a9c76a52e4bd91621b059b7354aa0752"
	ProductRILRunBackupHash            = "sha256:ec547bb419fd058f539bc21a526514a044d2aa163bffb31b04511c2198e00c3e"
	ProductRILStartHostContainerHash   = "sha256:f6d39105e02350a5e3748cd77a7f7845a8e773ceba2c1e19ac210f0789526572"
	ProductRILStartHostUnitHash        = "sha256:fa1f598d51c8576b40509e78ef4d3e165e8f8c5b29e6f582859ad68004269219"
	ProductRILStartServiceHash         = "sha256:b67ddc33d3d1cf0aa571685ad7ca502a42250132d34dd1d4ee5c79ca6e6574d9"
	ProductRILStopHostContainerHash    = "sha256:5515d4f14715da8ce8db700d1bd7ef8968352eb658aefd57ace6b48f052d1774"
	ProductRILStopHostUnitHash         = "sha256:d6279a05a1fdfca016c2f9be4e8f38c54554ae9e8e7961d18c16b874437f5cc7"
	ProductRILStopServiceHash          = "sha256:a847be7a4cae392e0f6f9f6d6baa42e8c82e69187418cf698259469abf20d766"
	ProductRILUpdateServiceHash        = "sha256:f80c181503d9b3f710cadbd0cfc5812571fff518f3eda659b72e34c262ec3365"
	ProductRILVerifyStackKitStateHash  = "sha256:0309f6453252e45cec967bb2c944872adc759f913a4911823ee8e52dcaf3887d"
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
