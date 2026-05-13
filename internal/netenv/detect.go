// Package netenv detects the network environment (home LAN vs VPS vs cloud).
package netenv

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/pkg/models"
)

// Result holds the outcome of network environment detection.
type Result struct {
	Environment        models.NetworkEnvironment
	PublicIP           string
	PrivateIP          string
	IsNAT              bool
	HasPublicInterface bool
}

// Detect determines the network environment by checking network interfaces
// and comparing the local IP to the external IP.
func Detect(ctx context.Context) *Result {
	r := &Result{Environment: models.NetEnvUnknown}

	// Check if this was provisioned by kombify Cloud
	if isKombifyCloud() {
		r.Environment = models.NetEnvCloud
		r.PublicIP = getPublicIP(ctx)
		r.PrivateIP = getPrivateIP()
		return r
	}

	r.PrivateIP = getPrivateIP()
	r.PublicIP = getPublicIP(ctx)

	// Check if any network interface has the public IP directly assigned
	if r.PublicIP != "" {
		r.HasPublicInterface = interfaceHasIP(r.PublicIP)
	}

	// Classify the environment
	if r.PublicIP != "" && r.HasPublicInterface {
		// Public IP is directly on an interface — this is a VPS/dedicated server
		r.Environment = models.NetEnvVPS
		r.IsNAT = false
	} else if r.PublicIP != "" && !r.HasPublicInterface {
		// Public IP exists but is not on any interface — behind NAT (home network)
		r.Environment = models.NetEnvHome
		r.IsNAT = true
	} else if r.PublicIP == "" && r.PrivateIP != "" {
		// No public IP reachable — likely home network without internet or isolated env
		r.Environment = models.NetEnvHome
		r.IsNAT = true
	}

	return r
}

// PrivateIP returns the first non-loopback RFC1918 address on the host.
// It is intentionally network-local only and does not call external services.
func PrivateIP() string {
	return getPrivateIP()
}

// isKombifyCloud checks if the server was provisioned by kombify Cloud.
// It checks for the KOMBIFY_CONTEXT env var or the /etc/kombify/context file.
// Both "cloud" and "vps" are treated as cloud context — VPS-type Sim nodes
// are injected with "cloud" by the Sim engine, but "vps" is accepted as a
// defense-in-depth fallback.
func isKombifyCloud() bool {
	if ctx := os.Getenv("KOMBIFY_CONTEXT"); ctx == "cloud" || ctx == "vps" {
		return true
	}
	data, err := os.ReadFile("/etc/kombify/context")
	if err == nil {
		val := strings.TrimSpace(string(data))
		if val == "cloud" || val == "vps" {
			return true
		}
	}
	return false
}

// GetCloudUserEmail returns the authenticated user's email when running in
// kombify Cloud context. The email is injected by the Cloud platform via the
// KOMBIFY_USER_EMAIL environment variable or /etc/kombify/user_email file.
//
// Returns an empty string when:
//   - Not running in kombify Cloud context
//   - Neither KOMBIFY_USER_EMAIL nor /etc/kombify/user_email is set
func GetCloudUserEmail() string {
	if !isKombifyCloud() {
		return ""
	}
	if email := strings.TrimSpace(os.Getenv("KOMBIFY_USER_EMAIL")); email != "" {
		return email
	}
	// Fallback: read from file (injected by Sim engine for Docker-based nodes)
	if data, err := os.ReadFile("/etc/kombify/user_email"); err == nil {
		if email := strings.TrimSpace(string(data)); email != "" {
			return email
		}
	}
	return ""
}

// getPublicIP fetches the external IP using a public API.
func getPublicIP(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Try multiple services for resilience
	services := []string{
		"https://ifconfig.me/ip",
		"https://api.ipify.org",
		"https://checkip.amazonaws.com",
	}

	for _, url := range services {
		ip := fetchIP(ctx, url)
		if ip != "" {
			return ip
		}
	}
	return ""
}

// fetchIP makes a GET request to a service that returns the public IP as plain text.
func fetchIP(ctx context.Context, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "stackkit/netenv")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	// Limit read to 64 bytes — an IP address is at most ~45 chars (IPv6)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return ""
	}

	ip := strings.TrimSpace(string(body))
	if net.ParseIP(ip) == nil {
		return ""
	}
	return ip
}

// getPrivateIP returns the first non-loopback private IP of the machine.
func getPrivateIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		ip := ipNet.IP.To4()
		if ip == nil {
			continue
		}
		if isPrivateIP(ip) {
			return ip.String()
		}
	}
	return ""
}

// interfaceHasIP checks if any network interface has the given IP assigned.
func interfaceHasIP(targetIP string) bool {
	target := net.ParseIP(targetIP)
	if target == nil {
		return false
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		if ipNet.IP.Equal(target) {
			return true
		}
	}
	return false
}

// isPrivateIP returns true if the IP is in a private range (RFC 1918).
func isPrivateIP(ip net.IP) bool {
	privateRanges := []struct {
		network string
		mask    string
	}{
		{"10.0.0.0", "255.0.0.0"},
		{"172.16.0.0", "255.240.0.0"},
		{"192.168.0.0", "255.255.0.0"},
	}
	for _, r := range privateRanges {
		network := net.ParseIP(r.network)
		mask := net.IPMask(net.ParseIP(r.mask).To4())
		if ip.Mask(mask).Equal(network.Mask(mask)) {
			return true
		}
	}
	return false
}

// FormatEnvironment returns a human-readable description of the network environment.
func FormatEnvironment(env models.NetworkEnvironment) string {
	switch env {
	case models.NetEnvHome:
		return "Home/office network (behind NAT)"
	case models.NetEnvVPS:
		return "VPS/dedicated server (public IP)"
	case models.NetEnvCloud:
		return "kombify Cloud (managed)"
	default:
		return "Unknown"
	}
}

// SuggestDomain returns the recommended domain strategy for the detected environment.
func SuggestDomain(env models.NetworkEnvironment, currentDomain string) (domain string, reason string) {
	switch env {
	case models.NetEnvCloud:
		return models.DomainKombifyMe, "deployed via kombify Cloud — using kombify.me for public access"
	case models.NetEnvVPS:
		if models.IsLocalDomain(currentDomain) {
			return models.DomainKombifyMe, fmt.Sprintf("running on a VPS (public server) — local domain '%s' won't be reachable from outside", currentDomain)
		}
		return currentDomain, ""
	case models.NetEnvHome:
		if currentDomain == "" {
			return DefaultLocalDomain(), "home network detected — using configured local domain"
		}
		return currentDomain, ""
	default:
		return currentDomain, ""
	}
}
