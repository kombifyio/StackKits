package nativehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"gopkg.in/yaml.v3"
)

// RequireAbsentKomodoWorkloadManager reuses the preparation owner's checked,
// bounded runner. No Linux socket or alternate Docker transport is introduced.
func RequireAbsentKomodoWorkloadManager(ctx context.Context, preparation NativeWorkloadComposeOperations, project string) error {
	ops, ok := preparation.(*osStandaloneComposeWorkloadOperations)
	if !ok || ctx == nil {
		return errors.New("Komodo ownership observation requires its native preparation owner")
	}
	args := []string{"ps", "--all", "--filter", "label=com.docker.compose.project=" + project, "--format", "{{.ID}}"}
	if !validWorkloadManagerQuery(args) {
		return errors.New("invalid Komodo workload project observation")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	raw, err := ops.runner.Run(ctx, args, ops.workspaceRoot)
	if err != nil {
		return errors.New("Komodo workload manager observation failed; preserve existing custody")
	}
	if strings.TrimSpace(string(raw)) != "" {
		return errors.New("workload containers exist without a Komodo provider claim; explicit exact-ID recovery is required")
	}
	return nil
}

// ObserveKomodoRecoveryBindings validates the same persisted CUE-derived
// recipe, pinned containers, workspace labels and local data volume contracts
// the removal owner uses, without preparing identity, writing files or data.
func ObserveKomodoRecoveryBindings(ctx context.Context, preparation NativeWorkloadComposeOperations, deployment SelectedPaaSWorkloadDeployment) (string, error) {
	return observeKomodoRecoveryBindings(ctx, preparation, deployment, nil)
}

// ObserveKomodoRecoveryRecipe inspects the still-running applied graph while a
// different, authenticated pending recipe occupies the host files. The caller
// must open these exact source bytes from its signed encrypted update journal.
// Rendering still validates the original bundle and this node's custody; only
// the persisted-file comparison uses that retained recipe instead of pending
// files. No identity, runtime file, container or data mutation is performed.
func ObserveKomodoRecoveryRecipe(ctx context.Context, preparation NativeWorkloadComposeOperations, deployment SelectedPaaSWorkloadDeployment, files map[string][]byte) (string, error) {
	env, envPresent := files[".env"]
	if len(files["compose.yaml"]) == 0 || !envPresent || len(env) == 0 && deployment.ModuleRef != cloudreveModuleRef {
		return "", errors.New("Komodo recovery requires its retained applied recipe")
	}
	return observeKomodoRecoveryBindings(ctx, preparation, deployment, files)
}

func observeKomodoRecoveryBindings(ctx context.Context, preparation NativeWorkloadComposeOperations, deployment SelectedPaaSWorkloadDeployment, files map[string][]byte) (string, error) {
	ops, ok := preparation.(*osStandaloneComposeWorkloadOperations)
	if !ok || ctx == nil {
		return "", errors.New("Komodo recovery requires its native preparation owner")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	project, err := ops.prepareWithIdentityMutation(ctx, deployment, false)
	if err != nil {
		return "", err
	}
	if files == nil {
		if err := ops.verifyPersisted(project); err != nil {
			return "", err
		}
	} else {
		// The Compose graph is public and deterministic from the source bundle.
		// Its environment may contain an older retained credential, which must
		// never be regenerated from the pending update's newer secret custody.
		if string(project.compose) != string(files["compose.yaml"]) {
			return "", errors.New("Komodo retained applied recipe differs from its authenticated source bundle")
		}
		project.environment = files[".env"]
	}
	r := standaloneComposeRemover{ops: ops}
	statuses, err := r.readComposeStatuses(ctx, project)
	if err != nil {
		return "", err
	}
	present, err := r.inspectPresentContainers(ctx, project, statuses)
	if err != nil {
		return "", err
	}
	owned, hostPaths := standaloneComposeVolumeDeclarations(project.bundle)
	if len(hostPaths) != 0 {
		return "", errors.New("Komodo recovery does not adopt bind-mounted application data")
	}
	if len(present) == 0 {
		if err := RequireAbsentKomodoWorkloadManager(ctx, preparation, project.name); err != nil {
			return "", err
		}
		// A failed first Create may own no containers yet. Retained data is
		// never inferred from an unmounted or merely same-name volume.
		for _, volume := range owned {
			filter, err := exactVolumeNameFilter(project.name + "_" + volume.Key)
			if err != nil {
				return "", err
			}
			raw, err := r.dockerCLI().Run(ctx, []string{"volume", "ls", "--filter", filter, "--format", "{{.Name}}"})
			if err != nil || strings.TrimSpace(string(raw)) != "" {
				return "", errors.New("Komodo recovery found unbound data; restore the original container/data contract before adoption")
			}
		}
	} else {
		if len(present) != len(project.bundle.Components) {
			return "", errors.New("Komodo recovery requires the complete original container/data graph")
		}
		var recipe architecturev2renderer.WorkloadComposeDocument
		if yaml.Unmarshal(project.compose, &recipe) != nil {
			return "", errors.New("Komodo recovery recipe cannot be inspected")
		}
		for _, container := range present {
			service, exists := recipe.Services[container.Service]
			if !exists || len(container.Mounts) != len(service.Volumes) {
				return "", errors.New("Komodo recovery refuses extra or missing data/config mounts")
			}
			for _, raw := range service.Volumes {
				mount, ok := raw.(string)
				parts := strings.Split(mount, ":")
				if !ok || len(parts) < 2 || len(parts) > 3 || (len(parts) == 3 && parts[2] != "ro") {
					return "", errors.New("Komodo recovery requires the exact governed short mount contract")
				}
				kind, source := "volume", project.name+"_"+parts[0]
				if strings.HasPrefix(parts[0], ".") || filepath.IsAbs(parts[0]) {
					kind, source = "bind", parts[0]
					if !filepath.IsAbs(source) {
						source = filepath.Join(project.directory, source)
					}
				}
				matched := false
				for _, actual := range container.Mounts {
					actualSource := actual.Source
					if kind == "volume" {
						actualSource = actual.Name
					}
					if actual.Type == kind && actualSource == source && actual.Destination == parts[1] && actual.RW == (len(parts) != 3) {
						matched = true
						break
					}
				}
				if !matched {
					return "", errors.New("Komodo recovery data/config mount differs from the original recipe")
				}
			}
		}
		mounted, err := r.volumesFromContainerMounts(ctx, project, owned, present)
		if err != nil {
			return "", err
		}
		if err := mountedMatchesDeclared(mounted, owned); err != nil {
			return "", err
		}
	}
	raw, err := json.Marshal(struct {
		Present any
		Compose string
	}{present, string(project.compose)})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
