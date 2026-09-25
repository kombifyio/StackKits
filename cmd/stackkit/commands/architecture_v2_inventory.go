package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/hostconformance"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"gopkg.in/yaml.v3"
)

var errInventoryNodeUnbound = errors.New("local inventory probe requires --local-node/--local-site when the spec has multiple enabled nodes")

// observeLocalInventory replaces the host observation behind lifecycle
// Inventory attestation. Nil (production) observes the local host.
var observeLocalInventory func(context.Context) (hostconformance.NodeInventoryFacts, error)

type inventorySpecView struct {
	Sites     []inventorySpecSite    `yaml:"sites"`
	Nodes     []inventorySpecNode    `yaml:"nodes"`
	Install   inventorySpecInstall   `yaml:"install"`
	Storage   inventorySpecStorage   `yaml:"storage"`
	Container inventorySpecContainer `yaml:"container"`
}

type inventorySpecInstall struct {
	Runtime string `yaml:"runtime"`
}

type inventorySpecStorage struct {
	DataRoot     string `yaml:"dataRoot"`
	VolumeDriver string `yaml:"volumeDriver"`
}

type inventorySpecContainer struct {
	DataRoot string `yaml:"dataRoot"`
}

type inventorySpecSite struct {
	ID   string `yaml:"id"`
	Kind string `yaml:"kind"`
}

type inventorySpecNode struct {
	ID      string `yaml:"id"`
	SiteRef string `yaml:"siteRef"`
	Enabled *bool  `yaml:"enabled"`
}

func locateArchitectureV2Inventory(wd, explicit string) (data []byte, path string, err error) {
	if strings.TrimSpace(explicit) != "" {
		path = resolvePathFromWorkDir(wd, explicit)
		data, err = os.ReadFile(path)
		if err != nil {
			return nil, "", fmt.Errorf("read architecture v2 Inventory %s: %w", path, err)
		}
		return data, path, nil
	}

	candidates := []string{
		filepath.Join(wd, ".stackkit", "inventory.yaml"),
		filepath.Join(wd, ".stackkit", "inventory.json"),
		filepath.Join(wd, "inventory.yaml"),
		filepath.Join(wd, "inventory.json"),
	}
	var selected []string
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			selected = append(selected, candidate)
		} else if err != nil && !os.IsNotExist(err) {
			return nil, "", fmt.Errorf("inspect architecture v2 Inventory candidate %s: %w", candidate, err)
		}
	}
	if len(selected) > 1 {
		return nil, "", fmt.Errorf("architecture v2 Inventory is ambiguous; choose exactly one with --inventory: %s", strings.Join(selected, ", "))
	}
	if len(selected) == 0 {
		return nil, "", nil
	}
	data, err = os.ReadFile(selected[0])
	if err != nil {
		return nil, "", fmt.Errorf("read architecture v2 Inventory %s: %w", selected[0], err)
	}
	return data, selected[0], nil
}

func readArchitectureV2Inventory(wd, explicit string) ([]byte, error) {
	data, _, err := locateArchitectureV2Inventory(wd, explicit)
	return data, err
}

// inventoryForGeneratedPlan retains only the generated local free-space sample
// for identity verification. Every other current fact remains an input to CUE;
// the independently observed resolution still governs current Apply readiness.
func inventoryForGeneratedPlan(wd string, rawSpec, inventory []byte, options architectureV2ExecutionCLIOptions, plan generationartifact.VerifiedPlan) ([]byte, bool, error) {
	if options.inventoryPath != "" {
		return inventory, false, nil
	}
	if architectureV2WorkspaceIsMember(wd) {
		// A member never re-measures facts into the shared Inventory, so the
		// admitted document is already the stable projection.
		return inventory, false, nil
	}
	nodeRef, _, err := localInventoryNode(wd, rawSpec, options)
	if err != nil {
		return nil, false, err
	}
	var snapshot struct {
		Source struct {
			Inventory struct {
				Document struct {
					Nodes map[string]struct {
						StorageCapacity map[string]any `json:"storageCapacity"`
					} `json:"nodes"`
				} `json:"document"`
			} `json:"inventory"`
		} `json:"source"`
	}
	if err := json.Unmarshal(plan.Canonical(), &snapshot); err != nil {
		return nil, false, err
	}
	document, err := decodeInventoryDocument(inventory)
	if err != nil {
		return nil, false, err
	}
	nodes, _ := document["nodes"].(map[string]any)
	node, _ := nodes[nodeRef].(map[string]any)
	capacity, _ := node["storageCapacity"].(map[string]any)
	previous := snapshot.Source.Inventory.Document.Nodes[nodeRef].StorageCapacity
	previousFree, exists := previous["freeGiB"]
	if !exists || capacity == nil || capacity["sourceRef"] != previous["sourceRef"] || capacity["path"] != previous["path"] || capacity["freeGiB"] == previousFree {
		return inventory, false, nil
	}
	capacity["freeGiB"] = previousFree
	encoded, err := json.Marshal(document)
	return encoded, err == nil, err
}

// inventoryForPersistedGeneration applies inventoryForGeneratedPlan for
// callers that read the workspace Inventory without executing a lifecycle
// phase (Advanced admission and rollback). Every non-generate command
// re-attests the local free-space sample, so the on-disk Inventory must be
// projected onto the persisted generated plan exactly as normal execution does
// before the binding is compared. Without a persisted plan the Inventory is
// returned unchanged and the caller's own plan read reports the absence.
func inventoryForPersistedGeneration(service *architecturev2.Service, wd string, rawSpec, inventory []byte) ([]byte, error) {
	resolved, err := service.Resolve(architecturev2.ResolveInput{StackSpec: rawSpec, Inventory: inventory})
	if err != nil {
		return nil, err
	}
	current, err := service.VerifyCanonicalPlan(resolved.CanonicalPlan)
	if err != nil {
		return nil, err
	}
	planPath, _, _ := current.MetadataPaths(wd)
	if _, err := os.Lstat(planPath); errors.Is(err, os.ErrNotExist) {
		return inventory, nil
	}
	persisted, err := service.ReadCanonicalPlan(planPath)
	if err != nil {
		return nil, err
	}
	stable, _, err := inventoryForGeneratedPlan(wd, rawSpec, inventory, architectureV2ExecutionCLIOptions{}, persisted)
	return stable, err
}

// materializePlanInventory writes the Inventory document a canonical
// ResolvedPlan was resolved from to a private temporary file. A child command
// given that file with --inventory resolves exactly that plan instead of a
// freshly re-measured local free-space sample; an explicit Inventory is never
// re-attested. An empty path means the plan recorded no Inventory document.
func materializePlanInventory(canonicalPlan []byte) (path string, cleanup func(), err error) {
	var plan struct {
		Source struct {
			Inventory struct {
				Document json.RawMessage `json:"document"`
			} `json:"inventory"`
		} `json:"source"`
	}
	if err := json.Unmarshal(canonicalPlan, &plan); err != nil {
		return "", nil, err
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(plan.Source.Inventory.Document, &document) != nil || len(document) == 0 {
		return "", func() {}, nil
	}
	directory, err := os.MkdirTemp("", "stackkit-plan-inventory-*")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(directory) }
	path = filepath.Join(directory, "inventory.json")
	if err := os.Chmod(directory, 0o700); err != nil {
		cleanup()
		return "", nil, err
	}
	if err := os.WriteFile(path, plan.Source.Inventory.Document, 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

func attestLocalInventoryFacts(
	ctx context.Context,
	wd string,
	rawSpec, inventory []byte,
	inventoryPath string,
	options architectureV2ExecutionCLIOptions,
	observe func(context.Context) (hostconformance.NodeInventoryFacts, error),
) (merged []byte, persistPath string, err error) {
	if strings.TrimSpace(options.inventoryPath) != "" {
		return inventory, "", nil
	}
	if architectureV2WorkspaceIsMember(wd) {
		// The Foundation Node owns the shared Inventory. A member compiles
		// exactly the admitted document so its plan hash cannot drift from the
		// Home plan through locally observed facts.
		if len(bytes.TrimSpace(inventory)) == 0 {
			return nil, "", errors.New("a Fleet member executes only with the shared Inventory the Home owner admitted; pass --inventory or keep the admitted .stackkit/inventory.yaml")
		}
		return inventory, "", nil
	}
	if observe == nil {
		probe, probeErr := localInventoryStorageProbe(wd, rawSpec)
		if probeErr != nil {
			return nil, "", probeErr
		}
		observe = func(ctx context.Context) (hostconformance.NodeInventoryFacts, error) {
			return hostconformance.ObserveNodeInventory(ctx, probe)
		}
	}
	nodeRef, siteKind, err := localInventoryNode(wd, rawSpec, options)
	if err != nil {
		return nil, "", err
	}
	facts, err := observe(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("observe local inventory facts: %w", err)
	}
	document, err := decodeInventoryDocument(inventory)
	if err != nil {
		return nil, "", err
	}
	attested, err := hostconformance.MergeNodeInventoryFacts(resolvedplan.InventoryFacts(document), nodeRef, facts, siteKind)
	if err != nil {
		return nil, "", err
	}
	encoded, err := encodeInventoryDocument(attested, inventoryPath)
	if err != nil {
		return nil, "", err
	}
	persistPath = inventoryPath
	if persistPath == "" {
		persistPath = filepath.Join(wd, ".stackkit", "inventory.yaml")
	}
	return encoded, persistPath, nil
}

func localInventoryStorageProbe(wd string, rawSpec []byte) (hostconformance.LocalProbe, error) {
	service, err := newArchitectureV2CLIService(wd, "", os.Getenv(architectureAuthorityRootEnv))
	if err != nil {
		return hostconformance.LocalProbe{}, fmt.Errorf("load CUE authority for local storage probe: %w", err)
	}
	normalized, err := service.ValidateStackSpec(rawSpec)
	if err != nil {
		return hostconformance.LocalProbe{}, fmt.Errorf("normalize StackSpec for local storage probe: %w", err)
	}
	view, err := decodeInventorySpecView(normalized.CanonicalStackSpec)
	if err != nil {
		return hostconformance.LocalProbe{}, err
	}
	if strings.EqualFold(strings.TrimSpace(view.Storage.VolumeDriver), "nfs") {
		// NFS free space is owned by the remote storage authority and cannot be
		// represented by this local host observation.
		return hostconformance.LocalProbe{}, nil
	}
	if strings.TrimSpace(view.Container.DataRoot) != "" || strings.EqualFold(strings.TrimSpace(view.Install.Runtime), "docker") || strings.EqualFold(strings.TrimSpace(view.Install.Runtime), "podman") {
		dataRoot := strings.TrimSpace(view.Container.DataRoot)
		return hostconformance.LocalProbe{StorageSourceRef: "system.container.dataRoot", StoragePath: dataRoot}, nil
	}
	dataRoot := strings.TrimSpace(view.Storage.DataRoot)
	return hostconformance.LocalProbe{StorageSourceRef: "storage.dataRoot", StoragePath: dataRoot}, nil
}

func localInventoryNode(wd string, rawSpec []byte, options architectureV2ExecutionCLIOptions) (nodeRef, siteKind string, err error) {
	view, err := decodeInventorySpecView(rawSpec)
	if err != nil {
		return "", "", err
	}
	requested := strings.TrimSpace(options.localNodeRef)
	if requested == "" {
		var memberDenial *localevidence.MemberSigningDenial
		if custody, loadErr := localevidence.LoadOwnerCustody(wd); loadErr == nil {
			requested = strings.TrimSpace(custody.Binding.NodeRef)
		} else if errors.As(loadErr, &memberDenial) {
			// A member is bound to its admitted tuple. It never merges its own
			// observations into the shared Inventory (see attestLocalInventoryFacts).
			requested = strings.TrimSpace(memberDenial.Binding.NodeRef)
		} else if !errors.Is(loadErr, localevidence.ErrOwnerCustodyMissing) {
			return "", "", fmt.Errorf("load local owner binding for inventory: %w", loadErr)
		}
	}
	if requested == "" {
		enabled := enabledSpecNodes(view)
		if len(enabled) != 1 {
			return "", "", errInventoryNodeUnbound
		}
		requested = enabled[0].ID
	}
	node, site, err := specNodeByID(view, requested)
	if err != nil {
		return "", "", err
	}
	if localSite := strings.TrimSpace(options.localSiteRef); localSite != "" && localSite != node.SiteRef {
		return "", "", fmt.Errorf("local inventory node %s belongs to site %s, not %s", node.ID, node.SiteRef, localSite)
	}
	return node.ID, site.Kind, nil
}

func decodeInventorySpecView(rawSpec []byte) (inventorySpecView, error) {
	var view inventorySpecView
	if err := yaml.Unmarshal(rawSpec, &view); err != nil {
		return inventorySpecView{}, fmt.Errorf("decode StackSpec topology for inventory: %w", err)
	}
	if len(view.Nodes) == 0 {
		return inventorySpecView{}, errors.New("StackSpec has no nodes to attest")
	}
	return view, nil
}

func enabledSpecNodes(view inventorySpecView) []inventorySpecNode {
	var enabled []inventorySpecNode
	for _, node := range view.Nodes {
		if node.Enabled != nil && !*node.Enabled {
			continue
		}
		enabled = append(enabled, node)
	}
	return enabled
}

func specNodeByID(view inventorySpecView, nodeRef string) (inventorySpecNode, inventorySpecSite, error) {
	for _, candidate := range view.Nodes {
		if candidate.ID != nodeRef {
			continue
		}
		if candidate.Enabled != nil && !*candidate.Enabled {
			return inventorySpecNode{}, inventorySpecSite{}, fmt.Errorf("inventory node %s is disabled", nodeRef)
		}
		for _, candidateSite := range view.Sites {
			if candidateSite.ID == candidate.SiteRef {
				return candidate, candidateSite, nil
			}
		}
		return inventorySpecNode{}, inventorySpecSite{}, fmt.Errorf("inventory node %s references unknown site %s", nodeRef, candidate.SiteRef)
	}
	return inventorySpecNode{}, inventorySpecSite{}, fmt.Errorf("inventory node %s is absent from the spec", nodeRef)
}

func decodeInventoryDocument(data []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{
			"schemaVersion": "stackkit.inventory/v1",
			"nodes":         map[string]any{},
		}, nil
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode architecture v2 Inventory: %w", err)
	}
	if document == nil {
		return nil, errors.New("architecture v2 Inventory is empty")
	}
	return document, nil
}

func encodeInventoryDocument(inventory resolvedplan.InventoryFacts, path string) ([]byte, error) {
	if strings.EqualFold(filepath.Ext(path), ".json") {
		encoded, err := json.MarshalIndent(inventory, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode architecture v2 Inventory: %w", err)
		}
		return append(encoded, '\n'), nil
	}
	encoded, err := yaml.Marshal(inventory)
	if err != nil {
		return nil, fmt.Errorf("encode architecture v2 Inventory: %w", err)
	}
	return encoded, nil
}

func persistInventoryDocument(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create inventory directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create inventory: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		return fmt.Errorf("secure inventory: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write inventory: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync inventory: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close inventory: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("replace inventory: %w", errors.Join(err, removeErr))
		}
		if err := os.Rename(temporaryPath, path); err != nil {
			return fmt.Errorf("install inventory: %w", err)
		}
	}
	return nil
}

// architectureV2WorkspaceIsMember reports whether the workspace holds Fleet
// member custody.
func architectureV2WorkspaceIsMember(wd string) bool {
	_, err := localevidence.LoadOwnerCustody(wd)
	var memberDenial *localevidence.MemberSigningDenial
	return errors.As(err, &memberDenial)
}
