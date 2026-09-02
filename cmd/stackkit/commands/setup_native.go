package commands

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"time"

	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/appsetup"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
	"github.com/spf13/cobra"
)

type nativeSetupOptions struct {
	credentialsFile, operationID                  string
	ownerApproved, completeOnboarding, outputJSON bool
}

func newSetupCommand() *cobra.Command {
	options := &nativeSetupOptions{}
	command := &cobra.Command{
		Use: "setup <workload>", Short: "Set up the owner account of an applied application",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		Long:        "Run a Plan-declared setup action on the existing local application. Read the private credential JSON fields and file path from the selected application's setup guide or status action. Credentials are never written into lifecycle evidence. Re-running after interruption re-observes the application before completing the same pending setup operation.",
		RunE:        func(cmd *cobra.Command, args []string) error { return runNativeSetup(cmd, args[0], *options) },
	}
	command.Flags().StringVar(&options.credentialsFile, "credentials-file", "", "Workspace-relative private owner credential JSON file (defaults to the selected application's setup guide)")
	command.Flags().StringVar(&options.operationID, "operation-id", "", "Resume a pending setup operation; defaults to the current pending setup or a new ID")
	command.Flags().BoolVar(&options.ownerApproved, "owner-approve", false, "Approve this Plan-bound application setup")
	command.Flags().BoolVar(&options.completeOnboarding, "complete-onboarding", false, "Complete the app user and administrator onboarding after verifying the owner login")
	command.Flags().BoolVar(&options.outputJSON, "json", false, "Emit the secret-free verified setup result")
	return command
}

func init() { rootCmd.AddCommand(newSetupCommand()) }

func runNativeSetup(cmd *cobra.Command, workload string, options nativeSetupOptions) error {
	if !options.ownerApproved {
		return errors.New("application setup requires --owner-approve")
	}
	ctx, cancel := context.WithTimeout(commandContext(cmd), 3*time.Minute)
	defer cancel()
	workspace := getWorkDir()
	initial, err := inspectNativeV2AppliedAuthority(ctx, workspace, specFile)
	if err != nil {
		return err
	}
	var result applicationlifecycle.SetupResult
	execute := func() error {
		return withArchitectureV2OutputLock(workspace, initial.OutputRoot, func(_ *confinedfs.Transaction, _ *confinedfs.OutputLock) error {
			current, err := inspectNativeV2AppliedAuthority(ctx, workspace, specFile)
			if err != nil {
				return err
			}
			if initial.Plan.Binding() != current.Plan.Binding() || initial.Lineage != current.Lineage {
				return errors.New("application setup authority changed while acquiring the output lock")
			}
			resolved, err := resolvedplan.DecodeCanonicalPlan(current.Plan.Canonical())
			if err != nil {
				return err
			}
			contract, err := applicationlifecycle.ContractFromResolvedPlan(resolved, workload)
			if err != nil {
				return err
			}
			setup := architectureV2SetupInput(resolved, contract, nil)
			if setup.Policy != "on-demand" || len(setup.ActionRefs) != 1 {
				return errors.New("the current Plan does not declare one on-demand owner setup action")
			}
			description, supported := appsetup.DescribeNativeAction(setup.ActionRefs[0], contract.Delivery.AdapterRef)
			if !supported {
				return errors.New("the selected runtime adapter has no executable native action for this application; follow its declared setup guide")
			}
			if options.credentialsFile == "" {
				options.credentialsFile = description.CredentialsFile
			}
			deployment, err := nativeAppliedWorkloadDeployment(current, workload)
			if err != nil {
				return err
			}
			if err := validateNativeOwnerSetupAction(deployment, setup.ActionRefs[0], options); err != nil {
				return err
			}
			store := applicationlifecycle.Store{Workspace: workspace}
			operationID, err := beginNativeSetup(store, contract, options.operationID)
			if err != nil {
				return err
			}
			fail := func(cause error) error {
				_, journalErr := store.Transition(contract, applicationlifecycle.TransitionRequest{ID: operationID, Status: applicationlifecycle.StatusFailed, LastError: "application setup did not complete; retry the same operation after correcting its reported cause", Now: time.Now().UTC()})
				return errors.Join(cause, journalErr)
			}
			var observed nativeOwnerSetupObservation
			err = runtimeexecutorlocal.WithStandaloneComposeHTTP(ctx, workspace, deployment, func(client *http.Client, baseURL string) error {
				value, setupErr := executeNativeOwnerSetupAction(ctx, client, baseURL, current.WorkspaceRoot, deployment, deployment.Release, setup.ActionRefs[0], options)
				observed = value
				return setupErr
			})
			if err != nil {
				return fail(err)
			}
			latest, err := inspectNativeV2AppliedAuthority(ctx, workspace, specFile)
			if err != nil {
				return fail(err)
			}
			if latest.Plan.Binding() != current.Plan.Binding() || latest.Lineage != current.Lineage {
				return fail(errors.New("application setup authority changed before result verification"))
			}
			result = applicationlifecycle.SetupResult{
				APIVersion:  applicationlifecycle.SetupResultAPIVersion,
				Authority:   applicationlifecycle.Authority{PlanHash: contract.PlanHash, LifecycleContractHash: contract.ContractHash, LifecycleVersion: contract.Version, PackageRef: contract.PackageRef},
				WorkloadRef: workload, OperationID: operationID, ActionRef: setup.ActionRefs[0], ApplyResultHash: current.Lineage.ApplyResultHash,
				ArtifactDigest: deployment.ArtifactDigest, InstanceRef: deployment.InstanceRef, ApplicationVersion: deployment.Release,
				AccountRef: observed.AccountRef, Initialized: observed.Initialized, AdminLoginVerified: observed.AdminLoginVerified,
				OnboardingComplete: observed.OnboardingComplete, Preparation: observed.Preparation, VerifiedAt: time.Now().UTC(),
			}
			evidence, err := store.SaveSetupResult(contract, result)
			if err != nil {
				return fail(err)
			}
			_, err = store.Transition(contract, applicationlifecycle.TransitionRequest{ID: operationID, Status: applicationlifecycle.StatusSucceeded, Evidence: []applicationlifecycle.Evidence{evidence}, Now: result.VerifiedAt})
			return err
		})
	}
	err = withLifecycleMutation(workspace, "setup", execute)
	if err != nil {
		return machineAwareCommandError(cmd, err)
	}
	if options.outputJSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}
	if result.Preparation != "" {
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s setup preparation %q recorded; complete the owner's encrypted account setup in the official client.\nPlan: %s\n", workload, result.Preparation, result.Authority.PlanHash)
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s owner login verified. Onboarding complete: %t\nPlan: %s\n", workload, result.OnboardingComplete, result.Authority.PlanHash)
	return err
}

func beginNativeSetup(store applicationlifecycle.Store, contract applicationlifecycle.Contract, requested string) (string, error) {
	state, err := store.Load(contract)
	if err != nil {
		return "", err
	}
	authority := applicationlifecycle.Authority{PlanHash: contract.PlanHash, LifecycleContractHash: contract.ContractHash, LifecycleVersion: contract.Version, PackageRef: contract.PackageRef}
	for _, operation := range state.Operations {
		if operation.ID != state.CurrentOperation {
			continue
		}
		if operation.Stage == "setup" && operation.OperationRef == "stackkit.setup" && operation.Authority == authority && (requested == "" || requested == operation.ID) {
			if operation.Status == applicationlifecycle.StatusRunning {
				return operation.ID, nil
			}
			if operation.Status == applicationlifecycle.StatusFailed {
				_, err := store.Transition(contract, applicationlifecycle.TransitionRequest{ID: operation.ID, Status: applicationlifecycle.StatusRunning, Now: time.Now().UTC()})
				return operation.ID, err
			}
		}
	}
	if requested == "" {
		requested, err = applicationlifecycle.NewOperationID("stackkit.setup")
		if err != nil {
			return "", err
		}
	}
	_, err = store.Begin(contract, applicationlifecycle.BeginRequest{ID: requested, Stage: "setup", OperationRef: "stackkit.setup", Now: time.Now().UTC()})
	return requested, err
}

func readNativeSetupCredentialJSON(workspace, path string, value any) error {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer transaction.Close()
	if !filepath.IsLocal(path) {
		return errors.New("setup credential file must be workspace-relative")
	}
	if err := backupcustody.RequirePrivatePath(filepath.Join(root.Name(), path), false); err != nil {
		return err
	}
	raw, _, err := transaction.ReadStable(filepath.ToSlash(path))
	if err != nil {
		return err
	}
	defer clear(raw)
	if len(raw) > 64<<10 {
		return errors.New("setup credential file exceeds 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errors.New("setup credential file must be a closed JSON object for the selected application")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("setup credential file has trailing data")
	}
	return nil
}

// nativeAppliedWorkloadDeployment narrows the shared signed-Apply authority to
// one owner-local application bundle. Setup and backup use the same selector.
func nativeAppliedWorkloadDeployment(authority nativeV2AppliedAuthority, workload string) (runtimeexecutorlocal.SelectedPaaSWorkloadDeployment, error) {
	root, err := confinedfs.Open(authority.WorkspaceRoot)
	if err != nil {
		return runtimeexecutorlocal.SelectedPaaSWorkloadDeployment{}, err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return runtimeexecutorlocal.SelectedPaaSWorkloadDeployment{}, err
	}
	defer transaction.Close()
	var selected *runtimeexecutorlocal.SelectedPaaSWorkloadDeployment
	for _, target := range authority.Plan.ApplyRequirements().RuntimeInstances {
		if target.WorkloadRef != workload {
			continue
		}
		if target.RuntimeAdapter == nil || target.RuntimeAdapter.ID != "standalone-compose" {
			return runtimeexecutorlocal.SelectedPaaSWorkloadDeployment{}, errors.New("local application operation requires the explicitly selected standalone-compose adapter")
		}
		if len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || target.SiteRefs[0] != authority.Owner.Binding.SiteRef || target.NodeRefs[0] != authority.Owner.Binding.NodeRef {
			return runtimeexecutorlocal.SelectedPaaSWorkloadDeployment{}, errors.New("native application setup requires the exact local owner-bound placement")
		}
		// The existing verifier has already authenticated the complete Plan,
		// artifact graph and runtime-owner result. Narrow that signed result to
		// this local placement; do not derive an execution channel from a name.
		var applied *architecturev2.AppliedWorkloadIdentity
		for _, candidate := range authority.AppliedWorkloads {
			if candidate.WorkloadRef == workload && candidate.RequirementID == target.ID && candidate.InstanceRef == target.InstanceRef {
				if applied != nil {
					return runtimeexecutorlocal.SelectedPaaSWorkloadDeployment{}, errors.New("application setup has ambiguous applied workload identity")
				}
				applied = &candidate
			}
		}
		if applied == nil || applied.RuntimeOwnerRef != target.RuntimeAdapter.ID || len(applied.Placements) != 1 || applied.Placements[0].SiteRef != authority.Owner.Binding.SiteRef || applied.Placements[0].NodeRef != authority.Owner.Binding.NodeRef || (applied.Placements[0].ExecutionChannelRef != "" && applied.Placements[0].ExecutionChannelRef != authority.Owner.Binding.ChannelRef) {
			return runtimeexecutorlocal.SelectedPaaSWorkloadDeployment{}, errors.New("application setup has no signed Apply for the exact local execution channel")
		}
		if applied.Placements[0].ExecutionChannelRef == "" {
			// A native local host carries its channel in verified Owner custody;
			// external hosts must retain their explicit signed channel instead.
			localHost := false
			for _, host := range authority.Plan.ApplyRequirements().Hosts {
				if host.SiteRef == authority.Owner.Binding.SiteRef && host.NodeRef == authority.Owner.Binding.NodeRef && !host.External {
					localHost = true
				}
			}
			if !localHost {
				return runtimeexecutorlocal.SelectedPaaSWorkloadDeployment{}, errors.New("application setup cannot infer an external execution channel")
			}
		}
		for _, artifact := range authority.Manifest.Artifacts {
			if !slices.Contains(target.ArtifactRefs, artifact.ID) {
				continue
			}
			raw, _, err := transaction.ReadStable(artifact.Path)
			if err != nil {
				return runtimeexecutorlocal.SelectedPaaSWorkloadDeployment{}, err
			}
			digest := sha256.Sum256(raw)
			if "sha256:"+hex.EncodeToString(digest[:]) != artifact.SHA256 {
				return runtimeexecutorlocal.SelectedPaaSWorkloadDeployment{}, errors.New("application setup bundle differs from the admitted generation artifact")
			}
			bundle, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(raw)
			if err != nil {
				continue
			}
			if !slices.Contains(applied.Artifacts, architecturev2.AppliedArtifactIdentity{Ref: artifact.ID, Digest: artifact.SHA256}) {
				return runtimeexecutorlocal.SelectedPaaSWorkloadDeployment{}, errors.New("application setup artifact differs from the signed applied workload")
			}
			if bundle.WorkloadRef != workload || bundle.ModuleRef != target.ModuleRef || bundle.InstanceRef != target.InstanceRef || bundle.EntryComponent != target.UnitRef || bundle.SiteRef != target.SiteRefs[0] || bundle.NodeRef != target.NodeRefs[0] {
				return runtimeexecutorlocal.SelectedPaaSWorkloadDeployment{}, errors.New("application bundle is outside its exact applied workload contract")
			}
			if selected != nil {
				return runtimeexecutorlocal.SelectedPaaSWorkloadDeployment{}, errors.New("application setup has ambiguous local runtime bundles")
			}
			adapter := target.RuntimeAdapter
			selected = &runtimeexecutorlocal.SelectedPaaSWorkloadDeployment{WorkloadRef: workload, ModuleRef: target.ModuleRef, UnitRef: target.UnitRef, Release: bundle.Release, SiteRef: bundle.SiteRef, NodeRef: bundle.NodeRef, InstanceRef: target.InstanceRef,
				ExecutionChannelRef: authority.Owner.Binding.ChannelRef, ArtifactRef: artifact.ID, ArtifactDigest: artifact.SHA256, Bundle: raw, Route: bundle.Route,
				RuntimeAdapter: runtimeexecutor.RuntimeAdapterBinding{ID: adapter.ID, ProviderRef: adapter.ProviderRef, ProviderVersion: adapter.ProviderVersion, ProviderContractHash: adapter.ProviderContractHash, ModuleRef: adapter.ModuleRef, ModuleVersion: adapter.ModuleVersion, ModuleContractHash: adapter.ModuleContractHash},
			}
		}
	}
	if selected == nil {
		return runtimeexecutorlocal.SelectedPaaSWorkloadDeployment{}, errors.New("no admitted local application setup bundle is available")
	}
	return *selected, nil
}
