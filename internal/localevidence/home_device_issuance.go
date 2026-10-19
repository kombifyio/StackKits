package localevidence

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
)

// basementDeviceProvisionerName is the step-ca device provisioner shared by
// the State Console ceremony and the CLI token fallback. It is separate from
// the workload peer provisioner so household devices and Cloud workloads never
// share an issuance profile.
const basementDeviceProvisionerName = "stackkits-home-device"

// HomeDeviceOrganizationalUnit marks every device leaf. Verifiers use it to
// tell household device certificates from workload peer certificates that
// chain to the same node-local authority.
const HomeDeviceOrganizationalUnit = "stackkit-home-device"

func basementDeviceProvisioner(root string) (basementStepCAProvisioner, error) {
	key, err := LoadOwnerKey(root)
	if err != nil {
		return basementStepCAProvisioner{}, err
	}
	return basementStepCAProvisioner{
		Type: "JWK", Name: basementDeviceProvisionerName,
		Key:    map[string]string{"kty": "OKP", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(key.Public()), "kid": key.KeyID, "alg": "EdDSA", "use": "sig"},
		Claims: map[string]any{"minTLSCertDuration": "5m", "maxTLSCertDuration": "24h", "defaultTLSCertDuration": "5m", "enableSSHCA": false, "disableRenewal": true},
		// The subject keeps only the device name and adds the fixed device
		// marker; a device has no DNS or IP authority.
		Options: map[string]any{"x509": map[string]any{"template": `{"subject":{"commonName":{{ toJson .Subject.CommonName }},"organizationalUnit":["` + HomeDeviceOrganizationalUnit + `"]},"sans":[],"keyUsage":["digitalSignature"],"extKeyUsage":["clientAuth"]}`}},
	}, nil
}

// ValidHomeDeviceName bounds the device name carried as the certificate
// common name: a lowercase DNS-label-like name, never a URL or principal.
func ValidHomeDeviceName(value string) bool {
	if value == "" || len(value) > 63 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// IssueHomeDeviceCertificate submits a device-generated CSR to the node-local
// step-ca through the device provisioner. It is called only after enrollment
// admission (Owner step-up, LAN, single-use pairing, proof of possession). The
// owner key signs the one-time token; step-ca alone signs the device leaf.
func IssueHomeDeviceCertificate(ctx context.Context, root string, csrPEM []byte, ttl int) ([]byte, error) {
	if ttl < 300 || ttl > 86400 {
		return nil, errors.New("localevidence: device credential TTL outside the enrollment policy")
	}
	block, rest := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(bytes.TrimSpace(rest)) != 0 || len(csrPEM) > 64<<10 {
		return nil, errors.New("localevidence: one device CSR required")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil || !ValidHomeDeviceName(csr.Subject.CommonName) ||
		len(csr.DNSNames)+len(csr.IPAddresses)+len(csr.URIs)+len(csr.EmailAddresses) != 0 {
		return nil, errors.New("localevidence: CSR does not prove a bounded device identity")
	}
	chain, err := issueBasementWorkloadCertificate(ctx, root, csr, csrPEM, ttl, basementDeviceProvisionerName, x509.ExtKeyUsageClientAuth, nil)
	if err != nil {
		return nil, err
	}
	leafBlock, _ := pem.Decode(chain)
	if leafBlock == nil {
		return nil, errors.New("localevidence: empty device certificate chain")
	}
	leaf, err := x509.ParseCertificate(leafBlock.Bytes)
	if err != nil || len(leaf.Subject.OrganizationalUnit) != 1 || leaf.Subject.OrganizationalUnit[0] != HomeDeviceOrganizationalUnit {
		return nil, errors.New("localevidence: step-ca device leaf lacks the device marker")
	}
	return chain, nil
}

// HomeEnrollmentServerName is the only DNS name of the LAN enrollment
// listener's server certificate. It sits under the reserved private-use
// ".internal" TLD so it never collides with a routed site name; a device
// connects to the listener's site address and verifies this name against the
// step-ca root it pinned from the pairing payload.
const HomeEnrollmentServerName = "stackkit-home-enroll.internal"

// IssueHomeEnrollmentServerCertificate obtains the enrollment listener's TLS
// identity from the node-local step-ca through the existing owner-authorized
// serverAuth provisioner. The private key is generated here and never leaves
// the process; the leaf names only HomeEnrollmentServerName.
func IssueHomeEnrollmentServerCertificate(ctx context.Context, root string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: HomeEnrollmentServerName}, DNSNames: []string{HomeEnrollmentServerName}}, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		return tls.Certificate{}, err
	}
	chainPEM, err := issueBasementWorkloadCertificate(ctx, root, csr, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), 86400, basementOriginProvisionerName, x509.ExtKeyUsageServerAuth, nil)
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
		return tls.Certificate{}, errors.New("localevidence: empty enrollment listener certificate chain")
	}
	if certificate.Leaf, err = x509.ParseCertificate(certificate.Certificate[0]); err != nil {
		return tls.Certificate{}, err
	}
	return certificate, nil
}

// HomeEnrollmentCARootSHA256 is the hex SHA-256 of the established step-ca
// root certificate (DER), the value `step ca bootstrap --fingerprint` takes.
// A device pins it before it trusts the enrollment listener.
func HomeEnrollmentCARootSHA256(root string) (string, error) {
	owner, err := LoadOwnerCustody(root)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode([]byte(owner.StepCARootCertificatePEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("localevidence: no established step-ca root")
	}
	sum := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(sum[:]), nil
}
