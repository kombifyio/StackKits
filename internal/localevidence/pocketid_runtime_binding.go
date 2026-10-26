package localevidence

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/pocketid"
)

// PocketIDRuntimeBinding extends the existing signed installation receipt.
// Ready is executor-owned; the receiver also re-observes the live incarnation.
// Contract survives invalidation so rollback cannot silently clear CAS custody.
type PocketIDRuntimeBinding struct {
	Project             string                  `json:"project"`
	ContainerID         string                  `json:"containerId"`
	ImageRef            string                  `json:"imageRef"`
	ImageID             string                  `json:"imageId"`
	StartedAt           string                  `json:"startedAt"`
	Volume              string                  `json:"volume"`
	Contract            string                  `json:"contract"`
	Ready               bool                    `json:"ready"`
	PreviousContainerID string                  `json:"previousContainerId,omitempty"`
	DrainedAt           time.Time               `json:"drainedAt"`
	PreviousProcess     *PocketIDStoppedProcess `json:"previousProcess,omitempty"`
}

type PocketIDStoppedProcess struct {
	PID           int    `json:"pid"`
	ExitCode      int    `json:"exitCode"`
	FinishedAt    string `json:"finishedAt"`
	RestartPolicy string `json:"restartPolicy"`
}

var pocketIDRuntimeID = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validatePocketIDRuntimeBinding(proof *PocketIDRuntimeBinding) error {
	if proof == nil {
		return nil
	}
	if proof.Contract != "" && proof.Contract != pocketid.MembershipContract {
		return errors.New("localevidence: unknown PocketID runtime contract")
	}
	if proof.PreviousContainerID != "" && (!pocketIDRuntimeID.MatchString(proof.PreviousContainerID) || proof.DrainedAt.IsZero()) {
		return errors.New("localevidence: invalid PocketID old-container drain observation")
	}
	if proof.PreviousContainerID != "" {
		if proof.PreviousProcess == nil || proof.PreviousProcess.PID != 0 || proof.PreviousProcess.RestartPolicy != "no" {
			return errors.New("localevidence: missing PocketID stopped-process observation")
		}
		if _, err := time.Parse(time.RFC3339Nano, proof.PreviousProcess.FinishedAt); err != nil {
			return errors.New("localevidence: invalid PocketID process exit observation")
		}
	}
	if !proof.Ready {
		return nil
	}
	if (proof.Project != "stackkit-basement-core" && proof.Project != "stackkit-cloud-core" && proof.Project != "stackkit-cloud-core-standalone") || !pocketIDRuntimeID.MatchString(proof.ContainerID) || !strings.HasPrefix(proof.ImageID, "sha256:") || !pocketIDRuntimeID.MatchString(strings.TrimPrefix(proof.ImageID, "sha256:")) || proof.Volume != proof.Project+"_pocketid-data" || proof.ImageRef == "" {
		return errors.New("localevidence: invalid PocketID runtime incarnation")
	}
	if _, err := time.Parse(time.RFC3339Nano, proof.StartedAt); err != nil {
		return errors.New("localevidence: invalid PocketID process start observation")
	}
	return nil
}

func updatePocketIDRuntime(workspace string, change func(*PocketIDRuntimeBinding) error) (OwnerRuntimeBinding, error) {
	record, err := LoadOwnerRuntimeBinding(workspace)
	if err != nil {
		return OwnerRuntimeBinding{}, err
	}
	if record.PocketIDRuntime == nil {
		record.PocketIDRuntime = &PocketIDRuntimeBinding{}
	}
	if err := change(record.PocketIDRuntime); err != nil {
		return OwnerRuntimeBinding{}, err
	}
	if err := validatePocketIDRuntimeBinding(record.PocketIDRuntime); err != nil {
		return OwnerRuntimeBinding{}, err
	}
	if err := signOwnerRuntimeBinding(workspace, &record); err != nil {
		return OwnerRuntimeBinding{}, err
	}
	path, err := confinedCustodyPath(workspace, ownerRuntimeBindingRelPath)
	if err != nil {
		return OwnerRuntimeBinding{}, err
	}
	if err := writePrivateJSON(path, record); err != nil {
		return OwnerRuntimeBinding{}, err
	}
	return LoadOwnerRuntimeBinding(workspace)
}

// InvalidatePocketIDRuntime persists denial before a destructive transition.
// A fresh installation has no owner binding until the existing realization.
func InvalidatePocketIDRuntime(workspace string) error {
	_, err := updatePocketIDRuntime(workspace, func(p *PocketIDRuntimeBinding) error { p.Ready = false; return nil })
	if errors.Is(err, ErrOwnerRuntimeBindingMissing) {
		return nil
	}
	return err
}

func RecordPocketIDDrain(workspace, containerID string, at time.Time, process PocketIDStoppedProcess) error {
	_, err := updatePocketIDRuntime(workspace, func(p *PocketIDRuntimeBinding) error {
		p.Ready = false
		p.PreviousContainerID = containerID
		p.DrainedAt = at.UTC()
		p.PreviousProcess = &process
		return nil
	})
	return err
}

func BindPocketIDRuntime(workspace string, observed PocketIDRuntimeBinding) (OwnerRuntimeBinding, error) {
	return updatePocketIDRuntime(workspace, func(p *PocketIDRuntimeBinding) error {
		if p.Contract == pocketid.MembershipContract && observed.Contract != p.Contract {
			return errors.New("localevidence: managed PocketID storage cannot lose its CAS contract")
		}
		observed.PreviousContainerID, observed.DrainedAt = p.PreviousContainerID, p.DrainedAt
		observed.PreviousProcess = p.PreviousProcess
		*p = observed
		return nil
	})
}
