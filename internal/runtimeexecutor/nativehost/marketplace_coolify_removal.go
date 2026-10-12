package nativehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/hostsecurity"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/workloadremoval"
)

// This installation journal binds original Docker identities before the first
// effect. A fresh signed removal request may resume their absence; matching
// labels on replacement containers or volumes never grant deletion authority.
type marketplaceRemovalCustody struct {
	Version              string                `json:"version"`
	AppliedRequestDigest string                `json:"appliedRequestDigest"`
	ArtifactDigest       string                `json:"artifactDigest"`
	DataDisposition      string                `json:"dataDisposition"`
	Containers           map[string]string     `json:"containers"`
	Volumes              []dockerVolumeInspect `json:"volumes"`
	Complete             bool                  `json:"complete"`
}

type marketplaceRemovalRecord struct {
	Custody   marketplaceRemovalCustody                     `json:"custody"`
	Signature localevidence.OwnerLifecycleMutationSignature `json:"signature"`
}

// RemoveMarketplaceWorkload implements the canonical Owner-signed workload
// removal for the installed node application. Core identity, application
// workloads, bind mounts, and other projects are outside its authority.
func RemoveMarketplaceWorkload(ctx context.Context, workspace string, request workloadremoval.Request) (result workloadremoval.Result, err error) {
	progressed := false
	defer func() {
		if err != nil && progressed {
			err = &RemovalProgressError{Progressed: true, Err: err}
		}
	}()
	if ctx == nil {
		return result, errors.New("Marketplace removal requires its operation context")
	}
	if err := request.ValidateAt(time.Now().UTC()); err != nil {
		return result, err
	}
	payload, err := request.AuthorizationBytes()
	if err != nil {
		return result, err
	}
	if err := localevidence.VerifyOwnerLifecycleMutation(workspace, payload, localevidence.OwnerLifecycleMutationSignature{
		OwnerRef: request.Authorization.OwnerRef, KeyID: request.Authorization.KeyID, Value: request.Authorization.Value,
	}); err != nil {
		return result, err
	}
	validUntil, err := time.Parse(time.RFC3339Nano, request.ValidUntil)
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithDeadline(ctx, validUntil)
	defer cancel()
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return result, err
	}
	target := request.Applied.RuntimeTargets[0]
	document, err := MarketplaceInstallationDocument(target, request.Applied.Artifacts, LocalTargetBinding{
		SiteRef: owner.Binding.SiteRef, NodeRef: owner.Binding.NodeRef, ExecutionChannelRef: owner.Binding.ChannelRef,
	})
	if err != nil {
		return result, err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return result, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return result, err
	}
	defer tx.Close()
	lock, err := tx.TryAcquireOutputLock(marketplaceDirectory)
	if err != nil {
		return result, err
	}
	defer lock.Release()
	view, err := root.View(marketplaceDirectory)
	if err != nil {
		return result, err
	}
	// Opening custody first makes this a read-only admission. The bootstrap
	// helper must never create a new installation as a side effect of removal.
	file, err := view.Open("custody.json")
	if err != nil {
		return result, errors.New("Marketplace removal requires existing private installation custody")
	}
	if err := file.Close(); err != nil {
		return result, err
	}
	ops := &osMarketplaceOperations{workspace: root.Name()}
	state, err := ops.loadMarketplaceCustody(ctx, view, owner.OwnerRef, document)
	if err != nil {
		return result, err
	}
	journal, exists, err := readMarketplaceRemoval(view, workspace)
	if err != nil {
		return result, err
	}
	present, err := ops.marketplaceRemovalContainerIDs(ctx)
	if err != nil {
		return result, err
	}
	artifactDigest := ""
	for _, artifact := range request.Applied.Artifacts {
		if artifact.ID == target.ArtifactRefs[0] {
			artifactDigest = artifact.Digest
		}
	}
	if !exists || journal.Complete && len(present) != 0 {
		if state.APIToken == "" || state.ServerID == "" {
			return result, errors.New("Marketplace removal requires completed installation custody")
		}
		ids, err := ObserveMarketplaceContainerCustody(ctx, workspace, document)
		if err != nil {
			return result, err
		}
		if err := requireMarketplaceRemovalContainers(present, ids); err != nil || len(present) != len(ids) {
			return result, errors.New("Marketplace removal refuses undeclared project containers")
		}
		volumes, err := ops.marketplaceRemovalVolumes(ctx, document, nil, false)
		if err != nil {
			return result, err
		}
		journal = marketplaceRemovalCustody{Version: "stackkit.marketplace-removal/v1", AppliedRequestDigest: request.AppliedRequestDigest, ArtifactDigest: artifactDigest, DataDisposition: request.DataDisposition, Containers: ids, Volumes: volumes}
		if err := writeMarketplaceRemoval(view, workspace, journal); err != nil {
			return result, err
		}
	} else if journal.AppliedRequestDigest != request.AppliedRequestDigest || journal.ArtifactDigest != artifactDigest || !journal.Complete && journal.DataDisposition != request.DataDisposition {
		return result, errors.New("Marketplace interrupted removal requires its original installation and data disposition")
	}
	if journal.Complete && journal.DataDisposition == workloadremoval.DataDispositionDelete && request.DataDisposition != workloadremoval.DataDispositionDelete {
		return result, errors.New("Marketplace data was explicitly deleted; retaining it requires a new explicit installation")
	}
	if err := requireMarketplaceRemovalContainers(present, journal.Containers); err != nil {
		return result, err
	}
	if _, err := ops.marketplaceRemovalVolumes(ctx, document, journal.Volumes, journal.DataDisposition == workloadremoval.DataDispositionDelete); err != nil {
		return result, err
	}
	journal.Complete = false
	journal.DataDisposition = request.DataDisposition
	if err := writeMarketplaceRemoval(view, workspace, journal); err != nil {
		return result, err
	}
	fresh := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return request.ValidateAt(time.Now().UTC())
	}
	if err := fresh(); err != nil {
		return result, err
	}
	progressed = true
	if err := (hostsecurity.Engine{Host: hostsecurity.LocalHost{}}).RemoveMarketplaceRootSSH(ctx, owner.OwnerRef); err != nil {
		return result, err
	}
	for _, id := range present {
		if err := fresh(); err != nil {
			return result, err
		}
		current, err := ops.marketplaceRemovalContainerIDs(ctx)
		if err != nil {
			return result, err
		}
		if err := requireMarketplaceRemovalContainers(current, journal.Containers); err != nil {
			return result, err
		}
		if !slices.Contains(current, id) {
			continue
		}
		if _, err := ops.run(ctx, []string{"stop", "--time", "20", id}, nil, ops.workspace); err != nil {
			return result, err
		}
		if _, err := ops.run(ctx, []string{"rm", id}, nil, ops.workspace); err != nil {
			return result, err
		}
	}
	if current, err := ops.marketplaceRemovalContainerIDs(ctx); err != nil || len(current) != 0 {
		return result, errors.New("Marketplace removal did not observe an absent installation runtime")
	}
	if request.DataDisposition == workloadremoval.DataDispositionDelete {
		for _, volume := range journal.Volumes {
			if err := fresh(); err != nil {
				return result, err
			}
			current, err := ops.marketplaceRemovalVolumes(ctx, document, journal.Volumes, true)
			if err != nil {
				return result, err
			}
			for _, observed := range current {
				if observed.Name == volume.Name {
					// Docker refuses an attached volume; there is no force or
					// fallback that could remove a foreign holder or its data.
					if _, err := ops.run(ctx, []string{"volume", "rm", volume.Name}, nil, ops.workspace); err != nil {
						return result, err
					}
				}
			}
		}
	}
	volumes, err := ops.marketplaceRemovalVolumes(ctx, document, journal.Volumes, request.DataDisposition == workloadremoval.DataDispositionDelete)
	if err != nil {
		return result, err
	}
	if request.DataDisposition == workloadremoval.DataDispositionDelete {
		if len(volumes) != 0 {
			return result, errors.New("Marketplace deletion did not observe its data volumes absent")
		}
		// A subsequently explicit install must issue new API/server custody
		// into the new database; the retired token cannot be silently reused.
		state.APIToken, state.ServerID = "", ""
		if err := saveMarketplaceCustody(view, state); err != nil {
			return result, err
		}
	}
	journal.Complete = true
	if err := writeMarketplaceRemoval(view, workspace, journal); err != nil {
		return result, err
	}
	observation, err := resolvedplan.CanonicalJSON(struct {
		Custody marketplaceRemovalCustody `json:"custody"`
		Volumes []dockerVolumeInspect     `json:"observedVolumes"`
	}{journal, volumes})
	if err != nil {
		return result, err
	}
	sum := sha256.Sum256(observation)
	if err := fresh(); err != nil {
		return result, err
	}
	return workloadremoval.NewResult(request, time.Now().UTC(), workloadremoval.Outcome{
		RequirementID: target.RequirementID, WorkloadRef: request.WorkloadRef, InstanceRef: target.InstanceRef,
		RuntimeOwnerRef: target.OwnerRef, ArtifactDigest: artifactDigest, DataDisposition: request.DataDisposition,
		Status: workloadremoval.StatusRemoved, ObservedState: workloadremoval.ObservedStateAbsent,
		ObservationRef:    "removal-observation://marketplace/" + target.InstanceRef,
		ObservationDigest: "sha256:" + hex.EncodeToString(sum[:]),
	})
}

func (o *osMarketplaceOperations) marketplaceRemovalContainerIDs(ctx context.Context) ([]string, error) {
	raw, err := o.run(ctx, []string{"ps", "--all", "--no-trunc", "--filter", "label=com.docker.compose.project=" + marketplaceProject, "--format", "{{.ID}}"}, nil, o.workspace)
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(raw)), nil
}

func requireMarketplaceRemovalContainers(present []string, admitted map[string]string) error {
	for _, id := range present {
		owned := false
		for _, wanted := range admitted {
			owned = owned || id == wanted
		}
		if !owned {
			return errors.New("Marketplace removal refuses a foreign or replacement project container")
		}
	}
	return nil
}

func (o *osMarketplaceOperations) marketplaceRemovalVolumes(ctx context.Context, d architecturev2renderer.MarketplaceCoolifyDocument, admitted []dockerVolumeInspect, allowAbsent bool) ([]dockerVolumeInspect, error) {
	raw, err := o.run(ctx, []string{"volume", "ls", "--format", "{{.Name}}"}, nil, o.workspace)
	if err != nil {
		return nil, err
	}
	names := strings.Fields(string(raw))
	result := []dockerVolumeInspect{}
	for _, ref := range []string{"postgres", "redis", "ssh", "applications", "databases", "services", "backups", "images"} {
		name := localbackuppolicy.ApplicationVolumeName("marketplace", d.NodeRef, "", ref)
		if !slices.Contains(names, name) {
			if allowAbsent {
				continue
			}
			return nil, errors.New("Marketplace removal requires every admitted data volume")
		}
		raw, err := o.run(ctx, []string{"volume", "inspect", name}, nil, o.workspace)
		if err != nil {
			return nil, err
		}
		var inspected []dockerVolumeInspect
		if json.Unmarshal(raw, &inspected) != nil || len(inspected) != 1 {
			return nil, errors.New("Marketplace removal volume observation is ambiguous")
		}
		v := inspected[0]
		if v.Name != name || v.Driver != "local" || v.Scope != "local" || len(v.Options) != 0 || v.Labels[composeProjectLabel] != marketplaceProject || v.Labels[composeVolumeLabel] != "marketplace-"+ref || v.Mountpoint != filepath.Join("/var/lib/docker/volumes", name, "_data") || requireParseableVolumeCreatedAt(v.CreatedAt) != nil {
			return nil, errors.New("Marketplace removal volume differs from the owned local installation")
		}
		if admitted != nil && !slices.ContainsFunc(admitted, func(original dockerVolumeInspect) bool { return reflect.DeepEqual(v, original) }) {
			return nil, errors.New("Marketplace removal refuses a replaced or retargeted data volume")
		}
		result = append(result, v)
	}
	return result, nil
}

func readMarketplaceRemoval(view confinedfs.View, workspace string) (marketplaceRemovalCustody, bool, error) {
	var record marketplaceRemovalRecord
	file, err := view.Open("removal.json")
	if errors.Is(err, os.ErrNotExist) {
		return record.Custody, false, nil
	}
	if err != nil {
		return record.Custody, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return record.Custody, false, errors.New("Marketplace removal custody must remain private")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 131073))
	if err != nil || len(raw) > 131072 || json.Unmarshal(raw, &record) != nil || record.Custody.Version != "stackkit.marketplace-removal/v1" {
		return record.Custody, false, errors.New("Marketplace removal custody is invalid")
	}
	payload, err := resolvedplan.CanonicalJSON(record.Custody)
	if err != nil {
		return record.Custody, false, err
	}
	err = localevidence.VerifyOwnerLifecycleMutation(workspace, payload, record.Signature)
	return record.Custody, err == nil, err
}

func writeMarketplaceRemoval(view confinedfs.View, workspace string, custody marketplaceRemovalCustody) error {
	payload, err := resolvedplan.CanonicalJSON(custody)
	if err != nil {
		return err
	}
	signature, err := localevidence.SignOwnerLifecycleMutation(workspace, payload)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(marketplaceRemovalRecord{Custody: custody, Signature: signature})
	if err != nil {
		return fmt.Errorf("encode Marketplace removal custody: %w", err)
	}
	_, err = view.WriteAtomic0600("removal.json", raw)
	return err
}
