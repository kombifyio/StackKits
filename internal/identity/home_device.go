package identity

import (
	"bytes"
	"context"
	"crypto"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"io/fs"
	"net"
	"sort"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

const (
	homeDeviceRegistryPath     = ".stackkit/custody/home-verifier/devices.json"
	homeDeviceRegistryMaxBytes = 512 << 10
	homeDeviceRegistrySchema   = "stackkit.home-device-registry/v1"
)

var (
	ErrHomeDeviceRevoked  = errors.New("identity: this device key was revoked and cannot enroll again; generate a new key")
	ErrHomeDeviceConflict = errors.New("identity: another active device already uses this name")
	ErrHomeDeviceNotFound = errors.New("identity: no enrolled device has this id")
)

// HomeDeviceIssuer turns an admitted device CSR into a certificate chain
// through the step-ca device provisioner. Production uses
// localevidence.IssueHomeDeviceCertificate.
type HomeDeviceIssuer func(ctx context.Context, csrPEM []byte, ttlSeconds int) ([]byte, error)

// HomeDevice is the public registry record of one enrolled device key. It
// holds no private key, pairing code or certificate body.
type HomeDevice struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	OwnerSubject string     `json:"ownerSubject"`
	SerialNumber string     `json:"serialNumber"`
	EnrolledAt   time.Time  `json:"enrolledAt"`
	NotAfter     time.Time  `json:"notAfter"`
	Revoked      bool       `json:"revoked"`
	RevokedAt    *time.Time `json:"revokedAt,omitempty"`
}

type homeDeviceRegistry struct {
	Schema  string       `json:"schema"`
	Devices []HomeDevice `json:"devices"`
}

// EnrollHomeDeviceWithPairing completes the shared enrollment ceremony used by
// the State Console code and the CLI token fallback: it redeems the single-use
// pairing (Owner step-up, LAN, proof of possession), refuses a revoked key or a
// name held by another active device, has the device provisioner issue a
// certificate bounded by credentialTTLSeconds, and records the device.
func EnrollHomeDeviceWithPairing(ctx context.Context, root string, request HomeEnrollmentRequest, issue HomeDeviceIssuer, now time.Time) (HomeDevice, []byte, error) {
	now = now.UTC()
	if issue == nil {
		return HomeDevice{}, nil, errors.New("identity: no device provisioner is bound")
	}
	grant, err := RedeemHomePairing(root, request, now)
	if err != nil {
		return HomeDevice{}, nil, err
	}
	if !localevidence.ValidHomeDeviceName(grant.DeviceCommonName) || grant.CredentialTTLSeconds < 300 || grant.CredentialTTLSeconds > 86400 {
		return HomeDevice{}, nil, ErrHomePairingDenied
	}
	registry, err := loadHomeDeviceRegistry(root)
	if err != nil {
		return HomeDevice{}, nil, err
	}
	for _, device := range registry.Devices {
		if device.ID == grant.DeviceKeyThumbprint && device.Revoked {
			return HomeDevice{}, nil, ErrHomeDeviceRevoked
		}
		if device.Name == grant.DeviceCommonName && device.ID != grant.DeviceKeyThumbprint && !device.Revoked && now.Before(device.NotAfter) {
			return HomeDevice{}, nil, ErrHomeDeviceConflict
		}
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: grant.CSRDER})
	chain, err := issue(ctx, csrPEM, grant.CredentialTTLSeconds)
	if err != nil {
		return HomeDevice{}, nil, err
	}
	leaf, err := homeDeviceLeaf(chain, grant.DeviceKeyThumbprint, grant.CredentialTTLSeconds)
	if err != nil {
		return HomeDevice{}, nil, err
	}
	device := HomeDevice{
		ID: grant.DeviceKeyThumbprint, Name: grant.DeviceCommonName, OwnerSubject: grant.OwnerSubject,
		SerialNumber: leaf.SerialNumber.Text(16), EnrolledAt: now, NotAfter: leaf.NotAfter.UTC(),
	}
	kept := registry.Devices[:0]
	for _, existing := range registry.Devices {
		if existing.ID != device.ID {
			kept = append(kept, existing)
		}
	}
	registry.Devices = append(kept, device)
	if err := writeHomeDeviceRegistry(root, registry); err != nil {
		return HomeDevice{}, nil, err
	}
	return device, chain, nil
}

// ListHomeDevices returns enrolled and revoked devices, newest first.
func ListHomeDevices(root string) ([]HomeDevice, error) {
	registry, err := loadHomeDeviceRegistry(root)
	if err != nil {
		return nil, err
	}
	devices := append(make([]HomeDevice, 0, len(registry.Devices)), registry.Devices...)
	sort.SliceStable(devices, func(i, j int) bool { return devices[i].EnrolledAt.After(devices[j].EnrolledAt) })
	return devices, nil
}

// RevokeHomeDevice permanently revokes a device key. Revocation only removes
// access, so it is a local Owner operation without a fresh step-up; the key
// can never enroll again.
func RevokeHomeDevice(root, id string, now time.Time) (HomeDevice, error) {
	registry, err := loadHomeDeviceRegistry(root)
	if err != nil {
		return HomeDevice{}, err
	}
	for index := range registry.Devices {
		device := &registry.Devices[index]
		if device.ID != id {
			continue
		}
		if !device.Revoked {
			at := now.UTC()
			device.Revoked, device.RevokedAt = true, &at
			if err := writeHomeDeviceRegistry(root, registry); err != nil {
				return HomeDevice{}, err
			}
		}
		return *device, nil
	}
	return HomeDevice{}, ErrHomeDeviceNotFound
}

// ObservedLANAddress classifies a transport peer address the server observed
// (a connection RemoteAddr or the SSH client address of a node-local CLI). It
// admits loopback, private (RFC 1918 / ULA) and link-local peers only; it is
// never read from a request header or body.
func ObservedLANAddress(address string) bool {
	host := address
	if h, _, err := net.SplitHostPort(address); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

func homeDeviceLeaf(chain []byte, thumbprint string, ttl int) (*x509.Certificate, error) {
	block, _ := pem.Decode(chain)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("identity: device provisioner returned no certificate")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, errors.New("identity: device provisioner returned an unreadable certificate")
	}
	issued, err := homeKeyThumbprint(leaf.PublicKey)
	if err != nil || issued != thumbprint || leaf.NotAfter.Sub(leaf.NotBefore) > time.Duration(ttl)*time.Second {
		return nil, errors.New("identity: device certificate does not match the enrolled key and credential lifetime")
	}
	return leaf, nil
}

func homeKeyThumbprint(key any) (string, error) {
	thumb, err := (&jose.JSONWebKey{Key: key}).Thumbprint(crypto.SHA256)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(thumb), nil
}

func loadHomeDeviceRegistry(root string) (homeDeviceRegistry, error) {
	registry := homeDeviceRegistry{Schema: homeDeviceRegistrySchema}
	raw, err := readHomePrivate(root, homeDeviceRegistryPath, homeDeviceRegistryMaxBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return registry, nil
	}
	if err != nil {
		return registry, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&registry) != nil || decoder.Decode(new(any)) != io.EOF || registry.Schema != homeDeviceRegistrySchema {
		return registry, errors.New("identity: unreadable device registry")
	}
	return registry, nil
}

func writeHomeDeviceRegistry(root string, registry homeDeviceRegistry) error {
	registry.Schema = homeDeviceRegistrySchema
	raw, err := json.Marshal(registry)
	if err != nil {
		return err
	}
	return writeHomePrivate(root, homeDeviceRegistryPath, raw, false)
}
