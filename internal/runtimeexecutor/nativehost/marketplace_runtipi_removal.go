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
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"github.com/kombifyio/stackkits/internal/workloadremoval"
)

const (
	runtipiMainNetwork             = "runtipi_tipi_main_network"
	runtipiMarketplaceRemovalFile  = "marketplace-runtipi-removal.json"
	runtipiMarketplaceRemovalScope = ".stackkit/runtime"
)

// runtipiMarketplaceRemoval binds the installation and its manager containers
// before the first effect. It lives beside, not inside, the runtime directory
// so a data deletion keeps the record of what it removed.
type runtipiMarketplaceRemoval struct {
	Version              string   `json:"version"`
	AppliedRequestDigest string   `json:"appliedRequestDigest"`
	ArtifactDigest       string   `json:"artifactDigest"`
	InstallationID       string   `json:"installationId"`
	DataDisposition      string   `json:"dataDisposition"`
	Containers           []string `json:"containers"`
	Complete             bool     `json:"complete"`
}

type runtipiMarketplaceRemovalRecord struct {
	Removal   runtipiMarketplaceRemoval                     `json:"removal"`
	Signature localevidence.OwnerLifecycleMutationSignature `json:"signature"`
}

// removeRuntipiMarketplace removes the node's Runtipi installation. It refuses
// while Runtipi still runs any app, including StackKits workloads: those are
// removed first through their own workload removal. Retain keeps the runtime
// directory and installation custody, so a later Apply resumes the same
// installation; delete removes both.
func removeRuntipiMarketplace(ctx context.Context, workspace string, request workloadremoval.Request) (result workloadremoval.Result, err error) {
	progressed := false
	defer func() {
		if err != nil && progressed {
			err = &RemovalProgressError{Progressed: true, Err: err}
		}
	}()
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return result, err
	}
	target := request.Applied.RuntimeTargets[0]
	document, err := RuntipiMarketplaceInstallationDocument(target, request.Applied.Artifacts, LocalTargetBinding{
		SiteRef: owner.Binding.SiteRef, NodeRef: owner.Binding.NodeRef, ExecutionChannelRef: owner.Binding.ChannelRef,
	})
	if err != nil {
		return result, err
	}
	artifactDigest := ""
	for _, artifact := range request.Applied.Artifacts {
		if artifact.ID == target.ArtifactRefs[0] {
			artifactDigest = artifact.Digest
		}
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
	lock, err := tx.TryAcquireOutputLock(runtipiRuntimeDirectory)
	if err != nil {
		return result, err
	}
	defer lock.Release()
	view, err := root.View(runtipiMarketplaceRemovalScope)
	if err != nil {
		return result, err
	}
	ops := &osMarketplaceOperations{workspace: root.Name()}
	journal, exists, err := readRuntipiMarketplaceRemoval(view, workspace)
	if err != nil {
		return result, err
	}
	present, err := runtipiMarketplaceContainers(ctx, ops)
	if err != nil {
		return result, err
	}
	if !exists || journal.Complete && len(present) != 0 {
		raw, err := localevidence.ResolveEncryptedLocalIssuedSecret(workspace, runtipiInstallationSecretRef)
		if err != nil {
			return result, errors.New("Marketplace removal requires existing Runtipi installation custody")
		}
		var custody runtipiInstallationCustody
		decodeErr := json.Unmarshal(raw, &custody)
		clear(raw)
		if decodeErr != nil || custody.OwnerRef != owner.OwnerRef || custody.SiteRef != document.SiteRef || custody.NodeRef != document.NodeRef || custody.InstallationID == "" {
			return result, errors.New("Runtipi installation custody differs from the exact Owner node")
		}
		journal = runtipiMarketplaceRemoval{Version: "stackkit.marketplace-runtipi-removal/v1", AppliedRequestDigest: request.AppliedRequestDigest, ArtifactDigest: artifactDigest, InstallationID: custody.InstallationID, DataDisposition: request.DataDisposition, Containers: present}
	} else if journal.AppliedRequestDigest != request.AppliedRequestDigest || journal.ArtifactDigest != artifactDigest || !journal.Complete && journal.DataDisposition != request.DataDisposition {
		return result, errors.New("Runtipi interrupted removal requires its original installation and data disposition")
	}
	if journal.Complete && journal.DataDisposition == workloadremoval.DataDispositionDelete && request.DataDisposition != workloadremoval.DataDispositionDelete {
		return result, errors.New("Runtipi data was explicitly deleted; retaining it requires a new explicit installation")
	}
	for _, id := range present {
		if !slices.Contains(journal.Containers, id) {
			return result, errors.New("Marketplace removal refuses a foreign or replacement Runtipi container")
		}
		if err := requireDeclaredRuntipiManagerContainer(ctx, ops, document, id); err != nil {
			return result, err
		}
	}
	if err := requireNoRuntipiWorkloads(workspace); err != nil {
		return result, err
	}
	if err := requireNoRuntipiApps(ctx, ops, journal.Containers); err != nil {
		return result, err
	}
	journal.Complete = false
	journal.DataDisposition = request.DataDisposition
	if err := writeRuntipiMarketplaceRemoval(view, workspace, journal); err != nil {
		return result, err
	}
	fresh := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return request.ValidateAt(time.Now().UTC())
	}
	for _, id := range present {
		if err := fresh(); err != nil {
			return result, err
		}
		progressed = true
		if _, err := ops.run(ctx, []string{"stop", "--time", "20", id}, nil, ops.workspace); err != nil {
			return result, err
		}
		if _, err := ops.run(ctx, []string{"rm", id}, nil, ops.workspace); err != nil {
			return result, err
		}
	}
	if current, err := runtipiMarketplaceContainers(ctx, ops); err != nil || len(current) != 0 {
		return result, errors.New("Marketplace removal did not observe an absent Runtipi runtime")
	}
	// The bootstrap refuses to adopt a leftover network without custody, so
	// the empty network goes with the containers that created it.
	if raw, err := ops.run(ctx, []string{"network", "ls", "--filter", "name=^" + runtipiMainNetwork + "$", "--quiet"}, nil, ops.workspace); err != nil {
		return result, err
	} else if strings.TrimSpace(string(raw)) != "" {
		if err := requireNoRuntipiApps(ctx, ops, nil); err != nil {
			return result, err
		}
		progressed = true
		if _, err := ops.run(ctx, []string{"network", "rm", runtipiMainNetwork}, nil, ops.workspace); err != nil {
			return result, err
		}
	}
	if request.DataDisposition == workloadremoval.DataDispositionDelete {
		if err := fresh(); err != nil {
			return result, err
		}
		progressed = true
		if err := tx.RemoveTree(runtipiRuntimeDirectory); err != nil {
			return result, err
		}
		if err := localevidence.RemoveLocalIssuedSecret(workspace, runtipiInstallationSecretRef); err != nil {
			return result, err
		}
	}
	journal.Complete = true
	if err := writeRuntipiMarketplaceRemoval(view, workspace, journal); err != nil {
		return result, err
	}
	observation, err := resolvedplan.CanonicalJSON(journal)
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

// requireDeclaredRuntipiManagerContainer admits only the bootstrap's own
// manager services: their fixed container names and pinned images.
func requireDeclaredRuntipiManagerContainer(ctx context.Context, ops *osMarketplaceOperations, d architecturev2renderer.MarketplaceRuntipiDocument, id string) error {
	raw, err := ops.run(ctx, []string{"inspect", "--format", "{{.Name}} {{.Config.Image}}", id}, nil, ops.workspace)
	if err != nil {
		return err
	}
	fields := strings.Fields(string(raw))
	for _, component := range d.Components {
		if len(fields) == 2 && fields[0] == "/"+runtipiComposeProject+"-"+component.ID && fields[1] == component.ImageRef+"@"+component.ImageDigest {
			return nil
		}
	}
	return errors.New("Marketplace removal refuses an undeclared Runtipi project container")
}

// A stopped Runtipi app has no container on the main network, so the signed
// node deployments of Runtipi workloads are checked too.
func requireNoRuntipiWorkloads(workspace string) error {
	entries, err := os.ReadDir(filepath.Join(workspace, ".stackkit", "runtime", "applications"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "stackkit-") {
			continue
		}
		present, err := platformdeploy.HasOwnerHostDeployment(workspace, entry.Name())
		if err != nil || !present {
			continue
		}
		state, err := platformdeploy.LoadOwnerHostDeployment(workspace, entry.Name())
		if err != nil {
			return err
		}
		if platformdeploy.HostDeploymentManager(state) == "runtipi" {
			return fmt.Errorf("Marketplace removal refuses while workload %q still runs on Runtipi; remove it first", state.WorkloadRef)
		}
	}
	return nil
}

func runtipiMarketplaceContainers(ctx context.Context, ops *osMarketplaceOperations) ([]string, error) {
	raw, err := ops.run(ctx, []string{"ps", "--all", "--no-trunc", "--filter", "label=com.docker.compose.project=" + runtipiComposeProject, "--format", "{{.ID}}"}, nil, ops.workspace)
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(raw)), nil
}

// Every Runtipi app joins the main network; only the manager's own
// containers may remain on it.
func requireNoRuntipiApps(ctx context.Context, ops *osMarketplaceOperations, manager []string) error {
	raw, err := ops.run(ctx, []string{"ps", "--all", "--no-trunc", "--filter", "network=" + runtipiMainNetwork, "--format", "{{.ID}}"}, nil, ops.workspace)
	if err != nil {
		return err
	}
	for _, id := range strings.Fields(string(raw)) {
		if !slices.Contains(manager, id) {
			return errors.New("Marketplace removal refuses while Runtipi still runs installed apps; remove them first")
		}
	}
	return nil
}

func readRuntipiMarketplaceRemoval(view confinedfs.View, workspace string) (runtipiMarketplaceRemoval, bool, error) {
	var record runtipiMarketplaceRemovalRecord
	file, err := view.Open(runtipiMarketplaceRemovalFile)
	if errors.Is(err, os.ErrNotExist) {
		return record.Removal, false, nil
	}
	if err != nil {
		return record.Removal, false, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 131073))
	if err != nil || len(raw) > 131072 || json.Unmarshal(raw, &record) != nil || record.Removal.Version != "stackkit.marketplace-runtipi-removal/v1" {
		return record.Removal, false, errors.New("Runtipi Marketplace removal custody is invalid")
	}
	payload, err := resolvedplan.CanonicalJSON(record.Removal)
	if err != nil {
		return record.Removal, false, err
	}
	err = localevidence.VerifyOwnerLifecycleMutation(workspace, payload, record.Signature)
	return record.Removal, err == nil, err
}

func writeRuntipiMarketplaceRemoval(view confinedfs.View, workspace string, removal runtipiMarketplaceRemoval) error {
	payload, err := resolvedplan.CanonicalJSON(removal)
	if err != nil {
		return err
	}
	signature, err := localevidence.SignOwnerLifecycleMutation(workspace, payload)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(runtipiMarketplaceRemovalRecord{Removal: removal, Signature: signature})
	if err != nil {
		return err
	}
	_, err = view.WriteAtomic0600(runtipiMarketplaceRemovalFile, raw)
	return err
}
