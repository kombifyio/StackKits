package commands

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/federationbinding"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/spf13/cobra"
)

const (
	federationCapabilityRef      = "inter-site-link"
	federationCanonicalPlanName  = ".stackkit/resolved-plan.json"
	federationEvidenceRoot       = ".stackkit/evidence"
	federationInventoryVersion   = "stackkit.inventory/v1"
	federationHermeticNonceBytes = 32
)

type federationBindingCommandDeps struct {
	workspace        func() string
	now              func() time.Time
	random           io.Reader
	stackKitsVersion func() string
	mutate           func(string, string, func() error) error
}

type federationBindingImportOptions struct {
	admission          string
	inventory          string
	allowHermeticProof bool
}

type federationBindingAdoptOptions struct {
	binding   string
	inventory string
}

type federationBindingIssueOptions struct {
	resolvedPlan       string
	candidateDigest    string
	validFor           time.Duration
	output             string
	allowHermeticProof bool
}

type federationBindingImportResult struct {
	SchemaVersion    string `json:"schemaVersion"`
	InventoryPath    string `json:"inventoryPath"`
	CapabilityRef    string `json:"capabilityRef"`
	BindingHash      string `json:"bindingHash"`
	RequirementsHash string `json:"requirementsHash"`
}

type federationBindingIssueResult struct {
	SchemaVersion    string `json:"schemaVersion"`
	EvidencePath     string `json:"evidencePath"`
	Purpose          string `json:"purpose"`
	CapabilityRef    string `json:"capabilityRef"`
	RequirementsHash string `json:"requirementsHash"`
	ValidUntil       string `json:"validUntil"`
}

type federationBindingAdoptResult struct {
	SchemaVersion    string `json:"schemaVersion"`
	InventoryPath    string `json:"inventoryPath"`
	EvidencePath     string `json:"evidencePath"`
	CapabilityRef    string `json:"capabilityRef"`
	BindingHash      string `json:"bindingHash"`
	RequirementsHash string `json:"requirementsHash"`
	OwnerRef         string `json:"ownerRef"`
	KeyID            string `json:"keyId"`
}

type federationBindingMutationInput struct {
	requirement resolvedplan.FederationLinkRequirement
	inventory   resolvedplan.InventoryFacts
	admission   []byte
}

var federationBindingDeps = federationBindingCommandDeps{
	workspace:        getWorkDir,
	now:              time.Now,
	random:           rand.Reader,
	stackKitsVersion: func() string { return version },
	mutate:           withLifecycleMutation,
}

var federationCmd = newFederationCommand(federationBindingDeps)
var internalProofCmd = newInternalProofCommand(federationBindingDeps)

func init() {
	rootCmd.AddCommand(federationCmd, internalProofCmd)
}

func newFederationCommand(deps federationBindingCommandDeps) *cobra.Command {
	options := federationBindingImportOptions{}
	adoptOptions := federationBindingAdoptOptions{}
	command := &cobra.Command{
		Use:   "federation",
		Short: "Manage local Federation binding evidence",
		Annotations: map[string]string{
			noDeployObservabilityAnnotation: "true",
		},
	}
	binding := &cobra.Command{
		Use:   "binding",
		Short: "Manage opaque external Federation-link bindings",
	}
	importCommand := &cobra.Command{
		Use:   "import",
		Short: "Admit an Owner-signed opaque binding into local Inventory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runFederationBindingImport(cmd, deps, options)
		},
	}
	importCommand.Flags().StringVar(
		&options.admission, "admission", "",
		"Workspace-confined Owner-signed Federation binding admission JSON",
	)
	importCommand.Flags().StringVar(
		&options.inventory, "inventory", "",
		"Workspace-confined inventory.json to update atomically",
	)
	importCommand.Flags().BoolVar(
		&options.allowHermeticProof, "allow-hermetic-proof", false,
		"Internal E2E only: admit a hermetic live-proof binding",
	)
	_ = importCommand.Flags().MarkHidden("allow-hermetic-proof")
	adoptCommand := &cobra.Command{
		Use:     "adopt",
		Aliases: []string{"approve"},
		Short:   "Validate, Owner-sign, and atomically adopt an external binding",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runFederationBindingAdopt(cmd, deps, adoptOptions)
		},
	}
	adoptCommand.Flags().StringVar(
		&adoptOptions.binding, "binding", "",
		"Workspace-confined unsigned external Federation binding JSON",
	)
	adoptCommand.Flags().StringVar(
		&adoptOptions.inventory, "inventory", "",
		"Workspace-confined inventory.json to update atomically",
	)
	binding.AddCommand(importCommand, adoptCommand)
	command.AddCommand(binding)
	return command
}

func runFederationBindingAdopt(
	cmd *cobra.Command,
	deps federationBindingCommandDeps,
	options federationBindingAdoptOptions,
) error {
	if strings.TrimSpace(options.binding) == "" || strings.TrimSpace(options.inventory) == "" {
		return errors.New("federation binding adopt requires --binding and --inventory")
	}
	workspace, err := federationWorkspace(deps.workspace())
	if err != nil {
		return err
	}
	bindingPath, err := federationWorkspacePath(workspace, options.binding, "unsigned binding")
	if err != nil {
		return err
	}
	inventoryPath, err := federationWorkspacePath(workspace, options.inventory, "Inventory")
	if err != nil {
		return err
	}
	if path.Base(inventoryPath) != "inventory.json" {
		return errors.New("federation binding adopt requires an inventory.json target")
	}
	planPath := federationPlanPathForInventory(inventoryPath)
	now := deps.now().UTC().Truncate(time.Second)

	preflight, err := loadFederationBindingAdoptionInput(
		workspace, planPath, bindingPath, inventoryPath,
	)
	if err != nil {
		return err
	}
	admission, err := federationbinding.SignProductionImport(
		workspace, preflight.binding, preflight.requirement, now,
	)
	if err != nil {
		return fmt.Errorf("preflight Federation binding adoption: %w", err)
	}
	if _, err := federationbinding.ImportIntoInventory(
		workspace, admission, preflight.requirement, preflight.inventory,
		now, federationbinding.ImportOptions{},
	); err != nil {
		return fmt.Errorf("preflight Owner-signed Federation binding import: %w", err)
	}

	var result federationBindingAdoptResult
	err = deps.mutate(workspace, "federation binding adopt", func() error {
		current, err := loadFederationBindingAdoptionInput(
			workspace, planPath, bindingPath, inventoryPath,
		)
		if err != nil {
			return err
		}
		currentAdmission, err := federationbinding.SignProductionImport(
			workspace, current.binding, current.requirement, now,
		)
		if err != nil {
			return fmt.Errorf("revalidate unsigned Federation binding under lifecycle lock: %w", err)
		}
		if !bytes.Equal(currentAdmission, admission) {
			return errors.New("Federation binding, requirement, or Owner custody changed before adoption")
		}
		updated, err := federationbinding.ImportIntoInventory(
			workspace, currentAdmission, current.requirement, current.inventory,
			now, federationbinding.ImportOptions{},
		)
		if err != nil {
			return fmt.Errorf("revalidate Owner-signed Federation binding import: %w", err)
		}
		canonicalInventory, err := resolvedplan.CanonicalJSON(updated)
		if err != nil {
			return fmt.Errorf("canonicalize adopted Federation Inventory: %w", err)
		}
		bindingHash := stringValue(current.binding, "bindingHash")
		evidencePath := path.Join(
			federationEvidenceRoot, "federation-bindings",
			strings.TrimPrefix(bindingHash, "sha256:")+".admission.json",
		)
		if err := writeFederationPrivateAtomic(
			workspace, evidencePath, currentAdmission,
		); err != nil {
			return fmt.Errorf("persist Owner-signed Federation adoption evidence: %w", err)
		}
		if err := writeFederationPrivateAtomic(
			workspace, inventoryPath, canonicalInventory,
		); err != nil {
			return fmt.Errorf("persist adopted Federation Inventory: %w", err)
		}
		owner, err := localevidence.LoadOwnerCustody(workspace)
		if err != nil {
			return fmt.Errorf("reload Owner custody after Federation adoption: %w", err)
		}
		result = federationBindingAdoptResult{
			SchemaVersion:    "stackkit.federation-binding-adopt-result/v1",
			InventoryPath:    inventoryPath,
			EvidencePath:     evidencePath,
			CapabilityRef:    federationCapabilityRef,
			BindingHash:      bindingHash,
			RequirementsHash: stringValue(current.requirement, "requirementsHash"),
			OwnerRef:         owner.OwnerRef,
			KeyID:            owner.KeyID,
		}
		return nil
	})
	if err != nil {
		return err
	}
	return writeCommandResult(cmd, cmd.CommandPath(), result)
}

type federationBindingAdoptionInput struct {
	requirement resolvedplan.FederationLinkRequirement
	inventory   resolvedplan.InventoryFacts
	binding     resolvedplan.ExternalFederationLinkBinding
}

func loadFederationBindingAdoptionInput(
	workspace, planPath, bindingPath, inventoryPath string,
) (federationBindingAdoptionInput, error) {
	requirement, err := readFederationRequirement(workspace, planPath)
	if err != nil {
		return federationBindingAdoptionInput{}, err
	}
	rawBinding, err := readFederationStableFile(workspace, bindingPath)
	if err != nil {
		return federationBindingAdoptionInput{}, fmt.Errorf("read unsigned Federation binding: %w", err)
	}
	binding, err := federationbinding.DecodeUnsignedProductionBinding(rawBinding)
	if err != nil {
		return federationBindingAdoptionInput{}, err
	}
	inventory, err := readFederationInventory(workspace, inventoryPath)
	if err != nil {
		return federationBindingAdoptionInput{}, err
	}
	return federationBindingAdoptionInput{
		requirement: requirement, inventory: inventory, binding: binding,
	}, nil
}

func newInternalProofCommand(deps federationBindingCommandDeps) *cobra.Command {
	options := federationBindingIssueOptions{validFor: 10 * time.Minute}
	internal := &cobra.Command{
		Use:    "internal",
		Short:  "Internal StackKits proof tooling",
		Hidden: true,
		Annotations: map[string]string{
			noDeployObservabilityAnnotation: "true",
		},
	}
	proof := &cobra.Command{Use: "proof", Short: "Create hermetic live-proof evidence"}
	federationBinding := &cobra.Command{
		Use:   "federation-binding",
		Short: "Create short-lived Federation binding proof evidence",
	}
	issue := &cobra.Command{
		Use:   "issue",
		Short: "Issue Owner-signed hermetic Federation binding evidence",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runFederationBindingIssue(cmd, deps, options)
		},
	}
	issue.Flags().StringVar(
		&options.resolvedPlan, "resolved-plan", "",
		"Workspace-confined canonical ResolvedPlan",
	)
	issue.Flags().StringVar(
		&options.candidateDigest, "candidate-digest", "",
		"Exact sha256:<hex> release candidate source digest",
	)
	issue.Flags().DurationVar(
		&options.validFor, "valid-for", 10*time.Minute,
		"Short proof validity, at most 15m",
	)
	issue.Flags().StringVar(
		&options.output, "output", "",
		"Workspace-confined output below .stackkit/evidence/",
	)
	issue.Flags().BoolVar(
		&options.allowHermeticProof, "allow-hermetic-proof", false,
		"Explicitly enable the hermetic proof lane in an E2E-tagged build",
	)
	federationBinding.AddCommand(issue)
	proof.AddCommand(federationBinding)
	internal.AddCommand(proof)
	return internal
}

func runFederationBindingImport(
	cmd *cobra.Command,
	deps federationBindingCommandDeps,
	options federationBindingImportOptions,
) error {
	if strings.TrimSpace(options.admission) == "" || strings.TrimSpace(options.inventory) == "" {
		return errors.New("federation binding import requires --admission and --inventory")
	}
	if options.allowHermeticProof {
		if err := requireFederationHermeticBuild(); err != nil {
			return err
		}
	}
	workspace, err := federationWorkspace(deps.workspace())
	if err != nil {
		return err
	}
	admissionPath, err := federationWorkspacePath(workspace, options.admission, "admission")
	if err != nil {
		return err
	}
	inventoryPath, err := federationWorkspacePath(workspace, options.inventory, "Inventory")
	if err != nil {
		return err
	}
	if path.Base(inventoryPath) != "inventory.json" {
		return errors.New("federation binding import requires an inventory.json target")
	}
	planPath := federationPlanPathForInventory(inventoryPath)
	now := deps.now().UTC().Truncate(time.Second)
	importOptions := federationbinding.ImportOptions{AllowHermeticProof: options.allowHermeticProof}

	preflight, err := loadFederationBindingMutationInput(
		workspace, planPath, admissionPath, inventoryPath,
	)
	if err != nil {
		return err
	}
	if _, err := federationbinding.ImportIntoInventory(
		workspace, preflight.admission, preflight.requirement,
		preflight.inventory, now, importOptions,
	); err != nil {
		return fmt.Errorf("preflight Federation binding import: %w", err)
	}

	var result federationBindingImportResult
	err = deps.mutate(workspace, "federation binding import", func() error {
		current, err := loadFederationBindingMutationInput(
			workspace, planPath, admissionPath, inventoryPath,
		)
		if err != nil {
			return err
		}
		updated, err := federationbinding.ImportIntoInventory(
			workspace, current.admission, current.requirement,
			current.inventory, now, importOptions,
		)
		if err != nil {
			return fmt.Errorf("revalidate Federation binding import under lifecycle lock: %w", err)
		}
		canonical, err := resolvedplan.CanonicalJSON(updated)
		if err != nil {
			return fmt.Errorf("canonicalize updated Inventory: %w", err)
		}
		if err := writeFederationPrivateAtomic(workspace, inventoryPath, canonical); err != nil {
			return fmt.Errorf("persist Federation binding Inventory: %w", err)
		}
		binding, err := federationBindingFromInventory(updated)
		if err != nil {
			return err
		}
		result = federationBindingImportResult{
			SchemaVersion:    "stackkit.federation-binding-import-result/v1",
			InventoryPath:    inventoryPath,
			CapabilityRef:    federationCapabilityRef,
			BindingHash:      stringValue(binding, "bindingHash"),
			RequirementsHash: stringValue(current.requirement, "requirementsHash"),
		}
		return nil
	})
	if err != nil {
		return err
	}
	return writeCommandResult(cmd, cmd.CommandPath(), result)
}

func runFederationBindingIssue(
	cmd *cobra.Command,
	deps federationBindingCommandDeps,
	options federationBindingIssueOptions,
) error {
	if !options.allowHermeticProof {
		return errors.New("internal proof federation-binding issue requires --allow-hermetic-proof")
	}
	if err := requireFederationHermeticBuild(); err != nil {
		return err
	}
	if strings.TrimSpace(options.resolvedPlan) == "" ||
		strings.TrimSpace(options.candidateDigest) == "" ||
		strings.TrimSpace(options.output) == "" {
		return errors.New("internal proof federation-binding issue requires --resolved-plan, --candidate-digest, and --output")
	}
	workspace, err := federationWorkspace(deps.workspace())
	if err != nil {
		return err
	}
	planPath, err := federationWorkspacePath(workspace, options.resolvedPlan, "ResolvedPlan")
	if err != nil {
		return err
	}
	outputPath, err := federationWorkspacePath(workspace, options.output, "proof output")
	if err != nil {
		return err
	}
	if outputPath == federationEvidenceRoot ||
		!strings.HasPrefix(outputPath, federationEvidenceRoot+"/") {
		return errors.New("hermetic Federation binding proof output must be below .stackkit/evidence/")
	}
	requirement, err := readFederationRequirement(workspace, planPath)
	if err != nil {
		return err
	}
	nonce := make([]byte, federationHermeticNonceBytes)
	if _, err := io.ReadFull(deps.random, nonce); err != nil {
		return fmt.Errorf("generate hermetic Federation binding nonce: %w", err)
	}
	issuedAt := deps.now().UTC().Truncate(time.Second)
	request := federationbinding.HermeticIssueRequest{
		Requirement:      requirement,
		StackKitsVersion: strings.TrimSpace(deps.stackKitsVersion()),
		CandidateDigest:  strings.TrimSpace(options.candidateDigest),
		IssuedAt:         issuedAt,
		Validity:         options.validFor,
		Nonce:            nonce,
	}
	admission, err := federationbinding.IssueHermeticProof(workspace, request)
	if err != nil {
		return fmt.Errorf("preflight hermetic Federation binding proof: %w", err)
	}

	err = deps.mutate(workspace, "internal proof federation-binding issue", func() error {
		currentRequirement, err := readFederationRequirement(workspace, planPath)
		if err != nil {
			return err
		}
		request.Requirement = currentRequirement
		current, err := federationbinding.IssueHermeticProof(workspace, request)
		if err != nil {
			return fmt.Errorf("revalidate hermetic Federation binding proof under lifecycle lock: %w", err)
		}
		if string(current) != string(admission) {
			return errors.New("ResolvedPlan changed before hermetic Federation binding proof persistence")
		}
		return writeFederationPrivateAtomic(workspace, outputPath, current)
	})
	if err != nil {
		return err
	}
	return writeCommandResult(cmd, cmd.CommandPath(), federationBindingIssueResult{
		SchemaVersion:    "stackkit.federation-binding-proof-result/v1",
		EvidencePath:     outputPath,
		Purpose:          federationbinding.PurposeHermeticProof,
		CapabilityRef:    federationCapabilityRef,
		RequirementsHash: stringValue(requirement, "requirementsHash"),
		ValidUntil:       issuedAt.Add(options.validFor).Format(time.RFC3339),
	})
}

func requireFederationHermeticBuild() error {
	if !federationHermeticBuildEnabled {
		return errors.New("hermetic Federation binding proof is disabled in production builds; rebuild with the internal stackkit_e2e tag")
	}
	return nil
}

func federationWorkspace(candidate string) (string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(candidate))
	if err != nil {
		return "", fmt.Errorf("resolve Federation workspace: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect Federation workspace: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("Federation workspace must be a plain directory")
	}
	return filepath.Clean(absolute), nil
}

func federationWorkspacePath(workspace, candidate, label string) (string, error) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return "", fmt.Errorf("%s path is required", label)
	}
	absolute := candidate
	if !filepath.IsAbs(absolute) {
		absolute = filepath.Join(workspace, absolute)
	}
	absolute, err := filepath.Abs(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve %s path: %w", label, err)
	}
	relative, err := filepath.Rel(workspace, absolute)
	if err != nil {
		return "", fmt.Errorf("confine %s path to workspace: %w", label, err)
	}
	if relative == "." || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(relative) {
		return "", fmt.Errorf("%s path must remain below the workspace", label)
	}
	return filepath.ToSlash(relative), nil
}

func federationPlanPathForInventory(inventoryPath string) string {
	directory := path.Dir(inventoryPath)
	if path.Base(directory) == ".stackkit" {
		return path.Join(directory, "resolved-plan.json")
	}
	return path.Join(directory, federationCanonicalPlanName)
}

func loadFederationBindingMutationInput(
	workspace, planPath, admissionPath, inventoryPath string,
) (federationBindingMutationInput, error) {
	requirement, err := readFederationRequirement(workspace, planPath)
	if err != nil {
		return federationBindingMutationInput{}, err
	}
	admission, err := readFederationStableFile(workspace, admissionPath)
	if err != nil {
		return federationBindingMutationInput{}, fmt.Errorf("read Federation binding admission: %w", err)
	}
	inventory, err := readFederationInventory(workspace, inventoryPath)
	if err != nil {
		return federationBindingMutationInput{}, err
	}
	return federationBindingMutationInput{
		requirement: requirement,
		inventory:   inventory,
		admission:   admission,
	}, nil
}

func readFederationRequirement(
	workspace, planPath string,
) (resolvedplan.FederationLinkRequirement, error) {
	raw, err := readFederationStableFile(workspace, planPath)
	if err != nil {
		return nil, fmt.Errorf("read canonical ResolvedPlan: %w", err)
	}
	plan, err := resolvedplan.DecodeCanonicalPlan(raw)
	if err != nil {
		return nil, fmt.Errorf("verify canonical ResolvedPlan: %w", err)
	}
	requirements, ok := plan["federationLinkRequirements"].(map[string]any)
	if !ok {
		return nil, errors.New("ResolvedPlan has no Federation link requirements")
	}
	requirement, ok := requirements[federationCapabilityRef].(map[string]any)
	if !ok {
		return nil, errors.New("ResolvedPlan has no compiler-derived inter-site-link requirement")
	}
	canonical, err := resolvedplan.CanonicalJSON(requirement)
	if err != nil {
		return nil, fmt.Errorf("canonicalize Federation link requirement: %w", err)
	}
	var detached resolvedplan.FederationLinkRequirement
	if err := json.Unmarshal(canonical, &detached); err != nil {
		return nil, fmt.Errorf("detach Federation link requirement: %w", err)
	}
	return detached, nil
}

func readFederationInventory(
	workspace, inventoryPath string,
) (resolvedplan.InventoryFacts, error) {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return nil, err
	}
	defer func() { _ = transaction.Close() }()
	exists, _, err := transaction.Exists(inventoryPath)
	if err != nil {
		return nil, fmt.Errorf("inspect Inventory: %w", err)
	}
	if !exists {
		return resolvedplan.InventoryFacts{
			"schemaVersion": federationInventoryVersion,
			"nodes":         map[string]any{},
		}, nil
	}
	raw, _, err := transaction.ReadStable(inventoryPath)
	if err != nil {
		return nil, fmt.Errorf("read stable Inventory: %w", err)
	}
	inventory, err := resolvedplan.DecodeDocument[resolvedplan.InventoryFacts](raw)
	if err != nil {
		return nil, fmt.Errorf("decode closed Inventory JSON: %w", err)
	}
	return inventory, nil
}

func readFederationStableFile(workspace, relative string) ([]byte, error) {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return nil, err
	}
	defer func() { _ = transaction.Close() }()
	raw, _, err := transaction.ReadStable(relative)
	return raw, err
}

func writeFederationPrivateAtomic(workspace, relative string, data []byte) error {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Close() }()
	parent := path.Dir(relative)
	if err := transaction.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	absoluteParent := filepath.Join(workspace, filepath.FromSlash(parent))
	if err := backupcustody.ProtectPrivatePath(absoluteParent, true); err != nil {
		return fmt.Errorf("protect Federation evidence parent: %w", err)
	}
	view, err := root.View(".")
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600(relative, data)
	if err != nil {
		return err
	}
	if !result.Installed || !result.FileSynced || !result.PermissionsVerified {
		return fmt.Errorf("atomic Federation write returned incomplete durability evidence: %#v", result)
	}
	absolute := filepath.Join(workspace, filepath.FromSlash(relative))
	if err := backupcustody.ProtectPrivatePath(absolute, false); err != nil {
		return fmt.Errorf("protect Federation evidence file: %w", err)
	}
	return root.VerifyPathIdentity()
}

func federationBindingFromInventory(
	inventory resolvedplan.InventoryFacts,
) (resolvedplan.ExternalFederationLinkBinding, error) {
	bindings, ok := inventory["externalFederationLinkBindings"].(map[string]any)
	if !ok {
		return nil, errors.New("updated Inventory has no Federation binding projection")
	}
	binding, ok := bindings[federationCapabilityRef].(map[string]any)
	if !ok {
		return nil, errors.New("updated Inventory has no inter-site-link binding")
	}
	return resolvedplan.ExternalFederationLinkBinding(binding), nil
}

func stringValue(object map[string]any, field string) string {
	value, _ := object[field].(string)
	return value
}
