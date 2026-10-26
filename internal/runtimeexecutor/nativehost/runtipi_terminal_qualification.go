package nativehost

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
)

type RuntipiTerminalQualificationRequest struct {
	SiteRef                     string
	NodeRef                     string
	ExpectedSourceDigest        string
	ManagerID                   string
	ManagerContainerID          string
	ManagerImage                string
	PrincipalID                 int
	InputTransportReceiptDigest string
}
type runtipiQualificationEvidence struct {
	Version       int                                           `json:"version"`
	ProbeExecID   string                                        `json:"probeExecId"`
	Nonce         string                                        `json:"nonce"`
	CommandDigest string                                        `json:"commandDigest"`
	Qualification platformdeploy.RuntipiTerminalQualification   `json:"qualification"`
	Signature     localevidence.OwnerLifecycleMutationSignature `json:"signature"`
}

// Explicit real-node qualification only. This owner does not start a manager,
// mutate an app, issue credentials or infer source support from an image/tag.
func QualifyRuntipiTerminalControl(ctx context.Context, workspace string, request RuntipiTerminalQualificationRequest) (RuntipiQualifiedInstallation, error) {
	var result RuntipiQualifiedInstallation
	if err := lifecyclemutation.RequireAppGroupLease(ctx, workspace, "runtipi-qualification"); err != nil {
		return result, err
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return result, err
	}
	raw, err := localevidence.ResolveEncryptedLocalIssuedSecret(workspace, runtipiInstallationSecretRef)
	if err != nil {
		return result, err
	}
	defer clear(raw)
	var saved runtipiInstallationCustody
	if json.Unmarshal(raw, &saved) != nil || !saved.Bootstrapped || saved.OwnerRef != owner.OwnerRef || saved.SiteRef != request.SiteRef || saved.NodeRef != request.NodeRef || saved.SiteRef != owner.Binding.SiteRef || saved.NodeRef != owner.Binding.NodeRef || saved.Username == "" || saved.Password == "" {
		return result, errors.New("qualification requires exact existing encrypted installation Owner/node/operator")
	}
	q := platformdeploy.RuntipiTerminalQualification{ManagerID: request.ManagerID, SourceDigest: request.ExpectedSourceDigest, ManagerContainerID: request.ManagerContainerID, ManagerImage: request.ManagerImage, PrincipalID: request.PrincipalID, HelperDigest: RuntipiTerminalHelperDigest(), NodeExecutable: "/usr/local/bin/node", InputTransport: "docker-exec-stdin/v1", InputTransportReceiptDigest: request.InputTransportReceiptDigest}
	if err = platformdeploy.ValidateRuntipiTerminalQualification(q); err != nil {
		return result, err
	}
	client, err := docker.NewLocalExecHandleClient()
	if err != nil {
		return result, err
	}
	if err = client.Qualify(ctx); err != nil {
		return result, err
	}
	manager, err := docker.NewLocalClient().InspectContainer(ctx, q.ManagerContainerID)
	if err != nil {
		return result, err
	}
	if !manager.State.Running || manager.Config.Image != q.ManagerImage {
		return result, errors.New("qualification exact running manager/image differs")
	}
	if err = validateRuntipiHelperEnvironment(manager); err != nil {
		return result, err
	}
	nonce := runtipiControlUUID()
	if nonce == "" {
		return result, errors.New("qualification entropy unavailable")
	}
	invocation := runtipiTerminalInvocation{Mode: "qualify", PrincipalID: q.PrincipalID, Nonce: nonce, Body: platformdeploy.RuntipiTerminalSubmission{RuntipiTerminalIntent: platformdeploy.RuntipiTerminalIntent{APIVersion: platformdeploy.RuntipiTerminalAPI, ManagerID: q.ManagerID, SourceDigest: q.SourceDigest}}}
	command, err := terminalCommand(q.NodeExecutable, invocation)
	if err != nil {
		return result, err
	}
	handle, err := client.PrepareInput(ctx, q.ManagerContainerID, command)
	if err != nil {
		return result, err
	}
	secret, err := json.Marshal(struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{saved.Username, saved.Password})
	if err != nil {
		return result, err
	}
	defer clear(secret)
	if err = client.StartInput(ctx, handle, command, secret); err != nil {
		return result, errors.New("attached qualification original handle reply unresolved; custody not issued")
	}
	completed, err := client.Complete(ctx, handle, q.ManagerContainerID, command, 73)
	if err != nil || !completed {
		return result, errors.New("actual qualified stdin/EOF/helper/API/principal probe did not complete")
	}
	evidence := runtipiQualificationEvidence{Version: 1, ProbeExecID: handle, Nonce: nonce, CommandDigest: backuplifecycle.AppGroupDigest(command), Qualification: q}
	unsigned, err := json.Marshal(evidence)
	if err != nil {
		return result, err
	}
	evidence.Signature, err = localevidence.SignOwnerLifecycleMutation(workspace, unsigned)
	if err != nil {
		return result, err
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(raw, &payload) != nil {
		return result, errors.New("installation payload invalid")
	}
	payload["terminalControl"], err = json.Marshal(q)
	if err != nil {
		return result, err
	}
	payload["terminalControlEvidence"], err = json.Marshal(evidence)
	if err != nil {
		return result, err
	}
	// Same existing custody owner, held OS lease, exact preimage and readback.
	current, err := localevidence.ResolveEncryptedLocalIssuedSecret(workspace, runtipiInstallationSecretRef)
	if err != nil {
		return result, err
	}
	same := backuplifecycle.AppGroupDigest(current) == backuplifecycle.AppGroupDigest(raw)
	clear(current)
	if !same {
		return result, errors.New("installation changed before qualification commit")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return result, err
	}
	defer clear(encoded)
	if err = localevidence.StoreEncryptedLocalIssuedSecret(workspace, runtipiInstallationSecretRef, encoded); err != nil {
		return result, err
	}
	readback, err := localevidence.ResolveEncryptedLocalIssuedSecret(workspace, runtipiInstallationSecretRef)
	if err != nil {
		return result, err
	}
	defer clear(readback)
	if backuplifecycle.AppGroupDigest(readback) != backuplifecycle.AppGroupDigest(encoded) {
		return result, errors.New("qualification custody readback changed")
	}
	return LoadRuntipiTerminalQualification(workspace, SelectedPaaSWorkloadDeployment{SiteRef: request.SiteRef, NodeRef: request.NodeRef}, request.ExpectedSourceDigest)
}
