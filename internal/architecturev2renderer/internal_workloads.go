package architecturev2renderer

import "slices"

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

// governedEntrypointSecretFiles admits, per component of a module, exactly
// the custody files its StackKits-governed entrypoint reads into the process
// environment of the application (plan 20, S2.2: the images read these
// values only from their environment). No value enters the container
// configuration, so a Compose and a native root deliver them alike.
func governedEntrypointSecretFiles(byComponent map[string][]selectedPaaSSecretFile) func(selectedPaaSRuntimeComponent, map[string]string, string) ([]ApplicationDeliverySecretFile, []string, error) {
	return func(component selectedPaaSRuntimeComponent, secretRefs map[string]string, path string) ([]ApplicationDeliverySecretFile, []string, error) {
		files, governed := byComponent[component.ID]
		if !governed || len(component.RestoreActivationEnvironment) != 0 || !slices.Equal(component.SecretFiles, files) {
			return nil, nil, fail(ErrInvalidPlan, path, "custody files are admitted only for the governed entrypoint of their component")
		}
		return governedSecretFileDescriptors(files, secretRefs, path)
	}
}

func governedSecretFileDescriptors(files []selectedPaaSSecretFile, secretRefs map[string]string, path string) ([]ApplicationDeliverySecretFile, []string, error) {
	descriptors := make([]ApplicationDeliverySecretFile, 0, len(files))
	for _, file := range files {
		if _, exists := secretRefs[file.Slot]; !exists {
			return nil, nil, fail(ErrInvalidPlan, path+".secretFiles", "references an undeclared secret slot")
		}
		descriptors = append(descriptors, ApplicationDeliverySecretFile{Slot: file.Slot, Target: file.Target, PathEnvironment: file.PathEnvironment, UID: file.UID, GID: file.GID})
	}
	return descriptors, nil, nil
}

// custodyFile is a root-owned custody file at /run/secrets/<slot>.
func custodyFile(slot, pathEnvironment string) selectedPaaSSecretFile {
	return selectedPaaSSecretFile{Slot: slot, Target: "/run/secrets/" + slot, PathEnvironment: pathEnvironment}
}

// Custody files of the StackKits-governed entrypoints.
var (
	hermesSecretFiles = []selectedPaaSSecretFile{
		custodyFile("dashboard-password", "STACKKIT_DASHBOARD_PASSWORD_FILE"),
		custodyFile("dashboard-session-secret", "STACKKIT_DASHBOARD_SESSION_SECRET_FILE"),
	}
	paperclipSecretFiles = []selectedPaaSSecretFile{
		custodyFile("database-password", "STACKKIT_PAPERCLIP_DB_PASSWORD_FILE"),
		custodyFile("session-secret", "STACKKIT_SESSION_SECRET_FILE"),
	}
	pterodactylPanelSecretFiles = []selectedPaaSSecretFile{
		custodyFile("database-password", "STACKKIT_DB_PASSWORD_FILE"),
		custodyFile("app-key", "STACKKIT_APP_KEY_FILE"),
		custodyFile("hashids-salt", "STACKKIT_HASHIDS_SALT_FILE"),
	}
	pterodactylBootstrapSecretFiles = append(slices.Clone(pterodactylPanelSecretFiles),
		custodyFile("owner-password", "STACKKIT_OWNER_PASSWORD_FILE"),
		custodyFile("application-api-key", "STACKKIT_APPLICATION_API_KEY_FILE"),
		custodyFile("client-api-key", "STACKKIT_CLIENT_API_KEY_FILE"),
	)
)

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
	governedCustodyNodeRights[paperclipWorkloadModuleID] = governedEntrypointSecretFiles(map[string][]selectedPaaSSecretFile{"paperclip": paperclipSecretFiles})
	governedCustodyNodeRights[pterodactylWorkloadModuleID] = governedEntrypointSecretFiles(map[string][]selectedPaaSSecretFile{
		"panel": pterodactylPanelSecretFiles, "panel-bootstrap": pterodactylBootstrapSecretFiles,
	})
}
