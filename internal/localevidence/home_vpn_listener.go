package localevidence

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"net"
	"net/netip"
	"slices"
)

// HomeVPNServerName is the first DNS name of the VPN add-on peer service's
// certificate; native device clients pin it with the step-ca root.
const HomeVPNServerName = "stackkit-home-vpn.internal"

const homeVPNListenerTTLSeconds = 86400

// IssueHomeVPNPeerServiceCertificate obtains the VPN add-on peer service's
// TLS identity from the node-local step-ca through the owner-authorized
// serverAuth provisioner, on the host that holds Owner custody. The leaf
// names HomeVPNServerName, the node site address and the Owner-declared
// endpoint host (a DNS name or an address), and nothing else. The private key
// is generated here and never leaves the process.
func IssueHomeVPNPeerServiceCertificate(ctx context.Context, root, endpointHost string) (tls.Certificate, error) {
	site, err := BasementLANDNSResolverAddress(root)
	if err != nil {
		return tls.Certificate{}, err
	}
	siteAddress, err := netip.ParseAddr(site)
	if err != nil {
		return tls.Certificate{}, err
	}
	names := []string{HomeVPNServerName}
	addresses := []net.IP{siteAddress.AsSlice()}
	if endpointHost != "" {
		if address, parseErr := netip.ParseAddr(endpointHost); parseErr == nil {
			if address != siteAddress {
				addresses = append(addresses, address.AsSlice())
			}
		} else if !slices.Contains(names, endpointHost) {
			names = append(names, endpointHost)
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: HomeVPNServerName}, DNSNames: names, IPAddresses: addresses}, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		return tls.Certificate{}, err
	}
	chainPEM, err := issueBasementWorkloadCertificate(ctx, root, csr, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), homeVPNListenerTTLSeconds, basementOriginProvisionerName, x509.ExtKeyUsageServerAuth, nil)
	if err != nil {
		return tls.Certificate{}, err
	}
	certificate := tls.Certificate{PrivateKey: key}
	for rest := chainPEM; ; {
		var block *pem.Block
		if block, rest = pem.Decode(rest); block == nil {
			break
		}
		certificate.Certificate = append(certificate.Certificate, block.Bytes)
	}
	if len(certificate.Certificate) == 0 {
		return tls.Certificate{}, errors.New("localevidence: empty VPN peer service certificate chain")
	}
	if certificate.Leaf, err = x509.ParseCertificate(certificate.Certificate[0]); err != nil {
		return tls.Certificate{}, err
	}
	return certificate, nil
}

// HomeVPNCARootPEM is the established step-ca root a device installs to trust
// the peer service and, for Headscale, its control endpoint.
func HomeVPNCARootPEM(root string) (string, error) {
	owner, err := LoadOwnerCustody(root)
	if err != nil {
		return "", err
	}
	if block, _ := pem.Decode([]byte(owner.StepCARootCertificatePEM)); block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("localevidence: no established step-ca root")
	}
	return owner.StepCARootCertificatePEM, nil
}
