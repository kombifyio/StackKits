// Package clientsetup renders the public LAN DNS and browser-CA handoff from verified host custody.
package clientsetup

import (
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/kombifyio/stackkits/internal/actionableerror"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/pkg/models"
)

const Path = "/device/setup"

//go:embed enroll-linux.sh
var linuxAdapter string

type Profile struct {
	Domain      string
	Address     string
	Fingerprint string
	RootPEM     string
	Linux       string
}

func (p Profile) SetupURL() string { return URL(p.Address) }

func (p Profile) LinuxSHA256() string {
	digest := sha256.Sum256([]byte(p.Linux))
	return hex.EncodeToString(digest[:])
}

// loadPublic uses only the host-written public Authority projection mounted in
// the Hub. The Owner key and service secrets remain on the host.
func loadPublic(root string) (Profile, error) {
	authority, err := localevidence.CurrentHomeAuthority(root)
	if err != nil {
		return Profile{}, err
	}
	return build(authority.IdentityDomain, authority.LANSiteAddress, []byte(authority.StepCARootCertificatePEM))
}

func URL(address string) string {
	return "http://" + net.JoinHostPort(address, "80") + Path
}

// Load accepts no network-supplied root, resolver or domain.
func Load(wd string) (Profile, error) {
	custody, err := localevidence.LoadBasementRuntimeCustody(wd)
	if err != nil {
		return Profile{}, err
	}
	domain := custody.Domain
	address, err := localevidence.BasementLANDNSResolverAddress(wd)
	if err != nil {
		return Profile{}, err
	}
	raw, _, err := localevidence.BasementStepCARootCAPEM(wd)
	if err != nil {
		return Profile{}, err
	}
	return build(domain, address, raw)
}

func build(domain, address string, raw []byte) (Profile, error) {
	if !models.IsLocalDomain(domain) || !strings.Contains(domain, ".") || models.IsKombifyMeDomain(domain) {
		return Profile{}, errors.New("client_trust_profile_invalid: this domain uses public DNS and certificates; open its printed HTTPS Hub URL")
	}

	resolverIP, err := netip.ParseAddr(address)
	if err != nil || resolverIP.Zone() != "" || resolverIP.IsLoopback() || !resolverIP.IsGlobalUnicast() {
		return Profile{}, errors.New("client_trust_profile_invalid: resolver must be a site IP address")
	}
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" || len(strings.TrimSpace(string(rest))) != 0 {
		return Profile{}, errors.New("client_trust_profile_invalid: expected one public CA certificate")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !certificate.IsCA {
		return Profile{}, errors.New("client_trust_profile_invalid: invalid public Owner CA")
	}
	if certificate.PublicKeyAlgorithm == x509.Ed25519 {
		return Profile{}, actionableerror.WithDiagnostic("client_trust_browser_ca_unsupported", errors.New("browser-facing CA requires a supported X.509 algorithm"))
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return Profile{}, errors.New("client_trust_profile_invalid: invalid private DNS label")
		}
		for _, c := range label {
			if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return Profile{}, errors.New("client_trust_profile_invalid: invalid private DNS label")
			}
		}
	}
	digest := sha256.Sum256(certificate.Raw)
	hexDigest := hex.EncodeToString(digest[:])
	zoneDigest := sha256.Sum256([]byte(domain))
	id := hexDigest[:16] + "-" + hex.EncodeToString(zoneDigest[:4])
	header := fmt.Sprintf("#!/bin/sh\n# Public StackKits client trust; no account or device credential.\nset -eu\nZONE='%s'\nRESOLVER='%s'\nCA_SHA256='%s'\nPROFILE_ID='%s'\n", domain, address, hexDigest, id)
	diagnostics, err := diagnosticShell()
	if err != nil {
		return Profile{}, err
	}
	rootPEM := string(pem.EncodeToMemory(block))
	body := header + diagnostics + "OWNER_CA=$(cat <<'STACKKIT_PUBLIC_CA'\n" + rootPEM + "STACKKIT_PUBLIC_CA\n)\n" + linuxAdapter
	groups := make([]string, len(digest))
	for i, b := range digest {
		groups[i] = fmt.Sprintf("%02X", b)
	}
	return Profile{Domain: domain, Address: address, Fingerprint: "SHA256:" + strings.Join(groups, ":"), RootPEM: rootPEM, Linux: body}, nil
}

func diagnosticShell() (string, error) {
	var handler strings.Builder
	handler.WriteString("fail() {\ncase \"$1\" in\n")
	for _, code := range []string{"client_trust_approval_required", "client_trust_browser_user_missing", "client_trust_adapter_unavailable", "client_trust_dns_upstream_unsupported", "client_trust_ca_mismatch", "client_trust_local_conflict", "client_trust_profile_missing", "unknown_failure"} {
		diagnostic, ok := actionableerror.Diagnostic(code)
		if !ok {
			return "", fmt.Errorf("client trust diagnostic %s is not registered", code)
		}
		message := code + ": " + diagnostic.Message + " Next: " + strings.Join(diagnostic.UserGuidance, " ")
		pattern := code
		if code == "unknown_failure" {
			pattern = "*"
		}
		fmt.Fprintf(&handler, "%s) printf '%%s\\n' %s >&2 ;;\n", pattern, "'"+strings.ReplaceAll(message, "'", "'\\''")+"'")
	}
	handler.WriteString("esac\n[ -z \"${2:-}\" ] || printf 'Context: %s\\n' \"$2\" >&2\nexit 2\n}\n")
	return handler.String(), nil
}
