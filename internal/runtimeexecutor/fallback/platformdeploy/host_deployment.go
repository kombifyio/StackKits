package platformdeploy

import "github.com/kombifyio/stackkits/internal/deploymentcontract"

type HostDeployment = deploymentcontract.HostDeployment
type HostManagerPlanBinding = deploymentcontract.HostManagerPlanBinding
type HostManagerHandover = deploymentcontract.HostManagerHandover

func HostDeploymentManager(state HostDeployment) string {
	return deploymentcontract.HostDeploymentManager(state)
}

func RequireHostDeploymentManager(state HostDeployment, manager string) error {
	return deploymentcontract.RequireHostDeploymentManager(state, manager)
}

func SaveOwnerHostDeployment(workspace, project string, state HostDeployment) error {
	return deploymentcontract.SaveOwnerHostDeployment(workspace, project, state)
}

func LoadOwnerHostDeployment(workspace, project string) (HostDeployment, error) {
	return deploymentcontract.LoadOwnerHostDeployment(workspace, project)
}

func RemoveOwnerHostDeployment(workspace, project string, expected HostDeployment) error {
	return deploymentcontract.RemoveOwnerHostDeployment(workspace, project, expected)
}

func HasOwnerHostDeployment(workspace, project string) (bool, error) {
	return deploymentcontract.HasOwnerHostDeployment(workspace, project)
}

func RequireComposeWorkloadOwner(workspace, project string) error {
	return deploymentcontract.RequireComposeWorkloadOwner(workspace, project)
}
