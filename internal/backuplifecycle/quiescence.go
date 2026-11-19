package backuplifecycle

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

// SnapshotQuiescence is the journaled pre-snapshot Docker identity set. The
// operation journal persists this value before the first stop mutation so a
// retry can address only the exact containers that were running at capture
// time. CaptureStartedAt, when present, precedes the first writer stop and
// provides a conservative age boundary, not application-consistency evidence.
type SnapshotQuiescence struct {
	TransferRetirementRequiresCapability bool                        `json:"transferRetirementRequiresCapability,omitempty"`
	TransferRetirementCapability         []byte                      `json:"transferRetirementCapability,omitempty"`
	TransferWorkloads                    []string                    `json:"transferWorkloads,omitempty"`
	TransferHoldID                       string                      `json:"transferHoldId,omitempty"`
	TransferOutcomeDigest                string                      `json:"transferOutcomeDigest,omitempty"`
	TransferOutcomeState                 string                      `json:"transferOutcomeState,omitempty"`
	TransferRetiredSpecHash              string                      `json:"transferRetiredSpecHash,omitempty"`
	TransferRetirementCandidateHash      string                      `json:"transferRetirementCandidateHash,omitempty"`
	ControlVersion                       int                         `json:"controlVersion,omitempty"`
	AppGroups                            []AppGroupControl           `json:"appGroups,omitempty"`
	Controls                             []RuntimeControl            `json:"controls,omitempty"`
	Phase                                string                      `json:"phase"`
	GraphDigest                          string                      `json:"graphDigest,omitempty"`
	CaptureStartedAt                     *time.Time                  `json:"captureStartedAt,omitempty"`
	Containers                           []SnapshotQuiescedContainer `json:"containers"`
}

type SnapshotQuiescedContainer struct {
	RestartPolicy        string                 `json:"restartPolicy,omitempty"`
	MarketplaceService   string                 `json:"marketplaceService,omitempty"`
	MarketplaceComponent string                 `json:"marketplaceComponent,omitempty"`
	ID                   string                 `json:"id"`
	Name                 string                 `json:"name"`
	WasRunning           bool                   `json:"wasRunning"`
	WorkloadRef          string                 `json:"workloadRef,omitempty"`
	SiteRef              string                 `json:"siteRef,omitempty"`
	NodeRef              string                 `json:"nodeRef,omitempty"`
	ComposeProject       string                 `json:"composeProject,omitempty"`
	ComposeService       string                 `json:"composeService,omitempty"`
	ComponentRef         string                 `json:"componentRef,omitempty"`
	Image                string                 `json:"image,omitempty"`
	StopOrder            int                    `json:"stopOrder,omitempty"`
	Mounts               []SnapshotQuiesceMount `json:"mounts"`
}

type SnapshotQuiesceMount struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	RW          bool   `json:"rw"`
	Propagation string `json:"propagation"`
}

func (s *Service) persistSnapshotQuiescence(
	operationPath string,
	input operationInput,
	quiescence SnapshotQuiescence,
) error {
	if err := validateSnapshotQuiescence(quiescence); err != nil {
		return err
	}
	if quiescence.ControlVersion == 2 {
		for _, g := range quiescence.AppGroups {
			if g.Binding.OwnerRef != input.OwnerRef || g.Binding.JournalOperationID != input.OperationID {
				return errors.New("snapshot group differs from owning operation")
			}
		}
		old, err := s.loadOperation(operationPath)
		if err != nil {
			return err
		}
		if old.State != "pending" || old.Anchor != nil || !operationInputsEqual(old.Input, input) {
			return errors.New("snapshot group journal is terminal or intent changed")
		}
		source, err := localbackuppolicy.SourceDigest(input.Policy.SourceProjection())
		if err != nil {
			return err
		}
		for _, g := range quiescence.AppGroups {
			if g.Binding.PolicySourceDigest != source || !reflect.DeepEqual(g.Binding.AuthorityLineage, input.Lineage) {
				return errors.New("group policy source/lineage differs from owning signed input")
			}
		}
		if old.Quiescence != nil && len(old.Quiescence.AppGroups) > 0 && !reflect.DeepEqual(old.Quiescence.AppGroups, quiescence.AppGroups) && !ValidAppGroupTransition(old.Quiescence.AppGroups, quiescence.AppGroups) {
			return errors.New("snapshot group lineage/handle changed outside one journal phase")
		}
	}
	cloned := cloneSnapshotQuiescence(quiescence)
	return s.writeSnapshotOperation(operationPath, snapshotOperation{
		APIVersion: snapshotOperationAPI,
		State:      "pending",
		Input:      cloneOperationInput(input),
		Quiescence: &cloned,
	})
}

func validateSnapshotQuiescence(quiescence SnapshotQuiescence) error {
	if err := validateApplicationTransferRetirementRecordShape(quiescence); err != nil {
		return err
	}
	if quiescence.ControlVersion != 0 && quiescence.ControlVersion != 1 && quiescence.ControlVersion != 2 {
		return errors.New("backuplifecycle: unsupported runtime control protocol")
	}
	if quiescence.ControlVersion != 2 && len(quiescence.AppGroups) != 0 {
		return errors.New("app groups require explicit v2 snapshot control")
	}
	if quiescence.ControlVersion == 2 {
		if err := validateSnapshotAppGroups(quiescence); err != nil {
			return err
		}
	}
	if quiescence.ControlVersion == 0 && len(quiescence.Controls) != 0 {
		return errors.New("backuplifecycle: runtime controls require their explicit protocol")
	}
	if err := ValidateRuntimeControls(quiescence.Controls); err != nil {
		return err
	}
	switch quiescence.Phase {
	case "resuming":
		if quiescence.ControlVersion != 2 {
			return errors.New("resuming requires v2 app-group control")
		}
	case "transfer-aborted":
		if !validDigest(quiescence.TransferHoldID) || !validDigest(quiescence.TransferOutcomeDigest) || quiescence.TransferOutcomeState != "stopped" || !validDigest(quiescence.TransferRetiredSpecHash) {
			return errors.New("target abort retirement lacks exact stopped outcome and retained-data intent CAS")
		}
	case "transfer-retired":
		if !validDigest(quiescence.TransferHoldID) || !validDigest(quiescence.TransferOutcomeDigest) || quiescence.TransferOutcomeState != "activated" || !validDigest(quiescence.TransferRetiredSpecHash) {
			return errors.New("transfer retirement lacks authenticated destination outcome and retained-data intent CAS")
		}
	case "prepared", "stopped", "snapshot-created", "restored":
	default:
		return errors.New("backuplifecycle: snapshot quiescence phase is invalid")
	}
	if quiescence.GraphDigest != "" && !validDigest(quiescence.GraphDigest) {
		return errors.New("backuplifecycle: snapshot quiescence graph digest is invalid")
	}
	if quiescence.CaptureStartedAt != nil && quiescence.CaptureStartedAt.IsZero() {
		return errors.New("backuplifecycle: snapshot capture start is invalid")
	}
	seen := make(map[string]struct{}, len(quiescence.Containers))
	for index, container := range quiescence.Containers {
		if strings.TrimSpace(container.ID) == "" || strings.TrimSpace(container.Name) == "" || quiescence.ControlVersion == 0 && !container.WasRunning {
			return fmt.Errorf("backuplifecycle: snapshot quiescence container %d identity or running state is incomplete", index)
		}
		if _, exists := seen[container.ID]; exists {
			return fmt.Errorf("backuplifecycle: snapshot quiescence repeats container %q", container.ID)
		}
		seen[container.ID] = struct{}{}
		if container.MarketplaceService != "" || container.MarketplaceComponent != "" {
			if container.MarketplaceService == "" || container.MarketplaceComponent == "" || container.ComposeProject != "" || (quiescence.ControlVersion != 1 && quiescence.ControlVersion != 2) || quiescence.GraphDigest == "" {
				return errors.New("backuplifecycle: upstream Store control has no exact source identity")
			}
		}
		if container.ComposeProject != "" {
			if quiescence.GraphDigest == "" || container.WorkloadRef == "" || container.SiteRef == "" ||
				container.NodeRef == "" || container.ComposeService == "" || container.ComponentRef == "" ||
				container.ComposeService != container.ComponentRef || container.Image == "" || container.StopOrder < 0 {
				return fmt.Errorf("backuplifecycle: snapshot quiescence container %q has incomplete runtime graph identity", container.ID)
			}
		} else if container.WorkloadRef != "" || container.SiteRef != "" || container.NodeRef != "" ||
			container.ComposeService != "" || container.ComponentRef != "" || container.Image != "" || container.StopOrder != 0 {
			return fmt.Errorf("backuplifecycle: snapshot quiescence container %q has unbound runtime graph identity", container.ID)
		}
		if len(container.Mounts) == 0 && container.ComposeProject == "" {
			return fmt.Errorf("backuplifecycle: snapshot quiescence container %q has no mount identity", container.ID)
		}
	}
	if quiescence.ControlVersion == 1 || quiescence.ControlVersion == 2 {
		if quiescence.ControlVersion == 1 && len(quiescence.Controls) != len(quiescence.Containers) {
			return errors.New("backuplifecycle: initial power/control identity set is incomplete")
		}
		for _, control := range quiescence.Controls {
			found := false
			for _, container := range quiescence.Containers {
				if container.ID == control.ContainerID && container.Name == control.ComponentRef && container.WasRunning == control.WasRunning {
					found = true
				}
			}
			if !found {
				return errors.New("backuplifecycle: control is outside its authenticated initial identity set")
			}
			if quiescence.Phase == "stopped" || quiescence.Phase == "snapshot-created" {
				if control.WasRunning && control.Stop.State != ControlObserved {
					return errors.New("backuplifecycle: snapshot requires completed writer stops")
				}
			}
			if quiescence.Phase == "restored" && control.Stop.State != "" && control.Start.State != ControlObserved {
				return errors.New("backuplifecycle: writer resume obligation is unresolved")
			}
		}
	}
	return nil
}

func validateRestoredSnapshotQuiescence(quiescence SnapshotQuiescence) error {
	if err := validateSnapshotQuiescence(quiescence); err != nil {
		return err
	}
	if quiescence.Phase != "restored" {
		return errors.New("backuplifecycle: snapshot anchor requires restored quiescence evidence")
	}
	return nil
}

func cloneSnapshotQuiescence(quiescence SnapshotQuiescence) SnapshotQuiescence {
	cloned := SnapshotQuiescence{
		TransferRetirementRequiresCapability: quiescence.TransferRetirementRequiresCapability,
		TransferRetirementCapability:         append([]byte(nil), quiescence.TransferRetirementCapability...),
		TransferWorkloads:                    append([]string(nil), quiescence.TransferWorkloads...),
		TransferHoldID:                       quiescence.TransferHoldID, TransferOutcomeDigest: quiescence.TransferOutcomeDigest, TransferOutcomeState: quiescence.TransferOutcomeState,
		TransferRetiredSpecHash:         quiescence.TransferRetiredSpecHash,
		TransferRetirementCandidateHash: quiescence.TransferRetirementCandidateHash,
		ControlVersion:                  quiescence.ControlVersion,
		AppGroups:                       CloneAppGroups(quiescence.AppGroups),
		Controls:                        append([]RuntimeControl(nil), quiescence.Controls...),
		Phase:                           quiescence.Phase,
		GraphDigest:                     quiescence.GraphDigest,
		Containers:                      make([]SnapshotQuiescedContainer, len(quiescence.Containers)),
	}
	if quiescence.CaptureStartedAt != nil {
		startedAt := *quiescence.CaptureStartedAt
		cloned.CaptureStartedAt = &startedAt
	}
	for index, container := range quiescence.Containers {
		cloned.Containers[index] = SnapshotQuiescedContainer{
			RestartPolicy:      container.RestartPolicy,
			MarketplaceService: container.MarketplaceService, MarketplaceComponent: container.MarketplaceComponent,
			ID: container.ID, Name: container.Name, WasRunning: container.WasRunning,
			WorkloadRef: container.WorkloadRef, SiteRef: container.SiteRef, NodeRef: container.NodeRef,
			ComposeProject: container.ComposeProject, ComposeService: container.ComposeService,
			ComponentRef: container.ComponentRef, Image: container.Image, StopOrder: container.StopOrder,
			Mounts: append([]SnapshotQuiesceMount(nil), container.Mounts...),
		}
	}
	return cloned
}

func snapshotQuiescencePointersEqual(left, right *SnapshotQuiescence) bool {
	if left == nil || right == nil {
		return left == right
	}
	return reflect.DeepEqual(*left, *right)
}
