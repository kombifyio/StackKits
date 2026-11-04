package architecturev2

import (
	"fmt"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// ProductRILVerifyHashV1 is embedded in the signed binary capability marker.
// The compiler-boundary check requires it to equal the normalized CUE catalog.
const ProductRILVerifyHashV1 = "sha256:a745b4b217392a05de34b757b98ee04ff66d3c9ad6debb68dc2425f79e240df0"
const ProductRILVerifyMarkerV1 = "stackkit.ril-primitive-contract/v1:" + ProductRILVerifyHashV1 + ":end-stackkit.ril-primitive-contract"

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
