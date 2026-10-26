package restoreactivation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
)

// ApplicationTransferSecretBindings resolves CUE secret slots from verified
// generated bundles. It does not need an already installed application.
func ApplicationTransferSecretBindings(workspace string, plan generationartifact.VerifiedPlan, manifest generationartifact.ArtifactManifest, selected []string) (map[string]map[string]string, error) {
	if manifest.Binding != plan.Binding() {
		return nil, errors.New("secret-slot generation differs from candidate Plan")
	}
	var document map[string]any
	if err := json.Unmarshal(plan.Canonical(), &document); err != nil {
		return nil, err
	}
	workloads, err := array(document, "workloads")
	if err != nil {
		return nil, err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return nil, err
	}
	defer tx.Close()
	all := map[string]map[string]string{}
	for _, raw := range workloads {
		w, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("invalid selected workload")
		}
		alternative, err := object(w, "alternative")
		if err != nil {
			return nil, err
		}
		runtime, e := object(alternative, "runtime")
		if e != nil || text(runtime, "delivery") != "application-adapter" {
			continue
		}
		adapter, e := object(runtime, "adapter")
		if e != nil || text(adapter, "id") != "standalone-compose" {
			continue
		}
		nodes, e := stringArray(w, "nodeRefs")
		if e != nil || len(nodes) != 1 {
			return nil, errors.New("transfer secret slots require exact node placement")
		}
		sites, e := stringArray(w, "siteRefs")
		if e != nil || len(sites) != 1 {
			return nil, errors.New("transfer secret slots require exact site placement")
		}
		binding := standaloneComposeApplicationBinding{WorkloadRef: text(w, "id"), BundleWorkloadRef: text(alternative, "id"), ModuleRef: text(alternative, "moduleRef"), SiteRef: sites[0], NodeRef: nodes[0]}
		artifact, e := standaloneComposeBundleArtifact(document, plan.OutputRoot(), manifest, binding)
		if e != nil {
			return nil, e
		}
		data, _, e := tx.ReadStable(artifact.Path)
		if e != nil {
			return nil, e
		}
		if runtimeFileDigest(data) != artifact.SHA256 {
			return nil, errors.New("transfer secret bundle differs from verified generation")
		}
		bundle, e := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(data)
		if e != nil {
			return nil, e
		}
		if bundle.WorkloadRef != binding.WorkloadRef || bundle.ModuleRef != binding.ModuleRef || bundle.SiteRef != binding.SiteRef || bundle.NodeRef != binding.NodeRef {
			return nil, errors.New("secret-slot bundle differs from selected workload")
		}
		all[binding.WorkloadRef] = bundle.SecretRefs
	}
	result := map[string]map[string]string{}
	for _, workload := range selected {
		slots, exists := all[workload]
		if !exists {
			return nil, errors.New("selected workload has no native secret-slot bundle")
		}
		for _, ref := range slots {
			for other, otherSlots := range all {
				if other != workload {
					for _, otherRef := range otherSlots {
						if ref == otherRef {
							return nil, errors.New("selected credential is shared with an unrelated workload")
						}
					}
				}
			}
		}
		result[workload] = slots
	}
	return result, nil
}

func ReadApplicationTransferSecrets(workspace string, bindings map[string]map[string]string) (map[string]map[string][]byte, error) {
	materials := map[string]map[string][]byte{}
	for workload, slots := range bindings {
		materials[workload] = map[string][]byte{}
		for slot, ref := range slots {
			material, err := localevidence.ResolveLocalSecretMaterial(workspace, ref)
			if err != nil {
				ClearApplicationTransferSecrets(materials)
				return nil, err
			}
			materials[workload][slot] = material
		}
	}
	return materials, nil
}
func ClearApplicationTransferSecrets(materials map[string]map[string][]byte) {
	for _, slots := range materials {
		for _, material := range slots {
			clear(material)
		}
	}
}

func VerifyApplicationTransferSecretCustody(workspace string, t backuplifecycle.ApplicationTransfer, bindings map[string]map[string]string) error {
	key, err := localevidence.ResolveLocalSecretMaterial(workspace, backuplifecycle.ApplicationTransferCommitmentKeyRef(t.ID))
	if err != nil {
		return err
	}
	defer clear(key)
	if len(bindings) != len(t.SecretDigests) {
		return errors.New("destination secret-slot closure differs from source")
	}
	for workload, slots := range bindings {
		digests, ok := t.SecretDigests[workload]
		if !ok || len(digests) != len(slots) {
			return errors.New("destination secret-slot closure differs from source")
		}
		for slot, ref := range slots {
			material, err := localevidence.ResolveLocalSecretMaterial(workspace, ref)
			if err != nil {
				return err
			}
			commitment := backuplifecycle.ApplicationTransferSecretCommitment(key, workload, slot, material)
			clear(material)
			if commitment != digests[slot] {
				return errors.New("destination application secrets have not been adopted through transfer-prepare before Apply")
			}
		}
	}
	return nil
}

func RequireApplicationTransferTargetAbsent(ctx context.Context, workspace string, policy localbackuppolicy.Policy, workloads []string, closure ...[]localbackuppolicy.ApplicationRuntime) error {
	r, err := NewDockerRuntime(workspace)
	if err != nil {
		return err
	}
	runtime := r.(*dockerRuntime)
	graphs, volumes, err := backuplifecycle.SelectApplicationTransfer(policy, workloads, closure...)
	if err != nil {
		return err
	}
	for _, g := range graphs {
		dir := filepath.Join(workspace, ".stackkit", "runtime", "applications", g.ComposeProject)
		if _, e := os.Lstat(dir); e == nil {
			return errors.New("receiving workload already has runtime custody; initial transfer preparation requires an un-applied application")
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		present, e := platformdeploy.HasOwnerHostDeployment(workspace, g.ComposeProject)
		if e != nil {
			return e
		}
		if present {
			return errors.New("receiving workload already has manager custody")
		}
		containers, e := runtime.docker(ctx, "ps", "-aq", "--filter", "label=com.docker.compose.project="+g.ComposeProject)
		if e != nil {
			return e
		}
		if strings.TrimSpace(string(containers)) != "" {
			return errors.New("receiving workload already owns containers")
		}
	}
	names, err := runtime.docker(ctx, "volume", "ls", "--format", "{{.Name}}")
	if err != nil {
		return err
	}
	existing := strings.Fields(string(names))
	for _, v := range volumes {
		if slices.Contains(existing, v.VolumeName) {
			return errors.New("receiving workload already has a destination data volume")
		}
	}
	return nil
}
