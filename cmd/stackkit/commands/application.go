package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/actionableerror"
	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/appsetup"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/spf13/cobra"
)

type applicationOptions struct {
	workload                                                string
	containerID, planDigest, operationID, ownerFile, action string
	approved, json                                          bool
}

func newApplicationCommand() *cobra.Command {
	command := &cobra.Command{Use: "application", Short: "Adopt supported existing applications without replacing their accounts or data", Annotations: map[string]string{noDeployObservabilityAnnotation: "true"}}
	for _, verb := range []string{"inspect", "adopt", "verify", "release", "control"} {
		verb := verb
		o := &applicationOptions{}
		child := &cobra.Command{Use: verb + " [workload]", Short: map[string]string{"inspect": "Assess the exact existing application and current configuration", "adopt": "Bind the reviewed existing application to its declared lifecycle", "verify": "Verify the signed adoption binding and current source", "release": "Release lifecycle custody while retaining the application", "control": "Start, stop or restart the exact adopted container"}[verb], Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(commandContext(cmd), 3*time.Minute)
			defer cancel()
			workload := o.workload
			if len(args) == 1 {
				if workload != "" {
					return errors.New("select workload once")
				}
				workload = args[0]
			}
			if workload == "" {
				return errors.New("application operation requires a workload")
			}
			result, err := executeApplication(ctx, getWorkDir(), workload, verb, *o)
			if err != nil {
				return machineAwareCommandError(cmd, applicationCommandError(err))
			}
			return writeCommandResult(cmd, cmd.CommandPath(), result)
		}}
		child.Flags().BoolVar(&o.json, "json", false, "Emit the versioned secret-free application evidence")
		child.Flags().StringVar(&o.workload, "workload", "", "Exact selected application workload (alternative to positional workload)")
		child.Flags().StringVar(&o.containerID, "container-id", "", "Exact immutable 64-character Docker container ID")
		if verb == "verify" {
			child.Flags().StringVar(&o.operationID, "operation-id", "", "Read correlation identifier")
		}
		if verb == "inspect" || verb == "adopt" || verb == "verify" {
			child.Flags().StringVar(&o.ownerFile, "owner-file", ".stackkit/setup/home-assistant-owner.json", "Workspace-relative existing private owner grant file containing accessToken")
		}
		if verb == "adopt" {
			child.Flags().StringVar(&o.planDigest, "plan-digest", "", "Exact reviewed source-bound adoption plan digest")
		}
		if verb == "adopt" || verb == "release" || verb == "control" {
			child.Flags().BoolVar(&o.approved, "owner-approve", false, "Approve this exact owner-bound application operation")
			child.Flags().StringVar(&o.operationID, "operation-id", "", "Stable operation identifier for idempotent resume")
		}
		if verb == "control" {
			child.Flags().StringVar(&o.action, "action", "", "Admitted power action: start, stop or restart")
		}
		command.AddCommand(child)
	}
	return command
}

func init() { rootCmd.AddCommand(newApplicationCommand()) }

func loadAdoptionContract(workspace, workload string) (applicationlifecycle.Contract, error) {
	service, err := newArchitectureV2CLIService(workspace, "", os.Getenv(architectureAuthorityRootEnv))
	if err != nil {
		return applicationlifecycle.Contract{}, err
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return applicationlifecycle.Contract{}, err
	}
	// The retained plan supplies only hash-verified baseline intent/inventory.
	// Current execution authority always comes from this build's CUE resolver,
	// including when an older release compiled the running stack's plan.
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return applicationlifecycle.Contract{}, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return applicationlifecycle.Contract{}, err
	}
	defer tx.Close()
	rawPlan, _, err := tx.ReadStable("deploy/.stackkit/resolved-plan.json")
	if err != nil {
		return applicationlifecycle.Contract{}, err
	}
	baseline, err := resolvedplan.DecodeCanonicalPlan(rawPlan)
	if err != nil {
		return applicationlifecycle.Contract{}, err
	}
	rawSpec, _, handled, err := classifyArchitectureV2ExecutionSpec(workspace, specFile)
	if err != nil {
		return applicationlifecycle.Contract{}, err
	}
	if !handled {
		return applicationlifecycle.Contract{}, errors.New("unsupported_source: native canonical StackSpec is required for adoption")
	}
	validated, err := service.ValidateStackSpec(rawSpec)
	if err != nil {
		return applicationlifecycle.Contract{}, err
	}
	if validated.SpecHash != baseline["specHash"] {
		return applicationlifecycle.Contract{}, errors.New("stale_revision: current StackSpec differs from the retained canonical plan")
	}
	prepared, err := service.PrepareApplicationAdoption(validated.CanonicalStackSpec, workload, owner.Binding.SiteRef, owner.Binding.NodeRef)
	if err != nil {
		return applicationlifecycle.Contract{}, err
	}
	source, _ := baseline["source"].(map[string]any)
	inventory, _ := source["inventory"].(map[string]any)
	document, ok := inventory["document"].(map[string]any)
	if !ok {
		return applicationlifecycle.Contract{}, errors.New("missing_preservation_evidence: retained plan has no bound inventory")
	}
	inventoryJSON, err := resolvedplan.CanonicalJSON(document)
	if err != nil {
		return applicationlifecycle.Contract{}, err
	}
	resolved, err := service.Resolve(architecturev2.ResolveInput{StackSpec: prepared.CanonicalStackSpec, Inventory: inventoryJSON})
	if err != nil {
		return applicationlifecycle.Contract{}, err
	}
	plan, err := service.VerifyCanonicalPlan(resolved.CanonicalPlan)
	if err != nil {
		return applicationlifecycle.Contract{}, err
	}
	selected := false
	for _, target := range plan.ApplyRequirements().RuntimeInstances {
		if target.WorkloadRef == workload {
			if len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || target.SiteRefs[0] != owner.Binding.SiteRef || target.NodeRefs[0] != owner.Binding.NodeRef {
				return applicationlifecycle.Contract{}, errors.New("source_identity_conflict: application placement is not bound to this local Owner execution channel")
			}
			selected = true
		}
	}
	if !selected {
		return applicationlifecycle.Contract{}, errors.New("unsupported_source: no exact local application placement exists")
	}
	contract, err := applicationlifecycle.ContractFromResolvedPlan(resolved.Plan, workload)
	if err != nil {
		return contract, err
	}
	if contract.Adoption == nil || contract.Adoption.ProfileRef != applicationlifecycle.AdoptionProfile || contract.Delivery.AdapterRef != "standalone-compose" {
		return contract, errors.New("unsupported_source: selected module has no executable application adoption profile")
	}
	contract.AdoptionBaselinePlanHash, _ = baseline["planHash"].(string)
	return contract, nil
}

func inspectApplicationPlan(ctx context.Context, workspace string, contract applicationlifecycle.Contract, o applicationOptions) (applicationlifecycle.AdoptionPlan, error) {
	observed, err := nativehost.InspectApplicationAdoption(ctx, o.containerID, *contract.Adoption, true)
	if err != nil {
		return applicationlifecycle.AdoptionPlan{}, err
	}
	var grant struct {
		AccessToken string `json:"accessToken"`
	}
	if err := readNativeSetupCredentialJSON(workspace, o.ownerFile, &grant); err != nil {
		return applicationlifecycle.AdoptionPlan{}, fmt.Errorf("missing_native_grant: existing private native Owner grant is unavailable: %w", err)
	}
	defer func() { grant.AccessToken = "" }()
	owner, err := appsetup.VerifyHomeAssistantExistingOwner(ctx, nil, observed.Endpoint, grant.AccessToken, contract.Adoption.Version)
	if err != nil {
		return applicationlifecycle.AdoptionPlan{}, fmt.Errorf("missing_native_grant: existing native Owner grant could not be verified: %w", err)
	}
	if owner.UserID != observed.NativeOwnerRef {
		return applicationlifecycle.AdoptionPlan{}, errors.New("source_identity_conflict: authenticated native Owner does not belong to the inspected source account store")
	}
	final, err := nativehost.InspectApplicationAdoption(ctx, o.containerID, *contract.Adoption, true)
	if err != nil {
		return applicationlifecycle.AdoptionPlan{}, err
	}
	if final.Source != observed.Source {
		return applicationlifecycle.AdoptionPlan{}, errors.New("stale_revision: application changed during source assessment; inspect again")
	}
	return applicationlifecycle.NewAdoptionPlan(contract, final.Source, owner.UserID)
}

func executeApplication(ctx context.Context, workspace, workload, verb string, o applicationOptions) (any, error) {
	contract, err := loadAdoptionContract(workspace, workload)
	if err != nil {
		return nil, err
	}
	store := applicationlifecycle.Store{Workspace: workspace}
	if verb == "inspect" {
		return inspectApplicationPlan(ctx, workspace, contract, o)
	}
	if verb == "verify" {
		receipt, bound, err := store.AdoptionBinding(workload)
		if err != nil {
			return nil, err
		}
		if !bound {
			return nil, errors.New("application has no verified adoption binding")
		}
		if o.containerID != "" && o.containerID != receipt.Result.Source.ContainerID {
			return nil, errors.New("source_identity_conflict: requested source differs from adoption binding")
		}
		if receipt.Result.Authority != (applicationlifecycle.Authority{PlanHash: contract.PlanHash, LifecycleContractHash: contract.ContractHash, LifecycleVersion: contract.Version, PackageRef: contract.PackageRef}) {
			return nil, errors.New("stale_revision: adoption binding belongs to another resolved authority")
		}
		o.containerID = receipt.Result.Source.ContainerID
		plan, err := inspectApplicationPlan(ctx, workspace, contract, o)
		if err != nil {
			return nil, err
		}
		if plan.Source != receipt.Result.Source || plan.VerifiedOwnerRef != receipt.Result.VerifiedOwnerRef || plan.PlanDigest != receipt.Result.PlanDigest {
			return nil, errors.New("stale_revision: adopted source or native Owner changed")
		}
		return map[string]any{"schemaVersion": applicationlifecycle.AdoptionSchemaVersion, "apiVersion": "stackkit.application-adoption-verification/v1", "signatureVerified": true, "receipt": receipt, "currentSource": plan.Source}, nil
	}
	if !o.approved {
		return nil, errors.New("owner_approval_required: application operation requires --owner-approve")
	}
	if verb == "control" && o.action != "start" && o.action != "stop" && o.action != "restart" {
		return nil, errors.New("unsupported application power action")
	}
	var result applicationlifecycle.AdoptionReceipt
	err = withLifecycleMutation(workspace, "application "+verb, func() error {
		current, err := loadAdoptionContract(workspace, workload)
		if err != nil {
			return err
		}
		if current.PlanHash != contract.PlanHash || current.ContractHash != contract.ContractHash || current.AdoptionBaselinePlanHash != contract.AdoptionBaselinePlanHash {
			return errors.New("stale_revision: application authority changed before mutation")
		}
		state, err := store.Load(contract)
		if err != nil {
			return err
		}
		for _, op := range state.Operations {
			if o.operationID != "" && op.ID == o.operationID && op.Status == applicationlifecycle.StatusSucceeded {
				receipt, _, err := store.AdoptionBinding(workload)
				if err != nil {
					return err
				}
				if receipt.Result.OperationID != o.operationID || op.OperationRef != "stackkit.application."+verb || (verb == "adopt" && (receipt.Result.PlanDigest != o.planDigest || receipt.Result.Source.ContainerID != o.containerID)) || (verb == "control" && receipt.Result.Action != o.action) {
					return errors.New("operation_conflict: idempotency key belongs to different application intent")
				}
				result = receipt
				return nil
			}
		}
		binding, bound, err := store.AdoptionBinding(workload)
		if err != nil {
			return err
		}
		if bound && o.containerID != "" && o.containerID != binding.Result.Source.ContainerID {
			return errors.New("source_identity_conflict: requested source differs from adoption binding")
		}
		var adopted applicationlifecycle.AdoptionResult
		if verb == "adopt" {
			if bound {
				return errors.New("operation_conflict: application already has an adopted source; release it before another adoption")
			}
			plan, err := inspectApplicationPlan(ctx, workspace, contract, o)
			if err != nil {
				return err
			}
			if plan.PlanDigest != o.planDigest {
				return errors.New("stale_revision: reviewed adoption plan no longer matches source and resolved authority")
			}
			adopted = applicationlifecycle.AdoptionResult{APIVersion: "stackkit.application-adoption-result/v1", Authority: plan.Authority, WorkloadRef: workload, ProfileRef: plan.ProfileRef, Mode: plan.Mode, PlanDigest: plan.PlanDigest, Source: plan.Source, OwnedFields: plan.OwnedFields, PreservedFields: plan.PreservedFields, AdmittedOperations: plan.AdmittedOperations, VerifiedOwnerRef: plan.VerifiedOwnerRef, Status: "verified"}
		} else {
			if !bound {
				return errors.New("application has no verified adoption binding")
			}
			adopted = binding.Result
			boundPlan, err := applicationlifecycle.NewAdoptionPlan(contract, adopted.Source, adopted.VerifiedOwnerRef)
			if err != nil {
				return err
			}
			if boundPlan.PlanDigest != adopted.PlanDigest {
				return errors.New("stale_revision: retained stack baseline differs from adopted lifecycle binding")
			}
			if adopted.Authority != (applicationlifecycle.Authority{PlanHash: contract.PlanHash, LifecycleContractHash: contract.ContractHash, LifecycleVersion: contract.Version, PackageRef: contract.PackageRef}) {
				return errors.New("stale_revision: adopted lifecycle authority changed")
			}
			if verb == "release" {
				adopted.Status = "released"
			} else if verb != "control" {
				return errors.New("unsupported application operation")
			}
		}
		id := o.operationID
		if id == "" {
			id, err = applicationlifecycle.NewOperationID("stackkit.application." + verb)
			if err != nil {
				return err
			}
		}
		// A failed attempt is safe to retry: adoption/release have no source
		// writes. A dispatched control only reconciles; it never redispatches.
		resumed := false
		dispatched := false
		var dispatchStarted time.Time
		intent, err := applicationlifecycle.AdoptionIntentDigest(verb, o.action, adopted)
		if err != nil {
			return err
		}
		for _, op := range state.Operations {
			if op.ID == id {
				if op.OperationRef != "stackkit.application."+verb || op.IntentDigest != intent {
					return errors.New("operation_conflict: operation belongs to another application action")
				}
				if op.Status == applicationlifecycle.StatusFailed {
					_, err = store.Transition(contract, applicationlifecycle.TransitionRequest{ID: id, Status: applicationlifecycle.StatusRunning})
					if err != nil {
						return err
					}
				}
				if op.Status != applicationlifecycle.StatusRunning && op.Status != applicationlifecycle.StatusFailed {
					return errors.New("operation_conflict: operation requires reconciliation")
				}
				resumed = true
				dispatched = op.Dispatched
				dispatchStarted = op.StartedAt
			}
		}
		if !resumed {
			if _, err = store.Begin(contract, applicationlifecycle.BeginRequest{ID: id, Stage: "adopt", OperationRef: "stackkit.application." + verb, IntentDigest: intent}); err != nil {
				return err
			}
		}
		fail := func(cause error) error {
			_, journalErr := store.Transition(contract, applicationlifecycle.TransitionRequest{ID: id, Status: applicationlifecycle.StatusFailed, LastError: cause.Error()})
			return errors.Join(cause, journalErr)
		}
		if verb == "control" {
			var observation nativehost.AdoptionObservation
			if dispatched {
				observation, err = nativehost.InspectApplicationAdoption(ctx, adopted.Source.ContainerID, *contract.Adoption, false)
				if err == nil && (observation.Source.Revision != adopted.Source.Revision || observation.Running != (o.action != "stop") || (o.action == "restart" && !observation.StartedAt.After(dispatchStarted))) {
					err = errors.New("outcome_unknown: retained application control was dispatched; observed source does not prove its intended result; read-only reconciliation cannot repeat it")
				}
			} else {
				if err := store.MarkAdoptionDispatch(contract, id, intent); err != nil {
					return fail(err)
				}
				observation, err = nativehost.ControlAdoptedApplication(ctx, *contract.Adoption, adopted.Source, o.action)
			}
			if err != nil {
				return fail(err)
			}
			adopted.Action = o.action
			adopted.PowerState = "stopped"
			if observation.Running {
				adopted.PowerState = "running"
			}
		}
		adopted.OperationID = id
		adopted.VerifiedAt = time.Now().UTC()
		result, err = store.SaveAdoptionResult(contract, adopted)
		if err != nil {
			return fail(err)
		}
		_, err = store.Transition(contract, applicationlifecycle.TransitionRequest{ID: id, Status: applicationlifecycle.StatusSucceeded, Evidence: []applicationlifecycle.Evidence{result.Evidence}, Now: adopted.VerifiedAt})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("application %s: %w", verb, err)
	}
	return result, nil
}

type applicationFailure struct {
	cause                     error
	reason, message, guidance string
}

func (e *applicationFailure) Error() string { return e.cause.Error() }
func (e *applicationFailure) Unwrap() error { return e.cause }
func (e *applicationFailure) ActionableError() actionableerror.Contract {
	return actionableerror.New("stackkit_command_failed", e.reason, e.message, []string{e.guidance, "The application remains under its native ownership; no successful adoption is inferred."}, false)
}

func applicationCommandError(err error) error {
	if err == nil {
		return nil
	}
	for _, detail := range []struct{ reason, message, guidance string }{
		{"unsupported_source", "The existing application is outside the supported adoption profile.", "Use the admitted immutable Home Assistant Compose source with one exclusive /config mount and unmodified image startup configuration."},
		{"missing_native_grant", "The existing native Owner grant is missing, private custody is invalid, or authentication could not be verified.", "Place a current existing Home Assistant Owner accessToken in the private workspace-relative --owner-file; keep native sign-in and token issuance in Home Assistant."},
		{"source_identity_conflict", "The inspected application does not match the approved source identity.", "Select the full immutable container ID, exact admitted image and matching native Owner, then inspect again."},
		{"stale_revision", "The application or resolved lifecycle authority changed after approval.", "Inspect the current source and authority and review the new plan digest before a new adoption attempt."},
		{"operation_conflict", "The operation identifier belongs to another application intent or needs reconciliation.", "Reconcile the existing operation before approving another action; do not reuse its identifier for different intent."},
		{"outcome_unknown", "A retained application control dispatch has no proven outcome.", "Read back the retained operation and exact source. Do not repeat the dispatched power action."},
		{"missing_preservation_evidence", "Preservation of the existing application could not be proved.", "Restore readable supported configuration and exclusive source access, then inspect again."},
		{"source_unavailable", "The existing application is unavailable for authenticated inspection.", "Restore the native application to its running state, then inspect it with a current native Owner grant."},
		{"owner_approval_required", "The application operation has no explicit Owner approval.", "Review the exact application intent and submit --owner-approve for that operation."},
	} {
		if strings.Contains(err.Error(), detail.reason+":") {
			return &applicationFailure{err, detail.reason, detail.message, detail.guidance}
		}
	}
	return &applicationFailure{err, "application_adoption_unavailable", "The application adoption operation could not be verified.", "Inspect the canonical selected Smart Home lifecycle, local Owner custody and current supported source before retrying."}
}
