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
