package nativehost

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

var cloudSessionRestoreArgs = []string{"up", "-d", "--wait", "--wait-timeout", "60", "--no-build", "--pull", "never", "--no-deps", "tinyauth", "hub"}

// Restore admits only the signed source or overlay configuration hashes,
// original images and original mounts. Failed consumers may be inspected;
// unrelated or replaced services remain a hard denial.
func (r *CloudSessionRuntime) Restore(ctx context.Context, state localevidence.CloudSessionRouting) error {
	var admissionErr error
	state, admissionErr = r.admit(state)
	if admissionErr != nil {
		return admissionErr
	}
	if state.Phase != "aborting" && state.Phase != "aborted" {
		return errors.New("cloud_session_transition_invalid: source restore requires its signed abort phase")
	}
	sourceHashes, err := r.sessionConfigHashes(ctx, false)
	if err != nil {
		return err
	}
	overlayHashes, err := r.sessionConfigHashes(ctx, true)
	if err != nil {
		return err
	}
	observed, err := r.containers(ctx, state.Before, state.After)
	if err != nil {
		return err
	}
	if len(observed) != len(state.Containers) {
		return errors.New("cloud_session_transition_drift: abort refuses a missing or additional service")
	}
	alreadyOriginal := true
	for index, current := range observed {
		original := state.Containers[index]
		if current.Service != original.Service || !sessionImageAdmitted(state, current, original) || current.MountsDigest != original.MountsDigest {
			return errors.New("cloud_session_transition_drift: abort refuses changed images, volumes or service identity")
		}
		if current.Service == "tinyauth" || current.Service == "hub" {
			if current.ConfigHash != sourceHashes[current.Service] && current.ConfigHash != overlayHashes[current.Service] {
				return errors.New("cloud_session_transition_drift: abort refuses an unrecognized consumer configuration")
			}
			alreadyOriginal = alreadyOriginal && current.ConfigHash == sourceHashes[current.Service]
			actual, err := r.docker.InspectContainer(ctx, current.ID)
			alreadyOriginal = alreadyOriginal && err == nil && actual != nil && actual.State.Running
		} else if current.ID != original.ID || current.ConfigHash != original.ConfigHash || current.RuntimeDigest != original.RuntimeDigest {
			return errors.New("cloud_session_transition_drift: abort refuses a changed non-session service")
		}
	}
	state, admissionErr = r.restoreComponentMigration(ctx, state)
	if admissionErr != nil {
		return admissionErr
	}
	if state.Migration != nil {
		alreadyOriginal = false
	}
	if !alreadyOriginal {
		compose := filepath.Join(r.workspace, filepath.FromSlash(localevidence.CloudSessionSourcePath))
		environment, err := localevidence.ComposeInterpolationEnvironment(r.workspace)
		if err != nil {
			return err
		}
		args := append([]string{"compose", "--project-name", "stackkit-cloud-core-standalone", "-f", compose}, cloudSessionRestoreArgs...)
		if _, err := (osCloudCoreProcessRunner{}).Run(ctx, args, filepath.Dir(compose), environment); err != nil {
			return errors.New("cloud_session_runtime_pending: original consumer restoration interrupted; repeat the same abort")
		}
	}
	for _, service := range []string{"tinyauth", "hub"} {
		current, err := r.docker.ResolveComposeServiceContainer(ctx, "stackkit-cloud-core-standalone", service)
		if err != nil || current.Config.Labels["com.docker.compose.config-hash"] != sourceHashes[service] {
			return errors.New("cloud_session_runtime_pending: original consumers have not reached verified health")
		}
	}
	return nil
}

func (r *CloudSessionRuntime) sessionConfigHashes(ctx context.Context, overlay bool) (map[string]string, error) {
	compose := filepath.Join(r.workspace, filepath.FromSlash(localevidence.CloudSessionSourcePath))
	environment, err := localevidence.ComposeInterpolationEnvironment(r.workspace)
	if err != nil {
		return nil, err
	}
	args := []string{"compose", "--project-name", "stackkit-cloud-core-standalone", "-f", compose}
	if overlay {
		args = append(args, "-f", filepath.Join(r.workspace, filepath.FromSlash(localevidence.CloudSessionOverlayPath)))
	}
	args = append(args, "config", "--format", "json")
	resolved, err := (osCloudCoreProcessRunner{}).Run(ctx, args, filepath.Dir(compose), environment)
	if err != nil {
		return nil, errors.New("cloud_session_runtime_unavailable: retained abort configuration cannot be resolved")
	}
	defer clear(resolved)
	raw, err := hashResolvedCloudSessionConfig(ctx, filepath.Dir(compose), environment, resolved)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	hashes := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, errors.New("cloud_session_transition_invalid: retained abort configuration hash malformed")
		}
		hashes[fields[0]] = fields[1]
	}
	return hashes, nil
}
