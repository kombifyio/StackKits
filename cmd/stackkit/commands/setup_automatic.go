package commands

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/appsetup"
	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/stackspecintent"
)

// automaticOwnerSetupTimeout bounds one application's owner setup; it is the
// `stackkit setup` budget for an owner account.
const automaticOwnerSetupTimeout = 3 * time.Minute

// Outcomes of the automatic owner setup (Tier 2) of one workload.
const (
	automaticSetupSucceeded           = "succeeded"
	automaticSetupFailed              = "failed"
	automaticSetupBootstrapDegraded   = applicationlifecycle.StatusBootstrapDegraded
	automaticSetupSkippedCleanRollout = applicationlifecycle.StatusSkippedCleanRollout
	automaticSetupAlreadyDone         = "already-set-up"
	automaticSetupOwnerRequired       = "owner-input-required"
)

const cleanRolloutSkipDetail = "Clean Rollout skipped Tier-2 automatic app personalization; application remains private behind Access Gate for its own first-run wizard"

// automaticOwnerSetupOptions configures Tier-2 automatic app personalization.
type automaticOwnerSetupOptions struct {
	SetupStyleOverride stackspecintent.SetupStyle
	WorkloadFilter     string
	OnlyDegraded       bool
}

// automaticOwnerSetupOutcome is the secret-free result for one workload.
type automaticOwnerSetupOutcome struct {
	WorkloadRef string `json:"workloadRef"`
	ActionRef   string `json:"actionRef,omitempty"`
	Status      string `json:"status"`
	Detail      string `json:"detail,omitempty"`
}

const lifecycleTiersSchemaVersion = "stackkit.lifecycle-tiers/v1"

// lifecycleTiersReport is the secret-free ADR-0055 two-tier rollout summary
// emitted under `data.lifecycle` by `stackkit apply --json`.
type lifecycleTiersReport struct {
	SchemaVersion string               `json:"schemaVersion"`
	SetupStyle    string               `json:"setupStyle"`
	Tier1         lifecycleTier1Report `json:"tier1"`
	Tier2         lifecycleTier2Report `json:"tier2"`
}

type lifecycleTier1Report struct {
	Status string `json:"status"`
}

type lifecycleTier2Report struct {
	Status       string                    `json:"status"`
	Applications []lifecycleTier2AppReport `json:"applications"`
}

type lifecycleTier2AppReport struct {
	Application string `json:"application"`
	Status      string `json:"status"`
	Exposure    string `json:"exposure"`
	RetryAction string `json:"retryAction,omitempty"`
}

func buildLifecycleTiersReport(
	workspace string,
	setupStyleOverride stackspecintent.SetupStyle,
	outcomes []automaticOwnerSetupOutcome,
) *lifecycleTiersReport {
	style := setupStyleOverride
	if style == "" {
		if setup, err := newAutomaticOwnerSetup(context.Background(), workspace); err == nil {
			style = stackspecintent.SetupStyleFromResolvedPlan(setup.plan)
		}
	}
	if style == "" {
		style = stackspecintent.SetupStyleBootstrapped
	}
	apps := make([]lifecycleTier2AppReport, 0, len(outcomes))
	anyDegraded := false
	for _, outcome := range outcomes {
		if strings.TrimSpace(outcome.WorkloadRef) == "" {
			continue
		}
		app := lifecycleTier2AppReport{
			Application: outcome.WorkloadRef,
			Exposure:    "access-gate",
		}
		switch outcome.Status {
		case automaticSetupSucceeded, automaticSetupAlreadyDone:
			app.Status = "passed"
		case automaticSetupSkippedCleanRollout, automaticSetupOwnerRequired:
			app.Status = "setup-pending"
			app.RetryAction = "retry-appsetup"
		case automaticSetupBootstrapDegraded, automaticSetupFailed:
			app.Status = automaticSetupBootstrapDegraded
			app.RetryAction = "retry-appsetup"
			anyDegraded = true
		default:
			app.Status = "setup-pending"
			app.RetryAction = "retry-appsetup"
		}
		apps = append(apps, app)
	}
	tier2Status := "passed"
	if style == stackspecintent.SetupStyleClean {
		tier2Status = automaticSetupSkippedCleanRollout
	} else if anyDegraded {
		tier2Status = automaticSetupBootstrapDegraded
	}
	return &lifecycleTiersReport{
		SchemaVersion: lifecycleTiersSchemaVersion,
		SetupStyle:    string(style),
		Tier1:         lifecycleTier1Report{Status: "passed"},
		Tier2: lifecycleTier2Report{
			Status:       tier2Status,
			Applications: apps,
		},
	}
}

// automaticOwnerSetup realizes Tier-2 optional application personalization
// (owner account / OIDC bootstrap) after mandatory Tier-1 Core rollout and
// safety initialization have converged.
//
// In Clean Rollout (`clean`), Tier-2 personalization is explicitly skipped
// (`skipped-clean-rollout`) while every application remains private behind
// Access Gate so the user can complete each app's own first-run wizard.
//
// In Bootstrapped (`bootstrapped`, default), Tier-2 runs after Tier 1 is
// healthy. Any failure records `bootstrap_degraded` in applicationlifecycle
// without ever failing Core/OpenTofu apply, keeping the application private
// behind Access Gate until retried via `stackkit retry-appsetup [workload]` or
// `stackkit setup <workload> --owner-approve`.
type automaticOwnerSetup struct {
	workspace      string
	owner          localevidence.OwnerProjection
	contracts      []applicationlifecycle.Contract
	plan           resolvedplan.ResolvedPlan
	supports       map[string]appsetup.NativeActionSupport
	setupStyle     stackspecintent.SetupStyle
	workloadFilter string
	onlyDegraded   bool
	execute        func(ctx context.Context, workload string, options nativeSetupOptions) error
}

// runAutomaticOwnerSetup runs Tier-2 app personalization after a converged
// top-level Apply, change-set apply, or Advanced reconcile using the SetupStyle
// persisted in the ResolvedPlan (defaulting to Bootstrapped).
func runAutomaticOwnerSetup(ctx context.Context, workspace string) []automaticOwnerSetupOutcome {
	return runAutomaticOwnerSetupWithOptions(ctx, workspace, automaticOwnerSetupOptions{})
}

// runAutomaticOwnerSetupWithOptions runs Tier-2 app personalization with
// optional SetupStyle or workload filters. Tier-2 failures NEVER fail
// Core/OpenTofu apply: each degraded workload is recorded as
// `bootstrap_degraded` in applicationlifecycle and stays private behind Access
// Gate until retried with `stackkit retry-appsetup [workload]` or `stackkit
// setup <workload> --owner-approve`.
func runAutomaticOwnerSetupWithOptions(ctx context.Context, workspace string, opts automaticOwnerSetupOptions) []automaticOwnerSetupOutcome {
	if strings.TrimSpace(lifecycleJoinOperation) != "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	setup, err := newAutomaticOwnerSetup(ctx, workspace)
	if err != nil {
		rolloutFailure("owner-setup", fmt.Errorf("application owner setup could not start: %w", err))
		return []automaticOwnerSetupOutcome{{Status: automaticSetupBootstrapDegraded, Detail: err.Error()}}
	}
	setup.setupStyle = stackspecintent.SetupStyleFromResolvedPlan(setup.plan)
	if opts.SetupStyleOverride != "" {
		setup.setupStyle = opts.SetupStyleOverride
	}
	setup.workloadFilter = strings.TrimSpace(opts.WorkloadFilter)
	setup.onlyDegraded = opts.OnlyDegraded
	if !setup.declared() {
		return nil
	}
	if setup.setupStyle == stackspecintent.SetupStyleClean {
		rolloutEvent("owner-setup", "skipped", "Clean Rollout skipped Tier-2 automatic app personalization; apps remain private behind Access Gate", map[string]string{
			"setupStyle": string(stackspecintent.SetupStyleClean),
			"outcome":    automaticSetupSkippedCleanRollout,
		})
		outcomes := setup.run(ctx)
		skipped := 0
		for _, outcome := range outcomes {
			attributes := map[string]string{
				"workloadRef": outcome.WorkloadRef,
				"actionRef":   outcome.ActionRef,
				"outcome":     outcome.Status,
				"setupStyle":  string(stackspecintent.SetupStyleClean),
			}
			status := "succeeded"
			if outcome.Status == automaticSetupSkippedCleanRollout {
				status = "skipped"
				skipped++
			}
			rolloutEvent("owner-setup.workload", status, outcome.WorkloadRef+" "+outcome.Status, attributes)
		}
		printInfo(
			"Clean Rollout: Tier-1 core safety init completed; skipped Tier-2 automatic app personalization for %d app(s). Apps remain private behind Access Gate for their own first-run wizards (or run `stackkit setup <workload> --owner-approve`).",
			skipped,
		)
		return outcomes
	}

	rolloutEvent("owner-setup", "started", "setting up application owners", map[string]string{
		"setupStyle": string(setup.setupStyle),
	})
	outcomes := setup.run(ctx)
	degraded := 0
	for _, outcome := range outcomes {
		attributes := map[string]string{
			"workloadRef": outcome.WorkloadRef,
			"actionRef":   outcome.ActionRef,
			"outcome":     outcome.Status,
			"setupStyle":  string(setup.setupStyle),
		}
		status := "succeeded"
		if outcome.Status == automaticSetupBootstrapDegraded || outcome.Status == automaticSetupFailed {
			status = "degraded"
			attributes["state"] = automaticSetupBootstrapDegraded
			degraded++
			printWarning(
				"%s Tier-2 app setup degraded (%s): %s; application remains private behind Access Gate. Retry with `stackkit retry-appsetup %s` or `stackkit setup %s --owner-approve`, or complete its first-run wizard behind Access Gate",
				outcome.WorkloadRef, automaticSetupBootstrapDegraded, outcome.Detail, outcome.WorkloadRef, outcome.WorkloadRef,
			)
		}
		rolloutEvent("owner-setup.workload", status, outcome.WorkloadRef+" "+outcome.Status, attributes)
	}
	if degraded > 0 {
		rolloutEvent("owner-setup", "degraded", fmt.Sprintf("%d application Tier-2 setups degraded; Core/Tier-1 remains healthy", degraded), map[string]string{
			"state":         automaticSetupBootstrapDegraded,
			"degradedCount": strconv.Itoa(degraded),
		})
	} else {
		rolloutEvent("owner-setup", "succeeded", "application owners set up", map[string]string{
			"setupStyle": string(setup.setupStyle),
		})
	}
	return outcomes
}

// newAutomaticOwnerSetup reads the generated Plan and owner custody; each
// setup it runs re-admits the signed Apply through executeNativeSetup.
func newAutomaticOwnerSetup(ctx context.Context, workspace string) (*automaticOwnerSetup, error) {
	authority, err := inspectNativeV2GeneratedAuthority(ctx, workspace, specFile)
	if err != nil {
		return nil, err
	}
	plan, err := resolvedplan.DecodeCanonicalPlan(authority.Plan.Canonical())
	if err != nil {
		return nil, err
	}
	contracts, err := applicationlifecycle.ContractsFromResolvedPlan(plan)
	if err != nil {
		return nil, err
	}
	supports := make(map[string]appsetup.NativeActionSupport, len(contracts))
	for _, contract := range contracts {
		if adapter, ok := nativeApplicationSetupAdapterForWorkload(authority.Plan.ApplyRequirements(), contract.WorkloadRef); ok {
			supports[contract.WorkloadRef] = adapter
		}
	}
	return &automaticOwnerSetup{
		workspace: authority.WorkspaceRoot, owner: authority.Owner.PocketID, contracts: contracts, plan: plan,
		supports: supports,
		execute: func(ctx context.Context, workload string, options nativeSetupOptions) error {
			ctx, cancel := context.WithTimeout(ctx, automaticOwnerSetupTimeout)
			defer cancel()
			_, err := executeNativeSetup(ctx, workspace, workload, options)
			return err
		},
	}, nil
}

// declared reports whether any workload declares a native owner setup.
func (s *automaticOwnerSetup) declared() bool {
	for _, contract := range s.contracts {
		if _, _, ok := s.action(contract); ok {
			return true
		}
	}
	return false
}

func (s *automaticOwnerSetup) action(contract applicationlifecycle.Contract) (string, appsetup.NativeActionDescription, bool) {
	setup := architectureV2SetupInput(s.plan, contract, nil)
	if _, staged := contract.Stages["setup"]; !staged || (setup.Policy != stackspecintent.ApplicationDefaultBootstrapped && setup.Policy != stackspecintent.ApplicationDefaultCleanRollout) || len(setup.ActionRefs) != 1 {
		return "", appsetup.NativeActionDescription{}, false
	}
	description, supported := appsetup.DescribeNativeAction(setup.ActionRefs[0], s.supports[contract.WorkloadRef])
	return setup.ActionRefs[0], description, supported
}

func (s *automaticOwnerSetup) run(ctx context.Context) []automaticOwnerSetupOutcome {
	contracts := append([]applicationlifecycle.Contract(nil), s.contracts...)
	sort.Slice(contracts, func(i, j int) bool { return contracts[i].WorkloadRef < contracts[j].WorkloadRef })
	store := applicationlifecycle.Store{Workspace: s.workspace}
	var outcomes []automaticOwnerSetupOutcome
	degrade := func(contract applicationlifecycle.Contract, action, detail string) automaticOwnerSetupOutcome {
		recordBootstrapDegradedIfMissing(store, contract, detail)
		return automaticOwnerSetupOutcome{
			WorkloadRef: contract.WorkloadRef,
			ActionRef:   action,
			Status:      automaticSetupBootstrapDegraded,
			Detail:      detail,
		}
	}
	for _, contract := range contracts {
		if s.workloadFilter != "" && contract.WorkloadRef != s.workloadFilter {
			continue
		}
		action, description, supported := s.action(contract)
		if !supported {
			continue
		}
		if s.onlyDegraded {
			needsRetry, _, retryErr := store.NeedsAppSetupRetry(contract)
			if retryErr != nil {
				outcomes = append(outcomes, degrade(contract, action, retryErr.Error()))
				continue
			}
			if !needsRetry {
				continue
			}
		}
		outcome := automaticOwnerSetupOutcome{WorkloadRef: contract.WorkloadRef, ActionRef: action}
		done, previouslySetUp, err := automaticSetupCompletion(store, contract)
		if err == nil && done && action == "home-assistant-owner-bootstrap" {
			requested, requestErr := requestedHomeAssistantConnector(s.workspace, description.CredentialsFile)
			if requestErr != nil {
				err = requestErr
			} else if requested {
				done = false
			}
		}
		if err != nil {
			outcomes = append(outcomes, degrade(contract, action, err.Error()))
			continue
		} else if done {
			outcome.Status = automaticSetupAlreadyDone
			outcomes = append(outcomes, outcome)
			continue
		}
		if s.setupStyle == stackspecintent.SetupStyleClean {
			_, _ = store.RecordSkippedCleanRollout(contract, cleanRolloutSkipDetail, time.Now().UTC())
			outcome.Status = automaticSetupSkippedCleanRollout
			outcome.Detail = cleanRolloutSkipDetail
			outcomes = append(outcomes, outcome)
			continue
		}
		if appsetup.CustodiedOwnerLoginAction(action) {
			// The application already bootstrapped from its sealed workload
			// secret. Observe that account through the same setup lifecycle;
			// never generate a replacement password or credential document.
			if err := s.execute(ctx, contract.WorkloadRef, nativeSetupOptions{ownerApproved: true, degradeOnFailure: true}); err != nil {
				outcomes = append(outcomes, degrade(contract, action, err.Error()))
			} else {
				outcome.Status = automaticSetupSucceeded
				outcomes = append(outcomes, outcome)
			}
			continue
		}
		credentials, automatic := automaticOwnerCredentials(action, s.owner)
		if !automatic {
			outcome.Status = automaticSetupOwnerRequired
			outcomes = append(outcomes, outcome)
			continue
		}
		if action == applicationlifecycle.VaultOwnerInviteActionRef {
			if _, err := os.Lstat(filepath.Join(s.workspace, description.CredentialsFile)); errors.Is(err, os.ErrNotExist) {
				outcome.Status = automaticSetupOwnerRequired
				outcome.Detail = fmt.Sprintf("stage the owner SMTP settings in %s before Apply, or run setup after configuring that private file", description.CredentialsFile)
				outcomes = append(outcomes, outcome)
				continue
			} else if err != nil {
				outcomes = append(outcomes, degrade(contract, action, err.Error()))
				continue
			}
		}
		if previouslySetUp {
			// A changed contract needs fresh SSO readback, not a replacement owner.
			if _, err := os.Lstat(filepath.Join(s.workspace, description.CredentialsFile)); err != nil {
				detail := fmt.Sprintf("restore the existing application owner credentials to %s and retry setup; an established owner password is never regenerated", description.CredentialsFile)
				outcomes = append(outcomes, degrade(contract, action, detail))
				continue
			}
		}
		if err := ensureAutomaticSetupCredentials(s.workspace, description.CredentialsFile, credentials); err != nil {
			outcomes = append(outcomes, degrade(contract, action, err.Error()))
			continue
		}
		err = s.execute(ctx, contract.WorkloadRef, nativeSetupOptions{
			credentialsFile:    description.CredentialsFile,
			ownerApproved:      true,
			completeOnboarding: description.SupportsOnboardingCompletion,
			degradeOnFailure:   true,
		})
		if err != nil {
			detail := err.Error()
			if action == "jellyfin-owner-bootstrap" || action == "home-assistant-owner-bootstrap" {
				detail += fmt.Sprintf("; if this application already has an owner, restore that owner’s credentials to %s and retry; setup does not reset existing passwords", description.CredentialsFile)
			}
			outcomes = append(outcomes, degrade(contract, action, detail))
			continue
		}
		outcome.Status = automaticSetupSucceeded
		outcomes = append(outcomes, outcome)
	}
	return outcomes
}

func recordBootstrapDegradedIfMissing(store applicationlifecycle.Store, contract applicationlifecycle.Contract, detail string) {
	if needsRetry, op, err := store.NeedsAppSetupRetry(contract); err == nil && needsRetry && op.Status == applicationlifecycle.StatusBootstrapDegraded {
		return
	}
	_, _ = store.RecordBootstrapDegraded(contract, detail, time.Now().UTC())
}

// Historical setup cannot prove a new Plan's identity/plugin configuration.
func automaticSetupCompletion(store applicationlifecycle.Store, contract applicationlifecycle.Contract) (current, previous bool, err error) {
	state, err := store.Load(contract)
	if err != nil {
		return false, false, err
	}
	authority := applicationlifecycle.Authority{PlanHash: contract.PlanHash, LifecycleContractHash: contract.ContractHash, LifecycleVersion: contract.Version, PackageRef: contract.PackageRef}
	for _, operation := range state.Operations {
		if operation.Stage != "setup" {
			continue
		}
		// A later failed/pending setup invalidates an earlier successful
		// observation; its next retry must not be skipped.
		current = operation.Status == applicationlifecycle.StatusSucceeded && operation.Authority == authority
		previous = previous || operation.Status == applicationlifecycle.StatusSucceeded
	}
	return current, previous, nil
}

// automaticOwnerCredentials derives the closed credential document of one
// owner setup action from the owner custody identity. Only owner-account
// actions qualify; the others need decisions only the owner can make.
func automaticOwnerCredentials(action string, owner localevidence.OwnerProjection) (func() (map[string]any, error), bool) {
	email, username, displayName := strings.TrimSpace(owner.Email), strings.TrimSpace(owner.Username), strings.TrimSpace(owner.DisplayName)
	if displayName == "" {
		displayName = username
	}
	withPassword := func(values map[string]any) func() (map[string]any, error) {
		return func() (map[string]any, error) {
			password, err := automaticOwnerPassword()
			if err != nil {
				return nil, err
			}
			values["password"] = password
			return values, nil
		}
	}
	switch action {
	case "cloudreve-owner-bootstrap":
		// The owner approved the Plan that selects Files, which authorizes
		// its first owner registration.
		return withPassword(map[string]any{"email": email, "language": "en-US", "allowFirstOwnerRegistration": true}), email != ""
	case "immich-owner-bootstrap":
		return withPassword(map[string]any{"email": email, "displayName": displayName}), email != "" && displayName != ""
	case "jellyfin-owner-bootstrap", appsetup.NavidromeOwnerBootstrapAction, appsetup.AudiobookshelfOwnerBootstrapAction:
		return withPassword(map[string]any{"username": username}), username != ""
	case "home-assistant-owner-bootstrap":
		return withPassword(map[string]any{"username": username, "displayName": displayName, "language": "en"}), username != "" && displayName != ""
	case applicationlifecycle.VaultOwnerInviteActionRef:
		// SMTP delivery is an owner-provided external credential. Automatic
		// setup can consume an already staged private document but cannot derive
		// or invent it from the local owner projection.
		return func() (map[string]any, error) { return map[string]any{"email": email}, nil }, email != ""
	case immichAddOnAPIKeyAction:
		// Reuses the Immich owner credentials of the photos owner setup, which
		// runs first; without them the key cannot be issued.
		return func() (map[string]any, error) {
			return nil, errors.New("the photos owner setup has not stored the Immich owner credentials yet")
		}, true
	default:
		return nil, false
	}
}

func automaticOwnerPassword() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate the application owner password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// ensureAutomaticSetupCredentials keeps an existing private credentials file
// (placed by the owner or orchestrator, or by an earlier automatic setup)
// and otherwise writes the derived one owner-only.
func ensureAutomaticSetupCredentials(workspace, relative string, derive func() (map[string]any, error)) error {
	if !filepath.IsLocal(relative) {
		return errors.New("setup credential file must be workspace-relative")
	}
	target := filepath.Join(workspace, relative)
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("setup credential file is not a regular file")
		}
		return backupcustody.RequirePrivatePath(target, false)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	values, err := derive()
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return err
	}
	defer clear(encoded)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return fmt.Errorf("create the private setup directory: %w", err)
	}
	root, err := confinedfs.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	// Validate the plain confined parent before restricting its ACL, and secure
	// it before any secret or atomic staging file is persisted. Unix modes
	// alone do not create an owner-only DACL on Windows.
	if err := backupcustody.ProtectPrivatePath(filepath.Dir(target), true); err != nil {
		return fmt.Errorf("protect the private setup directory: %w", err)
	}
	view, err := root.View(".")
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600(filepath.Base(target), encoded)
	if err != nil {
		return fmt.Errorf("write the private setup credentials: %w", err)
	}
	if !result.Installed || !result.FileSynced {
		return errors.New("private setup credentials were not durably installed")
	}
	return backupcustody.ProtectPrivatePath(target, false)
}
