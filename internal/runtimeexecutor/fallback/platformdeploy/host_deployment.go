package platformdeploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// HostDeployment is the node's custody of the platform identity for the same
// generated Compose project. It contains no credential or application data.
type HostDeployment struct {
	APIVersion     string `json:"apiVersion"`
	WorkloadRef    string `json:"workloadRef"`
	InstanceRef    string `json:"instanceRef"`
	ArtifactDigest string `json:"artifactDigest"`
	SiteRef        string `json:"siteRef"`
	NodeRef        string `json:"nodeRef"`
	ServerID       string `json:"serverId"`
	EnvironmentID  string `json:"environmentId,omitempty"`
	RuntimeProject string `json:"runtimeProject,omitempty"`
	RecipeDigest   string `json:"recipeDigest,omitempty"`
	// ActivationSourceIDs witnesses the runtime before an asynchronous restart.
	// It is present only while that exact Coolify activation is unsettled.
	ActivationSourceIDs map[string]string                        `json:"activationSourceIds,omitempty"`
	Deployment          DeploymentRef                            `json:"deployment"`
	Manager             string                                   `json:"manager,omitempty"`
	Handover            *HostManagerHandover                     `json:"handover,omitempty"`
	PlanBinding         *HostManagerPlanBinding                  `json:"planBinding,omitempty"`
	Signature           *localevidence.OwnerPolicyStateSignature `json:"signature,omitempty"`
}

// HostManagerPlanBinding authorizes control of the same applied workload after
// a manager transfer. It is signed as part of the existing node custody.
type HostManagerPlanBinding struct {
	SourceDigest        string            `json:"sourceDigest,omitempty"`
	VolumeNames         map[string]string `json:"volumeNames,omitempty"`
	PlanHash            string            `json:"planHash"`
	ApplyResultHash     string            `json:"applyResultHash"`
	ServiceContractHash string            `json:"serviceContractHash"`
	ServiceKey          string            `json:"serviceKey"`
	RuntimeRef          string            `json:"runtimeRef"`
	SourceAdapter       string            `json:"sourceAdapter"`
	ArtifactDigest      string            `json:"artifactDigest"`
}

// HostManagerHandover retains an interrupted transfer in the existing node
// custody. Neither manager may act until the exact transfer is completed.
type HostManagerHandover struct {
	AttachmentState   string `json:"attachmentState,omitempty"`
	SourceDetachState string `json:"sourceDetachState,omitempty"`
	ID                string `json:"id"`
	From              string `json:"from"`
	To                string `json:"to"`
	Status            string `json:"status"`
	RecipeDigest      string `json:"recipeDigest"`
	RuntimeDigest     string `json:"runtimeDigest"`
	SourceServerID    string `json:"sourceServerId,omitempty"`
	SourceExternalID  string `json:"sourceExternalId,omitempty"`
	TargetServerID    string `json:"targetServerId,omitempty"`
	TargetExternalID  string `json:"targetExternalId,omitempty"`
	TargetPlanHash    string `json:"targetPlanHash,omitempty"`
	TargetCreateState string `json:"targetCreateState,omitempty"`
	CoreProtocol      string `json:"coreProtocol,omitempty"`
}

func HostDeploymentManager(state HostDeployment) string {
	if state.Manager != "" {
		return state.Manager
	}
	return state.Deployment.Platform
}

func RequireHostDeploymentManager(state HostDeployment, manager string) error {
	if state.Handover != nil && state.Handover.Status != "succeeded" && state.Handover.Status != "cancelled" {
		return errors.New("workload manager handover is incomplete; resume its exact transition before control")
	}
	if HostDeploymentManager(state) != manager {
		return fmt.Errorf("workload manager is %q; explicit transition to %q is required", HostDeploymentManager(state), manager)
	}
	return nil
}

const hostDeploymentAPIVersion = "stackkit.platform-host-deployment/v1"
const hostManagerAPIVersion = "stackkit.platform-host-deployment/v2"
const hostDeploymentFile = "platform-deployment.json"

var hostProjectPattern = regexp.MustCompile(`^stackkit-[a-z0-9][a-z0-9-]{0,125}$`)
var hostArtifactDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func validateHostDeployment(project string, state HostDeployment) error {
	if !hostProjectPattern.MatchString(project) || (state.APIVersion != hostDeploymentAPIVersion && state.APIVersion != hostManagerAPIVersion) ||
		state.WorkloadRef == "" || state.InstanceRef == "" || state.SiteRef == "" || state.NodeRef == "" ||
		project != "stackkit-"+state.WorkloadRef+"-"+state.NodeRef ||
		!hostArtifactDigestPattern.MatchString(state.ArtifactDigest) ||
		(state.Deployment.Platform != "komodo" && state.Deployment.Platform != "coolify" && state.Deployment.Platform != "runtipi" && state.Deployment.Platform != "dokploy" && state.Deployment.Platform != "standalone-compose") ||
		(state.Deployment.Platform != "standalone-compose" && state.Deployment.Platform != "dokploy" && state.ServerID == "") || state.Deployment.AppName != project ||
		state.Deployment.ExternalID == "" || strings.TrimSpace(state.Deployment.ExternalID) != state.Deployment.ExternalID {
		return errors.New("platform deployment custody does not bind the exact node project")
	}
	if state.APIVersion == hostManagerAPIVersion && state.Manager == "" {
		return errors.New("v2 workload manager custody requires its explicit manager")
	}
	if state.Manager != "" && state.Manager != "komodo" && state.Manager != "coolify" && state.Manager != "runtipi" && state.Manager != "dokploy" && state.Manager != "standalone-compose" {
		return errors.New("workload manager custody names an unsupported manager")
	}
	if state.Deployment.Platform == "coolify" && state.Manager != "coolify" {
		return errors.New("Coolify requires explicit signed workload manager custody")
	}
	if len(state.ActivationSourceIDs) != 0 {
		if state.Deployment.Platform != "coolify" || (state.Deployment.ObservedStatus != "pending-provider-activation" && state.Deployment.ObservedStatus != "uncertain-provider-activation") {
			return errors.New("activation runtime witness requires unsettled Coolify custody")
		}
		for component, id := range state.ActivationSourceIDs {
			if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`).MatchString(component) || (id != "" && !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(id)) {
				return errors.New("activation runtime witness has an invalid component or container identity")
			}
		}
	}
	if state.Deployment.Platform == "dokploy" && (state.Manager != "dokploy" || !dokployIDPattern.MatchString(state.Deployment.ExternalID) ||
		!dokployIDPattern.MatchString(state.EnvironmentID) || !regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,125}$`).MatchString(state.RuntimeProject) ||
		!hostArtifactDigestPattern.MatchString(state.RecipeDigest)) {
		return errors.New("Dokploy requires explicit signed manager, environment, physical project and recipe custody")
	}
	if state.Deployment.Platform == "runtipi" && (state.Manager != "runtipi" || state.Deployment.ExternalID != project+":_user" || !runtipiAppName.MatchString(project)) {
		return errors.New("Runtipi requires its exact custom-app identity and explicit signed workload manager custody")
	}
	if state.Deployment.Platform == "coolify" && !regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,125}$`).MatchString(state.Deployment.ExternalID) {
		return errors.New("Coolify custody has an invalid physical Compose project")
	}
	if b := state.PlanBinding; b != nil {
		if b.SourceAdapter == "coolify" {
			if state.Manager != "coolify" || !hostArtifactDigestPattern.MatchString(b.SourceDigest) || len(b.VolumeNames) == 0 {
				return errors.New("Coolify source binding requires its signed data selection")
			}
			for logical, physical := range b.VolumeNames {
				prefix := project + "_"
				if !strings.HasPrefix(logical, prefix) || !regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`).MatchString(logical) || (physical != logical && physical != state.Deployment.ExternalID+"_"+strings.TrimPrefix(logical, prefix)) {
					return errors.New("Coolify physical source is outside its exact node project")
				}
			}
		}
		if state.Manager == "" || !hostArtifactDigestPattern.MatchString(b.PlanHash) ||
			!hostArtifactDigestPattern.MatchString(b.ApplyResultHash) || !hostArtifactDigestPattern.MatchString(b.ServiceContractHash) ||
			b.ArtifactDigest != state.ArtifactDigest || b.ServiceKey != state.WorkloadRef || b.RuntimeRef != state.WorkloadRef+"-"+state.NodeRef ||
			(b.SourceAdapter != "application-compose" && b.SourceAdapter != "komodo" && b.SourceAdapter != "coolify") {
			return errors.New("manager plan binding differs from the exact applied node workload")
		}
	}
	if state.Deployment.Platform == "standalone-compose" && (state.Manager != "standalone-compose" || state.Deployment.ExternalID != project) {
		return errors.New("Compose custody does not bind its native project identity")
	}
	if h := state.Handover; h != nil {
		if h.AttachmentState != "" && (h.CoreProtocol != "" || h.From == h.To ||
			!((h.Status == "pending" && h.AttachmentState == "updating") || (h.Status == "succeeded" && h.AttachmentState == "confirmed"))) {
			return errors.New("generic manager attachment journal has an invalid signed phase")
		}
		detachQualified := h.SourceDetachState == "detached" || (h.Status == "pending" && h.TargetCreateState == "not-started" && (h.SourceDetachState == "not-started" || h.SourceDetachState == "detaching"))
		coreTransfer := h.CoreProtocol == "komodo-core-detached-create/v2" && detachQualified && h.From == "komodo" && h.To == "komodo" && h.SourceServerID != "" && h.SourceExternalID != "" &&
			h.TargetServerID != "" && h.TargetServerID != h.SourceServerID && hostArtifactDigestPattern.MatchString(h.TargetPlanHash) &&
			(h.TargetCreateState == "not-started" || h.TargetCreateState == "creating" || h.TargetCreateState == "restoring-source" || ((h.TargetCreateState == "created" || h.TargetCreateState == "attaching") && h.TargetExternalID != "")) &&
			(((h.Status == "pending" || (h.Status == "cancelled" && h.TargetCreateState == "restoring-source")) && state.ServerID == h.SourceServerID && state.Deployment.ExternalID == h.SourceExternalID) ||
				(h.Status == "succeeded" && h.TargetCreateState == "attaching" && state.ServerID == h.TargetServerID && state.Deployment.ExternalID == h.TargetExternalID))
		if state.Manager == "" || !regexp.MustCompile(`^handover-[0-9a-f]{32}$`).MatchString(h.ID) ||
			(h.From != "komodo" && h.From != "standalone-compose") || (h.To != "komodo" && h.To != "standalone-compose") || (h.From == h.To && !coreTransfer) ||
			(h.Status != "pending" && h.Status != "succeeded" && !(coreTransfer && h.Status == "cancelled")) || !hostArtifactDigestPattern.MatchString(h.RecipeDigest) || !hostArtifactDigestPattern.MatchString(h.RuntimeDigest) ||
			((h.Status == "pending" || h.Status == "cancelled") && state.Manager != h.From) || (h.Status == "succeeded" && state.Manager != h.To) {
			return errors.New("workload manager handover custody is invalid")
		}
	}
	return nil
}

func SaveOwnerHostDeployment(workspace, project string, state HostDeployment) error {
	state.APIVersion = hostDeploymentAPIVersion
	if state.Manager != "" {
		state.APIVersion = hostManagerAPIVersion
	}
	if err := validateHostDeployment(project, state); err != nil {
		return err
	}
	if state.Manager != "" {
		state.Signature = nil
		canonical, err := json.Marshal(state)
		if err != nil {
			return err
		}
		signature, err := localevidence.SignOwnerPolicyState(workspace, canonical)
		if err != nil {
			return err
		}
		state.Signature = &signature
	}
	directory := filepath.Join(workspace, ".stackkit", "runtime", "applications", project)
	root, err := confinedfs.Open(directory)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600(hostDeploymentFile, append(data, '\n'))
	if err == nil && (!result.Installed || !result.FileSynced) {
		return errors.New("platform deployment custody was not durably installed")
	}
	return err
}

func LoadOwnerHostDeployment(workspace, project string) (HostDeployment, error) {
	if !hostProjectPattern.MatchString(project) {
		return HostDeployment{}, errors.New("platform project identity is invalid")
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return HostDeployment{}, err
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return HostDeployment{}, err
	}
	file, err := view.Open(filepath.ToSlash(filepath.Join(".stackkit", "runtime", "applications", project, hostDeploymentFile)))
	if err != nil {
		return HostDeployment{}, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return HostDeployment{}, errors.New("platform deployment custody is not private")
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return HostDeployment{}, errors.New("platform deployment custody cannot be read")
	}
	var state HostDeployment
	if err := json.Unmarshal(data, &state); err != nil {
		return state, errors.New("platform deployment custody is invalid")
	}
	if err := validateHostDeployment(project, state); err != nil {
		return state, err
	}
	if state.Manager != "" {
		if state.Signature == nil {
			return state, errors.New("explicit workload manager custody is unsigned")
		}
		signature := *state.Signature
		unsigned := state
		unsigned.Signature = nil
		canonical, err := json.Marshal(unsigned)
		if err != nil {
			return state, err
		}
		if err := localevidence.VerifyOwnerPolicyState(workspace, canonical, signature); err != nil {
			return state, err
		}
	}
	return state, nil
}

// HasOwnerHostDeployment distinguishes absent native custody from invalid
// custody. Only the former may use a historical deployment-state projection.
func HasOwnerHostDeployment(workspace, project string) (bool, error) {
	_, err := LoadOwnerHostDeployment(workspace, project)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// RequireComposeWorkloadOwner guards every direct Compose mutation, including
// service control under a retained plan. A platform claim is authoritative even
// when that older plan still selects Compose; malformed custody never falls back.
func RequireComposeWorkloadOwner(workspace, project string) error {
	state, err := LoadOwnerHostDeployment(workspace, project)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("workload manager custody cannot be verified: %w", err)
	}
	return RequireHostDeploymentManager(state, "standalone-compose")
}
