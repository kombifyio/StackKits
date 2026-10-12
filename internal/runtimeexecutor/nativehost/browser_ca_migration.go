package nativehost

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"go.step.sm/crypto/pemutil"
)

const basementACMEVolumeName = "stackkit-basement-core_basement-tls-acme"

// RebindBasementBrowserCAIngress preserves leaf keys, identities and ACME
// account custody. A CA-key change renews only authenticated existing leaves
// through the owner provisioner; a root-only change replaces the intermediate.
// The caller stops the router and reloads step-ca before this operation.
func RebindBasementBrowserCAIngress(ctx context.Context, workspace string) error {
	previous, next, pending, err := localevidence.BasementBrowserCAMigrationChain(workspace)
	if err != nil || !pending {
		return err
	}
	raw, err := (osStandaloneComposeDockerCLI{}).Run(ctx, []string{"volume", "inspect", basementACMEVolumeName})
	if err != nil {
		return err
	}
	var volumes []dockerVolumeInspect
	if err := json.Unmarshal(raw, &volumes); err != nil {
		return err
	}
	if len(volumes) != 1 {
		return errors.New("browser CA ingress requires one exact native ACME volume")
	}
	volume := volumes[0]
	if volume.Name != basementACMEVolumeName || volume.Driver != "local" || volume.Scope != "local" ||
		volume.Labels[composeProjectLabel] != "stackkit-basement-core" || volume.Labels[composeVolumeLabel] != "basement-tls-acme" ||
		volume.Mountpoint != filepath.Join("/var/lib/docker/volumes", basementACMEVolumeName, "_data") || len(volume.Options) != 0 {
		return errors.New("browser CA ingress volume differs from the installed native router custody")
	}
	root, err := confinedfs.Open(volume.Mountpoint)
	if err != nil {
		return err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	before, _, err := tx.ReadStableBounded("acme.json", 4<<20)
	if err != nil {
		return err
	}
	after, changed, err := rebindBrowserCAACME(before, previous, next, func(leaf *x509.Certificate, private crypto.Signer) ([]byte, error) {
		csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: leaf.Subject, DNSNames: leaf.DNSNames}, private)
		if err != nil {
			return nil, err
		}
		// Renewal does not extend the old lifetime unless it has entered the
		// provisioner's five-minute renewal floor.
		ttl := min(86400, max(300, int(time.Until(leaf.NotAfter).Seconds())))
		policy := architecturev2renderer.BridgeOriginMTLSPublicationPolicy{IdentityRef: leaf.Subject.CommonName, ServerName: leaf.DNSNames[0], MinimumTLSVersion: "TLS1.3", CredentialTTLSeconds: ttl}
		return localevidence.IssueBasementOriginCertificate(ctx, workspace, policy, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}))
	})
	if err != nil || !changed {
		return err
	}
	// Retain the original private ACME generation on this installed node only.
	// Its account and leaf private keys never enter results or command arguments.
	privateRoot, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer privateRoot.Close()
	privateView, err := privateRoot.View(".")
	if err != nil {
		return err
	}
	record := struct {
		Volume   string `json:"volume"`
		Previous []byte `json:"previous"`
		Updated  []byte `json:"updated"`
	}{volume.Name, before, after}
	evidence, err := json.Marshal(record)
	if err != nil {
		return err
	}
	signature, err := localevidence.SignOwnerPolicyState(workspace, evidence)
	if err != nil {
		return err
	}
	journal, err := json.Marshal(struct {
		Evidence  json.RawMessage                         `json:"evidence"`
		Signature localevidence.OwnerPolicyStateSignature `json:"signature"`
	}{evidence, signature})
	if err != nil {
		return err
	}
	if _, err := privateView.WriteAtomic0600(".stackkit/runtime/basement-core/browser-ca-acme-recovery.json", journal); err != nil {
		return err
	}
	view, err := root.View(".")
	if err != nil {
		return err
	}
	current, _, err := tx.ReadStableBounded("acme.json", 4<<20)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, before) {
		return errors.New("native ACME custody changed after router stop")
	}
	_, err = view.WriteAtomic0600("acme.json", after)
	return err
}

func rebindBrowserCAACME(raw, previous, next []byte, renew func(*x509.Certificate, crypto.Signer) ([]byte, error)) ([]byte, bool, error) {
	previousBlock, _ := pem.Decode(previous)
	nextBlock, _ := pem.Decode(next)
	if previousBlock == nil || nextBlock == nil {
		return nil, false, errors.New("browser CA migration intermediate is unavailable")
	}
	previousCert, err := x509.ParseCertificate(previousBlock.Bytes)
	if err != nil {
		return nil, false, err
	}
	nextCert, err := x509.ParseCertificate(nextBlock.Bytes)
	if err != nil {
		return nil, false, err
	}
	if !previousCert.IsCA || !nextCert.IsCA || !bytes.Equal(previousCert.RawSubject, nextCert.RawSubject) {
		return nil, false, errors.New("browser CA migration must preserve the authenticated runtime intermediate identity")
	}
	issuerChanged := !bytes.Equal(previousCert.RawSubjectPublicKeyInfo, nextCert.RawSubjectPublicKeyInfo)
	var providers map[string]json.RawMessage
	if err := json.Unmarshal(raw, &providers); err != nil {
		return nil, false, err
	}
	var resolver map[string]json.RawMessage
	if err := json.Unmarshal(providers["stackkits"], &resolver); err != nil {
		return nil, false, err
	}
	var certificates []map[string]json.RawMessage
	if err := json.Unmarshal(resolver["Certificates"], &certificates); err != nil {
		return nil, false, err
	}
	changed := false
	for i, certificate := range certificates {
		var encoded string
		if err := json.Unmarshal(certificate["certificate"], &encoded); err != nil {
			return nil, false, err
		}
		chain, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil {
			return nil, false, err
		}
		var rebuilt []byte
		replaced := false
		leafBlock, _ := pem.Decode(chain)
		if leafBlock == nil || leafBlock.Type != "CERTIFICATE" {
			return nil, false, errors.New("native ACME leaf is unreadable")
		}
		leaf, err := x509.ParseCertificate(leafBlock.Bytes)
		if err != nil {
			return nil, false, err
		}
		for remaining := chain; len(remaining) > 0; {
			block, rest := pem.Decode(remaining)
			if block == nil {
				if len(bytes.TrimSpace(remaining)) != 0 {
					return nil, false, errors.New("native ACME chain is unreadable")
				}
				rebuilt = append(rebuilt, remaining...)
				break
			}
			if block.Type == "CERTIFICATE" && bytes.Equal(block.Bytes, previousCert.Raw) {
				rebuilt = append(rebuilt, next...)
				replaced = true
			} else {
				rebuilt = append(rebuilt, remaining[:len(remaining)-len(rest)]...)
			}
			remaining = rest
		}
		if replaced {
			if err := leaf.CheckSignatureFrom(previousCert); err != nil {
				return nil, false, errors.New("native ACME leaf does not belong to the authenticated previous issuer")
			}
			if issuerChanged {
				var domain struct {
					Main string   `json:"main"`
					SANs []string `json:"sans"`
				}
				if err := json.Unmarshal(certificate["domain"], &domain); err != nil || domain.Main == "" || len(domain.SANs) != 0 || !slices.Equal(leaf.DNSNames, []string{domain.Main}) ||
					leaf.IsCA || len(leaf.IPAddresses)+len(leaf.URIs)+len(leaf.EmailAddresses) != 0 || leaf.Subject.CommonName == "" {
					return nil, false, errors.New("browser CA renewal requires the existing exact single-DNS ingress identity")
				}
				var encodedKey string
				if err := json.Unmarshal(certificate["key"], &encodedKey); err != nil {
					return nil, false, err
				}
				keyPEM, err := base64.StdEncoding.Strict().DecodeString(encodedKey)
				if err != nil {
					return nil, false, errors.New("native ACME leaf key is unreadable")
				}
				parsed, err := pemutil.Parse(keyPEM)
				private, ok := parsed.(crypto.Signer)
				if err != nil || !ok {
					return nil, false, errors.New("native ACME leaf key is not a signer")
				}
				public, err := x509.MarshalPKIXPublicKey(private.Public())
				if err != nil || !bytes.Equal(public, leaf.RawSubjectPublicKeyInfo) || renew == nil {
					return nil, false, errors.New("native ACME leaf key does not prove the existing certificate identity")
				}
				rebuilt, err = renew(leaf, private)
				if err != nil {
					return nil, false, err
				}
			}
			certificates[i]["certificate"], err = json.Marshal(base64.StdEncoding.EncodeToString(rebuilt))
			if err != nil {
				return nil, false, err
			}
			changed = true
		}
	}
	if !changed {
		return raw, false, nil
	}
	encoded, err := json.Marshal(certificates)
	if err != nil {
		return nil, false, err
	}
	resolver["Certificates"] = encoded
	encoded, err = json.Marshal(resolver)
	if err != nil {
		return nil, false, err
	}
	providers["stackkits"] = encoded
	encoded, err = json.MarshalIndent(providers, "", "  ")
	if err != nil {
		return nil, false, fmt.Errorf("encode native ACME CA transition: %w", err)
	}
	return encoded, true, nil
}
