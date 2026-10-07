package commands

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/appsetup"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

type homeAssistantSetupCredentials struct {
	Username    string                                  `json:"username"`
	Password    string                                  `json:"password"`
	DisplayName string                                  `json:"displayName"`
	Language    string                                  `json:"language"`
	Connector   *appsetup.HomeAssistantConnectorRequest `json:"connector,omitempty"`
}

// Read-only status never calls this. Apply/setup can discover the owner's
// explicit request in the same private setup document as credentials.
func requestedHomeAssistantConnector(workspace, path string) (bool, error) {
	if _, err := os.Lstat(filepath.Join(workspace, path)); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	var credentials homeAssistantSetupCredentials
	if err := readNativeSetupCredentialJSON(workspace, path, &credentials); err != nil {
		return false, err
	}
	defer func() { credentials.Password = "" }()
	return credentials.Connector != nil, nil
}

// Resource/revision are requested external correlation, never local authority.
// Production supplies the existing persisted-Compose custody observer.
func executeVerifiedHomeAssistantConnector(
	ctx context.Context, workspace string, deployment nativehost.SelectedPaaSWorkloadDeployment,
	request *appsetup.HomeAssistantConnectorRequest,
	observe func(context.Context, string, nativehost.SelectedPaaSWorkloadDeployment) (map[string]string, error),
	run func(*appsetup.HomeAssistantConnectorRequest) (appsetup.HomeAssistantOwnerResult, error),
) (appsetup.HomeAssistantOwnerResult, *applicationlifecycle.HomeAssistantConnectorObservation, error) {
	if request == nil {
		result, err := run(nil)
		return result, nil, err
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return appsetup.HomeAssistantOwnerResult{}, nil, err
	}
	bundle, err := architecturev2renderer.ParseHomeAssistantWorkloadBundle(deployment.Bundle)
	if err != nil {
		return appsetup.HomeAssistantOwnerResult{}, nil, err
	}
	node := localevidence.LocalBinding{SiteRef: deployment.SiteRef, NodeRef: deployment.NodeRef, ChannelRef: deployment.ExecutionChannelRef}
	if owner.Binding != node || request.Binding.Node != node || bundle.SiteRef != node.SiteRef || bundle.NodeRef != node.NodeRef || bundle.InstanceRef != deployment.InstanceRef || bundle.Release != deployment.Release || len(bundle.Components) != 1 || request.Binding.ImageDigest != bundle.Components[0].ImageDigest {
		return appsetup.HomeAssistantOwnerResult{}, nil, errors.New("home_assistant_connector_deployment_mismatch")
	}
	verify := func() error {
		ids, err := observe(ctx, workspace, deployment)
		if err != nil {
			return err
		}
		if ids[bundle.Components[0].ID] != request.Binding.ContainerID || request.Binding.ContainerID == "" {
			return errors.New("home_assistant_connector_container_mismatch")
		}
		return nil
	}
	if err := verify(); err != nil {
		return appsetup.HomeAssistantOwnerResult{}, nil, err
	}
	admitted := *request
	admitted.Workspace = workspace
	result, err := run(&admitted)
	if err != nil {
		return appsetup.HomeAssistantOwnerResult{}, nil, err
	}
	if err := verify(); err != nil {
		return appsetup.HomeAssistantOwnerResult{}, nil, err
	}
	if result.Connector == nil || result.Connector.RequestedBinding != admitted.Binding || result.Connector.OwnerRef != owner.OwnerRef || result.Connector.HAUserID != result.UserID || result.Connector.HAVersion != deployment.Release {
		return appsetup.HomeAssistantOwnerResult{}, nil, errors.New("home_assistant_connector_receipt_mismatch")
	}
	unsigned := *result.Connector
	unsigned.Signature = localevidence.OwnerPolicyStateSignature{}
	payload, _ := json.Marshal(unsigned)
	if err := localevidence.VerifyOwnerPolicyState(workspace, payload, result.Connector.Signature); err != nil {
		return appsetup.HomeAssistantOwnerResult{}, nil, err
	}
	return result, &applicationlifecycle.HomeAssistantConnectorObservation{
		Issuer:                  *result.Connector,
		VerifiedLocalDeployment: applicationlifecycle.HomeAssistantConnectorDeployment{Node: node, InstanceRef: deployment.InstanceRef, ContainerID: request.Binding.ContainerID, ImageDigest: bundle.Components[0].ImageDigest},
	}, nil
}
