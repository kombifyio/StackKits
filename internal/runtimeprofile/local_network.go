package runtimeprofile

import (
	"fmt"
	"net/netip"

	"github.com/kombifyio/stackkits/internal/dataprofilev1"
)

// LocalNetwork is compiler-owned cohort identity and DNS transport. The
// resolver is the attested node's signed LAN DNS service, not a host default.
type LocalNetwork struct {
	StackID  string   `json:"stackId"`
	Resolver string   `json:"resolver"`
	Upstream []string `json:"upstream"`
}

func (p LocalNetwork) Validate() error {
	if p.StackID == "" {
		return fmt.Errorf("Full Local network requires stack identity")
	}
	if err := ValidateLocalDNS(append([]string{p.Resolver}, p.Upstream...)); err != nil {
		return err
	}
	for _, server := range p.Upstream {
		if server == p.Resolver {
			return fmt.Errorf("Full Local DNS cannot forward to itself")
		}
	}
	return nil
}

func ValidateLocalDNS(servers []string) error {
	for _, server := range servers {
		address, err := netip.ParseAddr(server)
		if err != nil || !dataprofile.LocalAddr(address) || address.IsLoopback() {
			return fmt.Errorf("Full Local DNS resolver %q must be an explicit homelab address", server)
		}
	}
	return nil
}
