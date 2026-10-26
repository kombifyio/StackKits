package architecturev2renderer

import (
	"path"
	"slices"

	"github.com/kombifyio/stackkits/internal/runtimeprofile"
)

const OfflineModelMount = "/run/stackkit/offline-model"

// The upstream Ollama CLI streams local GGUF bytes to the existing daemon.
// No direct Docker-volume write, daemon-data path or registry pull is admitted.
const privateAIModelImportScript = `set -eu; model="$1"; expected="$2"; i=0; until ollama list >/dev/null 2>&1; do i=$((i+1)); if [ "$i" -ge 120 ]; then echo "Local Ollama is unavailable" >&2; exit 1; fi; sleep 1; done; ollama create "$model" -f /run/stackkit/offline-model/Modelfile; ollama show --modelfile "$model" | grep -qF "$expected" || { echo "Local model weights differ from the admitted bundle" >&2; exit 1; }; ollama list | awk 'NR > 1 {print $1}' | grep -qxF "$model"`

func OfflineModelImportCommand(component, size string) ([]string, error) {
	pin, err := OfflinePrivateAIModel(component, size)
	if err != nil {
		return nil, err
	}
	return []string{privateAIModelImportScript, "ollama-model-import", pin.Tag, "sha256-" + pin.ImportedWeightsSHA256}, nil
}

func OfflineModelSource(ref runtimeprofile.OfflineBundleRef, component string) (string, bool) {
	if ref.Validate() != nil {
		return "", false
	}
	if _, err := OfflinePrivateAIModel(component, "small"); err != nil {
		return "", false
	}
	return path.Join(ref.Path, "models", component+"-small"), true
}

func privateAIBundleProfile(profile runtimeprofile.DataProfile, ref *runtimeprofile.OfflineBundleRef, field string) (*runtimeprofile.OfflineBundleRef, error) {
	parsed, err := runtimeprofile.Parse(string(profile))
	if err != nil {
		return nil, fail(ErrInvalidPlan, field, "invalid data profile")
	}
	if !parsed.FullLocal() {
		if ref != nil {
			return nil, fail(ErrInvalidPlan, field, "Standard cannot carry a Full Local bundle reference")
		}
		return nil, nil
	}
	if ref == nil || ref.Validate() != nil {
		return nil, fail(ErrInvalidPlan, field, "Full Local requires its canonical offline bundle reference")
	}
	return ref, nil
}

func materializePrivateAIOffline(components []selectedPaaSRuntimeComponent, ref runtimeprofile.OfflineBundleRef, field string) ([]selectedPaaSRuntimeComponent, error) {
	modelFound := false
	for i := range components {
		component := &components[i]
		if component.ID == PrivateAIConnectorComponent || component.Accelerator != nil || component.Environment["ENABLE_OPENAI_API"] == "true" {
			return nil, fail(ErrInvalidPlan, field, "Full Local admits only local CPU Ollama inference")
		}
		component.Egress = false
		if !privateAIModelPackComponent(component.ID) {
			continue
		}
		pin, err := OfflinePrivateAIModel(component.ID, "small")
		if err != nil || len(component.Command) == 0 || component.Command[len(component.Command)-1] != pin.Tag {
			return nil, fail(ErrInvalidPlan, field, "selected model has no matching reviewed offline closure")
		}
		source, ok := OfflineModelSource(ref, component.ID)
		if !ok {
			return nil, fail(ErrInvalidPlan, field, "offline model source is invalid")
		}
		component.Command, err = OfflineModelImportCommand(component.ID, "small")
		if err != nil {
			return nil, err
		}
		component.Volumes = []selectedPaaSRuntimeVolume{{ID: "offline-model", Target: OfflineModelMount, HostPath: source, Class: "cache", ReadOnly: true}}
		modelFound = true
	}
	if !modelFound {
		return nil, fail(ErrInvalidPlan, field, "Full Local requires the bundle's pinned local model selection")
	}
	return components, nil
}

func validPrivateAIOfflineModel(component selectedPaaSRuntimeComponent, ref runtimeprofile.OfflineBundleRef) bool {
	pin, err := OfflinePrivateAIModel(component.ID, "small")
	source, ok := OfflineModelSource(ref, component.ID)
	command, commandErr := OfflineModelImportCommand(component.ID, "small")
	if err != nil || commandErr != nil || !ok || !exactStringList(component.Command, command) || len(component.Volumes) != 1 {
		return false
	}
	volume := component.Volumes[0]
	if volume.ID != "offline-model" || volume.Target != OfflineModelMount || volume.HostPath != source || volume.Class != "cache" || !volume.ReadOnly || volume.Backup || volume.SelfPath || volume.SharedFrom != nil {
		return false
	}
	// Reuse the complete existing one-shot capability check after replacing
	// only the explicitly admitted offline command and read-only source right.
	governed, valid := privateAIModelPullCommand()
	if !valid {
		return false
	}
	component.Command = append(slices.Clone(governed), pin.Tag)
	component.Volumes = nil
	return validPrivateAIModelPull(component)
}

func GovernedOfflineModelMount(module, component, id, target, source string, ref *runtimeprofile.OfflineBundleRef) bool {
	if module != privateAIWorkloadModuleID || ref == nil || id != "offline-model" || target != OfflineModelMount {
		return false
	}
	want, ok := OfflineModelSource(*ref, component)
	return ok && source == want
}
