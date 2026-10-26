package restoreactivation

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// TransferAdmission is explicit destination-local trust. Source public-key
// material inside the encrypted archive cannot supply its own trust anchor.
type TransferAdmission struct {
	RuntimeStartOrder []string
	Staged            backuplifecycle.EmergencyRestoreResult
	SourceOwner       string
	SourceFingerprint string
	TransferID        string
	DestinationPolicy localbackuppolicy.Policy
}

func TransferOperationID(id string) (string, error) {
	if !digestPattern.MatchString(id) {
		return "", errors.New("invalid transfer ID")
	}
	return "transfer-import." + strings.TrimPrefix(id, "sha256:"), nil
}

func DeriveTransferAuthority(workspace string, plan generationartifact.VerifiedPlan, manifest generationartifact.ArtifactManifest, lineage backuplifecycle.AuthorityLineage, operation string, admission TransferAdmission) (Authority, error) {
	t, err := backuplifecycle.VerifyApplicationTransfer(admission.Staged.Manifest, admission.SourceOwner, admission.SourceFingerprint, admission.TransferID)
	if err != nil {
		return Authority{}, err
	}
	expectedOperation, err := TransferOperationID(t.ID)
	if err != nil || operation != expectedOperation || !admission.Staged.ContentVerified {
		return Authority{}, errors.New("transfer requires verified staging and its single-use deterministic operation identity")
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return Authority{}, err
	}
	graph, err := DeriveRuntimeRecoveryGraph(workspace, plan, manifest, operation)
	if err != nil {
		return Authority{}, err
	}
	d := t.Destination
	if !d.ExpiresAt.After(time.Now()) {
		original, e := lifecyclemutation.ReadRestoreActivationRecord(workspace, operation)
		if e != nil || original.RestoreActivation.Authority.RestoreResultID != t.ID || original.RestoreActivation.Authority.PlanHash != plan.Binding().PlanHash {
			return Authority{}, errors.New("receiving transfer admission expired before a matching original activation was journaled")
		}
	}
	if d.OwnerRef != owner.OwnerRef || d.StackID != graph.StackID || d.SiteRef != owner.Binding.SiteRef || d.NodeRef != owner.Binding.NodeRef || d.PlanHash != plan.Binding().PlanHash || lineage.Binding != plan.Binding() || lineage.ManifestHash != graph.ManifestHash {
		return Authority{}, errors.New("transfer destination differs from current Owner/stack/node/Plan/Apply authority")
	}
	// Decode the actual generated policy instead of trusting a caller's projection.
	root, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(graph.CorePolicyPath)))
	if err != nil {
		return Authority{}, err
	}
	policy, err := localbackuppolicy.Decode(root)
	if err != nil {
		return Authority{}, err
	}
	actual, err := localbackuppolicy.ArtifactBytes(policy)
	if err != nil {
		return Authority{}, err
	}
	provided, err := localbackuppolicy.ArtifactBytes(admission.DestinationPolicy)
	if err != nil || string(actual) != string(provided) || runtimeFileDigest(root) != graph.CorePolicyDigest {
		return Authority{}, errors.New("transfer destination policy differs from verified generation")
	}
	selectedWorkloads := make([]string, len(t.Runtimes))
	for i, r := range t.Runtimes {
		selectedWorkloads[i] = r.WorkloadRef
	}
	if err = RequireApplicationTransferConfiguration(t, plan, selectedWorkloads); err != nil {
		return Authority{}, err
	}
	closure, err := ApplicationTransferProjection(workspace, plan, manifest, selectedWorkloads, policy)
	if err != nil {
		return Authority{}, err
	}
	mapped, err := backuplifecycle.MatchApplicationTransfer(t, policy, closure)
	if err != nil {
		return Authority{}, err
	}
	workloads := make([]string, len(t.Runtimes))
	for i, r := range t.Runtimes {
		workloads[i] = r.WorkloadRef
	}
	bindings, err := ApplicationTransferSecretBindings(workspace, plan, manifest, workloads)
	if err != nil {
		return Authority{}, err
	}
	if err = VerifyApplicationTransferSecretCustody(workspace, t, bindings); err != nil {
		return Authority{}, err
	}
	custody, err := ApplicationTransferRuntimeCustody(workspace, plan, manifest, selectedWorkloads)
	if err != nil {
		return Authority{}, err
	}
	selectedRuntimes := make([]ComposeRuntime, len(custody))
	for i, c := range custody {
		selectedRuntimes[i] = c.Runtime
	}
	if len(selectedRuntimes) > 1 {
		if len(admission.RuntimeStartOrder) != len(selectedRuntimes) {
			return Authority{}, errors.New("transfer requires canonical runtime dependency order")
		}
		ordered := make([]ComposeRuntime, 0, len(selectedRuntimes))
		seen := map[string]bool{}
		for _, ref := range admission.RuntimeStartOrder {
			if seen[ref] {
				return Authority{}, errors.New("duplicate transfer runtime order")
			}
			seen[ref] = true
			index := slices.IndexFunc(custody, func(c StandaloneComposeRuntimeCustody) bool {
				return c.Project == localbackuppolicy.StandaloneComposeProjectName(ref, d.NodeRef)
			})
			if index < 0 {
				return Authority{}, errors.New("foreign transfer runtime order")
			}
			ordered = append(ordered, custody[index].Runtime)
		}
		selectedRuntimes = ordered
	}
	staging := backuplifecycle.RestoreStagingPath(operation)
	var details []Volume
	for _, v := range mapped {
		found := false
		for _, g := range graph.VolumeDetails {
			if g.LiveName == v.VolumeName {
				g.StagingPath = path.Join(staging, g.LiveName, "_data")
				details = append(details, g)
				found = true
			}
		}
		if !found {
			return Authority{}, errors.New("transfer volume is outside destination runtime authority")
		}
	}
	sortVolumesByLiveName(details)
	names := make([]string, len(details))
	for i, v := range details {
		names[i] = v.LiveName
	}
	if len(slices.Compact(slices.Clone(names))) != len(names) {
		return Authority{}, errors.New("transfer maps multiple sources to one destination volume")
	}
	hash, err := resolvedplan.CanonicalSHA256(names)
	if err != nil {
		return Authority{}, err
	}
	return Authority{OperationID: operation, RenderTarget: graph.RenderTarget, OwnerRef: owner.OwnerRef, RestoreResultID: t.ID, PlanHash: graph.PlanHash, ManifestHash: graph.ManifestHash, ApplyResultHash: lineage.ApplyResultHash, ManagedVolumeSetHash: hash, StackID: graph.StackID, ComposeProject: graph.ComposeProject, ComposePath: graph.ComposePath, ComposeDigest: graph.ComposeDigest, ComposeRuntimes: selectedRuntimes, KopiaHelperImage: graph.KopiaHelperImage, StagingVolume: graph.StagingVolume, StagingPath: staging, Volumes: names, VolumeDetails: details}, nil
}

// StageApplicationTransfer copies only authenticated extracted source trees
// into the existing isolated staging volume, retaining destination names. It
// mounts no live application volume and never executes archive-supplied code.
func StageApplicationTransfer(ctx context.Context, workspace string, authority Authority, admission TransferAdmission) error {
	r, err := NewDockerRuntime(workspace)
	if err != nil {
		return err
	}
	runtime := r.(*dockerRuntime)
	if err = runtime.Inspect(ctx, authority); err != nil {
		return err
	}
	t, err := backuplifecycle.VerifyApplicationTransfer(admission.Staged.Manifest, admission.SourceOwner, admission.SourceFingerprint, admission.TransferID)
	if err != nil {
		return err
	}
	receiving := make([]localbackuppolicy.ApplicationRuntime, len(t.Runtimes))
	copy(receiving, t.Runtimes)
	for i := range receiving {
		receiving[i].SiteRef = t.Destination.SiteRef
		receiving[i].NodeRef = t.Destination.NodeRef
		receiving[i].ComposeProject = localbackuppolicy.StandaloneComposeProjectName(receiving[i].WorkloadRef, receiving[i].NodeRef)
	}
	mapped, err := backuplifecycle.MatchApplicationTransfer(t, admission.DestinationPolicy, receiving)
	if err != nil {
		return err
	}
	if err = backuplifecycle.RestoreApplicationTransferMetadata(admission.Staged.Target, admission.Staged.Manifest, admission.Staged.PrivateFiles); err != nil {
		return err
	}
	if _, e := lifecyclemutation.ReadRestoreActivationRecord(workspace, authority.OperationID); e == nil {
		return errors.New("activation journal already exists; use transfer-recover instead of re-staging")
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if err = runtime.settleApplicationTransferStageHelper(ctx, authority); err != nil {
		return err
	}
	leaf := path.Base(authority.StagingPath)
	if len(leaf) != 64 {
		return errors.New("invalid isolated transfer staging leaf")
	}
	if _, e := hex.DecodeString(leaf); e != nil {
		return e
	}
	clearArgs := []string{"run", "--rm", "--name", helperName(authority, "transfer-stage"), "--label", "io.stackkit.restore.operation=" + authority.OperationID, "--label", "io.stackkit.restore.plan=" + authority.PlanHash, "--network", "none", "--read-only", "--user", "0:0", "--cap-drop", "ALL", "--cap-add", "DAC_OVERRIDE", "--cap-add", "FOWNER", "--security-opt", "no-new-privileges", "--mount", "type=volume,src=" + authority.StagingVolume + ",dst=/staging", "--entrypoint", "/bin/sh", authority.KopiaHelperImage, "-ceu", `case "$1" in *[!0-9a-f]*|"") exit 1;; esac; test ${#1} -eq 64; rm -rf -- "/staging/$1"`, "--", leaf}
	if _, err = runtime.docker(ctx, clearArgs...); err != nil {
		return err
	}
	for i, v := range mapped {
		source := filepath.Join(admission.Staged.Target, "sources", fmt.Sprintf("%04d", i))
		// RestoreEmergency creates link-free trees. Recheck paths at this
		// boundary; a replaced local staging path is not trusted archive data.
		err = filepath.WalkDir(source, func(p string, e os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if e.Type()&os.ModeSymlink != 0 || (!e.IsDir() && !e.Type().IsRegular()) {
				return errors.New("transfer staging contains a link or special file")
			}
			return nil
		})
		if err != nil {
			return err
		}
		relative := path.Join(path.Base(authority.StagingPath), v.VolumeName, "_data")
		args := []string{"run", "--rm", "--name", helperName(authority, "transfer-stage"), "--label", "io.stackkit.restore.operation=" + authority.OperationID, "--label", "io.stackkit.restore.plan=" + authority.PlanHash, "--network", "none", "--read-only", "--user", "0:0", "--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE", "--cap-add", "FOWNER", "--security-opt", "no-new-privileges", "--mount", "type=bind,src=" + source + ",dst=/source,readonly", "--mount", "type=volume,src=" + authority.StagingVolume + ",dst=/staging", "--entrypoint", "/bin/sh", authority.KopiaHelperImage, "-ceu", `dst=/staging/$1; test ! -e "$dst"; mkdir -p "$dst"; cp -a /source/. "$dst"/; test "$(cd /source; find . -exec stat -c '%F|%a|%u|%g|%n' {} \; | LC_ALL=C sort)" = "$(cd "$dst"; find . -exec stat -c '%F|%a|%u|%g|%n' {} \; | LC_ALL=C sort)"`, "--", relative}
		if _, err = runtime.docker(ctx, args...); err != nil {
			return err
		}
	}
	return runtime.ValidateStaging(ctx, authority)
}

// A retry may settle only its exact labelled pre-journal staging helper. It
// cannot remove a live/rollback helper or a container mounting application data.
func (runtime *dockerRuntime) settleApplicationTransferStageHelper(ctx context.Context, a Authority) error {
	name := helperName(a, "transfer-stage")
	raw, err := runtime.docker(ctx, "ps", "-aq", "--filter", "name=^/"+name+"$")
	if err != nil {
		return err
	}
	ids := strings.Fields(string(raw))
	if len(ids) > 1 {
		return errors.New("ambiguous transfer staging helper")
	}
	if len(ids) == 0 {
		return nil
	}
	raw, err = runtime.docker(ctx, "inspect", ids[0])
	if err != nil {
		return err
	}
	var infos []struct {
		ID     string `json:"Id"`
		Name   string
		Config struct {
			Image  string
			Labels map[string]string
		}
		Mounts []struct {
			Type        string
			Name        string
			Source      string
			Destination string
			RW          bool
		}
	}
	if err = json.Unmarshal(raw, &infos); err != nil || len(infos) != 1 {
		return errors.New("invalid transfer staging helper observation")
	}
	c := infos[0]
	if c.ID != ids[0] || strings.TrimPrefix(c.Name, "/") != name || c.Config.Image != a.KopiaHelperImage || c.Config.Labels["io.stackkit.restore.operation"] != a.OperationID || c.Config.Labels["io.stackkit.restore.plan"] != a.PlanHash {
		return errors.New("foreign transfer staging helper")
	}
	staging := false
	for _, m := range c.Mounts {
		if m.Type == "volume" && m.Name == a.StagingVolume && m.Destination == "/staging" && m.RW {
			staging = true
			continue
		}
		if m.Type == "bind" && m.Destination == "/source" && !m.RW {
			relative, e := filepath.Rel(runtime.workspace, m.Source)
			if e == nil && strings.HasPrefix(filepath.ToSlash(relative), ".stackkit/backups/"+a.OperationID+"-private-") && !strings.Contains(filepath.ToSlash(relative), "../") {
				continue
			}
		}
		return errors.New("foreign transfer helper mount")
	}
	if !staging {
		return errors.New("transfer helper lacks isolated staging mount")
	}
	_, err = runtime.docker(ctx, "rm", "--force", c.ID)
	return err
}
