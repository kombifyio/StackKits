package nativehost

import (
	"context"
	"errors"
)

// StageKomodoProviderRecipe runs the retained native preparation, including
// identity admission, without replacing the host recipe. The provider owner
// journals its signed source/target and encrypted files before persistence.
func StageKomodoProviderRecipe(ctx context.Context, preparation NativeWorkloadComposeOperations, deployment SelectedPaaSWorkloadDeployment) (NativeWorkloadCompose, map[string][]byte, error) {
	ops, ok := preparation.(*osStandaloneComposeWorkloadOperations)
	if !ok || ops.adapterRef != "komodo" {
		return NativeWorkloadCompose{}, nil, errors.New("Komodo recipe staging requires its native preparation owner")
	}
	project, err := ops.prepare(ctx, deployment)
	if err != nil {
		return NativeWorkloadCompose{}, nil, err
	}
	return preparedWorkloadCompose(project), standaloneComposeProjectFiles(project), nil
}

// PersistKomodoProviderRecipe installs exactly the staged native project. It
// grants no Compose up/down operation; the common project lock is held by Apply.
func PersistKomodoProviderRecipe(preparation NativeWorkloadComposeOperations, prepared NativeWorkloadCompose) error {
	ops, ok := preparation.(*osStandaloneComposeWorkloadOperations)
	if !ok || ops.adapterRef != "komodo" || prepared.project.name == "" || prepared.project.name != prepared.ProjectName {
		return errors.New("Komodo recipe persistence requires its staged native project")
	}
	return ops.persist(prepared.project)
}

// RestoreKomodoProviderRecipe resumes a partially persisted authenticated
// pending recipe, after exact-ID and data admission. This reuses native file
// permissions and performs no identity registration or workload execution.
func RestoreKomodoProviderRecipe(ctx context.Context, preparation NativeWorkloadComposeOperations, deployment SelectedPaaSWorkloadDeployment, files map[string][]byte) error {
	ops, ok := preparation.(*osStandaloneComposeWorkloadOperations)
	if !ok || ops.adapterRef != "komodo" {
		return errors.New("Komodo recipe recovery requires its native preparation owner")
	}
	project, err := ops.prepareWithIdentityMutation(ctx, deployment, false)
	if err != nil {
		return err
	}
	env, envPresent := files[".env"]
	if string(project.compose) != string(files["compose.yaml"]) || !envPresent || len(env) == 0 && deployment.ModuleRef != cloudreveModuleRef || len(files) != len(standaloneComposeProjectFiles(project)) {
		return errors.New("Komodo pending recipe differs from its authenticated native contract")
	}
	for name := range standaloneComposeProjectFiles(project) {
		if _, exists := files[name]; !exists {
			return errors.New("Komodo pending recipe lost a native config file")
		}
	}
	return ops.persistFiles(project, files)
}
