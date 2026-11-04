package localevidence

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"slices"
)

// basementRetiredDeviceProvisionerName is the step-ca provisioner that once
// issued X.509 household device certificates. Devices now hold a key-bound
// device credential (internal/identity); step-ca issues server and workload
// certificates only, and the origin upgrade removes this provisioner from an
// installed configuration.
const basementRetiredDeviceProvisionerName = "stackkits-home-device"

// HomeEnrollmentServerName is the first DNS name of the LAN enrollment
// listener's server certificate; the Hub host is its only other name
// (HomeEnrollmentHubHost). It sits under the reserved private-use
// ".internal" TLD so it never collides with a routed site name; a device
// connects to the listener's site address and verifies this name against the
// step-ca root it pinned from the pairing payload.
const HomeEnrollmentServerName = "stackkit-home-enroll.internal"

// homeEnrollmentListenerTTLSeconds is the listener certificate's lifetime.
const homeEnrollmentListenerTTLSeconds = 86400

// HomeEnrollmentListenerNames are exactly the DNS names of the LAN enrollment
// listener's certificate: HomeEnrollmentServerName, which native clients pin
// with the root, and the Hub host (when the identity runtime address exists),
// which the installed Hub PWA opens on the listener's port from home.
func HomeEnrollmentListenerNames(hubHost string) []string {
	if hubHost == "" {
		return []string{HomeEnrollmentServerName}
	}
	return []string{HomeEnrollmentServerName, hubHost}
}

// IssueHomeEnrollmentListenerCertificate is the host side of the listener
// certificate (homelab identity target §4, StackKits enpy): the core server
// generates the key and sends only its CSR; this side, which holds Owner
// custody, admits the CSR only for exactly the listener's own names derived
// from custody (never a name the caller chooses), a P-256 key and no other
// subject alternative name, signs step-ca's one-time token with the Owner key
// and returns the verified chain (PEM). A CSR for any other name is refused,
// so a compromised core cannot obtain a certificate for another host.
func IssueHomeEnrollmentListenerCertificate(ctx context.Context, root string, csrDER []byte) ([]byte, error) {
	if len(csrDER) == 0 || len(csrDER) > 8<<10 {
		return nil, errors.New("localevidence: the listener certificate request is empty or oversized")
	}
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil || csr.CheckSignature() != nil {
		return nil, errors.New("localevidence: the listener certificate request does not verify")
	}
	hub, _ := HomeEnrollmentHubHost(root)
	key, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok || key.Curve != elliptic.P256() || csr.Subject.CommonName != HomeEnrollmentServerName ||
		!slices.Equal(csr.DNSNames, HomeEnrollmentListenerNames(hub)) ||
		len(csr.IPAddresses)+len(csr.URIs)+len(csr.EmailAddresses) != 0 || len(csr.Extensions) > 1 {
		return nil, errors.New("localevidence: the listener certificate request names more than the enrollment listener")
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	return issueBasementWorkloadCertificate(ctx, root, csr, csrPEM, homeEnrollmentListenerTTLSeconds, basementOriginProvisionerName, x509.ExtKeyUsageServerAuth, nil)
}

// HomeEnrollmentHubHost is the Hub host (the base host of the identity
// runtime address). The LAN enrollment listener serves it as its second name
// so the installed Hub PWA, a browser, can reach the listener directly.
func HomeEnrollmentHubHost(root string) (string, error) {
	address, err := LocalIdentityRuntimeAddress(root)
	if err != nil {
		return "", err
	}
	return address.ServiceHost("base"), nil
}
