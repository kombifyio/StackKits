package federationcontrol

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorprocess"
)

const custodyPath = ".stackkit/custody/federation-control/receiver.json"

// HomeTrust is explicit public custody admitted at Cloud. Withdrawing or
// replacing it takes effect on every request, including an existing TLS socket.
type HomeTrust struct {
	OwnerRef                string    `json:"ownerRef"`
	KeyID                   string    `json:"keyId"`
	PublicKey               string    `json:"publicKey"`
	HomeSiteRef             string    `json:"homeSiteRef"`
	ClientCertificateSHA256 string    `json:"clientCertificateSHA256"`
	RootCertificatePEM      string    `json:"rootCertificatePEM"`
	ValidUntil              time.Time `json:"validUntil"`
}

// ReceiverCustody supplies local handles only. It is never an Action payload.
// TLS keys are externally issued and held in confined files, not transported
// in this record or in an action. Only the selected Cloud node may receive.
type ReceiverCustody struct {
	Trust             HomeTrust                                              `json:"trust"`
	Policy            runtimeexecutorlocal.FederationControlAgentApplyPolicy `json:"policy"`
	PlanPath          string                                                 `json:"planPath"`
	PlanHash          string                                                 `json:"planHash"`
	Executable        string                                                 `json:"executable"`
	ExecutableSHA256  string                                                 `json:"executableSHA256"`
	ExecutableVersion string                                                 `json:"executableVersion"`
	ServerName        string                                                 `json:"serverName"`
	CertificatePath   string                                                 `json:"certificatePath"`
	PrivateKeyPath    string                                                 `json:"privateKeyPath"`
	Active            bool                                                   `json:"active"`
}

type signedState struct {
	Schema    string                                  `json:"schema"`
	Binding   localevidence.LocalBinding              `json:"binding"`
	Value     json.RawMessage                         `json:"value"`
	Signature localevidence.OwnerPolicyStateSignature `json:"signature"`
}

func BindReceiver(root string, c ReceiverCustody) error {
	if err := validateCustody(root, c, time.Now().UTC()); err != nil {
		return err
	}
	if _, err := verifyPlanAuthority(root, c); err != nil {
		return err
	}
	// Re-admission is explicit local owner authority. Historical replay records
	// remain in place so restart/rebinding cannot make old nonces executable.
	return writeState(root, custodyPath, c)
}

func WithdrawReceiver(root string) error {
	var c ReceiverCustody
	if err := readState(root, custodyPath, &c); err != nil {
		return err
	}
	c.Active = false
	return writeState(root, custodyPath, c)
}

func loadCustody(root string) (ReceiverCustody, error) {
	var c ReceiverCustody
	if err := readState(root, custodyPath, &c); err != nil {
		return c, err
	}
	return c, validateCustody(root, c, time.Now().UTC())
}

func validateCustody(root string, c ReceiverCustody, now time.Time) error {
	owner, err := localevidence.LoadOwnerCustody(root)
	if err != nil {
		return err
	}
	p := c.Policy
	if !c.Active || p.SiteKind != "cloud" || p.SiteRef != owner.Binding.SiteRef || p.NodeRef != owner.Binding.NodeRef || p.ExecutionChannelRef != owner.Binding.ChannelRef || p.SiteRef == c.Trust.HomeSiteRef || !digestPattern.MatchString(c.PlanHash) || !digestPattern.MatchString(p.ContractHash) || !c.Trust.ValidUntil.After(now) || c.Trust.ValidUntil.Sub(now) > 24*time.Hour {
		return errors.New("control: inactive, expired or foreign Cloud receiver custody")
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(c.Trust.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize || !digestPattern.MatchString(c.Trust.ClientCertificateSHA256) || c.Trust.RootCertificatePEM == "" || c.ServerName == "" {
		return errors.New("control: exact Home public and TLS authority required")
	}
	for _, path := range []string{c.PlanPath, c.CertificatePath, c.PrivateKeyPath} {
		if path == "" || filepath.IsAbs(path) || filepath.ToSlash(filepath.Clean(path)) != path || path == ".." || len(path) > 512 {
			return errors.New("control: confined canonical custody paths required")
		}
	}
	if len(p.Actions) == 0 {
		return errors.New("control: no CUE actions selected")
	}
	for _, action := range p.Actions {
		if action.ID != "plan" && action.ID != "verify" {
			return errors.New("control: requested mutation capability is unavailable")
		}
	}
	if !p.Partition.LocalIdentityAuthorityAvailable || !p.Partition.DenyNewCrossSiteSessions || p.Partition.OnCloudLoss != "local-continues" || p.Partition.OnLinkLoss != "local-continues" || p.Partition.CloudEdge != "fail-closed" {
		return errors.New("control: partition policy differs from local Home authority")
	}
	return runtimeexecutorprocess.ValidateBinding(runtimeexecutorprocess.Binding{ChannelRef: p.ExecutionChannelRef, SiteRef: p.SiteRef, NodeRef: p.NodeRef, Executable: c.Executable, ExecutableSHA256: c.ExecutableSHA256})
}

func readPrivate(root, path string, limit int64) ([]byte, error) {
	return openRead(root, path, limit, true)
}

func openRead(root, path string, limit int64, private bool) ([]byte, error) {
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
	raw, info, err := tx.ReadStableBounded(path, limit)
	if err == nil && private && runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("control: custody file must be private")
	}
	return raw, err
}

func readState(root, path string, out any) error {
	raw, err := readPrivate(root, path, 2<<20)
	if err != nil {
		return err
	}
	var state signedState
	if err = json.Unmarshal(raw, &state); err != nil {
		return err
	}
	sig := state.Signature
	state.Signature = localevidence.OwnerPolicyStateSignature{}
	unsigned, _ := json.Marshal(state)
	if state.Schema != "stackkit.federation-control-custody/v1" {
		return errors.New("control: invalid custody schema")
	}
	if err = localevidence.VerifyOwnerPolicyState(root, unsigned, sig); err != nil {
		return err
	}
	owner, err := localevidence.LoadOwnerCustody(root)
	if err != nil {
		return err
	}
	if state.Binding != owner.Binding {
		return errors.New("control: custody target changed")
	}
	return json.Unmarshal(state.Value, out)
}

func writeState(root, path string, value any) error {
	owner, err := localevidence.LoadOwnerCustody(root)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	state := signedState{Schema: "stackkit.federation-control-custody/v1", Binding: owner.Binding, Value: raw}
	unsigned, _ := json.Marshal(state)
	state.Signature, err = localevidence.SignOwnerPolicyState(root, unsigned)
	if err != nil {
		return err
	}
	raw, _ = json.Marshal(state)
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
	dir := filepath.ToSlash(filepath.Dir(path))
	if err = tx.MkdirAll(dir, 0700); err != nil {
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
		return errors.New("control: custody not durable")
	}
	_, err = tx.SyncDirectory(dir)
	return err
}

func hashBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
