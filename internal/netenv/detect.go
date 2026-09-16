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

const networkEnvOverride = "STACKKIT_NETWORK_ENV"

// IsPublicServer reports whether the detected environment is a VPS or managed
// cloud host with a public interface. Home/NAT and unknown are not.
func IsPublicServer(env models.NetworkEnvironment) bool {
	return env == models.NetEnvVPS || env == models.NetEnvCloud
}

// Detect determines the network environment by checking network interfaces
// and comparing the local IP to the external IP.
func Detect(ctx context.Context) *Result {
	if override := detectOverride(); override != nil {
		return override
	}

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
		// Public IP exists but is not on any interface: private/local target behind NAT.
		r.Environment = models.NetEnvHome
		r.IsNAT = true
	} else if r.PublicIP == "" && r.PrivateIP != "" {
		// No public IP reachable: private/local or isolated target.
		r.Environment = models.NetEnvHome
		r.IsNAT = true
	}

	return r
}

func detectOverride() *Result {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(networkEnvOverride)))
	switch raw {
	case "":
		return nil
	case string(models.NetEnvHome):
		return &Result{Environment: models.NetEnvHome, IsNAT: true, PrivateIP: "192.168.0.2"}
	case string(models.NetEnvVPS):
		return &Result{
			Environment:        models.NetEnvVPS,
			HasPublicInterface: true,
			PublicIP:           "203.0.113.10",
			PrivateIP:          "10.0.0.2",
		}
	case string(models.NetEnvCloud):
		return &Result{
			Environment:        models.NetEnvCloud,
			HasPublicInterface: true,
			PublicIP:           "203.0.113.10",
			PrivateIP:          "10.0.0.2",
		}
	default:
		return &Result{Environment: models.NetEnvUnknown}
	}
}

// PrivateIP returns the first non-loopback RFC1918 address on the host.
// It is intentionally network-local only and does not call external services.
func PrivateIP() string {
	return getPrivateIP()
}

// isKombifyCloud checks if the server was provisioned by kombify Cloud.
// It checks for the STACKKIT_NODE_CONTEXT env var or the local context file.
// Both "cloud" and "vps" are treated as cloud context — VPS-type Sim nodes
// are injected with "cloud" by the Sim engine, but "vps" is accepted as a
// defense-in-depth fallback.
func isKombifyCloud() bool {
	if ctx := os.Getenv("STACKKIT_NODE_CONTEXT"); ctx == "cloud" || ctx == "vps" {
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
// STACKKIT_USER_EMAIL environment variable or the local user-email file.
//
// Returns an empty string when:
//   - Not running in kombify Cloud context
//   - Neither STACKKIT_USER_EMAIL nor the local user-email file is set
func GetCloudUserEmail() string {
	if !isKombifyCloud() {
		return ""
	}
	if email := strings.TrimSpace(os.Getenv("STACKKIT_USER_EMAIL")); email != "" {
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
//
// The request goes out over IPv4 only and only an IPv4 answer counts. On a
// dual-stack home network the host owns a global IPv6 address, so an IPv6 answer
// is always "on an interface" and made every such home server look like a VPS;
// IPv4 NAT is what separates a home network from a public server.
func fetchIP(ctx context.Context, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "stackkit/netenv")

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp4", address)
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: transport}
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
	if parsed := net.ParseIP(ip); parsed == nil || parsed.To4() == nil {
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
		return "Local/private target (behind NAT)"
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
			return DefaultLocalDomain(), "local/private target detected — using configured local domain"
		}
		return currentDomain, ""
	default:
		return currentDomain, ""
	}
}
