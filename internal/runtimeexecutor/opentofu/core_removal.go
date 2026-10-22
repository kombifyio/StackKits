package opentofu

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/pkg/workloadremoval"
)

// RemoveBasementCoreWorkload removes an applied Basement core whose Compose
// project the OpenTofu or Terramate root owns. It proves the exact applied
// root artifact, destroys that root offline under its Owner state custody (the
// root's destroy provisioner runs `docker compose down` and never removes
// volumes), and leaves the observation, data disposition and absence proof to
// the shared native Basement core removal owner.
func RemoveBasementCoreWorkload(ctx context.Context, workspace string, request workloadremoval.Request) (workloadremoval.Result, error) {
	if len(request.Applied.RuntimeTargets) != 1 {
		return workloadremoval.Result{}, errors.New("Basement core removal requires exactly one applied runtime target")
	}
	target := request.Applied.RuntimeTargets[0]
	if unit, ok := nativehost.BasementCoreRemovalUnit(target); !ok || (unit != UnitRef && unit != TerramateUnitRef) {
		return workloadremoval.Result{}, errors.New("applied runtime is not a Basement core OpenTofu root")
	}
	var module *ModuleBinding
	for _, candidate := range DefaultModuleBindings() {
		if candidate.ModuleRef == target.ModuleRef && candidate.ProviderRef == target.ProviderRef && candidate.WorkloadRef == target.WorkloadRef {
			module = &candidate
			break
		}
	}
	if module == nil {
		return workloadremoval.Result{}, errors.New("Basement core module has no OpenTofu binding")
	}
	if want, ok := basementCoreRootContractHash(target.ModuleRef, target.UnitRef); !ok || target.UnitContractHash != want {
		return workloadremoval.Result{}, errors.New("applied OpenTofu unit is not the exact CUE-owned Basement core root")
	}
	artifact, _, err := exactRootArtifacts(request.Applied.Artifacts, target)
	if err != nil {
		return workloadremoval.Result{}, err
	}
	definition, err := rootComposePayload(artifact.Content)
	if err != nil {
		return workloadremoval.Result{}, err
	}
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return workloadremoval.Result{}, fmt.Errorf("resolve workspace: %w", err)
	}
	runtime := Runtime{WorkspaceRoot: absolute}
	relative, err := RootRelativePath(module.RuntimeDir)
	if err != nil {
		return workloadremoval.Result{}, err
	}
	teardown := func(ctx context.Context, environment []string) error {
		root := filepath.Join(absolute, filepath.FromSlash(relative))
		if _, err := os.Lstat(filepath.Join(root, StateFile)); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		binary, providers, err := runtime.packagedTools()
		if err != nil {
			return err
		}
		if err := runtime.requireStateCustody(ctx, relative, binary, providers.Directory()); err != nil {
			return err
		}
		environment = append(append([]string(nil), environment...), "COMPOSE_PROJECT_NAME="+module.ComposeProject)
		_, err = runtime.runRootDestroy(ctx, relative, root, binary, environment)
		return err
	}
	return nativehost.RemoveBasementCoreProject(ctx, workspace, request, nativehost.BasementCoreRemovalAuthority{
		ArtifactDigest: artifact.Digest, Definition: definition, Teardown: teardown,
	})
}

// basementCoreRootContractHash is the renderer contract of the Basement core
// OpenTofu or Terramate root the verified plan may select.
func basementCoreRootContractHash(moduleRef, unitRef string) (string, bool) {
	switch moduleRef + "/" + unitRef {
	case "stackkits-basement-core-runtime/" + UnitRef:
		return architecturev2renderer.BasementCoreOpenTofuRendererContract().ContractHash, true
	case "stackkits-basement-core-runtime/" + TerramateUnitRef:
		return architecturev2renderer.BasementCoreTerramateRendererContract().ContractHash, true
	case "stackkits-basement-core-lite-runtime/" + UnitRef:
		return architecturev2renderer.BasementCoreLiteOpenTofuRendererContract().ContractHash, true
	case "stackkits-basement-core-lite-runtime/" + TerramateUnitRef:
		return architecturev2renderer.BasementCoreLiteTerramateRendererContract().ContractHash, true
	}
	return "", false
}
