package commands

import (
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/netenv"
	"github.com/kombifyio/stackkits/pkg/models"
)

func defaultArchitectureV2InitDomain(kit, domain string) (string, bool) {
	domain = strings.TrimSpace(domain)
	if kit != "cloud-kit" {
		return domain, false
	}
	return netenv.SelectPublicCloudDomain(domain)
}

func admitArchitectureV2InitPlacement(kit, domain string, detected *netenv.Result) error {
	env := models.NetEnvUnknown
	if detected != nil {
		env = detected.Environment
	}
	domain = strings.TrimSpace(domain)
	local := domain == "" || models.IsLocalDomain(domain)
	if kit == "basement-kit" && netenv.IsPublicServer(env) && local {
		return fmt.Errorf(
			"this host looks like a public server; Basement Kit local addresses are not reachable from the internet. Use Cloud Kit (`stackkit init cloud-kit --owner-source=local`) or pass a public --domain / kombify.me",
		)
	}
	return nil
}
