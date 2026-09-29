package netenv

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/kombifyio/stackkits/pkg/models"
)

var lookupIP = net.LookupIP

// ErrLocalCloudDomain refuses explicit local names before public route authoring.
var ErrLocalCloudDomain = errors.New("cloud-kit requires a public domain; use a domain that points at this host, omit --domain for kombify.me, or use basement-kit for local addresses")

var detectPublicIP = func() string {
	detected := Detect(context.Background())
	if detected == nil {
		return ""
	}
	return strings.TrimSpace(detected.PublicIP)
}

// SelectPublicCloudDomain picks the Cloud Kit public domain. An empty value
// becomes kombify.me; an explicit local domain is refused. A custom domain is
// kept only when DNS already points at this host; otherwise kombify.me is the fallback.
func SelectPublicCloudDomain(requested string) (domain string, usedFallback bool, err error) {
	requested = strings.ToLower(strings.Trim(strings.TrimSpace(requested), "."))
	if requested == "" || models.IsKombifyMeDomain(requested) {
		return models.DomainKombifyMe, false, nil
	}
	if models.IsLocalDomain(requested) {
		return "", false, fmt.Errorf("%w: %q", ErrLocalCloudDomain, requested)
	}
	if documentationOrTestDomain(requested) {
		return requested, false, nil
	}
	if customDomainPointsAtThisHost(requested) {
		return requested, false, nil
	}
	return models.DomainKombifyMe, true, nil
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
