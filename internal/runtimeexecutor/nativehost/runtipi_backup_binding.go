package nativehost

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
)

type RuntipiQualifiedInstallation struct {
	OwnerRef           string
	SiteRef            string
	NodeRef            string
	InstallationDigest string
	Qualification      platformdeploy.RuntipiTerminalQualification
}

// Read-only retained qualification. No credentials escape this owner and no
// qualification is minted from a source tag, image build or native ACK.
func LoadRuntipiTerminalQualification(workspace string, deployment SelectedPaaSWorkloadDeployment, expectedSourceDigest string) (RuntipiQualifiedInstallation, error) {
	var result RuntipiQualifiedInstallation
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return result, err
	}
	raw, err := localevidence.ResolveEncryptedLocalIssuedSecret(workspace, runtipiInstallationSecretRef)
	if err != nil {
		return result, err
	}
	defer clear(raw)
	var saved struct {
		runtipiInstallationCustody
		TerminalControl *platformdeploy.RuntipiTerminalQualification `json:"terminalControl"`
	}
	if json.Unmarshal(raw, &saved) != nil || saved.TerminalControl == nil || !saved.Bootstrapped || saved.OwnerRef != owner.OwnerRef || saved.SiteRef != owner.Binding.SiteRef || saved.NodeRef != owner.Binding.NodeRef || saved.SiteRef != deployment.SiteRef || saved.NodeRef != deployment.NodeRef || saved.Username == "" || saved.Password == "" || saved.TerminalControl.SourceDigest != expectedSourceDigest {
		return result, errors.New("exact retained terminal qualification/Owner/source absent")
	}
	q := *saved.TerminalControl
	if err = platformdeploy.ValidateRuntipiTerminalQualification(q); err != nil {
		return result, err
	}
	if err = validateRuntipiQualificationEvidence(workspace, raw, q); err != nil {
		return result, err
	}
	if q.HelperDigest != RuntipiTerminalHelperDigest() {
		return result, errors.New("qualified helper bytes changed")
	}
	return RuntipiQualifiedInstallation{owner.OwnerRef, saved.SiteRef, saved.NodeRef, saved.InstallationID, q}, nil
}
func DeriveRuntipiAppGroupBinding(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, lineage backuplifecycle.AuthorityLineage, expectedSourceDigest, policySourceDigest, operation string, appliedRequestDigest ...string) (backuplifecycle.AppGroupBinding, error) {
	var b backuplifecycle.AppGroupBinding
	if len(appliedRequestDigest) != 1 || len(appliedRequestDigest[0]) != 71 || !strings.HasPrefix(appliedRequestDigest[0], "sha256:") {
		return b, errors.New("exact applied execution request digest required")
	}
	if err := lifecyclemutation.RequireAppGroupLease(ctx, workspace, operation); err != nil {
		return b, err
	}
	retained, err := LoadRuntipiTerminalQualification(workspace, deployment, expectedSourceDigest)
	if err != nil {
		return b, err
	}
	q := retained.Qualification
	b = backuplifecycle.AppGroupBinding{ManagerID: q.ManagerID, ManagerContainerID: q.ManagerContainerID, ManagerImage: q.ManagerImage, SourceDigest: q.SourceDigest, InstallationDigest: retained.InstallationDigest, OwnerRef: retained.OwnerRef, SiteRef: retained.SiteRef, NodeRef: retained.NodeRef, LogicalProject: "stackkit-" + deployment.WorkloadRef + "-" + deployment.NodeRef, JournalOperationID: operation, NativeJournalOperationID: runtipiControlUUID(), GroupID: runtipiControlUUID(), PolicySourceDigest: policySourceDigest, AuthorityLineage: lineage, QualificationDigest: backuplifecycle.AppGroupDigest(q), AppliedRequestDigest: appliedRequestDigest[0]}
	b.PhysicalProject = b.LogicalProject + "__user"
	b.AppURN = b.LogicalProject + ":_user"
	if b.GroupID == "" || b.NativeJournalOperationID == "" || !strings.HasPrefix(policySourceDigest, "sha256:") {
		return b, errors.New("native group identity/source digest invalid")
	}
	state, err := platformdeploy.LoadOwnerHostDeployment(workspace, b.LogicalProject)
	if err != nil {
		return b, err
	}
	if err = platformdeploy.RequireHostDeploymentManager(state, "runtipi"); err != nil {
		return b, err
	}
	if state.ServerID != retained.InstallationDigest || state.Deployment.ExternalID != b.AppURN || state.ArtifactDigest != deployment.ArtifactDigest || state.InstanceRef != deployment.InstanceRef || state.SiteRef != b.SiteRef || state.NodeRef != b.NodeRef {
		return b, errors.New("native app differs from exact held node deployment")
	}
	client, err := docker.NewLocalExecHandleClient()
	if err != nil {
		return b, err
	}
	d := &runtipiBackupControl{workspace: workspace, group: backuplifecycle.AppGroupControl{Binding: b}, q: q, client: client}
	if err = d.validate(ctx); err != nil {
		return b, err
	}
	p := runtipiTerminalInvocation{Mode: "derive", URN: b.AppURN, Group: b.GroupID, PrincipalID: q.PrincipalID, Body: platformdeploy.RuntipiTerminalSubmission{RuntipiTerminalIntent: platformdeploy.RuntipiTerminalIntent{APIVersion: platformdeploy.RuntipiTerminalAPI, ManagerID: b.ManagerID, SourceDigest: b.SourceDigest}}}
	command, err := terminalCommand(q.NodeExecutable, p)
	if err != nil {
		return b, err
	}
	_, secret, err := loadRuntipiTerminalCustody(workspace, b)
	if err != nil {
		return b, err
	}
	defer clear(secret)
	raw, err := docker.NewLocalClient().ExecWithStdin(ctx, q.ManagerContainerID, command, secret)
	if err != nil {
		return b, errors.New("current native app/recipe capability inspection failed")
	}
	var recipe struct {
		platformdeploy.RuntipiTerminalCapability
		AppID                int    `json:"appId"`
		AppURN               string `json:"appUrn"`
		RecipeDigest         string `json:"recipeDigest"`
		EffectiveInputDigest string `json:"effectiveInputDigest"`
	}
	if len(raw) > 262144 || json.Unmarshal([]byte(raw), &recipe) != nil || recipe.AppURN != b.AppURN || recipe.AppID < 1 || recipe.SourceDigest != b.SourceDigest || recipe.ManagerID != b.ManagerID {
		return b, errors.New("native app incarnation/recipe response changed")
	}
	b.AppID = recipe.AppID
	b.RecipeDigest = recipe.RecipeDigest
	b.EffectiveInputDigest = recipe.EffectiveInputDigest
	b.BindingDigest = runtipiBindingDigest(b, deployment)
	return b, nil
}
func runtipiBindingDigest(b backuplifecycle.AppGroupBinding, deployment SelectedPaaSWorkloadDeployment) string {
	b.BindingDigest = ""
	return backuplifecycle.AppGroupDigest(struct {
		Binding    backuplifecycle.AppGroupBinding
		Deployment SelectedPaaSWorkloadDeployment
	}{b, deployment})
}
func verifyRuntipiBinding(b backuplifecycle.AppGroupBinding, deployment SelectedPaaSWorkloadDeployment, lineage backuplifecycle.AuthorityLineage) error {
	if !reflect.DeepEqual(b.AuthorityLineage, lineage) || b.BindingDigest != runtipiBindingDigest(b, deployment) {
		return errors.New("native group does not bind full held lineage/deployment")
	}
	return nil
}
