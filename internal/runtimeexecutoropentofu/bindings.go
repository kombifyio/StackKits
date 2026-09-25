package runtimeexecutoropentofu

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
)

// ModuleBinding is one catalog module the OpenTofu executor may realize. The
// runtime directory and Compose project are the native executor's, so the
// OpenTofu root and the native fallback manage the same containers.
type ModuleBinding struct {
	ProviderRef    string
	ModuleRef      string
	WorkloadRef    string
	RuntimeDir     string
	ComposeProject string
}

// Validate rejects incomplete or non-portable bindings.
func (b ModuleBinding) Validate() error {
	for _, value := range []string{b.ProviderRef, b.ModuleRef, b.WorkloadRef, b.RuntimeDir, b.ComposeProject} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return errors.New("OpenTofu module binding requires canonical provider, module, workload, runtime directory, and Compose project")
		}
	}
	if _, err := RootRelativePath(b.RuntimeDir); err != nil {
		return err
	}
	return nil
}

// nativeModuleBinding derives a binding from the native executor's runtime
// directory and Compose project for moduleRef.
func nativeModuleBinding(providerRef, moduleRef, workloadRef string) ModuleBinding {
	runtimeDir, project, ok := runtimeexecutorlocal.NativeComposeProject(moduleRef)
	if !ok {
		panic(fmt.Sprintf("runtimeexecutoropentofu: module %q has no native Compose project", moduleRef))
	}
	return ModuleBinding{
		ProviderRef: providerRef, ModuleRef: moduleRef, WorkloadRef: workloadRef,
		RuntimeDir: runtimeDir, ComposeProject: project,
	}
}

// DefaultModuleBindings is the closed list of modules whose OpenTofu unit is
// executable today. Every entry has a native Verify owner in
// runtimeexecutorlocal.NativeComposeRuntime. P1.2 and P1.3 extend this list
// when they add OpenTofu units for further modules (workload bundles, Modern
// sites); a module without a native Verify owner needs one first.
func DefaultModuleBindings() []ModuleBinding {
	return []ModuleBinding{
		nativeModuleBinding("stackkits-basement-core", "stackkits-basement-core-runtime", "basement-core"),
		nativeModuleBinding("stackkits-basement-core", "stackkits-basement-core-lite-runtime", "basement-core"),
		nativeModuleBinding("stackkits-cloud-core", "stackkits-cloud-core-runtime", "cloud-core"),
		nativeModuleBinding("stackkits-cloud-core", "stackkits-cloud-core-standalone-runtime", "cloud-core"),
	}
}
