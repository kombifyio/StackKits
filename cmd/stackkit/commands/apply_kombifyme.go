package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/netenv"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/pkg/models"
)

func canonicalPlanDomainBase(plan resolvedplan.ResolvedPlan) string {
	network, _ := map[string]any(plan)["network"].(map[string]any)
	domain, _ := network["domain"].(map[string]any)
	base, _ := domain["base"].(string)
	return strings.TrimSpace(base)
}

// prepareKombifyMeAccess refuses kombify.me on a NAT/home host. Public CLI
// cannot import the hosted kombify.me client; the installer already requires a
// registry key before it authors this domain.
func prepareKombifyMeAccess(plan resolvedplan.ResolvedPlan) error {
	domain := canonicalPlanDomainBase(plan)
	if !models.IsKombifyMeDomain(domain) {
		return nil
	}
	detected := netenv.Detect(context.Background())
	if detected == nil || detected.PublicIP == "" || (!detected.HasPublicInterface && detected.IsNAT) {
		return fmt.Errorf("kombify.me on a home network behind NAT is not published yet; use Cloud Kit on a public VPS or keep local home addresses")
	}
	return nil
}
