package netenv

import (
	"context"
	"net"
	"strings"

	"github.com/kombifyio/stackkits/pkg/models"
)

var lookupIP = net.LookupIP

var detectPublicIP = func() string {
	detected := Detect(context.Background())
	if detected == nil {
		return ""
	}
	return strings.TrimSpace(detected.PublicIP)
}

// SelectPublicCloudDomain picks the Cloud Kit public domain. An empty or
// local value becomes kombify.me. A custom domain is kept only when DNS
// already points at this host; otherwise kombify.me is the fallback.
func SelectPublicCloudDomain(requested string) (domain string, usedFallback bool) {
	requested = strings.ToLower(strings.Trim(strings.TrimSpace(requested), "."))
	if requested == "" || models.IsKombifyMeDomain(requested) || models.IsLocalDomain(requested) {
		return models.DomainKombifyMe, false
	}
	if documentationOrTestDomain(requested) {
		return requested, false
	}
	if customDomainPointsAtThisHost(requested) {
		return requested, false
	}
	return models.DomainKombifyMe, true
}

func documentationOrTestDomain(domain string) bool {
	for _, suffix := range []string{".test", ".example", ".invalid", ".localhost"} {
		if domain == strings.TrimPrefix(suffix, ".") || strings.HasSuffix(domain, suffix) {
			return true
		}
	}
	for _, reserved := range []string{"example.com", "example.net", "example.org"} {
		if domain == reserved || strings.HasSuffix(domain, "."+reserved) {
			return true
		}
	}
	return false
}

func customDomainPointsAtThisHost(domain string) bool {
	publicIP := detectPublicIP()
	for _, name := range []string{domain, "base." + domain} {
		if hostResolvesTo(name, publicIP) {
			return true
		}
	}
	return false
}

func hostResolvesTo(name, publicIP string) bool {
	ips, err := lookupIP(name)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if ip == nil {
			continue
		}
		if publicIP != "" && ip.String() == publicIP {
			return true
		}
		if interfaceHasIP(ip.String()) {
			return true
		}
	}
	return false
}
