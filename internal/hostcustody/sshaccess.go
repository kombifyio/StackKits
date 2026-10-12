package hostcustody

import (
	"errors"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

const (
	sshAccessName       = "ssh-access.json"
	sshAccessAPIVersion = "stackkit.ssh-access-custody/v1"
)

// SSHAccess is the SSH access this node was told to hold: the owner's user CA
// trust with the principals each local user accepts, and the host
// certificate. None of it is secret, but it is integrity-critical, so it lives
// in owner-signed custody and host security repair reinstalls it from here.
type SSHAccess struct {
	APIVersion string              `json:"apiVersion"`
	CAKeys     []string            `json:"caKeys"`
	Principals map[string][]string `json:"principals"`
	// TrustRef and TrustVersion name the delivery the trust came from.
	TrustRef     string `json:"trustRef,omitempty"`
	TrustVersion uint64 `json:"trustVersion,omitempty"`
	// HostCertificate is one OpenSSH host certificate line.
	HostCertificate        string `json:"hostCertificate,omitempty"`
	HostCertificateRef     string `json:"hostCertificateRef,omitempty"`
	HostCertificateVersion uint64 `json:"hostCertificateVersion,omitempty"`
	// CleanupPending names target-side effects of a revocation that could not
	// be completed yet (for example an sshd reload).
	CleanupPending []string                                `json:"cleanupPending,omitempty"`
	UpdatedAt      string                                  `json:"updatedAt"`
	Signature      localevidence.OwnerPolicyStateSignature `json:"signature"`
}

// LoadSSHAccess reads the recorded SSH access. A node that never received one
// is ErrMissing.
func LoadSSHAccess(store Store) (SSHAccess, error) {
	var record SSHAccess
	if err := store.Read(sshAccessName, maxRecordBytes, &record, &record.Signature); err != nil {
		return SSHAccess{}, err
	}
	if record.APIVersion != sshAccessAPIVersion {
		return SSHAccess{}, errors.New("hostcustody: SSH access record is invalid")
	}
	return record, nil
}

// SaveSSHAccess records the SSH access this node must hold.
func SaveSSHAccess(store Store, record SSHAccess) error {
	_, err := LoadSSHAccess(store)
	if err != nil && !errors.Is(err, ErrMissing) {
		return err
	}
	record.APIVersion = sshAccessAPIVersion
	if record.CAKeys == nil {
		record.CAKeys = []string{}
	}
	if record.Principals == nil {
		record.Principals = map[string][]string{}
	}
	return store.Write(sshAccessName, &record, &record.Signature, errors.Is(err, ErrMissing))
}
