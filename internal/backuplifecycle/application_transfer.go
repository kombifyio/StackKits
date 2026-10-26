package backuplifecycle

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const ApplicationTransferAPI = "stackkit.application-transfer/v1"

// ApplicationTransfer is source provenance, not destination mutation authority.
// ManifestDigest covers every archived byte through the authenticated entry list.
type ApplicationTransfer struct {
	PortableConfiguration map[string]ApplicationTransferWorkloadConfiguration `json:"portableConfiguration"`
	Mode                  string                                              `json:"mode"`
	APIVersion            string                                              `json:"apiVersion"`
	ID                    string                                              `json:"id"`
	SourceOwner           string                                              `json:"sourceOwner"`
	Lineage               AuthorityLineage                                    `json:"lineage"`
	PolicyDigest          string                                              `json:"policyDigest"`
	ManifestDigest        string                                              `json:"manifestDigest"`
	Runtimes              []localbackuppolicy.ApplicationRuntime              `json:"runtimes"`
	Volumes               []localbackuppolicy.ApplicationVolume               `json:"volumes"`

	SecretDigests         map[string]map[string]string               `json:"secretDigests"`
	CredentialSourceIndex *int                                       `json:"credentialSourceIndex,omitempty"`
	Signature             localevidence.ApplicationTransferSignature `json:"signature"`
	Destination           ApplicationTransferDestination             `json:"destination"`
}

type ApplicationTransferDestination struct {
	OwnerRef           string    `json:"ownerRef"`
	SigningFingerprint string    `json:"signingFingerprint"`
	StackID            string    `json:"stackId"`
	SiteRef            string    `json:"siteRef"`
	NodeRef            string    `json:"nodeRef"`
	PlanHash           string    `json:"planHash"`
	Recipient          string    `json:"recipient"`
	Nonce              string    `json:"nonce"`
	ExpiresAt          time.Time `json:"expiresAt"`
}

type ApplicationTransferWorkloadConfiguration struct {
	AlternativeRef string         `json:"alternativeRef"`
	ModuleRef      string         `json:"moduleRef"`
	Settings       map[string]any `json:"settings"`
	Profiles       map[string]any `json:"profiles"`
}

type ApplicationTransferExportInput struct {
	PortableConfiguration map[string]ApplicationTransferWorkloadConfiguration
	Mode                  string
	WorkspaceRoot         string
	OwnerRef              string
	Lineage               AuthorityLineage
	Policy                Policy
	PolicyDigest          string
	Workloads             []string
	RuntimeClosure        []localbackuppolicy.ApplicationRuntime

	SecretMaterials map[string]map[string][]byte
	Target          string
	Recipients      []age.Recipient
	Destination     ApplicationTransferDestination
	// Quiesce must stop precisely the selected source closure and return its
	// resume operation. Export always resumes; it never deletes the source.
	Quiesce func(context.Context) (func() error, error)
}

func SelectApplicationTransfer(policy Policy, workloads []string, closure ...[]localbackuppolicy.ApplicationRuntime) ([]localbackuppolicy.ApplicationRuntime, []localbackuppolicy.ApplicationVolume, error) {
	if _, err := localbackuppolicy.ArtifactBytes(policy); err != nil {
		return nil, nil, err
	}
	if len(workloads) == 0 {
		return nil, nil, errors.New("application transfer requires selected workloads")
	}
	wanted := map[string]bool{}
	for _, ref := range workloads {
		if ref == "" || wanted[ref] {
			return nil, nil, errors.New("invalid or duplicate transfer workload")
		}
		wanted[ref] = true
	}
	var runtimes []localbackuppolicy.ApplicationRuntime
	var volumes []localbackuppolicy.ApplicationVolume
	graphs := policy.Source.ApplicationRuntimes
	if len(closure) > 1 {
		return nil, nil, errors.New("ambiguous transfer runtime closure")
	}
	if len(closure) == 1 {
		var err error
		graphs, err = localbackuppolicy.CanonicalApplicationTransferRuntimes(closure[0])
		if err != nil {
			return nil, nil, err
		}
	}
	for _, r := range graphs {
		if !wanted[r.WorkloadRef] {
			continue
		}
		if r.RuntimeAdapterRef != "" && r.RuntimeAdapterRef != "standalone-compose" {
			return nil, nil, fmt.Errorf("workload %s requires its manager transfer authority", r.WorkloadRef)
		}
		runtimes = append(runtimes, r)
		delete(wanted, r.WorkloadRef)
	}
	if len(wanted) != 0 {
		return nil, nil, errors.New("selected workload is outside this node's backup authority")
	}
	for _, v := range policy.Source.ApplicationVolumes {
		if slices.Contains(workloads, v.WorkloadRef) {
			volumes = append(volumes, v)
		}
	}
	if len(volumes) == 0 {
		return nil, nil, errors.New("selected workload closure has no governed persistent volumes")
	}

	return runtimes, volumes, nil
}

func ExportApplicationTransfer(ctx context.Context, input ApplicationTransferExportInput) (result EmergencyExportResult, returnErr error) {
	closure := input.RuntimeClosure
	if closure == nil {
		closure = input.Policy.Source.ApplicationRuntimes
	}
	runtimes, volumes, err := SelectApplicationTransfer(input.Policy, input.Workloads, closure)
	if err != nil {
		return result, err
	}
	if input.Quiesce == nil || input.OwnerRef == "" || input.Lineage.Binding.PlanHash == "" || input.PolicyDigest == "" {
		return result, errors.New("application transfer requires current node authority and explicit quiescence")
	}
	if input.Destination.OwnerRef == "" || input.Destination.StackID == "" || input.Destination.SiteRef == "" || input.Destination.NodeRef == "" || input.Destination.PlanHash == "" || input.Destination.Nonce == "" || !input.Destination.ExpiresAt.After(time.Now()) || input.Destination.ExpiresAt.After(time.Now().Add(24*time.Hour)) {
		return result, errors.New("transfer requires a fresh exact destination admission (maximum 24 hours)")
	}
	if len(input.Recipients) != 1 {
		return result, errors.New("application transfer requires one destination recipient")
	}
	recipient, ok := input.Recipients[0].(*age.X25519Recipient)
	if !ok || recipient.String() != input.Destination.Recipient {
		return result, errors.New("archive encryption recipient differs from signed destination")
	}
	transfer := ApplicationTransfer{APIVersion: ApplicationTransferAPI, PortableConfiguration: input.PortableConfiguration, SourceOwner: input.OwnerRef, Lineage: input.Lineage, PolicyDigest: input.PolicyDigest, Runtimes: runtimes, Volumes: volumes, Destination: input.Destination}
	if input.Mode != "copy" && input.Mode != "migrate" {
		return result, errors.New("transfer mode must be copy or migrate")
	}
	transfer.Mode = input.Mode
	commitmentKey := make([]byte, 32)
	if _, err = rand.Read(commitmentKey); err != nil {
		return result, err
	}
	defer clear(commitmentKey)
	selectedSecrets := map[string]map[string][]byte{}
	transfer.SecretDigests = map[string]map[string]string{}
	for _, runtime := range runtimes {
		slots, exists := input.SecretMaterials[runtime.WorkloadRef]
		if !exists {
			return result, errors.New("transfer requires exact selected application secret-slot closure")
		}
		selectedSecrets[runtime.WorkloadRef] = slots
		transfer.SecretDigests[runtime.WorkloadRef] = map[string]string{}
		for slot, material := range slots {
			if slot == "" || len(material) == 0 {
				return result, errors.New("invalid selected application secret slot")
			}
			transfer.SecretDigests[runtime.WorkloadRef][slot] = ApplicationTransferSecretCommitment(commitmentKey, runtime.WorkloadRef, slot, material)
		}
	}
	sources := make([]EmergencySource, len(volumes))
	for i, v := range volumes {
		sources[i] = EmergencySource{Class: classifyEmergencyVolume(v.VolumeName, v.DataClasses), Path: filepath.Join(input.Policy.Source.HostPath, v.VolumeName, "_data")}
	}
	material, err := json.Marshal(ApplicationTransferSecretPayload{Materials: selectedSecrets, CommitmentKey: commitmentKey})
	if err != nil {
		return result, err
	}
	defer clear(material)
	credentialIndex := len(sources)
	transfer.CredentialSourceIndex = &credentialIndex
	sources = append(sources, EmergencySource{Class: "secrets", Path: filepath.Join(input.WorkspaceRoot, ".stackkit", "backups", "transfer-memory-secrets")})
	resume, err := input.Quiesce(ctx)
	if err != nil {
		return result, err
	}
	if resume == nil {
		return result, errors.New("source quiescence did not provide a resume operation")
	}
	defer func() { returnErr = errors.Join(returnErr, resume()) }()
	return ExportEmergency(ctx, EmergencyExportInput{Target: input.Target, Sources: sources, Recipients: input.Recipients, LargeMediaMode: "include", memorySources: map[int]map[string][]byte{credentialIndex: {"application-secrets.json": material}}, finalizeManifest: func(manifest *EmergencyManifest) error {
		transfer.ManifestDigest, err = applicationTransferManifestDigest(*manifest)
		if err != nil {
			return err
		}
		transfer.ID, err = resolvedplan.CanonicalSHA256(transfer)
		if err != nil {
			return err
		}
		// ID is derived before signing and is itself authenticated.
		canonical, err := applicationTransferCanonical(transfer)
		if err != nil {
			return err
		}
		transfer.Signature, err = localevidence.SignApplicationTransfer(input.WorkspaceRoot, canonical)
		if err != nil {
			return err
		}
		if transfer.Signature.OwnerRef != input.OwnerRef {
			return errors.New("source signer differs from admitted transfer Owner")
		}
		manifest.ApplicationTransfer = &transfer
		return nil
	}})
}

func VerifyApplicationTransfer(manifest EmergencyManifest, expectedOwner, expectedFingerprint, expectedID string) (ApplicationTransfer, error) {
	if manifest.ApplicationTransfer == nil {
		return ApplicationTransfer{}, errors.New("archive is not an application transfer")
	}
	t := *manifest.ApplicationTransfer
	if t.APIVersion != ApplicationTransferAPI || (t.Mode != "copy" && t.Mode != "migrate") || t.ID == "" || t.ID != expectedID || t.SourceOwner != expectedOwner || len(t.Runtimes) == 0 || t.CredentialSourceIndex == nil || *t.CredentialSourceIndex != len(t.Volumes) || len(t.Volumes)+1 != len(manifest.Sources) {
		return t, errors.New("application transfer differs from explicit source admission or has expired")
	}
	digest, err := applicationTransferManifestDigest(manifest)
	if err != nil || digest != t.ManifestDigest {
		return t, errors.New("application transfer manifest differs from signed source provenance")
	}
	unsigned := t
	unsigned.ID = ""
	unsigned.Signature = localevidence.ApplicationTransferSignature{}
	id, err := resolvedplan.CanonicalSHA256(unsigned)
	if err != nil || id != t.ID {
		return t, errors.New("application transfer identity does not match its provenance")
	}
	canonical, err := applicationTransferCanonical(t)
	if err != nil {
		return t, err
	}
	return t, localevidence.VerifyApplicationTransfer(canonical, t.Signature, expectedOwner, expectedFingerprint)
}

// MatchApplicationTransfer rebinds only placement and generated Docker names.
// Images, component graph and data targets must match. Selected credential slots
// are separately adopted into the destination-local generated environment.
func MatchApplicationTransfer(t ApplicationTransfer, destination Policy, closure ...[]localbackuppolicy.ApplicationRuntime) ([]localbackuppolicy.ApplicationVolume, error) {
	workloads := make([]string, len(t.Runtimes))
	for i, r := range t.Runtimes {
		workloads[i] = r.WorkloadRef
	}
	runtimes, volumes, err := SelectApplicationTransfer(destination, workloads, closure...)
	if err != nil {
		return nil, err
	}
	if len(runtimes) != len(t.Runtimes) || len(volumes) != len(t.Volumes) {
		return nil, errors.New("destination workload closure differs from source")
	}
	for _, source := range t.Runtimes {
		found := false
		for _, target := range runtimes {
			if target.WorkloadRef == source.WorkloadRef {
				source.SiteRef = target.SiteRef
				source.NodeRef = target.NodeRef
				source.ComposeProject = target.ComposeProject
				if !reflect.DeepEqual(source, target) {
					return nil, errors.New("destination component/image graph is incompatible")
				}
				found = true
			}
		}
		if !found {
			return nil, errors.New("destination application credentials/configuration differ; deliver matching application secrets through existing custody before import")
		}
	}
	// Preserve source order: it is the encrypted archive's sources index.
	mapped := make([]localbackuppolicy.ApplicationVolume, len(t.Volumes))
	for i, source := range t.Volumes {
		found := false
		for _, target := range volumes {
			if source.WorkloadRef == target.WorkloadRef && source.ComponentRef == target.ComponentRef && source.VolumeRef == target.VolumeRef {
				comparison := source
				comparison.SiteRef = target.SiteRef
				comparison.NodeRef = target.NodeRef
				comparison.ComposeProject = target.ComposeProject
				comparison.VolumeName = target.VolumeName
				if !reflect.DeepEqual(comparison, target) {
					return nil, errors.New("destination persistent volume contract is incompatible")
				}
				mapped[i] = target
				found = true
			}
		}
		if !found {
			return nil, errors.New("destination lacks a selected source volume")
		}
	}
	return mapped, nil
}

func applicationTransferManifestDigest(manifest EmergencyManifest) (string, error) {
	manifest.ApplicationTransfer = nil
	return resolvedplan.CanonicalSHA256(manifest)
}
func applicationTransferCanonical(t ApplicationTransfer) ([]byte, error) {
	t.Signature = localevidence.ApplicationTransferSignature{}
	return json.Marshal(t)
}

// Secret commitments are keyed inside the encrypted payload so public receipts
// cannot be used to dictionary-test application secrets.
type ApplicationTransferSecretPayload struct {
	Materials     map[string]map[string][]byte `json:"materials"`
	CommitmentKey []byte                       `json:"commitmentKey"`
}

func ApplicationTransferSecretCommitment(key []byte, workload, slot string, value []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("stackkit.transfer-secret/v1\x00" + workload + "\x00" + slot + "\x00"))
	mac.Write(value)
	return "hmac-sha256:" + hex.EncodeToString(mac.Sum(nil))
}
func ApplicationTransferCommitmentKeyRef(id string) string {
	return "secret://application-transfers/" + strings.TrimPrefix(id, "sha256:") + "/commitment-key"
}
