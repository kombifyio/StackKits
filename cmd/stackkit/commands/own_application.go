package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	agentdocs "github.com/kombifyio/stackkits/docs/agent"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"os"
	"path/filepath"
	"strings"

	contract "github.com/kombifyio/stackkits/contracts/ownapplication"
	"github.com/kombifyio/stackkits/internal/crypto/decisionreceipt"
	"github.com/kombifyio/stackkits/internal/ownapplication"
	"github.com/spf13/cobra"
)

func init() {
	appCmd.AddCommand(newOwnApplicationCommand())
	raw, _ := agentdocs.FS.ReadFile("homelab-agent.md")
	agentPromptBodies["homelab-agent"] = string(raw)
}
func newOwnApplicationCommand() *cobra.Command {
	var action, name, document, installationFile, operation, review, resolvedImage, resolvedImagesFile, secretsFile, imageTag, capabilityFile, checkpoint string
	var dry, approved, deleteData, keepSecrets, outputJSON, allowMajor bool
	var adoption ownApplicationAdoptionFlags
	c := &cobra.Command{Use: "own", Short: "Render or operate one Techstack-owned container or Compose project on this node, or adopt an existing container (--from-container)", Annotations: map[string]string{noDeployObservabilityAnnotation: "true"}, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if adoption.container != "" || adoption.release || adoption.restore {
			if cmd.Flags().Changed("action") || document != "" || secretsFile != "" || resolvedImage != "" || resolvedImagesFile != "" || review != "" || dry || deleteData || keepSecrets || decisionReceiptPresented() {
				return errors.New("--from-container takes --phase, --name, --plan-digest, --operation-id and --owner-approve; it renders its own document")
			}
			return runOwnApplicationAdoption(cmd, adoption, name, installationFile, operation, approved)
		}
		// Every refusal below writes one result with a stable reasonCode.
		fail := func(code string, err error) error {
			return writeOwnApplicationFailure(cmd, action, name, imageTag != "" || checkpoint != "", code, err)
		}
		if adoption.phase != "" || adoption.planDigest != "" {
			return fail(ownAppReasonInvalidRequest, errors.New("--phase and --plan-digest belong to --from-container"))
		}
		if imageTag != "" && (action != "update" || document != "" || secretsFile != "" || resolvedImagesFile != "" || installationFile != "") {
			return fail(ownAppReasonInvalidRequest, errors.New("--image-tag is a tag update of the application's own image: --action update without --document, --secrets-file, --resolved-images-file or --installation-file"))
		}
		if allowMajor && imageTag == "" {
			return fail(ownAppReasonInvalidRequest, errors.New("--allow-major belongs to --image-tag"))
		}
		if action == "discard-checkpoint" && checkpoint == "" {
			return fail(ownAppReasonInvalidRequest, errors.New("--action discard-checkpoint names the checkpoint with --checkpoint"))
		}
		if checkpoint != "" && (action != "rollback" && action != "discard-checkpoint" || document != "" || secretsFile != "" || resolvedImage != "" || resolvedImagesFile != "" || installationFile != "") {
			return fail(ownAppReasonInvalidRequest, errors.New("--checkpoint names a tag update's data checkpoint: --action rollback or discard-checkpoint without a document, secrets, resolved images or installation file"))
		}
		receipted := false
		if decisionReceiptPresented() {
			if action != "remove" || !deleteData || dry {
				return fail(ownAppReasonDecisionReceipt, refuseDecisionReceipt("a decision receipt covers only removing an own application with --delete-data, and never a dry run"))
			}
			var err error
			receipted, err = admitDecisionReceipt(func() (decisionreceipt.Request, error) {
				return ownApplicationDecisionRequest(getWorkDir(), name, operation, review, keepSecrets)
			})
			if err != nil {
				return fail(ownAppReasonDecisionReceipt, err)
			}
		}
		req := ownapplication.Request{Action: action, Name: name, OperationID: operation, ReviewDigest: review, DryRun: dry, OwnerApproved: approved || receipted, DeleteData: deleteData, KeepSecrets: keepSecrets, ResolvedImage: resolvedImage, Checkpoint: checkpoint}
		if document != "" {
			raw, err := readOwnApplicationDocument(document)
			if err != nil {
				return fail(ownAppReasonDocumentInvalid, err)
			}
			a, err := contract.Parse(raw)
			if err != nil {
				return fail(ownAppReasonDocumentInvalid, err)
			}
			// Only the reviewed adoption creates a v4 application: its
			// adoptedFrom and published ports come from the observed container,
			// never from a hand-written document. Update keeps both immutable.
			if a.IsAdopted() && action == "create" {
				return fail(ownAppReasonDocumentInvalid, errors.New("a techstack.own-application/v4 application is created only by --from-container adoption"))
			}
			req.Application = &a
		}
		if resolvedImagesFile != "" {
			raw, e := readOwnApplicationDocument(resolvedImagesFile)
			if e != nil {
				return fail(ownAppReasonResolvedImages, errors.New("resolved own-application images unavailable"))
			}
			if contract.Decode(raw, &req.ResolvedImages) != nil {
				return fail(ownAppReasonResolvedImages, errors.New("resolved own-application images must be a JSON object of service names and digests"))
			}
			clear(raw)
		}
		if secretsFile != "" {
			info, e := os.Lstat(secretsFile)
			if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
				return fail(ownAppReasonSecretInput, errors.New("secrets-file must be a private regular file (0600 or stricter)"))
			}
			raw, e := readOwnApplicationDocument(secretsFile)
			if e != nil {
				return fail(ownAppReasonSecretInput, errors.New("private own-application secret input unavailable"))
			}
			if contract.Decode(raw, &req.SecretMaterial) != nil {
				return fail(ownAppReasonSecretInput, errors.New("private secret input must be a JSON object of environment keys and values"))
			}
			clear(raw)
		}
		var i contract.Installation
		var helperImage, planHash string
		if installationFile != "" {
			if !dry {
				return fail(ownAppReasonInvalidRequest, errors.New("--installation-file is inspection-only and requires --dry-run"))
			}
			raw, err := readOwnApplicationDocument(installationFile)
			if err != nil {
				return fail(ownAppReasonInstallation, err)
			}
			if err = contract.Decode(raw, &i); err != nil {
				return fail(ownAppReasonInstallation, err)
			}
		} else {
			authority, err := inspectNativeV2AppliedAuthority(commandContext(cmd), getWorkDir(), specFile)
			if err != nil {
				return fail(ownAppReasonInstallation, fmt.Errorf("own application requires an installed and verified Direct Compose node: %w", err))
			}
			if err = checkOwnApplicationCollisions(authority, name); err != nil {
				return fail(ownAppReasonNameCollision, err)
			}
			if !dry && ownApplicationLifecycleAction(action) {
				if err = admitOwnApplicationLifecycle(authority, capabilityFile, name, action, operation, review); err != nil {
					return fail(ownAppReasonCapability, err)
				}
			} else if capabilityFile != "" {
				return fail(ownAppReasonInvalidRequest, errors.New("--capability admits an executed start, stop, restart, update or rollback on a managed node only"))
			}
			if i, err = ownApplicationInstallation(authority); err != nil {
				return fail(ownAppReasonInstallation, err)
			}
			if action == "create" || action == "update" || action == "rollback" {
				state, e := ownapplication.Load(getWorkDir())
				if e != nil {
					return fail(ownAppReasonCustody, e)
				}
				hasVolume := req.Application != nil && req.Application.HasVolume() || state.Applications[name].Bundle.Application.HasVolume()
				if hasVolume {
					if e = requireOwnApplicationBackupCoverage(authority); e != nil {
						return fail(ownAppReasonBackupCoverage, e)
					}
					// A tag update checkpoints the data with the installed backup
					// helper; without it the update fails closed before stopping.
					if imageTag != "" && !dry {
						if helperImage, e = ownAdoptionHelperImage(authority); e != nil {
							return fail(ownapplication.ReasonCheckpoint, e)
						}
						planHash = authority.Plan.Binding().PlanHash
					}
				}
			}
		}
		if imageTag != "" {
			return runOwnApplicationImageUpdate(cmd, i, ownapplication.ImageUpdateRequest{Name: name, Tag: imageTag, AllowMajor: allowMajor, DryRun: dry, ResolvedImage: resolvedImage, OperationID: operation, ReviewDigest: review, OwnerApproved: approved, HelperImage: helperImage, PlanHash: planHash})
		}
		if action == "discard-checkpoint" {
			return writeOwnApplicationUpdateResult(cmd, func() (ownapplication.ImageUpdateResult, error) {
				return ownapplication.DiscardCheckpoint(commandContext(cmd), getWorkDir(), req)
			})
		}
		if checkpoint != "" {
			return runOwnApplicationCheckpointRollback(cmd, i, req)
		}
		result, err := ownapplication.Execute(commandContext(cmd), getWorkDir(), i, req)
		if err != nil {
			return fail("", err)
		}
		// The public operation always returns its versioned result, including the
		// review digest and signed custody operation; --json is accepted explicitly.
		_ = outputJSON
		if ownapplication.Mutating(action) && !dry {
			runAutomaticBackupRebind(commandContext(cmd), getWorkDir())
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}}
	c.Flags().StringVar(&action, "action", "render", "create, update, rollback, start, stop, restart, remove, status, logs, render or discard-checkpoint")
	c.Flags().StringVar(&name, "name", "", "Application DNS name, without the reserved own- prefix")
	c.Flags().StringVar(&document, "document", "", "Techstack own-application/v1, v2 or v3 JSON document (v2 outbound requires managed host firewall)")
	c.Flags().StringVar(&installationFile, "installation-file", "", "Offline installation context; dry-run only")
	c.Flags().StringVar(&resolvedImage, "resolved-image", "", "Exact digest from dry review; required for executing a tagged image")
	c.Flags().StringVar(&resolvedImagesFile, "resolved-images-file", "", "JSON object of service and digest pairs from dry review; required for executing a tagged project image")
	c.Flags().StringVar(&secretsFile, "secrets-file", "", "Private JSON environment-key/value input for application-scoped custody (a project uses <service>.<KEY> slots)")
	c.Flags().BoolVar(&keepSecrets, "keep-secrets", false, "On remove only: retain secrets with the retained named volume")
	c.Flags().StringVar(&operation, "operation-id", "", "Stable identity of this exact reviewed mutation")
	c.Flags().StringVar(&review, "review-digest", "", "Exact review digest returned by dry-run of this action")
	c.Flags().BoolVar(&dry, "dry-run", false, "Review inputs and render only; no Docker or custody writes")
	c.Flags().BoolVar(&approved, "owner-approve", false, "Approve this exact local operation")
	c.Flags().BoolVar(&deleteData, "delete-data", false, "Delete the named volume on remove; default retains it in backup")
	c.Flags().BoolVar(&outputJSON, "json", false, "Return the versioned result as JSON")
	c.Flags().StringVar(&imageTag, "image-tag", "", "Update only: move a one-container application to this newer version tag of its own image (re-resolved before stopping, data checkpoint, verify, automatic rollback of image and data)")
	c.Flags().BoolVar(&allowMajor, "allow-major", false, "With --image-tag: admit a different major version, or a running image that names no version; never a downgrade or another variant")
	c.Flags().StringVar(&checkpoint, "checkpoint", "", "Rollback: restore the data checkpoint this tag update operation took, then return to its prior image; discard-checkpoint: remove its retained copy")
	c.Flags().StringVar(&capabilityFile, "capability", "", "Managed node: Techstack capability (own-application.lifecycle) for an executed start, stop, restart, update or rollback")
	c.Flags().StringVar(&adoption.container, "from-container", "", "Stack it: adopt this existing container (ID or name) StackKits did not install, rebuilt as an own application")
	c.Flags().StringVar(&adoption.phase, "phase", "", "Adoption phase: inspect, plan, adopt or verify")
	c.Flags().StringVar(&adoption.planDigest, "plan-digest", "", "Exact adoption plan digest returned by --phase plan; required for --phase adopt")
	c.Flags().BoolVar(&adoption.release, "release-original", false, "Remove the stopped original of an adopted container (requires --owner-approve); never automatic")
	c.Flags().BoolVar(&adoption.restore, "restore-original", false, "Within 7 days of adoption: remove the rebuilt application and start the original again (requires --owner-approve)")
	c.Flags().StringArrayVar(&adoption.secretEnv, "secret-env", nil, "Adoption: also classify this environment variable as a credential (repeatable); every value is hidden anyway")
	c.Flags().BoolVar(&adoption.noOutbound, "no-outbound", false, "Adopt without outbound Internet; published host ports stay and only answer connections made to them")
	c.Flags().IntVar(&adoption.httpPort, "http-port", 0, "Adoption: the container's web port when it exposes several")
	c.Flags().IntVar(&adoption.memoryMiB, "memory-mib", 0, "Adoption: memory limit (64..8192 MiB; default the original's limit or 2048)")
	c.Flags().Float64Var(&adoption.cpus, "cpus", 0, "Adoption: CPU limit (0.1..4; default the original's limit or 2)")
	addDecisionReceiptFlags(c)
	return c
}

// ownApplicationDecisionRequest is the first-slice request removing one own
// application with its data presents to a decision receipt
// (docs/DECISION-RECEIPTS.md, "Binding definition"). The review digest of the
// dry run is the plan digest: the removal itself refuses a review digest that
// differs from the exact action, document, installation and current revision,
// so the receipt binds the reviewed removal that runs.
func ownApplicationDecisionRequest(workspace, name, operationID, reviewDigest string, keepSecrets bool) (decisionreceipt.Request, error) {
	plan, err := decisionPlanDigest("--review-digest", reviewDigest)
	if err != nil {
		return decisionreceipt.Request{}, err
	}
	revision, err := decisionReceiptRevision(workspace)
	if err != nil {
		return decisionreceipt.Request{}, err
	}
	name = strings.TrimSpace(name)
	return decisionreceipt.Request{
		Operation:  decisionreceipt.OperationApplicationRemove,
		Target:     decisionreceipt.Target{Kind: decisionreceipt.TargetOwnApplication, Ref: name},
		Arguments:  decisionreceipt.OwnApplicationRemoveArguments(name, true, keepSecrets, strings.TrimSpace(operationID)),
		PlanSHA256: plan,
		Revision:   revision,
	}, nil
}

type ownApplicationAdoptionFlags struct {
	container, phase, planDigest string
	release, restore, noOutbound bool
	secretEnv                    []string
	httpPort, memoryMiB          int
	cpus                         float64
}

// runOwnApplicationAdoption runs one phase of "Stack it": an existing
// container rebuilt as an own application under the node's own custody.
func runOwnApplicationAdoption(cmd *cobra.Command, f ownApplicationAdoptionFlags, name, installationFile, operation string, approved bool) error {
	phase := f.phase
	if f.release || f.restore {
		if phase != "" || (f.release && f.restore) {
			return errors.New("--release-original and --restore-original are phases of their own")
		}
		phase = "release-original"
		if f.restore {
			phase = "restore-original"
		}
	}
	if phase == "" {
		phase = "inspect"
	}
	req := ownapplication.AdoptionRequest{Phase: phase, Container: f.container, Name: name, HTTPPort: f.httpPort, NoOutbound: f.noOutbound, MemoryMiB: f.memoryMiB, CPUs: f.cpus, PlanDigest: f.planDigest, OperationID: operation, OwnerApproved: approved, SecretEnv: f.secretEnv}
	if f.container == "" {
		return errors.New("--from-container is required")
	}
	if f.planDigest != "" && phase != "adopt" {
		return errors.New("--plan-digest belongs to --phase adopt")
	}
	hooks := ownapplication.AdoptionHooks{}
	switch {
	case phase == "inspect":
		if installationFile != "" {
			return errors.New("--phase inspect needs no installation")
		}
	case installationFile != "":
		if phase != "plan" {
			return errors.New("--installation-file allows only an offline --phase plan")
		}
		raw, err := readOwnApplicationDocument(installationFile)
		if err != nil {
			return err
		}
		if err = contract.Decode(raw, &req.Installation); err != nil {
			return err
		}
		req.Offline = true
	default:
		authority, err := inspectNativeV2AppliedAuthority(commandContext(cmd), getWorkDir(), specFile)
		if err != nil {
			return fmt.Errorf("adoption requires an installed and verified Direct Compose node: %w", err)
		}
		if phase == "plan" || phase == "adopt" {
			if name != "" {
				if err = checkOwnApplicationCollisions(authority, name); err != nil {
					return err
				}
			}
			if req.HelperImage, err = ownAdoptionHelperImage(authority); err != nil {
				return err
			}
		}
		if req.Installation, err = ownApplicationInstallation(authority); err != nil {
			return err
		}
		hooks.BeforeCreate = func(a contract.Application) error {
			if err := checkOwnApplicationCollisions(authority, a.Name); err != nil {
				return err
			}
			if a.HasVolume() {
				return requireOwnApplicationBackupCoverage(authority)
			}
			return nil
		}
		hooks.AfterMutation = func() { runAutomaticBackupRebind(commandContext(cmd), getWorkDir()) }
	}
	result, err := ownapplication.Adopt(commandContext(cmd), getWorkDir(), req, hooks)
	if err != nil {
		if result.APIVersion != "" {
			_ = json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		}
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
}

// ownAdoptionHelperImage returns the installed, digest-pinned backup helper
// (the kit core's kopia-agent image) that measures, copies and checksums
// adoption data. Adoption never pulls a helper image.
func ownAdoptionHelperImage(a nativeV2AppliedAuthority) (string, error) {
	var plan any
	if err := json.Unmarshal(a.Plan.Canonical(), &plan); err != nil {
		return "", err
	}
	images := map[string]bool{}
	var walk func(any)
	walk = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			if v["id"] == "kopia-agent" {
				if image, ok := v["image"].(map[string]any); ok {
					ref, _ := image["ref"].(string)
					digest, _ := image["digest"].(string)
					if ref != "" && strings.HasPrefix(digest, "sha256:") {
						images[ref+"@"+digest] = true
					}
				}
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(plan)
	if len(images) != 1 {
		return "", errors.New("adoption copies data with the installed backup helper (kopia-agent); the applied plan names none or several")
	}
	for image := range images {
		return image, nil
	}
	return "", nil
}
func readOwnApplicationDocument(name string) ([]byte, error) {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return nil, err
	}
	root, err := confinedfs.Open(filepath.Dir(absolute))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return nil, err
	}
	defer transaction.Close()
	raw, _, err := transaction.ReadStableBounded(filepath.Base(absolute), 64<<10)
	return raw, err
}
func ownApplicationInstallation(a nativeV2AppliedAuthority) (contract.Installation, error) {
	var p struct {
		Network struct {
			Configuration struct {
				Domain struct {
					Base string `json:"base"`
				} `json:"domain"`
			} `json:"configuration"`
			Routes []json.RawMessage `json:"routes"`
		} `json:"network"`
	}
	if err := json.Unmarshal(a.Plan.Canonical(), &p); err != nil {
		return contract.Installation{}, err
	}
	// The route and its issuer come from the signed applied bundle, with no
	// caller-selected TLS, exposure or daemon endpoint.
	for _, w := range a.AppliedWorkloads {
		deployment, err := nativeAppliedWorkloadDeploymentForBackup(a, w.WorkloadRef)
		if err != nil {
			continue
		}
		route := deployment.Route
		if route.Host == "" || !route.TLSRequired || route.TLSIssuerRef == "" {
			continue
		}
		if deployment.RuntimeAdapter.ID != "standalone-compose" || deployment.NodeRef != a.Owner.Binding.NodeRef || deployment.SiteRef != a.Owner.Binding.SiteRef {
			continue
		}
		domain := p.Network.Configuration.Domain.Base
		if domain == "" {
			continue
		}
		if !strings.HasSuffix(route.Host, "."+domain) {
			continue
		}
		return contract.Installation{Domain: domain, SiteRef: a.Owner.Binding.SiteRef, NodeRef: a.Owner.Binding.NodeRef, CoreModuleRef: route.CoreModuleRef, TLSIssuerRef: route.TLSIssuerRef, TLSProfileRef: route.TLSProfileRef, Exposure: route.Exposure}, nil
	}
	// A core-only installation still has exact renderer-owned route inputs.
	for _, raw := range p.Network.Routes {
		var route struct {
			Host, ModuleRef, CoreModuleRef, Exposure string
			TLS                                      struct {
				Required              bool
				IssuerRef, ProfileRef string
			}
		}
		if json.Unmarshal(raw, &route) != nil || !route.TLS.Required || route.TLS.IssuerRef == "" {
			continue
		}
		if route.CoreModuleRef == "" {
			route.CoreModuleRef = route.ModuleRef
		}
		if route.CoreModuleRef != "stackkits-basement-core-runtime" && route.CoreModuleRef != "stackkits-basement-core-lite-runtime" {
			continue
		}
		return contract.Installation{Domain: p.Network.Configuration.Domain.Base, SiteRef: a.Owner.Binding.SiteRef, NodeRef: a.Owner.Binding.NodeRef, CoreModuleRef: route.CoreModuleRef, TLSIssuerRef: route.TLS.IssuerRef, TLSProfileRef: route.TLS.ProfileRef, Exposure: route.Exposure}, nil
	}
	return contract.Installation{}, errors.New("own application requires an applied Direct Compose Home core with an existing TLS route")
}

func checkOwnApplicationCollisions(a nativeV2AppliedAuthority, name string) error {
	var p struct {
		Network struct {
			Routes []struct {
				ID   string `json:"id"`
				Host string `json:"host"`
			} `json:"routes"`
			Configuration struct {
				Domain struct {
					Base string `json:"base"`
				} `json:"domain"`
			} `json:"configuration"`
		} `json:"network"`
	}
	if err := json.Unmarshal(a.Plan.Canonical(), &p); err != nil {
		return err
	}
	ref := "own-" + name
	for _, workload := range a.AppliedWorkloads {
		if workload.WorkloadRef == ref {
			return errors.New("own application name collides with a catalog workload")
		}
	}
	for _, route := range p.Network.Routes {
		if route.ID == ref || route.Host == ref+"."+p.Network.Configuration.Domain.Base {
			return errors.New("own application hostname collides with an installation route")
		}
	}
	return nil
}

func requireOwnApplicationBackupCoverage(a nativeV2AppliedAuthority) error {
	_, requirement, err := nativeV2BackupPolicyRequirement(a.Plan, a.Owner.Binding.SiteRef, a.Owner.Binding.NodeRef)
	if err != nil {
		return err
	}
	root, err := confinedfs.Open(a.WorkspaceRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer transaction.Close()
	_, _, policy, err := readNativeV2BackupPolicyForArtifact(transaction, a.Manifest, requirement.ID)
	if err != nil {
		return err
	}
	return ownapplication.RequireBackupCoverage(policy)
}
