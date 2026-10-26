package nativehost

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"os"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
)

// VerifyRuntipiCurrentBackupGraph is read-only. An unrelated selected activation
// does not invent membership in its journal. Instead this verifies the existing
// native generation custody. Missing custody cannot become same-name adoption.
func VerifyRuntipiCurrentBackupGraph(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, lineage backuplifecycle.AuthorityLineage, expectedSourceDigest, policySourceDigest, appliedRequestDigest string) error {
	retained, err := LoadRuntipiTerminalQualification(workspace, deployment, expectedSourceDigest)
	if err != nil {
		return err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	custody, _, err := readRuntipiVolumeCustody(workspace, tx, volumeCustodyPath(appliedRequestDigest, deployment.WorkloadRef))
	if err != nil {
		return err
	}
	if custody.AppGroupLineage == nil || custody.AppGroupLineage.Head == nil {
		return errors.New("exact current native app-group generation is not yet retained")
	}
	g, err := runtipiCompletedOwningGroup(workspace, *custody.AppGroupLineage.Head)
	if err != nil {
		return err
	}
	b := g.Binding
	if b.AppliedRequestDigest != appliedRequestDigest || b.SourceDigest != expectedSourceDigest || b.PolicySourceDigest != policySourceDigest || b.QualificationDigest != backuplifecycle.AppGroupDigest(retained.Qualification) || b.InstallationDigest != retained.InstallationDigest || custody.InstanceRef != deployment.InstanceRef || custody.Project != b.LogicalProject {
		return errors.New("current native group differs from held source/qualification/deployment")
	}
	if err = verifyRuntipiBinding(b, deployment, lineage); err != nil {
		return err
	}
	client, err := docker.NewLocalExecHandleClient()
	if err != nil {
		return err
	}
	d := &runtipiBackupControl{workspace: workspace, group: g, q: retained.Qualification, client: client}
	if err = d.validate(ctx); err != nil {
		return err
	}
	invocation := runtipiTerminalInvocation{Mode: "derive", URN: b.AppURN, Group: b.GroupID, AppID: b.AppID, PrincipalID: retained.Qualification.PrincipalID, Body: platformdeploy.RuntipiTerminalSubmission{RuntipiTerminalIntent: platformdeploy.RuntipiTerminalIntent{APIVersion: platformdeploy.RuntipiTerminalAPI, ManagerID: b.ManagerID, SourceDigest: b.SourceDigest}}}
	command, err := terminalCommand(retained.Qualification.NodeExecutable, invocation)
	if err != nil {
		return err
	}
	_, secret, err := loadRuntipiTerminalCustody(workspace, b)
	if err != nil {
		return err
	}
	defer clear(secret)
	raw, err := docker.NewLocalClient().ExecWithStdin(ctx, b.ManagerContainerID, command, secret)
	if err != nil {
		return errors.New("current native recipe inspection unconfirmed")
	}
	var recipe struct {
		AppID                int    `json:"appId"`
		RecipeDigest         string `json:"recipeDigest"`
		EffectiveInputDigest string `json:"effectiveInputDigest"`
	}
	if len(raw) > 262144 || json.Unmarshal([]byte(raw), &recipe) != nil || recipe.AppID != b.AppID || recipe.RecipeDigest != b.RecipeDigest || recipe.EffectiveInputDigest != b.EffectiveInputDigest {
		return errors.New("native current app incarnation/recipe changed")
	}
	actual, err := ObserveRuntipiAppGroupGeneration(ctx, g, "verify")
	if err != nil {
		return err
	}
	if backuplifecycle.AppGroupDigest(actual) != backuplifecycle.AppGroupDigest(currentRuntipiCustody(custody)) {
		return errors.New("daemon current graph differs from signed native custody")
	}
	return nil
}

func runtipiCompletedOwningGroup(workspace string, head backuplifecycle.AppGroupControl) (backuplifecycle.AppGroupControl, error) {
	evidence, err := backuplifecycle.ReadSnapshotAppGroupEvidence(workspace, head.Binding.JournalOperationID)
	var groups []backuplifecycle.AppGroupControl
	if err == nil {
		if (evidence.State != "completed" && evidence.State != "failed") || !evidence.NativeHoldsClosed {
			return head, errors.New("original snapshot still has native obligations")
		}
		groups = evidence.Groups
	} else if errors.Is(err, os.ErrNotExist) {
		record, e := lifecyclemutation.ReadRestoreActivationRecord(workspace, head.Binding.JournalOperationID)
		if e != nil {
			return head, e
		}
		if lifecyclemutation.RestoreAppGroupClosuresPending(record) || record.Status != lifecyclemutation.StatusSucceeded && record.Status != lifecyclemutation.StatusRecovered {
			return head, errors.New("original native activation not settled")
		}
		groups = record.RestoreActivation.AppGroups
	} else {
		return head, err
	}
	for _, g := range groups {
		if g.Binding.GroupID == head.Binding.GroupID && backuplifecycle.AppGroupDigest(normalizedRuntipiGroup(g)) == backuplifecycle.AppGroupDigest(normalizedRuntipiGroup(head)) && backuplifecycle.AppGroupRestored(g) {
			return g, nil
		}
	}
	return head, errors.New("native current generation differs from completed owning signed group")
}
