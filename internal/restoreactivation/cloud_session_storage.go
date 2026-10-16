package restoreactivation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// CloudSessionStorage reuses the restore runtime's isolated copy/digest
// primitives. Its authority is an existing signed component transaction,
// never a fabricated current Plan or Apply result.
type CloudSessionStorage struct{ runtime *dockerRuntime }

func NewCloudSessionStorage(workspace string) (*CloudSessionStorage, error) {
	r, err := NewDockerRuntime(workspace)
	if err != nil {
		return nil, err
	}
	return &CloudSessionStorage{runtime: r.(*dockerRuntime)}, nil
}

func (s *CloudSessionStorage) admit(state localevidence.CloudSessionRouting) (*localevidence.CloudSessionComponentMigration, error) {
	actual, err := localevidence.LoadCloudSessionRouting(s.runtime.workspace)
	if err != nil {
		return nil, err
	}
	if localevidence.SessionRoutingValueDigest(actual) != localevidence.SessionRoutingValueDigest(state) || actual.Migration == nil {
		return nil, errors.New("cloud_session_transition_invalid: storage requires the exact signed component transaction")
	}
	m := actual.Migration
	images := map[string]string{}
	for _, component := range architecturev2renderer.CloudStandaloneCoreServiceContracts() {
		images[component.Ref] = component.ImageRef + "@" + component.ImageDigest
	}
	if m.HelperImage != images["kopia-agent"] || m.ToImage != images["tinyauth"] || !strings.HasPrefix(m.ToImage, "ghcr.io/tinyauthapp/tinyauth:v5.1.2@sha256:") {
		return nil, errors.New("cloud_session_tinyauth_pin_unsupported: component storage policy differs from its qualified CUE image pair")
	}
	return m, nil
}

func (s *CloudSessionStorage) DigestLive(ctx context.Context, state localevidence.CloudSessionRouting) (string, error) {
	m, err := s.admit(state)
	if err != nil {
		return "", err
	}
	if err := s.originalVolume(ctx, m); err != nil {
		return "", err
	}
	if err := s.quiescent(ctx, m.Volume); err != nil {
		return "", err
	}
	return s.digest(ctx, m, m.Volume)
}

// Snapshot copies only a quiesced TinyAuth volume to the journal-derived
// private original or target-retention volume and verifies every file's hash.
func (s *CloudSessionStorage) Snapshot(ctx context.Context, state localevidence.CloudSessionRouting, target bool) error {
	m, err := s.admit(state)
	if err != nil {
		return err
	}
	destination, expected, phase := m.SnapshotVolume, m.SnapshotDigest, "snapshot-copying"
	if target {
		destination, expected, phase = m.RetainedTargetVolume, m.RetainedTargetDigest, "target-preserving"
	}
	if m.Phase != phase || expected == "" {
		return errors.New("cloud_session_transition_invalid: snapshot phase is not authorized")
	}
	if err := s.originalVolume(ctx, m); err != nil {
		return err
	}
	if err := s.quiescent(ctx, m.Volume); err != nil {
		return err
	}
	if err := s.ensureSnapshot(ctx, m, destination); err != nil {
		return err
	}
	if err := s.copy(ctx, m, m.Volume, destination); err != nil {
		return err
	}
	for _, volume := range []string{m.Volume, destination} {
		actual, err := s.digest(ctx, m, volume)
		if err != nil || actual != expected {
			return errors.New("cloud_session_transition_drift: cold snapshot readback differs")
		}
	}
	return nil
}

func (s *CloudSessionStorage) RestoreOriginal(ctx context.Context, state localevidence.CloudSessionRouting) error {
	m, err := s.admit(state)
	if err != nil {
		return err
	}
	if m.Phase != "original-restoring" || m.SnapshotDigest == "" {
		return errors.New("cloud_session_transition_invalid: original-data restore phase is not authorized")
	}
	if err := s.originalVolume(ctx, m); err != nil {
		return err
	}
	if err := s.inspectSnapshot(ctx, m, m.SnapshotVolume); err != nil {
		return err
	}
	if err := s.quiescent(ctx, m.Volume); err != nil {
		return err
	}
	actual, err := s.digest(ctx, m, m.SnapshotVolume)
	if err != nil || actual != m.SnapshotDigest {
		return errors.New("cloud_session_transition_drift: retained original data differs")
	}
	if err := s.copy(ctx, m, m.SnapshotVolume, m.Volume); err != nil {
		return err
	}
	actual, err = s.digest(ctx, m, m.Volume)
	if err != nil || actual != m.SnapshotDigest {
		return errors.New("cloud_session_transition_drift: restored original data readback differs")
	}
	return nil
}

func (s *CloudSessionStorage) originalVolume(ctx context.Context, m *localevidence.CloudSessionComponentMigration) error {
	var rows []struct {
		Name, Driver string
		Labels       map[string]string
		Options      map[string]string
	}
	raw, err := s.runtime.docker(ctx, "volume", "inspect", m.Volume)
	if err != nil || json.Unmarshal(raw, &rows) != nil || len(rows) != 1 {
		return errors.New("cloud_session_runtime_unavailable: original TinyAuth volume cannot be inspected")
	}
	v := rows[0]
	if v.Name != m.Volume || v.Driver != "local" || len(v.Options) != 0 || v.Labels["com.docker.compose.project"] != localevidence.CloudSessionProject || v.Labels["com.docker.compose.volume"] != "tinyauth-data" {
		return errors.New("cloud_session_transition_drift: original TinyAuth volume ownership differs")
	}
	return nil
}

func (s *CloudSessionStorage) inspectSnapshot(ctx context.Context, m *localevidence.CloudSessionComponentMigration, name string) error {
	var rows []struct {
		Name, Driver string
		Labels       map[string]string
		Options      map[string]string
	}
	raw, err := s.runtime.docker(ctx, "volume", "inspect", name)
	if err != nil || json.Unmarshal(raw, &rows) != nil || len(rows) != 1 {
		return errors.New("cloud_session_runtime_unavailable: component snapshot cannot be inspected")
	}
	v := rows[0]
	if (name != m.SnapshotVolume && name != m.RetainedTargetVolume) || v.Name != name || v.Driver != "local" || len(v.Options) != 0 || v.Labels["io.stackkit.session.operation"] != m.OperationID || v.Labels["io.stackkit.session.source"] != m.Volume || v.Labels["com.docker.compose.project"] != localevidence.CloudSessionProject {
		return errors.New("cloud_session_transition_drift: component snapshot ownership differs")
	}
	return nil
}

func (s *CloudSessionStorage) ensureSnapshot(ctx context.Context, m *localevidence.CloudSessionComponentMigration, name string) error {
	raw, err := s.runtime.docker(ctx, "volume", "ls", "--format", "{{.Name}}")
	if err != nil {
		return err
	}
	if slices.Contains(strings.Fields(string(raw)), name) {
		return s.inspectSnapshot(ctx, m, name)
	}
	_, err = s.runtime.docker(ctx, "volume", "create", "--driver", "local", "--label", "io.stackkit.session.operation="+m.OperationID, "--label", "io.stackkit.session.source="+m.Volume, "--label", "com.docker.compose.project="+localevidence.CloudSessionProject, name)
	if err != nil {
		return err
	}
	return s.inspectSnapshot(ctx, m, name)
}

func (s *CloudSessionStorage) quiescent(ctx context.Context, volume string) error {
	raw, err := s.runtime.docker(ctx, "ps", "-q", "--filter", "volume="+volume)
	if err != nil {
		return err
	}
	ids := strings.Fields(string(raw))
	if len(ids) == 0 {
		return nil
	}
	raw, err = s.runtime.docker(ctx, append([]string{"inspect"}, ids...)...)
	var rows []struct {
		Mounts []struct {
			Name string
			RW   bool
		}
	}
	if err != nil || json.Unmarshal(raw, &rows) != nil {
		return errors.New("cloud_session_runtime_unavailable: volume writers cannot be inspected")
	}
	for _, row := range rows {
		for _, mount := range row.Mounts {
			if mount.Name == volume && mount.RW {
				return errors.New("cloud_session_runtime_pending: TinyAuth data still has a running writer")
			}
		}
	}
	return nil
}

func (s *CloudSessionStorage) copy(ctx context.Context, m *localevidence.CloudSessionComponentMigration, source, destination string) error {
	name := "stackkit-session-copy-" + m.OperationID
	// Resume a known helper before starting another copy. An unknown same-name
	// container is never killed or adopted.
	listed, err := s.runtime.docker(ctx, "ps", "-aq", "--filter", "name=^/"+name+"$")
	if err != nil {
		return err
	}
	if id := strings.TrimSpace(string(listed)); id != "" {
		var rows []struct {
			Config struct {
				Image  string
				Cmd    []string
				Labels map[string]string
			}
			HostConfig struct {
				NetworkMode string
				Privileged  bool
			}
			Mounts []struct {
				Name, Destination string
				RW                bool
			}
			State struct{ Running bool }
		}
		raw, e := s.runtime.docker(ctx, "inspect", id)
		if e != nil || json.Unmarshal(raw, &rows) != nil || len(rows) != 1 {
			return errors.New("cloud_session_transition_drift: copy helper cannot be authenticated")
		}
		h := rows[0]
		if h.Config.Image != m.HelperImage || h.Config.Labels["io.stackkit.session.operation"] != m.OperationID || h.HostConfig.NetworkMode != "none" || h.HostConfig.Privileged || len(h.Mounts) != 2 || !slices.Equal(h.Config.Cmd, []string{"-ceu", copyScript, "--", "/source/", "/target"}) {
			return errors.New("cloud_session_transition_drift: copy helper identity differs")
		}
		foundSource, foundTarget := false, false
		for _, mount := range h.Mounts {
			foundSource = foundSource || mount.Name == source && mount.Destination == "/source" && !mount.RW
			foundTarget = foundTarget || mount.Name == destination && mount.Destination == "/target" && mount.RW
		}
		if !foundSource || !foundTarget {
			return errors.New("cloud_session_transition_drift: copy helper mounts differ")
		}
		if h.State.Running {
			if _, e := s.runtime.docker(ctx, "wait", id); e != nil {
				return e
			}
		}
		// --rm may have completed during wait; remove only this verified helper.
		_, _ = s.runtime.docker(ctx, "rm", id)
	}
	if err := s.quiescent(ctx, destination); err != nil {
		return err
	}
	_, err = s.runtime.docker(ctx, "run", "--name", name, "--label", "io.stackkit.session.operation="+m.OperationID, "--rm", "--network", "none", "--read-only", "--user", "0:0", "--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "FOWNER", "--cap-add", "DAC_OVERRIDE", "--security-opt", "no-new-privileges", "--mount", "type=volume,src="+source+",dst=/source,readonly", "--mount", "type=volume,src="+destination+",dst=/target", "--entrypoint", "/bin/sh", m.HelperImage, "-ceu", copyScript, "--", "/source/", "/target")
	if err != nil {
		return fmt.Errorf("cloud_session_runtime_pending: component data copy interrupted: %w", err)
	}
	return nil
}

func (s *CloudSessionStorage) digest(ctx context.Context, m *localevidence.CloudSessionComponentMigration, volume string) (string, error) {
	if volume != m.Volume && volume != m.SnapshotVolume && volume != m.RetainedTargetVolume {
		return "", errors.New("cloud_session_transition_invalid: digest volume is outside the transaction")
	}
	raw, err := s.runtime.docker(ctx, "run", "--rm", "--network", "none", "--read-only", "--user", "0:0", "--cap-drop", "ALL", "--cap-add", "DAC_OVERRIDE", "--security-opt", "no-new-privileges", "--mount", "type=volume,src="+volume+",dst=/source,readonly", "--entrypoint", "/bin/sh", m.HelperImage, "-ceu", digestScript, "--", "/source")
	if err != nil {
		return "", err
	}
	readback, err := parseVolumeDigest(raw)
	if err != nil {
		return "", err
	}
	return localevidence.SessionRoutingValueDigest(readback), nil
}
