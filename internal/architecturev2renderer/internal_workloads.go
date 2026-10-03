package architecturev2renderer

// RestoreActivationComposeVariable is the Compose interpolation variable that
// restore activation sets to "true" when it starts a restored workload. A
// component's restoreActivationEnvironment reads it with a "false" default,
// so every other start leaves it false.
const RestoreActivationComposeVariable = "STACKKIT_RESTORE_ACTIVATION"

// internalWorkloadBundleRenderers registers the renderers of internal-only
// workloads (authority manifest internalSources). The private build adds them
// from files the public export removes with their authority source.
var internalWorkloadBundleRenderers []func() (RendererContract, UnitRenderer)

// governedCustodyNodeRights admits custody files and restore-activation
// variables per workload module; a module without an entry receives neither.
var governedCustodyNodeRights = map[string]func(component selectedPaaSRuntimeComponent, secretRefs map[string]string, path string) ([]ApplicationDeliverySecretFile, []string, error){}

func parseGovernedCustodyNodeFields(component selectedPaaSRuntimeComponent, moduleRef string, secretRefs map[string]string, path string) ([]ApplicationDeliverySecretFile, []string, error) {
	if len(component.SecretFiles) == 0 && len(component.RestoreActivationEnvironment) == 0 {
		return nil, nil, nil
	}
	parse, governed := governedCustodyNodeRights[moduleRef]
	if !governed {
		return nil, nil, fail(ErrInvalidPlan, path, "custody files and restore activation variables are admitted only for a governed component")
	}
	return parse(component, secretRefs, path)
}

// governedCommandSecretFile admits exactly one custody file, read by the
// StackKits-governed command of moduleRef's componentID instead of a secret
// environment variable (plan 20, S2.2 inventory): the value never enters the
// container configuration, so a Compose and a native root deliver it alike.
func governedCommandSecretFile(componentID string, file selectedPaaSSecretFile) func(selectedPaaSRuntimeComponent, map[string]string, string) ([]ApplicationDeliverySecretFile, []string, error) {
	return func(component selectedPaaSRuntimeComponent, secretRefs map[string]string, path string) ([]ApplicationDeliverySecretFile, []string, error) {
		if component.ID != componentID || len(component.RestoreActivationEnvironment) != 0 ||
			len(component.SecretFiles) != 1 || component.SecretFiles[0] != file {
			return nil, nil, fail(ErrInvalidPlan, path, "the custody file is admitted only for the governed command of %s", componentID)
		}
		if _, exists := secretRefs[file.Slot]; !exists {
			return nil, nil, fail(ErrInvalidPlan, path+".secretFiles", "references an undeclared secret slot")
		}
		return []ApplicationDeliverySecretFile{{Slot: file.Slot, Target: file.Target, PathEnvironment: file.PathEnvironment, UID: file.UID, GID: file.GID}}, nil, nil
	}
}

// Custody files of the StackKits-governed commands.
var (
	giteaOwnerPasswordFile    = selectedPaaSSecretFile{Slot: "owner-password", Target: "/run/secrets/owner-password", PathEnvironment: "STACKKITS_OWNER_PASSWORD_FILE", UID: 1000, GID: 1000}
	forgejoOwnerPasswordFile  = giteaOwnerPasswordFile
	mosquittoPasswordFile     = selectedPaaSSecretFile{Slot: "mqtt-password", Target: "/run/secrets/mqtt-password", PathEnvironment: "MQTT_PASSWORD_FILE"}
	stalwartAdminPasswordFile = selectedPaaSSecretFile{Slot: "admin-password", Target: "/run/secrets/admin-password", PathEnvironment: "STACKKIT_ADMIN_SECRET_FILE", UID: 2000, GID: 2000}
)

func init() {
	governedCustodyNodeRights[giteaWorkloadModuleID] = governedCommandSecretFile("gitea", giteaOwnerPasswordFile)
	governedCustodyNodeRights[forgejoWorkloadModuleID] = governedCommandSecretFile("forgejo", forgejoOwnerPasswordFile)
	governedCustodyNodeRights[mosquittoWorkloadModuleID] = governedCommandSecretFile("mosquitto", mosquittoPasswordFile)
	governedCustodyNodeRights[stalwartWorkloadModuleID] = governedCommandSecretFile("stalwart", stalwartAdminPasswordFile)
}
