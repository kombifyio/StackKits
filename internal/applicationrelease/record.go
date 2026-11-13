// Package applicationrelease holds the owner-signed record of one scoped
// application release change: the retained copy of the application's prior
// data, the prior intent, and the release transition it authorizes.
//
// A forward change stops only the selected application, copies each of its
// governed volumes into an operation-bound retained volume, reads the copy
// back as a file-tree digest and then selects the target release in the
// StackSpec; normal generate and Apply run the change. A rollback stops the
// application again, copies the retained data back, proves the live tree
// equals the checkpoint digest and restores the prior intent; the next Apply
// may then start the earlier release on exactly that data. While a record is
// open, Apply starts only the release it expects; without one, Apply refuses
// every move to an earlier release.
package applicationrelease

import (
	"errors"
	"regexp"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

const APIVersion = "stackkit.application-release/v1"

type State string

const (
	// StateCheckpointStarted: the application may be stopped and its
	// retained copies may be incomplete; live data is unchanged.
	StateCheckpointStarted State = "checkpoint-started"
	// StateCheckpointed: retained copies are complete and read back, the
	// target release is selected, and the application stays stopped until
	// Apply starts the target release.
	StateCheckpointed State = "checkpointed"
	// StateRollbackStarted: live data may be partly restored.
	StateRollbackStarted State = "rollback-started"
	// StateRolledBack: live data equals the checkpoint, the prior intent is
	// selected, and the next Apply may start the prior release.
	StateRolledBack State = "rolled-back"
	// StateAbandoned: a checkpoint that never completed was released; the
	// prior intent and runtime stay authoritative.
	StateAbandoned State = "abandoned"
	// StateFinalized: retained copies are removed; the record is history.
	StateFinalized State = "finalized"
)

var operationPattern = regexp.MustCompile(`^release\.[a-z0-9][a-z0-9-]{0,62}\.[0-9]{8}T[0-9]{6}Z$`)

type Image struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}

type Readback struct {
	Files  int64  `json:"files"`
	Bytes  int64  `json:"bytes"`
	Digest string `json:"digest"`
}

// ManagedAuthority is the Techstack authority of a release change on a
// managed node: the exact application-release.update capability the node
// verified against its Owner-approved trust before it stopped the
// application. A standalone record carries none.
type ManagedAuthority struct {
	CapabilityID     string `json:"capabilityId"`
	CapabilitySHA256 string `json:"capabilitySha256"`
	IssuerID         string `json:"issuerId"`
	KeyID            string `json:"keyId"`
}

// Volume is one governed live volume of the application and its retained,
// operation-bound copy.
type Volume struct {
	ComponentRef   string    `json:"componentRef"`
	LogicalName    string    `json:"logicalName"`
	ComposeProject string    `json:"composeProject"`
	LiveName       string    `json:"liveName"`
	RollbackName   string    `json:"rollbackName"`
	Checkpoint     *Readback `json:"checkpoint,omitempty"`
}

type Record struct {
	APIVersion     string `json:"apiVersion"`
	OperationID    string `json:"operationId"`
	State          State  `json:"state"`
	UseCaseRef     string `json:"useCaseRef"`
	WorkloadRef    string `json:"workloadRef"`
	ModuleRef      string `json:"moduleRef"`
	NodeRef        string `json:"nodeRef"`
	ComposeProject string `json:"composeProject"`
	EntryComponent string `json:"entryComponent"`
	PriorRelease   string `json:"priorRelease"`
	PriorImage     Image  `json:"priorImage"`
	TargetRelease  string `json:"targetRelease"`
	TargetImage    Image  `json:"targetImage"`
	// TargetUntested marks a target outside the catalog's tested releases,
	// accepted explicitly by the owner.
	TargetUntested bool `json:"targetUntested,omitempty"`
	// Managed is set when Techstack manages the node; the intent change was
	// admitted by that capability, and resume and rollback stay bound to it.
	Managed        *ManagedAuthority `json:"managed,omitempty"`
	PriorSpecHash  string            `json:"priorSpecHash"`
	TargetSpecHash string            `json:"targetSpecHash"`
	// PriorSpec and TargetSpec are the exact canonical StackSpec bytes.
	PriorSpec        []byte   `json:"priorSpec"`
	TargetSpec       []byte   `json:"targetSpec"`
	PlanHash         string   `json:"planHash"`
	KopiaHelperImage string   `json:"kopiaHelperImage"`
	Volumes          []Volume `json:"volumes"`
	// RuntimeFiles are the private runtime files of a native installation
	// (the Marketplace) retained beside its volumes and restored with them.
	RuntimeFiles []RuntimeFile `json:"runtimeFiles,omitempty"`
	CreatedAt    time.Time     `json:"createdAt"`
	UpdatedAt    time.Time     `json:"updatedAt"`

	Signature localevidence.OwnerApplicationReleaseSignature `json:"signature"`
}

// Open reports whether the record still governs its application: retained
// data exists or a transition is incomplete.
func (record Record) Open() bool {
	return record.State != StateFinalized && record.State != StateAbandoned
}

// ValidOperationID reports whether id is a scoped release operation identity.
func ValidOperationID(id string) bool { return operationPattern.MatchString(id) }

var transitions = map[State][]State{
	StateCheckpointStarted: {StateCheckpointStarted, StateCheckpointed, StateAbandoned},
	StateCheckpointed:      {StateRollbackStarted, StateFinalized},
	StateRollbackStarted:   {StateRollbackStarted, StateRolledBack},
	StateRolledBack:        {StateFinalized},
}

// CanAdvance reports whether a record in state from may move to next.
func CanAdvance(from, next State) bool {
	for _, allowed := range transitions[from] {
		if allowed == next {
			return true
		}
	}
	return false
}

func (record Record) validate() error {
	if record.APIVersion != APIVersion || !ValidOperationID(record.OperationID) ||
		record.UseCaseRef == "" || record.WorkloadRef == "" || record.ModuleRef == "" || record.NodeRef == "" || record.ComposeProject == "" || record.EntryComponent == "" ||
		record.PriorRelease == "" || record.TargetRelease == "" || record.PriorRelease == record.TargetRelease ||
		record.PriorImage.Ref == "" || record.PriorImage.Digest == "" || record.TargetImage.Ref == "" || record.TargetImage.Digest == "" ||
		record.PriorSpecHash == "" || record.TargetSpecHash == "" || record.PriorSpecHash == record.TargetSpecHash ||
		len(record.PriorSpec) == 0 || len(record.TargetSpec) == 0 || record.PlanHash == "" || record.KopiaHelperImage == "" ||
		len(record.Volumes) == 0 || record.CreatedAt.IsZero() || record.UpdatedAt.Before(record.CreatedAt) {
		return errors.New("applicationrelease: record is incomplete")
	}
	if !validRuntimeFiles(record.RuntimeFiles) {
		return errors.New("applicationrelease: record runtime files are invalid")
	}
	if record.Managed != nil && (record.Managed.CapabilityID == "" || len(record.Managed.CapabilitySHA256) != len("sha256:")+64 || record.Managed.IssuerID == "" || record.Managed.KeyID == "") {
		return errors.New("applicationrelease: managed authority is incomplete")
	}
	if _, known := transitions[record.State]; !known && record.Open() {
		return errors.New("applicationrelease: record state is unknown")
	}
	complete := record.State != StateCheckpointStarted && record.State != StateAbandoned
	seen := map[string]bool{}
	for _, volume := range record.Volumes {
		if volume.LiveName == "" || volume.RollbackName == "" || volume.LiveName == volume.RollbackName ||
			volume.ComposeProject != record.ComposeProject || seen[volume.LiveName] || seen[volume.RollbackName] {
			return errors.New("applicationrelease: record volume is invalid")
		}
		seen[volume.LiveName], seen[volume.RollbackName] = true, true
		if complete && (volume.Checkpoint == nil || len(volume.Checkpoint.Digest) != 64) {
			return errors.New("applicationrelease: completed checkpoint lacks its data readback")
		}
	}
	return nil
}
