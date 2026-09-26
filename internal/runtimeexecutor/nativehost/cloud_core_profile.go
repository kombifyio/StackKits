package nativehost

import (
	"path"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
)

const (
	cloudStandaloneCoreModuleRef = "stackkits-cloud-core-standalone-runtime"
	// The Cloud core units under the opentofu and terramate generation
	// targets: an OpenTofu root main.tf that embeds the Compose artifact.
	cloudCoreOpenTofuUnitRef  = "opentofu"
	cloudCoreTerramateUnitRef = "terramate"
)

// The closed profile selects immutable contracts, never a second lifecycle.
// unit is empty for the compose unit the Cloud core executor applies; Verify
// also accepts the OpenTofu and Terramate units the OpenTofu executor applies.
type cloudCoreExecutionProfile struct {
	standalone bool
	unit       string
}

func cloudCoreProfileForModule(module string) (cloudCoreExecutionProfile, bool) {
	return cloudCoreExecutionProfile{standalone: module == cloudStandaloneCoreModuleRef}, module == cloudCoreModuleRef || module == cloudStandaloneCoreModuleRef
}

func (p cloudCoreExecutionProfile) moduleRef() string {
	if p.standalone {
		return cloudStandaloneCoreModuleRef
	}
	return cloudCoreModuleRef
}

func (p cloudCoreExecutionProfile) artifactPrefix() string {
	if p.standalone {
		return "cloud-core-standalone-compose-instance-"
	}
	return cloudCoreArtifactPrefix
}

func (p cloudCoreExecutionProfile) outputRef() string {
	if p.standalone {
		return "platform/cloud-core-standalone/compose.yaml"
	}
	return cloudCoreOutputRef
}

func (p cloudCoreExecutionProfile) image() (string, string) {
	if p.standalone {
		return "docker.io/library/nginx:alpine", "sha256:4a73073bd557c65b759505da037898b61f1be6cbcc3c2c3aeac22d2a470c1752"
	}
	return cloudCoreImageRef, cloudCoreImageDigest
}

func (p cloudCoreExecutionProfile) unitRef() string {
	if p.unit == "" {
		return cloudCoreUnitRef
	}
	return p.unit
}

func (p cloudCoreExecutionProfile) openTofu() bool {
	return p.unit == cloudCoreOpenTofuUnitRef || p.unit == cloudCoreTerramateUnitRef
}

// coreOutputRef is the governed Core artifact: the Compose artifact, or the
// OpenTofu root main.tf beside it under the OpenTofu units.
func (p cloudCoreExecutionProfile) coreOutputRef() string {
	if p.openTofu() {
		return path.Join(path.Dir(p.outputRef()), "main.tf")
	}
	return p.outputRef()
}

func (p cloudCoreExecutionProfile) contract() architecturev2renderer.RendererContract {
	switch {
	case p.unit == cloudCoreOpenTofuUnitRef && p.standalone:
		return architecturev2renderer.CloudStandaloneCoreOpenTofuRendererContract()
	case p.unit == cloudCoreOpenTofuUnitRef:
		return architecturev2renderer.CloudCoreOpenTofuRendererContract()
	case p.unit == cloudCoreTerramateUnitRef && p.standalone:
		return architecturev2renderer.CloudStandaloneCoreTerramateRendererContract()
	case p.unit == cloudCoreTerramateUnitRef:
		return architecturev2renderer.CloudCoreTerramateRendererContract()
	}
	if p.standalone {
		return architecturev2renderer.CloudStandaloneCoreComposeRendererContract()
	}
	return architecturev2renderer.CloudCoreComposeRendererContract()
}

func (p cloudCoreExecutionProfile) validArtifact(content []byte) bool {
	if p.standalone {
		return architecturev2renderer.ValidateCloudStandaloneCoreComposeArtifact(content)
	}
	return architecturev2renderer.ValidateCloudCoreComposeArtifact(content)
}

func (p cloudCoreExecutionProfile) services() []architecturev2renderer.BasementCoreServiceContract {
	if p.standalone {
		return architecturev2renderer.CloudStandaloneCoreServiceContracts()
	}
	return architecturev2renderer.CloudCoreServiceContracts()
}

func (p cloudCoreExecutionProfile) healthSpecs() []basementCoreHealthSpec {
	if !p.standalone {
		return cloudCoreHealthSpecs
	}
	var specs []basementCoreHealthSpec
	for _, spec := range cloudCoreHealthSpecs {
		if spec.source == "cloud-coolify-http" {
			continue
		}
		spec.targetRef = p.moduleRef()
		specs = append(specs, spec)
	}
	return specs
}

func (p cloudCoreExecutionProfile) healthSpec(source string) (basementCoreHealthSpec, bool) {
	for _, spec := range p.healthSpecs() {
		if spec.source == source {
			return spec, true
		}
	}
	return basementCoreHealthSpec{}, false
}

// LocalCoreRecoveryProfile is the finite identity of a local Core runtime that
// owns a local Kopia source and can therefore carry an executor-state
// recovery checkpoint: Basement Full-Core, CoreLite, or the Cloud standalone
// core. It carries no endpoints, credentials, or caller-controlled paths.
type LocalCoreRecoveryProfile struct {
	ProviderRef string
	ModuleRef   string
	UnitRef     string
	WorkloadRef string
	// OutputRef is the governed Core Compose artifact output.
	OutputRef string
	// RuntimeComposePath is the workspace-relative Compose file Apply writes
	// and runs under the compose target.
	RuntimeComposePath string
}

// LocalCoreRecoveryProfileForModule returns the recovery profile of one known
// local Core module. The Coolify Cloud core has no local Kopia source and is
// rejected, as is every unknown module.
func LocalCoreRecoveryProfileForModule(moduleRef string) (LocalCoreRecoveryProfile, bool) {
	runtimeDir, _, ok := NativeComposeProject(moduleRef)
	if !ok {
		return LocalCoreRecoveryProfile{}, false
	}
	runtimeCompose := path.Join(".stackkit", "runtime", runtimeDir, "compose.yaml")
	if basement, ok := BasementCoreRuntimeProfileForModule(moduleRef); ok {
		return LocalCoreRecoveryProfile{
			ProviderRef: basement.ProviderRef, ModuleRef: basement.ModuleRef,
			UnitRef: basement.UnitRef, WorkloadRef: basement.WorkloadRef,
			OutputRef: basement.OutputRef, RuntimeComposePath: runtimeCompose,
		}, true
	}
	if moduleRef != cloudStandaloneCoreModuleRef {
		return LocalCoreRecoveryProfile{}, false
	}
	cloud := cloudCoreExecutionProfile{standalone: true}
	return LocalCoreRecoveryProfile{
		ProviderRef: cloudCoreProviderRef, ModuleRef: cloud.moduleRef(),
		UnitRef: cloud.unitRef(), WorkloadRef: cloudCoreWorkloadRef,
		OutputRef: cloud.outputRef(), RuntimeComposePath: runtimeCompose,
	}, true
}
