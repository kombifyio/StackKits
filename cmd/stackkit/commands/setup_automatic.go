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
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/appsetup"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// automaticOwnerSetupTimeout bounds one application's owner setup; it is the
// `stackkit setup` budget for an owner account.
const automaticOwnerSetupTimeout = 3 * time.Minute

// Outcomes of the automatic owner setup of one workload.
const (
	automaticSetupSucceeded     = "succeeded"
	automaticSetupFailed        = "failed"
	automaticSetupAlreadyDone   = "already-set-up"
	automaticSetupOwnerRequired = "owner-input-required"
)

// automaticOwnerSetupOutcome is the secret-free result for one workload.
type automaticOwnerSetupOutcome struct {
	WorkloadRef string
	ActionRef   string
	Status      string
	Detail      string
}

// automaticOwnerSetup realizes the owner account of every applied workload
// whose Plan declares one on-demand native owner setup action, after the
// runtime of the plan converged. It is the product promise that the owner
// is already set up in every tool, on every execution path: standalone
// Apply, the Techstack-managed rollout, change-set apply and Advanced
// reconcile all end here, and each runs the same code as `stackkit setup`
// (executeNativeSetup), recorded in the application lifecycle.
//
// Credentials come from the private credentials file the action's setup
// guide names when the owner or orchestrator placed one before Apply;
// otherwise from the owner custody identity (email, username, display name)
// and a new random password, written owner-only to that same file, so
// `stackkit setup <workload>` repairs with the same account. A workload with
// a succeeded setup is not set up again. Actions that need owner decisions
// the Plan does not hold (the game server EULA and profile, mailbox and mail
// domain) are reported as owner-input-required and stay with `stackkit
// setup`.
type automaticOwnerSetup struct {
	workspace string
	owner     localevidence.OwnerProjection
	contracts []applicationlifecycle.Contract
	plan      resolvedplan.ResolvedPlan
	execute   func(ctx context.Context, workload string, options nativeSetupOptions) error
}

// runAutomaticOwnerSetup runs after a converged top-level Apply, change-set
// apply or Advanced reconcile. A joined child (the target apply of a change
// set) leaves the setup to its parent, which holds the lifecycle mutation.
// Failures never undo the converged runtime: each is reported as an
// `owner-setup` rollout event, recorded as a failed setup operation, and
// retried by the next Apply or by `stackkit setup <workload>`.
func runAutomaticOwnerSetup(ctx context.Context, workspace string) []automaticOwnerSetupOutcome {
	if strings.TrimSpace(lifecycleJoinOperation) != "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	setup, err := newAutomaticOwnerSetup(ctx, workspace)
	if err != nil {
		rolloutFailure("owner-setup", fmt.Errorf("application owner setup could not start: %w", err))
		return []automaticOwnerSetupOutcome{{Status: automaticSetupFailed, Detail: err.Error()}}
	}
	if !setup.declared() {
		return nil
	}
	rolloutEvent("owner-setup", "started", "setting up application owners", nil)
	outcomes := setup.run(ctx)
	failed := 0
	for _, outcome := range outcomes {
		attributes := map[string]string{"workloadRef": outcome.WorkloadRef, "actionRef": outcome.ActionRef, "outcome": outcome.Status}
		status := "succeeded"
		if outcome.Status == automaticSetupFailed {
			status = "failed"
			failed++
			printWarning("%s owner setup did not complete: %s; retry with `stackkit setup %s --owner-approve`", outcome.WorkloadRef, outcome.Detail, outcome.WorkloadRef)
		}
		rolloutEvent("owner-setup.workload", status, outcome.WorkloadRef+" "+outcome.Status, attributes)
	}
	if failed > 0 {
		rolloutEvent("owner-setup", "failed", fmt.Sprintf("%d application owner setups did not complete", failed), nil)
	} else {
		rolloutEvent("owner-setup", "succeeded", "application owners set up", nil)
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
	return &automaticOwnerSetup{
		workspace: authority.WorkspaceRoot, owner: authority.Owner.PocketID, contracts: contracts, plan: plan,
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
	if _, staged := contract.Stages["setup"]; !staged || setup.Policy != "on-demand" || len(setup.ActionRefs) != 1 {
		return "", appsetup.NativeActionDescription{}, false
	}
	description, supported := appsetup.DescribeNativeAction(setup.ActionRefs[0], contract.Delivery.AdapterRef)
	return setup.ActionRefs[0], description, supported
}

func (s *automaticOwnerSetup) run(ctx context.Context) []automaticOwnerSetupOutcome {
	contracts := append([]applicationlifecycle.Contract(nil), s.contracts...)
	sort.Slice(contracts, func(i, j int) bool { return contracts[i].WorkloadRef < contracts[j].WorkloadRef })
	store := applicationlifecycle.Store{Workspace: s.workspace}
	var outcomes []automaticOwnerSetupOutcome
	for _, contract := range contracts {
		action, description, supported := s.action(contract)
		if !supported {
			continue
		}
		outcome := automaticOwnerSetupOutcome{WorkloadRef: contract.WorkloadRef, ActionRef: action}
		if done, err := hasSucceededSetup(store, contract); err != nil {
			outcome.Status, outcome.Detail = automaticSetupFailed, err.Error()
			outcomes = append(outcomes, outcome)
			continue
		} else if done {
			outcome.Status = automaticSetupAlreadyDone
			outcomes = append(outcomes, outcome)
			continue
		}
		credentials, automatic := automaticOwnerCredentials(action, s.owner)
		if !automatic {
			outcome.Status = automaticSetupOwnerRequired
			outcomes = append(outcomes, outcome)
			continue
		}
		if err := ensureAutomaticSetupCredentials(s.workspace, description.CredentialsFile, credentials); err != nil {
			outcome.Status, outcome.Detail = automaticSetupFailed, err.Error()
			outcomes = append(outcomes, outcome)
			continue
		}
		err := s.execute(ctx, contract.WorkloadRef, nativeSetupOptions{
			credentialsFile: description.CredentialsFile, ownerApproved: true,
			completeOnboarding: description.SupportsOnboardingCompletion,
		})
		if err != nil {
			outcome.Status, outcome.Detail = automaticSetupFailed, err.Error()
		} else {
			outcome.Status = automaticSetupSucceeded
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes
}

func hasSucceededSetup(store applicationlifecycle.Store, contract applicationlifecycle.Contract) (bool, error) {
	state, err := store.Load(contract)
	if err != nil {
		return false, err
	}
	for _, operation := range state.Operations {
		if operation.Stage == "setup" && operation.Status == applicationlifecycle.StatusSucceeded {
			return true, nil
		}
	}
	return false, nil
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
	case "jellyfin-owner-bootstrap":
		return withPassword(map[string]any{"username": username}), username != ""
	case "home-assistant-owner-bootstrap":
		return withPassword(map[string]any{"username": username, "displayName": displayName, "language": "en"}), username != "" && displayName != ""
	case applicationlifecycle.VaultOwnerInviteActionRef:
		return func() (map[string]any, error) { return map[string]any{"email": email}, nil }, email != ""
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
		return nil
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
	return nil
}
