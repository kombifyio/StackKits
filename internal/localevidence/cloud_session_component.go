package localevidence

import (
	"errors"
	"regexp"
)

const CloudSessionProject = "stackkit-cloud-core-standalone"
const CloudSessionTinyAuthVolume = CloudSessionProject + "_tinyauth-data"
const CloudSessionLegacyTinyAuthImage = "ghcr.io/steveiliop56/tinyauth:v5.0.7@sha256:0793c71c49906e079d90c7e693cded9df569217a92d717dc9b171f2116fcd1c6"

// CloudSessionComponentMigration is part of the same Owner-signed session
// transaction. Its cold original-data snapshot is never mounted by TinyAuth.
// It does not attest a current release, Plan or whole-kit upgrade.
type CloudSessionComponentMigration struct {
	OperationID          string `json:"operationId"`
	FromImage            string `json:"fromImage"`
	ToImage              string `json:"toImage"`
	TargetImageID        string `json:"targetImageId,omitempty"`
	HelperImage          string `json:"helperImage"`
	Volume               string `json:"volume"`
	SnapshotVolume       string `json:"snapshotVolume"`
	RetainedTargetVolume string `json:"retainedTargetVolume"`
	Phase                string `json:"phase"`
	SnapshotDigest       string `json:"snapshotDigest,omitempty"`
	RetainedTargetDigest string `json:"retainedTargetDigest,omitempty"`
}

var cloudSessionOperationID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var cloudSessionImageDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func (m CloudSessionComponentMigration) Validate() error {
	if !cloudSessionOperationID.MatchString(m.OperationID) || m.FromImage != CloudSessionLegacyTinyAuthImage ||
		m.Volume != CloudSessionTinyAuthVolume || m.SnapshotVolume != CloudSessionProject+"_session-"+m.OperationID+"-original" ||
		m.RetainedTargetVolume != CloudSessionProject+"_session-"+m.OperationID+"-target" {
		return errors.New("cloud_session_transition_invalid: component migration escapes the retained TinyAuth identity")
	}
	switch m.Phase {
	case "prepared", "quiescing", "snapshot-copying", "snapshot-ready", "target-starting", "target-active", "abort-quiescing", "target-preserving", "original-restoring", "original-restored":
	default:
		return errors.New("cloud_session_transition_invalid: unknown component migration phase")
	}
	if m.TargetImageID != "" && !cloudSessionImageDigest.MatchString(m.TargetImageID) ||
		m.SnapshotDigest != "" && !cloudSessionImageDigest.MatchString(m.SnapshotDigest) ||
		m.RetainedTargetDigest != "" && !cloudSessionImageDigest.MatchString(m.RetainedTargetDigest) {
		return errors.New("cloud_session_transition_invalid: malformed component migration digest")
	}
	return nil
}

type CloudSessionCapture struct {
	SourceDigest  string
	OverlayDigest string
	Containers    []CloudSessionContainer
	Migration     *CloudSessionComponentMigration
}
