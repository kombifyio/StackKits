package applicationlifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	// AdoptionProfile is retained as an alias for the Home Assistant profile for
	// backward compatibility with existing callers.
	AdoptionProfile              = "home-assistant-container-in-place/v1"
	HomeAssistantAdoptionProfile = "home-assistant-container-in-place/v1"
	VaultwardenAdoptionProfile   = "vaultwarden-container-in-place/v1"
	CloudreveAdoptionProfile     = "cloudreve-container-in-place/v1"
	AdoptionSchemaVersion        = "stackkit.application-adoption/v1"
)

// AdoptionProfileSpec defines a supported First-Class in-place adoption
// profile. Each profile owns only container power state and source
// verification; native accounts, configuration, and persistent data remain
// native-owned and are verified read-only.
type AdoptionProfileSpec struct {
	ProfileRef         string
	PackageRef         string
	WorkloadRef        string
	Alternative        string
	DisplayName        string
	DataMount          string
	DefaultPort        int
	PortEnvVar         string
	OfficialImageRepos []string
	SafeEnvKeys        []string
	HashedOnlyEnvKeys  []string
	HealthProbePaths   []string
	DefaultOwnerFile   string
	RequiresOwnerGrant bool
	AllowHostNetwork   bool
	AllowSerialDevices bool
}

var adoptionProfileRegistry = []AdoptionProfileSpec{
	{
		ProfileRef:  HomeAssistantAdoptionProfile,
		PackageRef:  "smart-home",
		WorkloadRef: "smart-home",
		Alternative: "home-assistant",
		DisplayName: "Home Assistant",
		DataMount:   "/config",
		DefaultPort: 8123,
		OfficialImageRepos: []string{
			"ghcr.io/home-assistant/home-assistant",
			"homeassistant/home-assistant",
			"lscr.io/linuxserver/homeassistant",
		},
		SafeEnvKeys: []string{
			"TZ",
			"LANG",
			"LC_ALL",
			"PUID",
			"PGID",
			"UMASK",
			"DISABLE_JEMALLOC",
		},
		HealthProbePaths:   []string{"/api/config"},
		DefaultOwnerFile:   ".stackkit/setup/home-assistant-owner.json",
		RequiresOwnerGrant: true,
		AllowHostNetwork:   true,
		AllowSerialDevices: true,
	},
	{
		ProfileRef:  VaultwardenAdoptionProfile,
		PackageRef:  "vault",
		WorkloadRef: "vault",
		Alternative: "vaultwarden",
		DisplayName: "Vaultwarden",
		DataMount:   "/data",
		DefaultPort: 80,
		PortEnvVar:  "ROCKET_PORT",
		OfficialImageRepos: []string{
			"vaultwarden/server",
			"ghcr.io/dani-garcia/vaultwarden",
		},
		SafeEnvKeys: []string{
			"TZ",
			"DOMAIN",
			"SIGNUPS_ALLOWED",
			"ROCKET_PORT",
			"WEBSOCKET_ENABLED",
		},
		HashedOnlyEnvKeys:  []string{"ADMIN_TOKEN"},
		HealthProbePaths:   []string{"/alive"},
		RequiresOwnerGrant: false,
		AllowHostNetwork:   true,
		AllowSerialDevices: false,
	},
	{
		ProfileRef:  CloudreveAdoptionProfile,
		PackageRef:  "files",
		WorkloadRef: "files",
		Alternative: "cloudreve",
		DisplayName: "Cloudreve",
		DataMount:   "/cloudreve/data",
		DefaultPort: 5212,
		PortEnvVar:  "CR_CONF_SYSTEM_LISTEN",
		OfficialImageRepos: []string{
			"cloudreve/cloudreve",
			"ghcr.io/cloudreve/cloudreve",
		},
		SafeEnvKeys: []string{
			"TZ",
			"CR_CONF_SYSTEM_LISTEN",
			"PUID",
			"PGID",
		},
		HealthProbePaths:   []string{"/api/v4/site/ping", "/api/v3/site/ping"},
		RequiresOwnerGrant: false,
		AllowHostNetwork:   true,
		AllowSerialDevices: false,
	},
}

// SupportedAdoptionProfiles returns a copy of all registered First-Class
// in-place adoption profiles.
func SupportedAdoptionProfiles() []AdoptionProfileSpec {
	out := make([]AdoptionProfileSpec, len(adoptionProfileRegistry))
	for i, p := range adoptionProfileRegistry {
		p.OfficialImageRepos = slices.Clone(p.OfficialImageRepos)
		p.SafeEnvKeys = slices.Clone(p.SafeEnvKeys)
		p.HashedOnlyEnvKeys = slices.Clone(p.HashedOnlyEnvKeys)
		p.HealthProbePaths = slices.Clone(p.HealthProbePaths)
		out[i] = p
	}
	return out
}

// LookupAdoptionProfile resolves a First-Class in-place adoption profile by
// profileRef.
func LookupAdoptionProfile(profileRef string) (AdoptionProfileSpec, bool) {
	for _, p := range adoptionProfileRegistry {
		if p.ProfileRef == profileRef {
			p.OfficialImageRepos = slices.Clone(p.OfficialImageRepos)
			p.SafeEnvKeys = slices.Clone(p.SafeEnvKeys)
			p.HashedOnlyEnvKeys = slices.Clone(p.HashedOnlyEnvKeys)
			p.HealthProbePaths = slices.Clone(p.HealthProbePaths)
			return p, true
		}
	}
	return AdoptionProfileSpec{}, false
}

// LookupAdoptionProfileByWorkload resolves a First-Class in-place adoption
// profile by workloadRef (or packageRef).
func LookupAdoptionProfileByWorkload(workload string) (AdoptionProfileSpec, bool) {
	for _, p := range adoptionProfileRegistry {
		if p.WorkloadRef == workload || p.PackageRef == workload {
			p.OfficialImageRepos = slices.Clone(p.OfficialImageRepos)
			p.SafeEnvKeys = slices.Clone(p.SafeEnvKeys)
			p.HashedOnlyEnvKeys = slices.Clone(p.HashedOnlyEnvKeys)
			p.HealthProbePaths = slices.Clone(p.HealthProbePaths)
			return p, true
		}
	}
	return AdoptionProfileSpec{}, false
}

// IsSupportedAdoptionProfile reports whether profileRef is a registered
// First-Class in-place adoption profile.
func IsSupportedAdoptionProfile(profileRef string) bool {
	_, ok := LookupAdoptionProfile(profileRef)
	return ok
}

// DefaultAdoptionStage returns the standard non-destructive adopt lifecycle
// stage contract used for First-Class in-place adoption profiles.
func DefaultAdoptionStage() StageContract {
	return StageContract{
		Name:          "adopt",
		Operations:    []string{"stackkit.application.adopt", "stackkit.application.release", "stackkit.application.control"},
		Phases:        []string{"inspect", "authorize", "verify", "bind"},
		Evidence:      []string{"adoption-result"},
		Mutation:      true,
		Destructive:   false,
		OwnerApproval: true,
	}
}

type AdoptionContract struct {
	ProfileRef      string   `json:"profileRef"`
	Mode            string   `json:"mode"`
	SourceRuntime   string   `json:"sourceRuntime"`
	ImageRef        string   `json:"imageRef"`
	ImageDigest     string   `json:"imageDigest"`
	Version         string   `json:"version"`
	OwnedFields     []string `json:"ownedFields"`
	PreservedFields []string `json:"preservedFields"`
	Operations      []string `json:"operations"`
}

// AdoptionSource contains hashes of inspected configuration, never environment
// values or account material. Revision excludes changing runtime power state.
type AdoptionSource struct {
	ContainerID         string   `json:"containerId"`
	ImageID             string   `json:"imageId"`
	ImageDigest         string   `json:"imageDigest"`
	Revision            string   `json:"revision"`
	ConfigMount         string   `json:"configMount"`
	ConfigurationDigest string   `json:"configurationDigest"`
	AccountsDigest      string   `json:"accountsDigest"`
	SourceOrigin        string   `json:"sourceOrigin,omitempty"`
	NetworkMode         string   `json:"networkMode,omitempty"`
	PreservedDevices    []string `json:"preservedDevices,omitempty"`
	HasCustomComponents bool     `json:"hasCustomComponents,omitempty"`
	HasExternalRecorder bool     `json:"hasExternalRecorder,omitempty"`
	VersionDrift        bool     `json:"versionDrift,omitempty"`
	ObservedImageDigest string   `json:"observedImageDigest,omitempty"`
}

func (s AdoptionSource) Equal(other AdoptionSource) bool {
	return s.ContainerID == other.ContainerID &&
		s.ImageID == other.ImageID &&
		s.ImageDigest == other.ImageDigest &&
		s.Revision == other.Revision &&
		s.ConfigMount == other.ConfigMount &&
		s.ConfigurationDigest == other.ConfigurationDigest &&
		s.AccountsDigest == other.AccountsDigest &&
		s.SourceOrigin == other.SourceOrigin &&
		s.NetworkMode == other.NetworkMode &&
		slices.Equal(s.PreservedDevices, other.PreservedDevices) &&
		s.HasCustomComponents == other.HasCustomComponents &&
		s.HasExternalRecorder == other.HasExternalRecorder &&
		s.VersionDrift == other.VersionDrift &&
		s.ObservedImageDigest == other.ObservedImageDigest
}

type AdoptionNotice struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

func adoptionNotices(profile AdoptionProfileSpec, source AdoptionSource) []AdoptionNotice {
	var notices []AdoptionNotice
	if source.HasCustomComponents {
		notices = append(notices, AdoptionNotice{
			Code:     "custom_components_preserved",
			Severity: "warning",
			Message:  fmt.Sprintf("%s custom_components/ (such as HACS) detected in %s; preserved read-only under native ownership.", profile.DisplayName, profile.DataMount),
		})
	}
	if source.HasExternalRecorder {
		notices = append(notices, AdoptionNotice{
			Code:     "external_recorder_preserved",
			Severity: "warning",
			Message:  fmt.Sprintf("External recorder configuration (db_url) detected in %s; preserved read-only under native ownership.", profile.DataMount),
		})
	}
	if source.VersionDrift {
		notices = append(notices, AdoptionNotice{
			Code:     "image_version_drift",
			Severity: "warning",
			Message:  fmt.Sprintf("Running official %s image digest differs from the catalog pin; in-place power control is admitted without container recreation.", profile.DisplayName),
		})
	}
	if len(source.PreservedDevices) > 0 {
		notices = append(notices, AdoptionNotice{
			Code:     "usb_serial_devices_preserved",
			Severity: "info",
			Message:  "Attached USB/serial radio devices are preserved in place without container recreation.",
		})
	}
	if source.NetworkMode == "host" {
		notices = append(notices, AdoptionNotice{
			Code:     "host_network_preserved",
			Severity: "info",
			Message:  fmt.Sprintf("Host networking is preserved in place; owner verification probed http://127.0.0.1:%d.", profile.DefaultPort),
		})
	}
	return notices
}

type AdoptionPlan struct {
	SchemaVersion      string           `json:"schemaVersion"`
	APIVersion         string           `json:"apiVersion"`
	Authority          Authority        `json:"authority"`
	WorkloadRef        string           `json:"workloadRef"`
	ProfileRef         string           `json:"profileRef"`
	Mode               string           `json:"mode"`
	Source             AdoptionSource   `json:"source"`
	OwnedFields        []string         `json:"ownedFields"`
	PreservedFields    []string         `json:"preservedFields"`
	AdmittedOperations []string         `json:"admittedOperations"`
	VerifiedOwnerRef   string           `json:"verifiedOwnerRef"`
	Notices            []AdoptionNotice `json:"notices,omitempty"`
	PlanDigest         string           `json:"planDigest,omitempty"`
}

type AdoptionResult struct {
	APIVersion         string           `json:"apiVersion"`
	Authority          Authority        `json:"authority"`
	WorkloadRef        string           `json:"workloadRef"`
	OperationID        string           `json:"operationId"`
	ProfileRef         string           `json:"profileRef"`
	Mode               string           `json:"mode"`
	PlanDigest         string           `json:"planDigest"`
	Source             AdoptionSource   `json:"source"`
	OwnedFields        []string         `json:"ownedFields"`
	PreservedFields    []string         `json:"preservedFields"`
	AdmittedOperations []string         `json:"admittedOperations"`
	VerifiedOwnerRef   string           `json:"verifiedOwnerRef"`
	Notices            []AdoptionNotice `json:"notices,omitempty"`
	Status             string           `json:"status"`
	Action             string           `json:"action,omitempty"`
	PowerState         string           `json:"powerState,omitempty"`
	VerifiedAt         time.Time        `json:"verifiedAt"`
}

type AdoptionReceipt struct {
	SchemaVersion string                                        `json:"schemaVersion"`
	Result        AdoptionResult                                `json:"result"`
	Signature     localevidence.OwnerLifecycleMutationSignature `json:"signature"`
	Evidence      Evidence                                      `json:"evidence"`
}

func NewAdoptionPlan(contract Contract, source AdoptionSource, ownerRef string) (AdoptionPlan, error) {
	if contract.Adoption == nil || contract.Delivery.AdapterRef != "standalone-compose" {
		return AdoptionPlan{}, errors.New("application adoption is unsupported for the selected lifecycle and runtime adapter")
	}
	profile, ok := LookupAdoptionProfile(contract.Adoption.ProfileRef)
	if !ok || contract.WorkloadRef != profile.WorkloadRef {
		return AdoptionPlan{}, errors.New("application adoption is unsupported for the selected lifecycle and runtime adapter")
	}
	a := contract.Adoption
	p := AdoptionPlan{SchemaVersion: AdoptionSchemaVersion, APIVersion: "stackkit.application-adoption-plan/v1", Authority: authorityFromContract(contract), WorkloadRef: contract.WorkloadRef, ProfileRef: a.ProfileRef, Mode: a.Mode, Source: source, OwnedFields: a.OwnedFields, PreservedFields: a.PreservedFields, AdmittedOperations: a.Operations, VerifiedOwnerRef: ownerRef, Notices: adoptionNotices(profile, source)}
	raw, err := resolvedplan.CanonicalJSON(p)
	if contract.AdoptionBaselinePlanHash != "" {
		raw, err = resolvedplan.CanonicalJSON(map[string]any{"plan": p, "baselinePlanHash": contract.AdoptionBaselinePlanHash})
	}
	if err != nil {
		return AdoptionPlan{}, err
	}
	p.PlanDigest = setupDigest(raw)
	return p, nil
}

// SaveAdoptionResult seals evidence before terminal journal publication. A
// membership consumer must accept this receipt, never a successful exit alone.
func (store Store) SaveAdoptionResult(contract Contract, result AdoptionResult) (AdoptionReceipt, error) {
	state, err := store.Load(contract)
	if err != nil {
		return AdoptionReceipt{}, err
	}
	op := currentOperation(state)
	if op == nil || op.ID != result.OperationID || op.Stage != "adopt" || op.Status != StatusRunning || result.Authority != authorityFromContract(contract) || result.WorkloadRef != contract.WorkloadRef || (result.Status != "verified" && result.Status != "released") {
		return AdoptionReceipt{}, errors.New("adoption receipt has no matching running lifecycle operation")
	}
	payload, err := resolvedplan.CanonicalJSON(result)
	if err != nil {
		return AdoptionReceipt{}, err
	}
	signature, err := localevidence.SignOwnerLifecycleMutation(store.Workspace, payload)
	if err != nil {
		return AdoptionReceipt{}, err
	}
	receipt := AdoptionReceipt{SchemaVersion: AdoptionSchemaVersion, Result: result, Signature: signature}
	raw, err := resolvedplan.CanonicalJSON(receipt)
	if err != nil {
		return AdoptionReceipt{}, err
	}
	digest := setupDigest(raw)
	ref := adoptionReceiptPath(contract.WorkloadRef, digest)
	root, err := confinedfs.Open(store.Workspace)
	if err != nil {
		return AdoptionReceipt{}, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return AdoptionReceipt{}, err
	}
	defer tx.Close()
	dir := filepath.ToSlash(filepath.Dir(ref))
	if err := tx.MkdirAll(dir, 0700); err != nil {
		return AdoptionReceipt{}, err
	}
	if err := backupcustody.ProtectPrivatePath(filepath.Join(root.Name(), filepath.FromSlash(dir)), true); err != nil {
		return AdoptionReceipt{}, err
	}
	view, err := root.View(".")
	if err != nil {
		return AdoptionReceipt{}, err
	}
	written, err := view.WriteAtomic0600NoReplace(ref, raw)
	if err != nil || !written.Installed || !written.FileSynced {
		return AdoptionReceipt{}, fmt.Errorf("persist adoption receipt: %w", errors.Join(err, errors.New("receipt was not durably installed")))
	}
	if err := backupcustody.ProtectPrivatePath(filepath.Join(root.Name(), filepath.FromSlash(ref)), false); err != nil {
		return AdoptionReceipt{}, err
	}
	receipt.Evidence = Evidence{Kind: "adoption-result", Ref: ref, Digest: digest}
	return receipt, nil
}

// AdoptionBinding derives membership from the existing lifecycle journal and
// verifies the Owner signature using local trusted custody. It also reads old
// authority receipts so changing the Plan cannot evade a preservation guard.
func (store Store) AdoptionBinding(workload string) (AdoptionReceipt, bool, error) {
	state, exists, err := store.load(workload)
	if err != nil || !exists {
		return AdoptionReceipt{}, false, err
	}
	if state.StateDigest != stateDigest(state) {
		return AdoptionReceipt{}, false, errors.New("application lifecycle state digest is invalid")
	}
	for i := len(state.Operations) - 1; i >= 0; i-- {
		op := state.Operations[i]
		if op.Stage != "adopt" || op.Status != StatusSucceeded {
			continue
		}
		if op.Digest != operationDigest(op) {
			return AdoptionReceipt{}, false, errors.New("adoption operation digest is invalid")
		}
		if len(op.Evidence) != 1 {
			return AdoptionReceipt{}, false, errors.New("adoption operation evidence is invalid")
		}
		e := op.Evidence[0]
		if e.Kind != "adoption-result" || e.Ref != adoptionReceiptPath(workload, e.Digest) || !digestPattern.MatchString(e.Digest) {
			return AdoptionReceipt{}, false, errors.New("adoption receipt reference is invalid")
		}
		root, err := confinedfs.Open(store.Workspace)
		if err != nil {
			return AdoptionReceipt{}, false, err
		}
		tx, err := root.BeginTransaction()
		if err != nil {
			root.Close()
			return AdoptionReceipt{}, false, err
		}
		raw, _, err := tx.ReadStable(e.Ref)
		tx.Close()
		root.Close()
		if err != nil {
			return AdoptionReceipt{}, false, err
		}
		var receipt AdoptionReceipt
		if setupDigest(raw) != e.Digest || json.Unmarshal(raw, &receipt) != nil {
			return AdoptionReceipt{}, false, errors.New("adoption receipt digest is invalid")
		}
		payload, err := resolvedplan.CanonicalJSON(receipt.Result)
		if err != nil {
			return AdoptionReceipt{}, false, err
		}
		if err := localevidence.VerifyOwnerLifecycleMutation(store.Workspace, payload, receipt.Signature); err != nil {
			return AdoptionReceipt{}, false, err
		}
		profile, ok := LookupAdoptionProfile(receipt.Result.ProfileRef)
		if receipt.Result.Authority != op.Authority || receipt.Result.OperationID != op.ID || receipt.Result.WorkloadRef != workload || !ok || profile.WorkloadRef != workload || (receipt.Result.Status != "verified" && receipt.Result.Status != "released") {
			return AdoptionReceipt{}, false, errors.New("adoption receipt does not bind its lifecycle operation")
		}
		receipt.Evidence = e
		return receipt, receipt.Result.Status == "verified", nil
	}
	return AdoptionReceipt{}, false, nil
}

func adoptionReceiptPath(workload, digest string) string {
	return filepath.ToSlash(filepath.Join(stateRoot, workload, "adoption", strings.TrimPrefix(digest, "sha256:")+".json"))
}

func AdoptionIntentDigest(verb, action string, result AdoptionResult) (string, error) {
	raw, err := resolvedplan.CanonicalJSON(map[string]any{"verb": verb, "action": action, "authority": result.Authority, "source": result.Source, "planDigest": result.PlanDigest})
	if err != nil {
		return "", err
	}
	return setupDigest(raw), nil
}

func (store Store) RequireNoAdoption() error {
	for _, profile := range adoptionProfileRegistry {
		_, bound, err := store.AdoptionBinding(profile.WorkloadRef)
		if err != nil {
			return err
		}
		if bound {
			return fmt.Errorf("application_binding_preserved: release the adopted %s lifecycle before any fresh apply, generate, setup or removal; release retains its container, accounts and data", profile.WorkloadRef)
		}
	}
	return nil
}

// MarkAdoptionDispatch persists uncertainty before the only source side effect.
// A retry reconciles read-only; it may never send the action a second time.
func (store Store) MarkAdoptionDispatch(contract Contract, id, intent string) error {
	_, err := store.mutate(contract, func(state *State) error {
		op := currentOperation(*state)
		if op == nil || op.ID != id || op.OperationRef != "stackkit.application.control" || op.Status != StatusRunning || op.IntentDigest != intent || op.Dispatched {
			return errors.New("application dispatch has no exact undispatched intent")
		}
		op.Dispatched = true
		op.UpdatedAt = time.Now().UTC()
		op.Digest = operationDigest(*op)
		return nil
	})
	return err
}
