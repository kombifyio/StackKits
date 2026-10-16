package nativehost

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
	"gopkg.in/yaml.v3"
)

var cloudSessionStopTinyAuthArgs = []string{"stop", "-t", "30", "tinyauth"}

func (r *CloudSessionRuntime) captureComponentMigration(ctx context.Context, source []byte) (*localevidence.CloudSessionComponentMigration, error) {
	if requireCloudSessionTinyAuthPin(source) == nil {
		return nil, nil
	}
	if !r.migrateComponent {
		return nil, requireCloudSessionTinyAuthPin(source)
	}
	var model struct {
		Services map[string]struct {
			Image string `yaml:"image"`
		} `yaml:"services"`
	}
	if yaml.Unmarshal(source, &model) != nil || model.Services["tinyauth"].Image != localevidence.CloudSessionLegacyTinyAuthImage {
		return nil, errors.New("cloud_session_tinyauth_pin_unsupported: only the qualified exact legacy TinyAuth image can migrate")
	}
	images := sessionComponentImages()
	if !strings.HasPrefix(images["tinyauth"], "ghcr.io/tinyauthapp/tinyauth:v5.1.2@sha256:") || images["kopia-agent"] == "" {
		return nil, errors.New("cloud_session_tinyauth_pin_unsupported: target component pair requires new qualification")
	}
	container, err := r.docker.ResolveComposeServiceContainer(ctx, localevidence.CloudSessionProject, "tinyauth")
	if err != nil || len(container.Mounts) != 1 || container.Mounts[0].Name != localevidence.CloudSessionTinyAuthVolume || container.Mounts[0].Destination != "/data" || !container.Mounts[0].RW {
		return nil, errors.New("cloud_session_transition_drift: TinyAuth migration requires its exact original data mount")
	}
	for _, entry := range container.Config.Env {
		// This is the canonical node-custodied SQLite path, not an external DB.
		if entry == "TINYAUTH_DATABASE_PATH=/data/tinyauth.db" {
			continue
		}
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "TINYAUTH_DATABASE_") || strings.HasPrefix(key, "TINYAUTH_DB_") {
			return nil, errors.New("cloud_session_tinyauth_pin_unsupported: a custom TinyAuth database needs its own qualified migration")
		}
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(nonce[:])
	m := &localevidence.CloudSessionComponentMigration{OperationID: id, FromImage: model.Services["tinyauth"].Image, ToImage: images["tinyauth"], HelperImage: images["kopia-agent"], Volume: localevidence.CloudSessionTinyAuthVolume, SnapshotVolume: localevidence.CloudSessionProject + "_session-" + id + "-original", RetainedTargetVolume: localevidence.CloudSessionProject + "_session-" + id + "-target", Phase: "prepared"}
	return m, m.Validate()
}

func sessionComponentImages() map[string]string {
	images := map[string]string{}
	for _, component := range architecturev2renderer.CloudStandaloneCoreServiceContracts() {
		images[component.Ref] = component.ImageRef + "@" + component.ImageDigest
	}
	return images
}

// The source artifact stays immutable. Only the separately verified session
// derivative can change this one expected component; observations retain its
// actual image and both derivative digests rather than claiming the old image.
func sessionComponentServices(source []BasementCoreServiceExpectation, migration *localevidence.CloudSessionComponentMigration) ([]BasementCoreServiceExpectation, error) {
	result := append([]BasementCoreServiceExpectation(nil), source...)
	if migration == nil {
		return result, nil
	}
	if migration.Validate() != nil || migration.Phase != "target-active" || migration.ToImage != sessionComponentImages()["tinyauth"] || !strings.HasPrefix(migration.ToImage, "ghcr.io/tinyauthapp/tinyauth:v5.1.2@sha256:") {
		return nil, errors.New("cloud_session_transition_invalid: component observation lacks its qualified active derivative")
	}
	found := false
	for index, service := range result {
		if service.Ref != "tinyauth" {
			continue
		}
		if found || service.ImageRef+"@"+service.ImageDigest != migration.FromImage {
			return nil, errors.New("cloud_session_transition_drift: component source expectation differs from retained TinyAuth")
		}
		found = true
		result[index].ImageRef, result[index].ImageDigest, _ = strings.Cut(migration.ToImage, "@")
	}
	if !found {
		return nil, errors.New("cloud_session_transition_drift: retained TinyAuth expectation is absent")
	}
	return result, nil
}

func sessionStateOverlay(source []byte, state localevidence.CloudSessionRouting) ([]byte, error) {
	image := ""
	if state.Migration != nil {
		image = state.Migration.ToImage
	}
	return architecturev2renderer.CloudSessionRoutingComponentOverlay(source, state.Before, state.After, image)
}

func sessionImageAdmitted(state localevidence.CloudSessionRouting, current, original localevidence.CloudSessionContainer) bool {
	return current.ImageID == original.ImageID || current.Service == "tinyauth" && state.Migration != nil && state.Migration.TargetImageID != "" && current.ImageID == state.Migration.TargetImageID
}

func (r *CloudSessionRuntime) saveComponent(state localevidence.CloudSessionRouting) (localevidence.CloudSessionRouting, error) {
	if err := localevidence.SaveCloudSessionRouting(r.workspace, state); err != nil {
		return state, err
	}
	return localevidence.LoadCloudSessionRouting(r.workspace)
}

func (r *CloudSessionRuntime) imageID(ctx context.Context, image string) (string, error) {
	command := exec.CommandContext(ctx, "docker", "image", "inspect", image) //nolint:gosec // only CUE-pinned component references reach this function
	raw, err := command.Output()
	if err != nil {
		return "", errors.New("cloud_session_runtime_unavailable: pinned component image cannot be inspected")
	}
	var rows []struct {
		ID          string `json:"Id"`
		RepoDigests []string
	}
	if json.Unmarshal(raw, &rows) != nil || len(rows) != 1 || rows[0].ID == "" {
		return "", errors.New("cloud_session_runtime_unavailable: pinned image readback is malformed")
	}
	_, digest, _ := strings.Cut(image, "@")
	for _, ref := range rows[0].RepoDigests {
		if strings.HasSuffix(ref, "@"+digest) {
			return rows[0].ID, nil
		}
	}
	return "", errors.New("cloud_session_transition_drift: pulled component digest differs")
}

func (r *CloudSessionRuntime) stopTinyAuth(ctx context.Context) error {
	source := filepath.Join(r.workspace, filepath.FromSlash(localevidence.CloudSessionSourcePath))
	environment, err := localevidence.ComposeInterpolationEnvironment(r.workspace)
	if err != nil {
		return err
	}
	args := append([]string{"compose", "--project-name", localevidence.CloudSessionProject, "-f", source}, cloudSessionStopTinyAuthArgs...)
	_, err = (osCloudCoreProcessRunner{}).Run(ctx, args, filepath.Dir(source), environment)
	if err != nil {
		return errors.New("cloud_session_runtime_pending: TinyAuth quiescence interrupted; resume the original component operation")
	}
	return nil
}

func (r *CloudSessionRuntime) prepareComponentMigration(ctx context.Context, state localevidence.CloudSessionRouting) (localevidence.CloudSessionRouting, error) {
	if state.Migration == nil {
		return state, nil
	}
	images := sessionComponentImages()
	m := state.Migration
	if m.ToImage != images["tinyauth"] || m.HelperImage != images["kopia-agent"] {
		return state, errors.New("cloud_session_tinyauth_pin_unsupported: resume with the original qualified component controller")
	}
	if m.Phase == "target-starting" || m.Phase == "target-active" {
		return state, nil
	}
	if m.Phase == "prepared" {
		for _, image := range []string{m.ToImage, m.HelperImage} {
			if err := r.docker.Pull(ctx, image); err != nil {
				return state, errors.New("cloud_session_runtime_unavailable: qualified migration image download failed before quiescence")
			}
		}
		id, err := r.imageID(ctx, m.ToImage)
		if err != nil {
			return state, err
		}
		m.TargetImageID = id
		m.Phase = "quiescing"
		state, err = r.saveComponent(state)
		if err != nil {
			return state, err
		}
		m = state.Migration
	}
	storage, err := restoreactivation.NewCloudSessionStorage(r.workspace)
	if err != nil {
		return state, err
	}
	if m.Phase == "quiescing" || m.Phase == "snapshot-copying" {
		if err := r.stopTinyAuth(ctx); err != nil {
			return state, err
		}
		if m.Phase == "quiescing" {
			digest, err := storage.DigestLive(ctx, state)
			if err != nil {
				return state, err
			}
			m.SnapshotDigest = digest
			m.Phase = "snapshot-copying"
			state, err = r.saveComponent(state)
			if err != nil {
				return state, err
			}
			m = state.Migration
		}
		if err := storage.Snapshot(ctx, state, false); err != nil {
			return state, err
		}
		m.Phase = "snapshot-ready"
		state, err = r.saveComponent(state)
		if err != nil {
			return state, err
		}
		m = state.Migration
	}
	if m.Phase != "snapshot-ready" {
		return state, errors.New("cloud_session_transition_pending: component migration is not in a forward phase")
	}
	// Record uncertainty before the new binary may open the live data. Abort
	// subsequently restores the cold original rather than downgrading that data.
	m.Phase = "target-starting"
	return r.saveComponent(state)
}

func (r *CloudSessionRuntime) restoreComponentMigration(ctx context.Context, state localevidence.CloudSessionRouting) (localevidence.CloudSessionRouting, error) {
	m := state.Migration
	if m == nil {
		return state, nil
	}
	switch m.Phase {
	case "prepared", "quiescing", "snapshot-copying", "snapshot-ready", "original-restored":
		return state, nil
	}
	storage, err := restoreactivation.NewCloudSessionStorage(r.workspace)
	if err != nil {
		return state, err
	}
	if m.Phase == "target-starting" || m.Phase == "target-active" {
		m.Phase = "abort-quiescing"
		state, err = r.saveComponent(state)
		if err != nil {
			return state, err
		}
		m = state.Migration
	}
	if err := r.stopTinyAuth(ctx); err != nil {
		return state, err
	}
	if m.Phase == "abort-quiescing" {
		digest, err := storage.DigestLive(ctx, state)
		if err != nil {
			return state, err
		}
		m.RetainedTargetDigest = digest
		m.Phase = "target-preserving"
		state, err = r.saveComponent(state)
		if err != nil {
			return state, err
		}
		m = state.Migration
	}
	if m.Phase == "target-preserving" {
		if err := storage.Snapshot(ctx, state, true); err != nil {
			return state, err
		}
		m.Phase = "original-restoring"
		state, err = r.saveComponent(state)
		if err != nil {
			return state, err
		}
		m = state.Migration
	}
	if m.Phase != "original-restoring" {
		return state, errors.New("cloud_session_transition_pending: unknown original-data recovery phase")
	}
	if err := storage.RestoreOriginal(ctx, state); err != nil {
		return state, err
	}
	m.Phase = "original-restored"
	return r.saveComponent(state)
}
