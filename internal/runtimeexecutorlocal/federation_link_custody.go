package runtimeexecutorlocal

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// WireGuardFabricCustody resolves an already-configured external fabric into
// local runtime handles. It is private Owner custody, never a StackSpec field
// or executor artifact. The external fabric keeps keys, endpoint discovery and
// interface creation; StackKits controls only this interface's local activation.
type WireGuardFabricCustody struct {
	BindingHash           string `json:"bindingHash"`
	FabricRef             string `json:"fabricRef"`
	CustodyAttestationRef string `json:"custodyAttestationRef"`
	SiteKind              string `json:"siteKind"`
	PeerPublicKey         string `json:"peerPublicKey"`
	PeerAddress           string `json:"peerAddress"`
	OriginServerName      string `json:"originServerName"`
	OriginSocket          string `json:"originSocket"`
	PeerRef               string `json:"peerRef"`
	ServicePort           uint16 `json:"servicePort"`
}

type wireGuardCustodyRecord struct {
	Schema    string                                  `json:"schema"`
	Binding   localevidence.LocalBinding              `json:"binding"`
	Fabric    WireGuardFabricCustody                  `json:"fabric"`
	Activated bool                                    `json:"activated,omitempty"`
	Signature localevidence.OwnerPolicyStateSignature `json:"signature"`
}

func WireGuardFabricInterface(fabricRef string) string {
	digest := sha256.Sum256([]byte(fabricRef))
	return "skf" + hex.EncodeToString(digest[:])[:12]
}

func wireGuardCustodyPath(fabricRef string) string {
	return ".stackkit/custody/federation-link/" + WireGuardFabricInterface(fabricRef) + ".json"
}

func validateWireGuardCustody(c WireGuardFabricCustody) error {
	key, keyErr := base64.StdEncoding.DecodeString(c.PeerPublicKey)
	if keyErr != nil || len(key) != 32 || base64.StdEncoding.EncodeToString(key) != c.PeerPublicKey {
		return errors.New("federation: exact public WireGuard peer key required")
	}
	if !validCoreHostBootstrapDigest(c.BindingHash) || !strings.HasPrefix(c.FabricRef, "federation-link-fabric://sha256/") ||
		!validCoreHostBootstrapDigest("sha256:"+strings.TrimPrefix(c.FabricRef, "federation-link-fabric://sha256/")) ||
		!strings.HasPrefix(c.CustodyAttestationRef, "federation-link-custody-attestation://sha256/") ||
		!validCoreHostBootstrapDigest("sha256:"+strings.TrimPrefix(c.CustodyAttestationRef, "federation-link-custody-attestation://sha256/")) ||
		(c.SiteKind != "home" && c.SiteKind != "cloud") || c.ServicePort == 0 || c.OriginServerName == "" {
		return errors.New("federation: exact external fabric custody required")
	}
	peer, err := netip.ParseAddr(c.PeerAddress)
	if err != nil || !peer.Is4() || !peer.IsPrivate() || peer.IsLoopback() || peer.String() != c.PeerAddress {
		return errors.New("federation: one canonical private IPv4 peer address required")
	}
	host, port, err := net.SplitHostPort(c.OriginSocket)
	ip := net.ParseIP(host)
	number, numberErr := strconv.Atoi(port)
	if err != nil || ip == nil || !ip.IsLoopback() || numberErr != nil || number < 1 || number > 65535 {
		return errors.New("federation: external fabric must supply one literal loopback origin socket")
	}
	if c.SiteKind == "cloud" && !localevidence.ValidWorkloadPeerRef(c.PeerRef) {
		return errors.New("federation: installed Cloud workload peer required")
	}
	return nil
}

// BindWireGuardFabric records the local Owner's adoption of external handles.
// It does not create a fabric, select peers, copy keys or activate networking.
func BindWireGuardFabric(root string, custody WireGuardFabricCustody) error {
	if err := validateWireGuardCustody(custody); err != nil {
		return err
	}
	owner, err := localevidence.LoadOwnerCustody(root)
	if err != nil {
		return err
	}
	record := wireGuardCustodyRecord{Schema: "stackkit.wireguard-fabric-custody/v1", Binding: owner.Binding, Fabric: custody}
	if previous, err := loadWireGuardCustody(root, custody.FabricRef); err == nil {
		if previous.Binding != owner.Binding {
			return errors.New("federation: existing fabric custody belongs to another target")
		}
		if previous.Fabric == custody {
			return nil
		}
		if previous.Activated {
			return errors.New("federation: stop current local activation before replacing its custody")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeWireGuardCustody(root, record)
}

func writeWireGuardCustody(root string, record wireGuardCustodyRecord) error {
	record.Signature = localevidence.OwnerPolicyStateSignature{}
	unsigned, err := json.Marshal(record)
	if err != nil {
		return err
	}
	record.Signature, err = localevidence.SignOwnerPolicyState(root, unsigned)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	fs, err := confinedfs.Open(root)
	if err != nil {
		return err
	}
	defer fs.Close()
	tx, err := fs.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	path := wireGuardCustodyPath(record.Fabric.FabricRef)
	if err := tx.MkdirAll(filepath.ToSlash(filepath.Dir(path)), 0700); err != nil {
		return err
	}
	view, err := fs.View(".")
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600(path, raw)
	if err != nil {
		return err
	}
	if !result.Installed || !result.FileSynced {
		return errors.New("federation: custody write was not durably installed")
	}
	_, err = tx.SyncDirectory(filepath.ToSlash(filepath.Dir(path)))
	return err
}

func setWireGuardActivation(root, fabricRef string, active bool) error {
	record, err := loadWireGuardCustody(root, fabricRef)
	if err != nil {
		return err
	}
	record.Activated = active
	return writeWireGuardCustody(root, record)
}

func activatedWireGuardCustodies(root string) ([]wireGuardCustodyRecord, error) {
	fs, err := confinedfs.Open(root)
	if err != nil {
		return nil, err
	}
	defer fs.Close()
	tx, err := fs.BeginTransaction()
	if err != nil {
		return nil, err
	}
	defer tx.Close()
	entries, err := tx.Walk(".stackkit/custody/federation-link")
	if err != nil {
		return nil, err
	}
	var result []wireGuardCustodyRecord
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Path, ".json") {
			continue
		}
		raw, _, err := tx.ReadStableBounded(entry.Path, 64<<10)
		if err != nil {
			return nil, err
		}
		var hint wireGuardCustodyRecord
		if err := json.Unmarshal(raw, &hint); err != nil {
			return nil, err
		}
		if entry.Path != wireGuardCustodyPath(hint.Fabric.FabricRef) {
			return nil, errors.New("federation: substituted custody path")
		}
		record, err := loadWireGuardCustody(root, hint.Fabric.FabricRef)
		if err != nil {
			return nil, err
		}
		if record.Activated {
			result = append(result, record)
		}
	}
	return result, nil
}

func loadWireGuardCustody(root, fabricRef string) (wireGuardCustodyRecord, error) {
	var record wireGuardCustodyRecord
	fs, err := confinedfs.Open(root)
	if err != nil {
		return record, err
	}
	defer fs.Close()
	tx, err := fs.BeginTransaction()
	if err != nil {
		return record, err
	}
	defer tx.Close()
	raw, _, err := tx.ReadStableBounded(wireGuardCustodyPath(fabricRef), 64<<10)
	if err != nil {
		return record, err
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return record, err
	}
	signature := record.Signature
	record.Signature = localevidence.OwnerPolicyStateSignature{}
	unsigned, err := json.Marshal(record)
	if err != nil {
		return record, err
	}
	if err := localevidence.VerifyOwnerPolicyState(root, unsigned, signature); err != nil {
		return record, err
	}
	if record.Schema != "stackkit.wireguard-fabric-custody/v1" || record.Fabric.FabricRef != fabricRef {
		return record, errors.New("federation: substituted external fabric custody")
	}
	return record, validateWireGuardCustody(record.Fabric)
}
