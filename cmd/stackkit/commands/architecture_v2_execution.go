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
	"time"

	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/applyledger"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/hostpreflight"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeapplyv2"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorprocess"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
	"github.com/kombifyio/stackkits/internal/runtimeobservation"
	"github.com/kombifyio/stackkits/internal/servicecontrol"
	"github.com/kombifyio/stackkits/internal/stackspecadmission"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
	"github.com/kombifyio/stackkits/internal/workloadremoval"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type architectureV2ExecutionMode string

const (
	architectureV2Generate architectureV2ExecutionMode = "generate"
	architectureV2Plan     architectureV2ExecutionMode = "plan"
	architectureV2Apply    architectureV2ExecutionMode = "apply"
	architectureV2Verify   architectureV2ExecutionMode = "verify"
	architectureV2Prepare  architectureV2ExecutionMode = "prepare"
	architectureV2Remove   architectureV2ExecutionMode = "remove"
	architectureV2Upgrade  architectureV2ExecutionMode = "upgrade"
	architectureV2Cluster  architectureV2ExecutionMode = "cluster join-token"
	architectureV2Status   architectureV2ExecutionMode = "status"
	architectureV2Validate architectureV2ExecutionMode = "validate"
	architectureV2AppAdd   architectureV2ExecutionMode = "app add"
	architectureV2AddonAdd architectureV2ExecutionMode = "addon add"
	architectureV2AddonRm  architectureV2ExecutionMode = "addon remove"
	architectureV2AddonLs  architectureV2ExecutionMode = "addon list"
)

type architectureV2ExecutionCLIOptions struct {
	inventoryPath       string
	planPath            string
	manifestPath        string
	stackSpecData       []byte
	inventoryData       []byte
	receiptPath         string
	expectedPlanHash    string
	localSiteRef        string
	localNodeRef        string
	localChannelRef     string
	outputRoot          string
	fragments           bool
	force               bool
	context             context.Context
	planOut             string
	planDestroy         bool
	inspectionSink      func(generationartifact.PlanInspection) error
	verifiedPlanSink    func(generationartifact.VerifiedPlan) error
	applySink           func(architectureV2ApplyCommandResult) error
	verifySink          func(architectureV2VerifyReport) error
	verifyOffline       bool
	driftObservation    bool
	legacyPlanFile      string
	workloadRef         string
	removalJSON         bool
	removalSink         func(workloadremoval.Result) error
	removalEvidenceJSON bool
	removalEvidenceSink func(workloadremoval.Evidence) error
	preflightPolicy     string
}

type architectureV2ExecutionAuthority interface {
	ResolveCurrent(architecturev2.ResolveInput) (architecturev2.CurrentResolution, error)
	AuthorizeGeneration(architecturev2.GenerationAuthorizationInput) (architecturev2.GenerationAuthorization, error)
	VerifyCanonicalPlan([]byte) (generationartifact.VerifiedPlan, error)
	ReadCanonicalPlan(string) (generationartifact.VerifiedPlan, error)
	PersistCanonicalPlan(string, []byte) (generationartifact.VerifiedPlan, error)
}

type architectureV2ProductApplyAuthority interface {
	architectureV2ExecutionAuthority
	ExecuteProductApply(context.Context, architecturev2.ProductApplyInput) (architecturev2.VerifiedApplyResult, error)
	ReconcileProductApply(context.Context, architecturev2.ProductApplyReconcileInput) (architecturev2.VerifiedApplyResult, error)
}

type architectureV2ProductVerifyAuthority interface {
	VerifyProductApplyResult(architecturev2.ProductApplyResultVerificationInput) (architecturev2.VerifiedApplyResult, error)
}

type architectureV2AppliedRuntimeCustody interface {
	LoadAppliedRuntimeRequest(context.Context, string) (runtimeexecutor.ExecutionRequest, error)
}

type architectureV2ExecutionGate struct {
	newAuthority       func() (architectureV2ExecutionAuthority, error)
	newApplyAuthority  func(string, architectureV2ExecutionCLIOptions) (architectureV2ExecutionAuthority, error)
	newVerifyAuthority func(string, architectureV2ExecutionCLIOptions) (architectureV2ExecutionAuthority, error)
	newRegistry        func() (*architecturev2renderer.Registry, error)
	versions           generationartifact.ComponentVersions
	rejectV1           bool
	now                func() time.Time
}

func newArchitectureV2ExecutionGate() architectureV2ExecutionGate {
	componentVersion := architectureV2ComponentVersion(version)
	return architectureV2ExecutionGate{
		newAuthority: func() (architectureV2ExecutionAuthority, error) {
			return architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
		},
		newApplyAuthority:  newArchitectureV2ProductRuntimeAuthority,
		newVerifyAuthority: newArchitectureV2ProductVerifyAuthority,
		newRegistry:        architecturev2renderer.NewProductRegistry,
		versions: generationartifact.ComponentVersions{
			CLI:       componentVersion,
			Generator: componentVersion,
			Runtime:   componentVersion,
		},
		rejectV1: architectureV2RejectsV1Execution(version),
		now:      time.Now,
	}
}

// architectureV2ComponentVersion models a development build explicitly as a
// SemVer pre-release. It intentionally remains below the 0.6.0 release
// minimum; tests and release builds provide their actual component version.
func architectureV2ComponentVersion(buildVersion string) string {
	normalized := strings.TrimSpace(buildVersion)
	normalized = strings.TrimPrefix(normalized, "v")
	if normalized == "dev" || normalized == "" {
		return "0.6.0-dev"
	}
	return normalized
}

// preflight preserves the v0.6 compatibility executor only for that explicitly
// versioned M release. From v0.7/M+1 onward every classified v1 document is
// handled at the migration boundary and cannot fall through to a legacy
// generator or executor. v2 always continues through the governed path.
func (g architectureV2ExecutionGate) preflight(wd, requestedSpecPath string, mode architectureV2ExecutionMode, options architectureV2ExecutionCLIOptions) (bool, error) {
	rawSpec, sourceVersion, handled, err := classifyArchitectureV2ExecutionSpec(wd, requestedSpecPath)
	if err != nil || !handled {
		return handled, err
	}
	if sourceVersion == stackspecmigration.SourceVersionV1 {
		if !g.rejectV1 {
			return false, nil
		}
		return true, g.rejectV1Execution(rawSpec, mode)
	}
	return true, g.preflightV2(wd, rawSpec, mode, options)
}

// architectureV2RejectsV1Execution makes ADR-0029's M+1 removal explicit.
// v0.6 remains the sole compatibility minor because its first-party init and
// mutation commands still write v1. v0.7+ may read v1 for migration, but raw
// v1 cannot enter generation or runtime execution.
func architectureV2RejectsV1Execution(buildVersion string) bool {
	return stackspecadmission.RejectOperationalV1(buildVersion)
}

// admitCommandBeforeDeployObservability is the lightweight root boundary for
// commands whose real versioned preflight lives in RunE. It classifies intent
// only; CUE resolution, plan/artifact verification, and execution remain owned
// by the command after logging starts.
func admitCommandBeforeDeployObservability(cmd *cobra.Command) error {
	if cmd == nil || commandDisablesDeployObservability(cmd) {
		return nil
	}
	switch cmd {
	case generateCmd:
		if err := admitLifecycleMutationBeforeObservability(
			getWorkDir(), "generate", true,
		); err != nil {
			return err
		}
	case verifyCmd:
		if err := admitLifecycleMutationBeforeObservability(
			getWorkDir(), "verify", false,
		); err != nil {
			return err
		}
	}
	// Current-source generation is native-v2-only for every build identity.
	// Immutable v0.6 release artifacts retain their historical implementation,
	// but rebuilding current source with an old version string must not restore
	// the retired v1 generator or create lifecycle state before denying it.
	if !architectureV2RejectsV1Execution(version) && cmd != generateCmd {
		return nil
	}
	if cmd == verifyCmd && verifyOffline {
		return nil
	}
	for current := cmd; current != nil; current = current.Parent() {
		if operation := strings.TrimSpace(current.Annotations[legacyV06BeforeObservabilityAnnotation]); operation != "" {
			return requireLegacyV06Command(operation, "this command still depends on exact-v0.6 operational artifacts and has no governed Architecture v2 implementation")
		}
	}
	mode, native := map[*cobra.Command]architectureV2ExecutionMode{
		generateCmd: architectureV2Generate,
		planCmd:     architectureV2Plan,
		validateCmd: architectureV2Validate,
		verifyCmd:   architectureV2Verify,
		prepareCmd:  architectureV2Prepare,
		removeCmd:   architectureV2Remove,
	}[cmd]
	if !native {
		return nil
	}
	if err := requireNativeV2StackSpec(getWorkDir(), specFile, mode); err != nil {
		return err
	}
	rawSpec, sourceVersion, handled, err := classifyArchitectureV2ExecutionSpec(getWorkDir(), specFile)
	if err != nil {
		return err
	}
	if !handled {
		return fmt.Errorf("%s: required local StackSpec could not be classified before deploy observability", mode)
	}
	if sourceVersion == stackspecmigration.SourceVersionV1 {
		return newArchitectureV2ExecutionGate().rejectV1Execution(rawSpec, mode)
	}
	if sourceVersion != stackspecmigration.SourceVersionV2Alpha1 {
		return fmt.Errorf("%s: required local StackSpec has unsupported version %q", mode, sourceVersion)
	}
	if mode == architectureV2Prepare {
		return fmt.Errorf("prepare: canonical StackSpec v2 has no governed host-preparation implementation; use external host admission/conformance and the resolved execution channel")
	}
	return nil
}

// requireNativeV2StackSpec prevents native-line commands from interpreting a
// missing intent document as permission to enter a legacy default, host
// preparation, or IaC path. Exact v0.6 builds retain their bounded
// compatibility behavior; v0.7+ must begin from an explicitly initialized v2
// StackSpec (or, for managed apply, from the separately verified fetch flow).
func requireNativeV2StackSpec(wd, requestedSpecPath string, mode architectureV2ExecutionMode) error {
	if !architectureV2RejectsV1Execution(version) {
		return nil
	}
	loader := config.NewLoader(wd)
	resolvedPath, displayPath, _, err := loader.ResolveStackSpecPathForRead(requestedSpecPath)
	if err != nil {
		return fmt.Errorf("%s: resolve required StackSpec v2: %w", mode, err)
	}
	info, err := os.Stat(resolvedPath)
	if err == nil {
		if info.IsDir() {
			return fmt.Errorf("%s: required StackSpec v2 path %s is a directory", mode, displayPath)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("%s: inspect required StackSpec v2 %s: %w", mode, displayPath, err)
	}
	return fmt.Errorf(
		"%s: canonical StackSpec v2 is required on the v0.7 line; %s is missing and implicit legacy defaults are disabled (run stackkit init, then retry)",
		mode,
		displayPath,
	)
}

// admitApplyBeforeDeployObservability classifies local intent before the root
// command creates deploy logs, rollout receipts, or telemetry.
func admitApplyBeforeDeployObservability(wd, requestedSpecPath string) error {
	if err := admitLifecycleMutationBeforeObservability(
		wd, "apply", true,
	); err != nil {
		return err
	}
	if !architectureV2RejectsV1Execution(version) {
		return nil
	}
	if err := requireNativeV2StackSpec(wd, requestedSpecPath, architectureV2Apply); err != nil {
		return err
	}

	rawSpec, sourceVersion, handled, err := classifyArchitectureV2ExecutionSpec(wd, requestedSpecPath)
	if err != nil {
		return err
	}
	if !handled {
		return fmt.Errorf("apply: required local StackSpec could not be classified before deploy observability")
	}
	if sourceVersion == stackspecmigration.SourceVersionV1 {
		return newArchitectureV2ExecutionGate().rejectV1Execution(rawSpec, architectureV2Apply)
	}
	if sourceVersion != stackspecmigration.SourceVersionV2Alpha1 {
		return fmt.Errorf("apply: required local StackSpec has unsupported version %q", sourceVersion)
	}
	return nil
}

func classifyArchitectureV2ExecutionSpec(wd, requestedSpecPath string) ([]byte, stackspecmigration.SourceVersion, bool, error) {
	loader := config.NewLoader(wd)
	specPath, _, _, err := loader.ResolveStackSpecPathForRead(requestedSpecPath)
	if err != nil {
		return nil, "", false, nil // Preserve the legacy loader's existing diagnostic.
	}
	rawSpec, err := os.ReadFile(specPath)
	if err != nil {
		return nil, "", false, nil // Preserve the legacy loader's existing diagnostic.
	}

	document, readErr := stackspecmigration.Read(rawSpec)
	if readErr != nil {
		if claimsNonLegacyAPIVersion(rawSpec) {
			return nil, "", true, fmt.Errorf("architecture v2 execution classification: %w", readErr)
		}
		return nil, "", true, fmt.Errorf("StackSpec execution classification: %w", readErr)
	}
	if document.Version == stackspecmigration.SourceVersionV1 {
		return rawSpec, document.Version, true, nil
	}
	if document.Version != stackspecmigration.SourceVersionV2Alpha1 || document.V2 == nil {
		return nil, "", true, fmt.Errorf("architecture v2 execution classification returned no canonical v2 identity")
	}
	return rawSpec, document.Version, true, nil
}

func (g architectureV2ExecutionGate) rejectV1Execution(rawSpec []byte, mode architectureV2ExecutionMode) error {
	if g.newAuthority == nil {
		return fmt.Errorf("architecture v2 execution authority is not configured")
	}
	authority, err := g.newAuthority()
	if err != nil {
		return err
	}
	_, err = authority.ResolveCurrent(architecturev2.ResolveInput{StackSpec: rawSpec})
	if err == nil {
		return fmt.Errorf("StackSpec v1 unexpectedly resolved for %s execution; refusing legacy fallback", mode)
	}
	var migrationErr *architecturev2.ResolveError
	if !errors.As(err, &migrationErr) || (migrationErr.Code != architecturev2.ErrMigrationRequired && migrationErr.Code != architecturev2.ErrMigrationBlocked) {
		return err
	}
	return &architecturev2.ResolveError{
		Code: migrationErr.Code,
		Message: fmt.Sprintf(
			"StackSpec v1 is readable only through the migration adapter and cannot enter %s; persist a completed v2 StackSpec with stackkit migrate --complete-with <explicit-v2> --spec-output <stack-spec-v2.json>, then retry with --spec <stack-spec-v2.json>",
			mode,
		),
		Report: migrationErr.Report,
		Cause:  migrationErr.Cause,
	}
}

// loadLegacyOperationalStackSpec is the bounded v0.6-only bridge for commands
// that have not yet acquired a governed Architecture-v2 implementation. It
// classifies raw bytes before models.StackSpec decoding, rejects v1 at M+1,
// and never lets a canonical v2 document fall through the lossy legacy model.
func loadLegacyOperationalStackSpec(wd, requestedSpecPath string, mode architectureV2ExecutionMode) (*models.StackSpec, error) {
	loader := config.NewLoader(wd)
	loaded, err := loader.ReadStackSpecDocument(requestedSpecPath)
	if err != nil {
		return nil, err
	}
	switch loaded.Document.Version {
	case stackspecmigration.SourceVersionV1:
		gate := newArchitectureV2ExecutionGate()
		if gate.rejectV1 {
			return nil, gate.rejectV1Execution(loaded.Document.Raw, mode)
		}
		return loader.LoadLegacyStackSpec(requestedSpecPath)
	case stackspecmigration.SourceVersionV2Alpha1:
		return nil, fmt.Errorf(
			"%s: canonical StackSpec v2 cannot use the legacy %s implementation; a governed ResolvedPlan-based path is required",
			mode,
			mode,
		)
	default:
		return nil, fmt.Errorf("%s: unsupported classified StackSpec version %q", mode, loaded.Document.Version)
	}
}

func (g architectureV2ExecutionGate) preflightV2(wd string, rawSpec []byte, mode architectureV2ExecutionMode, options architectureV2ExecutionCLIOptions) (returnErr error) {
	inventory, err := readArchitectureV2Inventory(wd, options.inventoryPath)
	if err != nil {
		return err
	}
	options.inventoryData = append([]byte(nil), inventory...)
	options.stackSpecData = append([]byte(nil), rawSpec...)
	authority, err := g.openV2Authority(wd, mode, options)
	if err != nil {
		return err
	}
	if closer, ok := authority.(interface{ Close() error }); ok {
		defer func() { returnErr = errors.Join(returnErr, closer.Close()) }()
	}
	currentResolution, err := authority.ResolveCurrent(architecturev2.ResolveInput{StackSpec: rawSpec, Inventory: inventory})
	if err != nil {
		return err
	}
	resolved, err := currentResolution.Result()
	if err != nil {
		return err
	}
	current, err := authority.VerifyCanonicalPlan(resolved.CanonicalPlan)
	if err != nil {
		return err
	}
	defaultPlanPath, defaultManifestPath, defaultReceiptPath := current.MetadataPaths(wd)
	planPath := architectureV2MetadataPath(wd, options.planPath, defaultPlanPath)
	if mode == architectureV2Generate {
		planPath, err = architectureV2CanonicalMetadataPath(wd, options.planPath, defaultPlanPath, "resolved plan")
		if err != nil {
			return err
		}
		if err := validateArchitectureV2GenerateOptions(wd, options, current.OutputRoot()); err != nil {
			return err
		}
		if _, err := authority.PersistCanonicalPlan(planPath, resolved.CanonicalPlan); err != nil {
			return fmt.Errorf("persist current canonical ResolvedPlan for generation: %w", err)
		}
	}
	execute := func(transaction *confinedfs.Transaction, outputLock *confinedfs.OutputLock) error {
		persisted, err := authority.ReadCanonicalPlan(planPath)
		if err != nil {
			return err
		}
		if err := persisted.VerifyCurrentResolution(resolved.CanonicalPlan); err != nil {
			return err
		}
		if err := persisted.VerifyCompatibility(g.versions); err != nil {
			return err
		}
		// Mutating modes reach execute only while the lifecycle and exact
		// output-root locks are held. Keep the caller's admitted plan identity
		// at this boundary so PLAN -> APPLY cannot mutate a newly resolved plan
		// after an orchestrator approved an older one.
		if mode == architectureV2Apply {
			if err := persisted.RequireExpectedPlanHash(options.expectedPlanHash); err != nil {
				return err
			}
		}
		if mode == architectureV2Plan {
			if err := validateArchitectureV2PlanOptions(options); err != nil {
				return err
			}
		}
		if mode == architectureV2Apply || mode == architectureV2Remove {
			if err := validateArchitectureV2ApplyOptions(options); err != nil {
				return err
			}
		}
		if mode == architectureV2Apply {
			now := time.Now
			if g.now != nil {
				now = g.now
			}
			canonicalPlan, err := resolvedplan.DecodeCanonicalPlan(persisted.Canonical())
			if err != nil {
				return fmt.Errorf("decode verified canonical plan for external host freshness: %w", err)
			}
			if err := resolvedplan.ValidateHostConformanceReceiptsForApply(canonicalPlan, now().UTC()); err != nil {
				return err
			}
			// Admit the host before anything is mutated. This runs inside the
			// lifecycle and output locks but ahead of every executor, so a
			// device that cannot run the kit is refused while the workspace is
			// still untouched.
			if err := admitApplyHost(
				options, canonicalPlanKitSlug(canonicalPlan), wd,
				persisted.ApplyRequirements(), persisted.Binding().PlanHash, now().UTC(),
			); err != nil {
				return err
			}
		}
		phase := architectureV2ReadinessPhase(mode)
		if err := persisted.RequireReady(phase); err != nil {
			return err
		}
		return g.continueV2Execution(wd, mode, options, authority, currentResolution, persisted, resolved.CanonicalPlan, defaultManifestPath, defaultReceiptPath, transaction, outputLock)
	}
	if mode == architectureV2Generate {
		return withLifecycleMutation(wd, "generate", func() error {
			return execute(nil, nil)
		})
	}
	if mode == architectureV2Verify {
		return withLifecycleJoinIfPresent(wd, "verify", func() error {
			return withArchitectureV2ReadOnlyOutput(
				wd, current.OutputRoot(), func() error { return execute(nil, nil) },
			)
		})
	}
	if mode == architectureV2Plan {
		return withArchitectureV2ReadOnlyOutput(wd, current.OutputRoot(), func() error { return execute(nil, nil) })
	}
	operation := "apply"
	if mode == architectureV2Remove {
		operation = "remove"
	}
	return withLifecycleMutation(wd, operation, func() error {
		return withArchitectureV2OutputLock(wd, current.OutputRoot(), func(transaction *confinedfs.Transaction, outputLock *confinedfs.OutputLock) error {
			if err := architecturev2.RequireNoPendingOutputTransaction(transaction, current.OutputRoot()); err != nil {
				return err
			}
			return execute(transaction, outputLock)
		})
	})
}

func (g architectureV2ExecutionGate) openV2Authority(wd string, mode architectureV2ExecutionMode, options architectureV2ExecutionCLIOptions) (architectureV2ExecutionAuthority, error) {
	newAuthority := g.newAuthority
	if (mode == architectureV2Apply || mode == architectureV2Remove) && g.newApplyAuthority != nil {
		return g.newApplyAuthority(wd, options)
	}
	if newAuthority == nil {
		return nil, fmt.Errorf("architecture v2 execution authority is not configured")
	}
	return newAuthority()
}

// withArchitectureV2ReadOnlyOutput rejects an incomplete output transaction
// without creating a lock or any other workspace entry. A concurrent atomic
// generation swap can make the subsequent closed-tree verification fail, but
// cannot make inspection authorize or report unverified bytes.
func withArchitectureV2ReadOnlyOutput(wd, outputRoot string, execute func() error) (returnErr error) {
	root, err := confinedfs.Open(wd)
	if err != nil {
		return &architecturev2renderer.Error{Code: architecturev2renderer.ErrOutputTransaction, Path: wd, Message: "open held workspace for architecture v2 read-only inspection", Err: err}
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return &architecturev2renderer.Error{Code: architecturev2renderer.ErrOutputTransaction, Path: wd, Message: "begin held workspace transaction for architecture v2 read-only inspection", Err: err}
	}
	defer func() { returnErr = errors.Join(returnErr, transaction.Close()) }()
	if err := architecturev2.RequireNoPendingOutputTransaction(transaction, outputRoot); err != nil {
		return err
	}
	if execute == nil {
		return &architecturev2renderer.Error{Code: architecturev2renderer.ErrOutputTransaction, Path: outputRoot, Message: "architecture v2 read-only inspection callback is required"}
	}
	return execute()
}

// withArchitectureV2OutputLock serializes mutating executor/verifier handoff
// against generation for the same governed output root. The held-root lock is
// deliberately nonblocking so an operator receives an immediate diagnostic.
func withArchitectureV2OutputLock(wd, outputRoot string, execute func(*confinedfs.Transaction, *confinedfs.OutputLock) error) (returnErr error) {
	root, err := confinedfs.Open(wd)
	if err != nil {
		return &architecturev2renderer.Error{Code: architecturev2renderer.ErrOutputTransaction, Path: wd, Message: "open held workspace for architecture v2 output lock", Err: err}
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return &architecturev2renderer.Error{Code: architecturev2renderer.ErrOutputTransaction, Path: wd, Message: "begin held workspace transaction for architecture v2 output lock", Err: err}
	}
	defer func() { returnErr = errors.Join(returnErr, transaction.Close()) }()
	lock, err := transaction.TryAcquireOutputLock(outputRoot)
	if err != nil {
		code := architecturev2renderer.ErrOutputTransaction
		message := "acquire architecture v2 output transaction lock"
		if errors.Is(err, confinedfs.ErrOutputLockBusy) {
			code = architecturev2renderer.ErrOutputBusy
			message = "another process owns the architecture v2 output transaction"
		}
		return &architecturev2renderer.Error{Code: code, Path: filepath.Join(wd, filepath.FromSlash(outputRoot)), Message: message, Err: err}
	}
	defer func() { returnErr = errors.Join(returnErr, lock.Release()) }()
	if execute == nil {
		return &architecturev2renderer.Error{Code: architecturev2renderer.ErrOutputTransaction, Path: outputRoot, Message: "architecture v2 output transaction callback is required"}
	}
	return execute(transaction, lock)
}

func architectureV2ReadinessPhase(mode architectureV2ExecutionMode) generationartifact.ExecutionPhase {
	if mode == architectureV2Apply || mode == architectureV2Remove {
		return generationartifact.ExecutionPhaseApply
	}
	return generationartifact.ExecutionPhaseGeneration
}

func (g architectureV2ExecutionGate) continueV2Execution(wd string, mode architectureV2ExecutionMode, options architectureV2ExecutionCLIOptions, authority architectureV2ExecutionAuthority, current architecturev2.CurrentResolution, persisted generationartifact.VerifiedPlan, currentCanonical []byte, defaultManifestPath, defaultReceiptPath string, transaction *confinedfs.Transaction, outputLock *confinedfs.OutputLock) error {
	switch mode {
	case architectureV2Generate:
		return g.generateV2(wd, options.context, authority, current)
	case architectureV2Plan, architectureV2Apply, architectureV2Verify, architectureV2Remove:
		return g.verifyV2Generation(wd, mode, options, authority, current, persisted, currentCanonical, defaultManifestPath, defaultReceiptPath, transaction, outputLock)
	default:
		return fmt.Errorf("unsupported architecture v2 execution mode %q", mode)
	}
}

func (g architectureV2ExecutionGate) generateV2(wd string, renderContext context.Context, authority architectureV2ExecutionAuthority, current architecturev2.CurrentResolution) (returnErr error) {
	if g.newRegistry == nil {
		return fmt.Errorf("architecture v2 renderer registry is not configured")
	}
	workspaceRoot, err := filepath.Abs(wd)
	if err != nil {
		return fmt.Errorf("resolve architecture v2 generation workspace: %w", err)
	}
	workspaceRoot = filepath.Clean(workspaceRoot)
	authorization, err := authority.AuthorizeGeneration(architecturev2.GenerationAuthorizationInput{
		Current:       current,
		WorkspaceRoot: workspaceRoot,
		Versions:      g.versions,
	})
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, authorization.Close())
	}()

	registry, err := g.newRegistry()
	if err != nil {
		return err
	}
	now := time.Now
	if g.now != nil {
		now = g.now
	}
	if renderContext == nil {
		renderContext = context.Background()
	}
	_, err = authorization.RenderAndInstall(renderContext, registry, architecturev2renderer.InstallOptions{
		WorkspaceRoot: workspaceRoot,
		GeneratedAt:   now().UTC().Format(time.RFC3339Nano),
	})
	return err
}

func validateArchitectureV2GenerateOptions(wd string, options architectureV2ExecutionCLIOptions, governedOutputRoot string) error {
	if options.fragments {
		return fmt.Errorf("architecture v2 generation strategy is owned by ResolvedPlan; --fragments is not accepted")
	}
	if options.force {
		return fmt.Errorf("architecture v2 generation replaces its governed output root transactionally; --force is not accepted")
	}
	if strings.TrimSpace(options.outputRoot) == "" {
		return nil
	}
	requested, err := filepath.Abs(resolvePathFromWorkDir(wd, options.outputRoot))
	if err != nil {
		return fmt.Errorf("resolve requested architecture v2 output root: %w", err)
	}
	governed, err := filepath.Abs(filepath.Join(wd, filepath.FromSlash(governedOutputRoot)))
	if err != nil {
		return fmt.Errorf("resolve governed architecture v2 output root: %w", err)
	}
	requested = filepath.Clean(requested)
	governed = filepath.Clean(governed)
	equal := requested == governed
	if runtime.GOOS == "windows" {
		equal = strings.EqualFold(requested, governed)
	}
	if !equal {
		return fmt.Errorf("architecture v2 --output must resolve to governed ResolvedPlan outputRoot %s", governedOutputRoot)
	}
	return nil
}

func (g architectureV2ExecutionGate) verifyV2Generation(wd string, mode architectureV2ExecutionMode, options architectureV2ExecutionCLIOptions, authority architectureV2ExecutionAuthority, current architecturev2.CurrentResolution, persisted generationartifact.VerifiedPlan, currentCanonical []byte, defaultManifestPath, defaultReceiptPath string, transaction *confinedfs.Transaction, outputLock *confinedfs.OutputLock) error {
	if options.verifiedPlanSink != nil {
		if err := options.verifiedPlanSink(persisted); err != nil {
			return err
		}
	}
	manifestPath, err := architectureV2CanonicalMetadataPath(wd, options.manifestPath, defaultManifestPath, "artifact manifest")
	if err != nil {
		return err
	}
	receiptPath, err := architectureV2CanonicalMetadataPath(wd, options.receiptPath, defaultReceiptPath, "generation receipt")
	if err != nil {
		return err
	}
	manifest, err := generationartifact.ReadManifest(manifestPath)
	if err != nil {
		return err
	}
	receipt, err := generationartifact.ReadReceipt(receiptPath)
	if err != nil {
		return err
	}
	input := generationartifact.ExecutionGateInput{
		CurrentCanonical: currentCanonical,
		Plan:             persisted,
		Phase:            architectureV2ReadinessPhase(mode),
		Versions:         g.versions,
		Root:             wd,
		Manifest:         manifest,
		Receipt:          receipt,
	}
	if mode == architectureV2Plan {
		inspection, err := generationartifact.InspectExecution(input)
		if err != nil {
			return err
		}
		if options.inspectionSink != nil {
			return options.inspectionSink(inspection)
		}
		return nil
	}
	if err := generationartifact.VerifyExecution(input); err != nil {
		return err
	}
	if mode == architectureV2Remove {
		return g.removeV2Workload(
			wd, options, authority, persisted, manifest, receipt, transaction,
		)
	}
	if mode == architectureV2Verify {
		if g.newVerifyAuthority == nil {
			return generationartifact.VerifierNotImplemented(persisted.Binding().Renderer)
		}
		rawVerifyAuthority, err := g.newVerifyAuthority(wd, options)
		if err != nil {
			return err
		}
		if closer, ok := rawVerifyAuthority.(interface{ Close() error }); ok {
			defer func() { _ = closer.Close() }()
		}
		verifyAuthority, ok := rawVerifyAuthority.(architectureV2ProductVerifyAuthority)
		if !ok {
			return generationartifact.VerifierNotImplemented(persisted.Binding().Renderer)
		}
		result, err := readCurrentArchitectureV2ApplyResult(wd, persisted.Binding(), func(data []byte) (architecturev2.VerifiedApplyResult, error) {
			return verifyAuthority.VerifyProductApplyResult(architecturev2.ProductApplyResultVerificationInput{
				Plan: persisted, Manifest: manifest, Receipt: receipt, Versions: g.versions, Result: data,
			})
		})
		if err != nil {
			return err
		}
		verifyContext := options.context
		if verifyContext == nil {
			verifyContext = context.Background()
		}
		configuredRuntime, processRuntime, err := architectureV2ConfiguredStandardRuntimeFromInventory(options)
		if err != nil {
			return err
		}
		var owner architectureV2OwnerVerifySummary
		var runtime *architectureV2RuntimeVerifySummary
		if processRuntime {
			owner, _, err = verifyArchitectureV2OwnerCustody(wd)
			runtime = &architectureV2RuntimeVerifySummary{
				ExecutionMode: "standard-process", Live: false, Status: "verified-apply-evidence",
				ServiceCount: result.Summary().RuntimeCount, ProbeCount: result.Summary().HealthCount,
			}
		} else {
			var appliedRequest runtimeexecutor.ExecutionRequest
			if !options.verifyOffline {
				custody, ok := rawVerifyAuthority.(architectureV2AppliedRuntimeCustody)
				if !ok {
					return errors.New("Architecture v2 local Verify requires applied runtime request custody")
				}
				requestDigest, digestErr := architectureV2SharedRequestDigest(result)
				if digestErr != nil {
					return digestErr
				}
				if requestDigest == "" {
					return errors.New("verified Product Apply result has no applied runtime request custody")
				}
				appliedRequest, err = custody.LoadAppliedRuntimeRequest(verifyContext, requestDigest)
				if err != nil {
					return err
				}
			}
			owner, runtime, err = verifyArchitectureV2LocalState(verifyContext, wd, persisted, manifest, options.verifyOffline, appliedRequest)
		}
		ownerCustody, custodyErr := localevidence.LoadOwnerCustody(wd)
		if custodyErr != nil {
			return custodyErr
		}
		verifyObservedAt, verifyObservationRunID := historicalRuntimeObservationIdentity(result.Summary().AppliedAt)
		observationSource, observationLive := runtimeobservation.SourceVerifiedApplyEvidence, false
		var cloudVerify *runtimeexecutorlocal.CloudCoreVerifyObservation
		if runtime != nil && runtime.cloud != nil {
			now := time.Now
			if g.now != nil {
				now = g.now
			}
			verifyObservedAt = now().UTC()
			verifyObservationRunID = runtimeObservationRunID(verifyObservedAt)
			observationSource, observationLive, cloudVerify = runtimeobservation.SourceLocalRuntime, true, runtime.cloud
		}
		access, accessErr := readArchitectureV2AccessSummary(wd, persisted, result.Summary())
		if accessErr != nil {
			return accessErr
		}
		observations, observationErr := buildArchitectureV2RuntimeObservations(architectureV2RuntimeObservationInput{
			Plan: persisted, Access: access, Phase: runtimeobservation.PhaseVerify,
			Source: observationSource, Live: observationLive,
			ObservedAt: verifyObservedAt, RunID: verifyObservationRunID,
			Apply: result.Summary(), Outcomes: result.ObservationSummary(),
			FallbackSiteRef: ownerCustody.Binding.SiteRef, FallbackNodeRef: ownerCustody.Binding.NodeRef,
			FallbackChannelRef: ownerCustody.Binding.ChannelRef,
			RolloutEvidence:    rolloutRecorder != nil || deployLog != nil,
			AccessEvidence:     access != nil,
			CloudVerify:        cloudVerify,
		})
		if observationErr != nil {
			return observationErr
		}
		if processRuntime && runtime != nil {
			runtime.ServiceCount, runtime.ProbeCount = runtimeObservationCounts(observations)
			runtime.ExecutionMode = runtimeObservationExecutionMode(observations, runtimeObservationProcessChannelRefs(configuredRuntime))
		}
		report := architectureV2VerifyReport{
			SchemaVersion: "stackkit.verify-result/v1", Offline: options.verifyOffline,
			PlanHash: persisted.Binding().PlanHash, Apply: result.Summary(),
			Owner: owner, Runtime: runtime, Observations: observations,
		}
		if err != nil {
			var drift architectureV2DriftCarrier
			if options.driftObservation && errors.As(err, &drift) {
				return &architectureV2DriftDetectedError{
					Verification: report,
					Differences: []architectureV2DriftDifference{{
						Subject: drift.DriftSubject(), Code: drift.DriftCode(),
						ProjectRef: drift.DriftProjectRef(),
					}},
				}
			}
			return err
		}
		if options.verifySink != nil {
			return options.verifySink(report)
		}
		return nil
	}
	if mode != architectureV2Apply {
		return generationartifact.ExecutorNotImplemented(persisted.Binding().Renderer)
	}
	if transaction == nil || outputLock == nil {
		return fmt.Errorf("architecture v2 Apply requires the held workspace transaction and output lock")
	}
	applyAuthority, ok := authority.(architectureV2ProductApplyAuthority)
	if !ok {
		return generationartifact.ExecutorNotImplemented(persisted.Binding().Renderer)
	}
	executionContext := options.context
	if executionContext == nil {
		executionContext = context.Background()
	}
	applyInput := architecturev2.ProductApplyInput{
		Current: current, Workspace: transaction, OutputLock: outputLock, Versions: g.versions,
	}
	now := time.Now
	if g.now != nil {
		now = g.now
	}
	lifecycleStartedAt := now().UTC()
	var lifecycleRuns []architectureV2ApplicationLifecycleRun
	if strings.TrimSpace(lifecycleJoinOperation) == "" {
		lifecycleRuns, err = beginArchitectureV2ApplicationLifecycles(
			wd, persisted, "install", "stackkit.apply", "", lifecycleStartedAt,
		)
		if err != nil {
			return err
		}
	}
	result, err := executeArchitectureV2ProductApply(
		executionContext,
		applyAuthority,
		applyInput,
		architecturev2.ResolveInput{StackSpec: options.stackSpecData, Inventory: options.inventoryData},
	)
	if err != nil {
		// The durable journal knows which units ran, which failed, and which
		// were never attempted. Report that instead of one opaque error, so an
		// operator is not left guessing what is running on the host.
		reportApplyLedgerForFailure(options, persisted, err, now().UTC())
		return failArchitectureV2ApplicationLifecycles(
			wd, lifecycleRuns, "Product Apply failed before owner evidence was persisted", now().UTC(), err,
		)
	}
	persistedResult, err := persistArchitectureV2ApplyResult(transaction, result)
	if err != nil {
		return requireArchitectureV2ApplicationLifecycleRecovery(
			wd, lifecycleRuns,
			"Product Apply completed but its owner evidence could not be persisted",
			"urn:stackkit:apply-result:"+result.ResultHash(), now().UTC(), err,
		)
	}
	access, err := buildArchitectureV2AccessSummary(persisted, result.Summary())
	if err != nil {
		return requireArchitectureV2ApplicationLifecycleRecovery(
			wd, lifecycleRuns, "Product Apply completed but its service access manifest could not be projected",
			persistedResult.ResultPath, now().UTC(), err,
		)
	}
	if err := writeAccessSummary(wd, access); err != nil {
		return requireArchitectureV2ApplicationLifecycleRecovery(
			wd, lifecycleRuns, "Product Apply completed but its service access manifest could not be persisted",
			persistedResult.ResultPath, now().UTC(), err,
		)
	}
	defaultServicePlanPath, _, _ := persisted.MetadataPaths(wd)
	servicePlanPath := architectureV2MetadataPath(wd, options.planPath, defaultServicePlanPath)
	serviceController, err := servicecontrol.NewOSController(wd, servicePlanPath, persisted)
	if err != nil {
		return requireArchitectureV2ApplicationLifecycleRecovery(
			wd, lifecycleRuns, "Product Apply completed but service desired-state authority was unavailable",
			persistedResult.ResultPath, now().UTC(), err,
		)
	}
	if err := serviceController.ReconcileAfterApply(executionContext); err != nil {
		return requireArchitectureV2ApplicationLifecycleRecovery(
			wd, lifecycleRuns, "Product Apply completed but durable service desired state could not be reconciled",
			persistedResult.ResultPath, now().UTC(), err,
		)
	}
	ownerCustody, err := localevidence.LoadOwnerCustody(wd)
	if err != nil {
		return requireArchitectureV2ApplicationLifecycleRecovery(
			wd, lifecycleRuns, "Product Apply completed but its exact owner runtime binding could not be loaded",
			persistedResult.ResultPath, now().UTC(), err,
		)
	}
	configuredRuntime, _, err := architectureV2ConfiguredStandardRuntimeFromInventory(options)
	if err != nil {
		return requireArchitectureV2ApplicationLifecycleRecovery(
			wd, lifecycleRuns, "Product Apply completed but its configured runtime could not be projected",
			persistedResult.ResultPath, now().UTC(), err,
		)
	}
	observations, err := buildArchitectureV2RuntimeObservations(architectureV2RuntimeObservationInput{
		Plan: persisted, Access: access, Phase: runtimeobservation.PhaseApply,
		Source: runtimeobservation.SourceLocalRuntime, Live: true, ObservedAt: result.Summary().AppliedAt,
		RunID: runtimeObservationRunID(result.Summary().AppliedAt), Apply: result.Summary(),
		Outcomes:        result.ObservationSummary(),
		FallbackSiteRef: ownerCustody.Binding.SiteRef, FallbackNodeRef: ownerCustody.Binding.NodeRef,
		FallbackChannelRef: ownerCustody.Binding.ChannelRef,
		RolloutEvidence:    rolloutRecorder != nil || deployLog != nil,
		AccessEvidence:     true,
		ProcessChannelRefs: runtimeObservationProcessChannelRefs(configuredRuntime),
	})
	if err != nil {
		return requireArchitectureV2ApplicationLifecycleRecovery(
			wd, lifecycleRuns, "Product Apply completed but its versioned runtime observation could not be projected",
			persistedResult.ResultPath, now().UTC(), err,
		)
	}
	defaultPlanPath, _, _ := persisted.MetadataPaths(wd)
	planPath := architectureV2MetadataPath(wd, options.planPath, defaultPlanPath)
	planRef, err := architectureV2WorkspaceEvidenceRef(wd, planPath)
	if err != nil {
		return requireArchitectureV2ApplicationLifecycleRecovery(
			wd, lifecycleRuns, "Product Apply evidence is outside owner workspace custody",
			persistedResult.ResultPath, now().UTC(), err,
		)
	}
	generationReceiptRef, err := architectureV2WorkspaceEvidenceRef(wd, receiptPath)
	if err != nil {
		return requireArchitectureV2ApplicationLifecycleRecovery(
			wd, lifecycleRuns, "Generation receipt evidence is outside owner workspace custody",
			persistedResult.ResultPath, now().UTC(), err,
		)
	}
	generationReceiptHash, err := receipt.Hash()
	if err != nil {
		return requireArchitectureV2ApplicationLifecycleRecovery(
			wd, lifecycleRuns, "Generation receipt evidence could not be content-addressed",
			persistedResult.ResultPath, now().UTC(), err,
		)
	}
	if err := succeedArchitectureV2ApplicationLifecycles(
		wd,
		lifecycleRuns,
		[]applicationlifecycle.Evidence{
			{Kind: "resolved-plan", Ref: planRef, Digest: persisted.Binding().PlanHash},
			{Kind: "generation-receipt", Ref: generationReceiptRef, Digest: generationReceiptHash},
			{Kind: "apply-result", Ref: persistedResult.ResultPath, Digest: result.ResultHash()},
			{
				Kind: "owner-observation", Ref: persistedResult.OwnerReceiptPath,
				Digest: persistedResult.OwnerReceiptDigest,
			},
		},
		now().UTC(),
	); err != nil {
		return err
	}
	rolloutEvent("architecture_v2.apply", "succeeded", "native Architecture v2 Apply result persisted", map[string]string{
		"result_hash": result.ResultHash(), "result_path": persistedResult.ResultPath,
	})
	ledger := applyledger.Applied(persisted.ApplyRequirements(), persisted.Binding().PlanHash, result.Summary().AppliedAt)
	persistApplyLedger(ledger)
	applyOutput := architectureV2ApplyCommandResult{
		SchemaVersion: "stackkit.apply-result/v2", Status: string(ledger.Overall), Apply: result.Summary(), Observations: observations,
		EvidenceLinks: []runtimeobservation.EvidenceLink{
			{Kind: "apply-result", Ref: persistedResult.ResultPath, Digest: result.ResultHash()},
			{Kind: "owner-apply-result-receipt", Ref: persistedResult.OwnerReceiptPath, Digest: persistedResult.OwnerReceiptDigest},
		},
		Outcomes: &ledger,
	}
	if options.applySink != nil {
		return options.applySink(applyOutput)
	}
	printSuccess("Architecture v2 Apply completed: %s", result.ResultHash())
	return nil
}

func (g architectureV2ExecutionGate) removeV2Workload(
	wd string,
	options architectureV2ExecutionCLIOptions,
	authority architectureV2ExecutionAuthority,
	plan generationartifact.VerifiedPlan,
	manifest generationartifact.ArtifactManifest,
	receipt generationartifact.GenerationReceipt,
	transaction *confinedfs.Transaction,
) error {
	workloadRef := strings.TrimSpace(options.workloadRef)
	if workloadRef == "" {
		return errors.New("architecture v2 remove requires --workload with one exact ResolvedPlan workload ref")
	}
	if transaction == nil {
		return errors.New("architecture v2 workload removal requires the held lifecycle transaction")
	}
	verifyAuthority, ok := authority.(architectureV2ProductVerifyAuthority)
	if !ok {
		return errors.New("architecture v2 workload removal requires Product Apply verification authority")
	}
	applied, err := readCurrentArchitectureV2ApplyResult(
		wd,
		plan.Binding(),
		func(data []byte) (architecturev2.VerifiedApplyResult, error) {
			return verifyAuthority.VerifyProductApplyResult(architecturev2.ProductApplyResultVerificationInput{
				Plan: plan, Manifest: manifest, Receipt: receipt, Versions: g.versions, Result: data,
			})
		},
	)
	if err != nil {
		return fmt.Errorf("verify current Product Apply before workload removal: %w", err)
	}
	sharedRequestDigest, err := architectureV2SharedRequestDigest(applied)
	if err != nil {
		return err
	}
	if sharedRequestDigest == "" {
		return errors.New("current Product Apply result has no sealed runtime request custody")
	}
	custody, ok := authority.(architectureV2AppliedRuntimeCustody)
	if !ok {
		return errors.New("architecture v2 workload removal requires applied runtime request custody")
	}
	ctx := options.context
	if ctx == nil {
		ctx = context.Background()
	}
	rootRequest, err := custody.LoadAppliedRuntimeRequest(ctx, sharedRequestDigest)
	if err != nil {
		return err
	}
	configured, active, err := architectureV2ConfiguredStandardRuntimeFromInventory(options)
	if err != nil {
		return err
	}
	if !active {
		return errors.New("selected-PaaS workload removal requires the exact configured Standard execution channel from Inventory")
	}
	placement, err := appliedWorkloadRemovalPlacement(applied, rootRequest.RequestDigest, workloadRef, options)
	if err != nil {
		return err
	}
	selected, err := workloadremoval.SelectAppliedWorkloadPlacement(rootRequest, placement)
	if err != nil {
		return err
	}
	target := selected.RuntimeTargets[0]
	var processBinding *runtimeexecutorprocess.Binding
	for _, binding := range configured.bindings {
		if binding.ChannelRef == target.ExecutionChannelRef &&
			binding.SiteRef == target.SiteRefs[0] && binding.NodeRef == target.NodeRefs[0] {
			candidate := runtimeexecutorprocess.Binding{
				ChannelRef: binding.ChannelRef, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef,
				Executable: binding.Executable, ExecutableSHA256: binding.ExecutableSHA256,
			}
			processBinding = &candidate
			break
		}
	}
	if processBinding == nil {
		return fmt.Errorf("applied workload %q has no exact configured Standard execution channel", workloadRef)
	}
	executor, err := runtimeexecutorprocess.New(architectureV2ComponentVersion(version), *processBinding)
	if err != nil {
		return fmt.Errorf("construct exact workload-removal execution channel: %w", err)
	}
	now := time.Now
	if g.now != nil {
		now = g.now
	}
	requestedAt := now().UTC()
	validUntil := requestedAt.Add(5 * time.Minute)
	authorizationBytes, err := workloadremoval.AuthorizationBytesForPlacement(rootRequest, placement, requestedAt, validUntil)
	if err != nil {
		return err
	}
	signature, err := localevidence.SignOwnerLifecycleMutation(wd, authorizationBytes)
	if err != nil {
		return fmt.Errorf("sign workload-removal Owner authorization: %w", err)
	}
	if err := localevidence.VerifyOwnerLifecycleMutation(wd, authorizationBytes, signature); err != nil {
		return fmt.Errorf("verify workload-removal Owner authorization: %w", err)
	}
	request, err := workloadremoval.SealRequestForPlacement(
		rootRequest, placement, requestedAt, validUntil,
		workloadremoval.OwnerAuthorization{
			OwnerRef: signature.OwnerRef, KeyID: signature.KeyID, Value: signature.Value,
		},
	)
	if err != nil {
		return err
	}
	lifecycleRuns, err := beginArchitectureV2ApplicationLifecycles(
		wd, plan, "remove", "stackkit.remove", workloadRef, requestedAt,
	)
	if err != nil {
		return err
	}
	result, err := executor.RemoveWorkload(ctx, request)
	if err != nil {
		return failArchitectureV2ApplicationLifecycles(
			wd, lifecycleRuns, "workload removal failed before owner evidence was persisted", now().UTC(), err,
		)
	}
	evidence, err := workloadremoval.NewEvidence(request, result)
	if err != nil {
		return requireArchitectureV2ApplicationLifecycleRecovery(
			wd, lifecycleRuns,
			"workload removal completed but its terminal evidence was invalid",
			"urn:stackkit:removal-result:"+result.ResultDigest, now().UTC(), err,
		)
	}
	requestPath, resultPath, evidencePath, err := persistArchitectureV2Removal(transaction, request, result, evidence)
	if err != nil {
		return requireArchitectureV2ApplicationLifecycleRecovery(
			wd, lifecycleRuns,
			"workload removal completed but its owner evidence could not be persisted",
			"urn:stackkit:removal-result:"+result.ResultDigest, now().UTC(), err,
		)
	}
	if err := succeedArchitectureV2ApplicationLifecycles(
		wd,
		lifecycleRuns,
		[]applicationlifecycle.Evidence{{
			Kind: "removal-result", Ref: resultPath, Digest: result.ResultDigest,
		}},
		now().UTC(),
	); err != nil {
		return err
	}
	if options.removalEvidenceSink != nil {
		if err := options.removalEvidenceSink(evidence); err != nil {
			return err
		}
	} else if options.removalSink != nil {
		if err := options.removalSink(result); err != nil {
			return err
		}
	} else if options.removalEvidenceJSON {
		canonical, _ := evidence.Canonical()
		if _, err := os.Stdout.Write(canonical); err != nil {
			return err
		}
	} else if options.removalJSON {
		canonical, _ := result.Canonical()
		fmt.Fprintln(os.Stdout, string(canonical))
	} else {
		printSuccess("Architecture v2 workload %s removed: %s", workloadRef, result.ResultDigest)
	}
	rolloutEvent("architecture_v2.remove", "succeeded", "native Architecture v2 workload removal result persisted", map[string]string{
		"workload_ref": workloadRef, "request_path": requestPath, "result_path": resultPath,
		"evidence_path": evidencePath,
		"result_hash":   result.ResultDigest,
	})
	return nil
}

func appliedWorkloadRemovalPlacement(applied architecturev2.VerifiedApplyResult, appliedRequestDigest, workloadRef string, options architectureV2ExecutionCLIOptions) (workloadremoval.AppliedPlacement, error) {
	summary := applied.Summary()
	if summary.AppliedRequestDigest != appliedRequestDigest {
		return workloadremoval.AppliedPlacement{}, errors.New("verified Apply result does not match runtime request custody")
	}
	candidate := workloadremoval.AppliedPlacement{
		AppliedRequestDigest: appliedRequestDigest, WorkloadRef: workloadRef,
		SiteRef: strings.TrimSpace(options.localSiteRef), NodeRef: strings.TrimSpace(options.localNodeRef),
		ExecutionChannelRef: strings.TrimSpace(options.localChannelRef),
	}
	if candidate.SiteRef == "" || candidate.NodeRef == "" || candidate.ExecutionChannelRef == "" {
		return workloadremoval.AppliedPlacement{}, errors.New("workload removal requires one explicit local Site, node, and execution channel")
	}
	matches := 0
	for _, workload := range summary.AppliedWorkloads {
		if workload.WorkloadRef != workloadRef {
			continue
		}
		for _, placement := range workload.Placements {
			if placement.SiteRef == candidate.SiteRef && placement.NodeRef == candidate.NodeRef && placement.ExecutionChannelRef == candidate.ExecutionChannelRef {
				candidate.RequirementID, candidate.InstanceRef = workload.RequirementID, workload.InstanceRef
				matches++
			}
		}
	}
	if matches != 1 {
		return workloadremoval.AppliedPlacement{}, fmt.Errorf("applied workload %q has %d exact local placements; one is required", workloadRef, matches)
	}
	return candidate, nil
}

func architectureV2SharedRequestDigest(result architecturev2.VerifiedApplyResult) (string, error) {
	canonical, err := result.Canonical()
	if err != nil {
		return "", err
	}
	var envelope struct {
		SharedRequestDigest string `json:"sharedRequestDigest"`
	}
	if err := json.Unmarshal(canonical, &envelope); err != nil {
		return "", fmt.Errorf("decode verified Product Apply request custody: %w", err)
	}
	return strings.TrimSpace(envelope.SharedRequestDigest), nil
}

func persistArchitectureV2Removal(
	transaction *confinedfs.Transaction,
	request workloadremoval.Request,
	result workloadremoval.Result,
	evidence workloadremoval.Evidence,
) (string, string, string, error) {
	requestCanonical, err := request.Canonical()
	if err != nil {
		return "", "", "", err
	}
	resultCanonical, err := result.Canonical()
	if err != nil {
		return "", "", "", err
	}
	evidenceCanonical, err := evidence.Canonical()
	if err != nil {
		return "", "", "", err
	}
	requestPath := filepath.Join(".stackkit", "evidence", "removal", "requests", strings.TrimPrefix(request.RequestDigest, "sha256:")+".json")
	resultPath := filepath.Join(".stackkit", "evidence", "removal", "results", strings.TrimPrefix(result.ResultDigest, "sha256:")+".json")
	evidencePath := filepath.Join(".stackkit", "evidence", "removal", "terminal", strings.TrimPrefix(evidence.EvidenceDigest, "sha256:")+".json")
	for path, data := range map[string][]byte{requestPath: requestCanonical, resultPath: resultCanonical, evidencePath: evidenceCanonical} {
		if err := transaction.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", "", "", fmt.Errorf("create workload-removal evidence directory: %w", err)
		}
		if err := transaction.WriteFileExclusive(path, data, 0o600); err != nil {
			existing, info, readErr := transaction.ReadStable(path)
			if readErr != nil || !info.Mode().IsRegular() || !bytes.Equal(existing, data) {
				return "", "", "", fmt.Errorf("persist content-addressed workload-removal evidence: %w", err)
			}
		}
	}
	return filepath.ToSlash(requestPath), filepath.ToSlash(resultPath), filepath.ToSlash(evidencePath), nil
}

func executeArchitectureV2ProductApply(
	ctx context.Context,
	authority architectureV2ProductApplyAuthority,
	input architecturev2.ProductApplyInput,
	resolveInput architecturev2.ResolveInput,
) (architecturev2.VerifiedApplyResult, error) {
	result, err := authority.ExecuteProductApply(ctx, input)
	if err == nil {
		return result, nil
	}
	var reconcile *architecturev2.ProductApplyReconcileRequiredError
	if !errors.As(err, &reconcile) || reconcile.RequestDigest() == "" {
		return architecturev2.VerifiedApplyResult{}, err
	}
	return reconcileArchitectureV2ProductApply(ctx, authority, input, resolveInput, reconcile.RequestDigest(), err)
}

func reconcileArchitectureV2ProductApply(
	ctx context.Context,
	authority architectureV2ProductApplyAuthority,
	input architecturev2.ProductApplyInput,
	resolveInput architecturev2.ResolveInput,
	requestDigest string,
	primaryErr error,
) (architecturev2.VerifiedApplyResult, error) {
	freshCurrent, err := authority.ResolveCurrent(resolveInput)
	if err != nil {
		return architecturev2.VerifiedApplyResult{}, errors.Join(primaryErr, fmt.Errorf("refresh current resolution for Product Apply reconciliation: %w", err))
	}
	result, err := authority.ReconcileProductApply(ctx, architecturev2.ProductApplyReconcileInput{
		Current:       freshCurrent,
		Workspace:     input.Workspace,
		OutputLock:    input.OutputLock,
		Versions:      input.Versions,
		RequestDigest: requestDigest,
	})
	if err != nil {
		return architecturev2.VerifiedApplyResult{}, errors.Join(primaryErr, err)
	}
	return result, nil
}

type architectureV2PersistedApplyResult struct {
	ResultPath         string
	OwnerReceiptPath   string
	OwnerReceiptDigest string
}

func persistArchitectureV2ApplyResult(
	transaction *confinedfs.Transaction,
	result architecturev2.VerifiedApplyResult,
) (architectureV2PersistedApplyResult, error) {
	canonical, err := result.Canonical()
	if err != nil {
		return architectureV2PersistedApplyResult{}, err
	}
	hash := strings.TrimPrefix(result.ResultHash(), "sha256:")
	if len(hash) != 64 {
		return architectureV2PersistedApplyResult{}, fmt.Errorf("persist Architecture v2 Apply result: invalid result hash")
	}
	directory := filepath.Join(".stackkit", "evidence", "apply", "results")
	if err := transaction.MkdirAll(directory, 0o700); err != nil {
		return architectureV2PersistedApplyResult{}, fmt.Errorf("create Architecture v2 Apply result directory: %w", err)
	}
	path := filepath.Join(directory, hash+".json")
	if err := transaction.WriteFileExclusive(path, canonical, 0o600); err != nil {
		existing, info, readErr := transaction.ReadStable(path)
		if readErr != nil || !info.Mode().IsRegular() || !bytes.Equal(existing, canonical) {
			return architectureV2PersistedApplyResult{}, fmt.Errorf("persist content-addressed Architecture v2 Apply result: %w", err)
		}
	}
	_, canonicalReceipt, err := newOwnerApplyResultReceipt(transaction.Name(), result)
	if err != nil {
		return architectureV2PersistedApplyResult{}, err
	}
	receiptDirectory := filepath.Join(".stackkit", "evidence", "apply", "receipts")
	if err := transaction.MkdirAll(receiptDirectory, 0o700); err != nil {
		return architectureV2PersistedApplyResult{}, fmt.Errorf("create owner-signed Architecture v2 Apply result receipt directory: %w", err)
	}
	receiptPath := filepath.Join(receiptDirectory, hash+".json")
	if err := transaction.WriteFileExclusive(receiptPath, canonicalReceipt, 0o600); err != nil {
		existing, info, readErr := transaction.ReadStable(receiptPath)
		if readErr != nil || !info.Mode().IsRegular() || !bytes.Equal(existing, canonicalReceipt) {
			return architectureV2PersistedApplyResult{}, fmt.Errorf("persist owner-signed Architecture v2 Apply result receipt: %w", err)
		}
	}
	return architectureV2PersistedApplyResult{
		ResultPath: filepath.ToSlash(path), OwnerReceiptPath: filepath.ToSlash(receiptPath),
		OwnerReceiptDigest: architectureV2ApplicationLifecycleDigest(canonicalReceipt),
	}, nil
}

func validateArchitectureV2PlanOptions(options architectureV2ExecutionCLIOptions) error {
	if strings.TrimSpace(options.planOut) != "" {
		return &architectureV2PlanOptionError{Flag: "--out", Message: "native v2 plan inspection does not create an OpenTofu plan file"}
	}
	if options.planDestroy {
		return &architectureV2PlanOptionError{Flag: "--destroy", Message: "native v2 plan inspection cannot claim a destroy diff without a governed executor"}
	}
	return nil
}

func validateArchitectureV2ApplyOptions(options architectureV2ExecutionCLIOptions) error {
	if strings.TrimSpace(options.legacyPlanFile) != "" {
		return fmt.Errorf("architecture v2 apply does not accept an OpenTofu plan file; execution is owned by the canonical ResolvedPlan and runtime registry")
	}
	return nil
}

func architectureV2MetadataPath(wd, explicit, derived string) string {
	if strings.TrimSpace(explicit) == "" {
		return filepath.Clean(derived)
	}
	return resolvePathFromWorkDir(wd, explicit)
}

func architectureV2CanonicalMetadataPath(wd, explicit, derived, label string) (string, error) {
	canonical := filepath.Clean(derived)
	if strings.TrimSpace(explicit) == "" {
		return canonical, nil
	}
	requested := filepath.Clean(resolvePathFromWorkDir(wd, explicit))
	canonicalAbsolute, err := filepath.Abs(canonical)
	if err != nil {
		return "", fmt.Errorf("resolve canonical architecture v2 %s path %s: %w", label, canonical, err)
	}
	requestedAbsolute, err := filepath.Abs(requested)
	if err != nil {
		return "", fmt.Errorf("resolve requested architecture v2 %s path %s: %w", label, requested, err)
	}
	pathsEqual := canonicalAbsolute == requestedAbsolute
	if runtime.GOOS == "windows" {
		pathsEqual = strings.EqualFold(canonicalAbsolute, requestedAbsolute)
	}
	if !pathsEqual {
		return "", fmt.Errorf("architecture v2 %s override must resolve to canonical governed path %s", label, canonical)
	}
	return canonical, nil
}

func readArchitectureV2Inventory(wd, explicit string) ([]byte, error) {
	if strings.TrimSpace(explicit) != "" {
		path := resolvePathFromWorkDir(wd, explicit)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read architecture v2 Inventory %s: %w", path, err)
		}
		return data, nil
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
			return nil, fmt.Errorf("inspect architecture v2 Inventory candidate %s: %w", candidate, err)
		}
	}
	if len(selected) > 1 {
		return nil, fmt.Errorf("architecture v2 Inventory is ambiguous; choose exactly one with --inventory: %s", strings.Join(selected, ", "))
	}
	if len(selected) == 0 {
		return nil, nil
	}
	data, err := os.ReadFile(selected[0])
	if err != nil {
		return nil, fmt.Errorf("read architecture v2 Inventory %s: %w", selected[0], err)
	}
	return data, nil
}

func claimsNonLegacyAPIVersion(data []byte) bool {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil || len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return false
	}
	mapping := root.Content[0]
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value != "apiVersion" || mapping.Content[index+1].Kind != yaml.ScalarNode {
			continue
		}
		value := strings.TrimSpace(mapping.Content[index+1].Value)
		if value != "" && value != stackspecmigration.APIVersionV1 {
			return true
		}
	}
	return false
}

// canonicalPlanKitSlug reports the kit a verified plan is bound to.
func canonicalPlanKitSlug(plan resolvedplan.ResolvedPlan) string {
	kit, ok := map[string]any(plan)["kit"].(map[string]any)
	if !ok {
		return ""
	}
	slug, _ := kit["slug"].(string)
	return slug
}

// admitApplyHost measures the target against the floor its kit declares and
// refuses before any executor runs. Under the default warn policy only a
// blocking condition stops Apply; a degraded host installs and says so.
func admitApplyHost(
	options architectureV2ExecutionCLIOptions,
	kitSlug, workspace string,
	requirements generationartifact.ApplyRequirements,
	planHash string,
	observedAt time.Time,
) error {
	policy, err := resolveHostPreflightPolicy(options.preflightPolicy)
	if err != nil {
		return err
	}
	if policy == hostpreflight.PolicySkip {
		printWarning("Host preflight skipped; this Apply may mutate a host that cannot run the kit")
		return nil
	}
	executionContext := options.context
	if executionContext == nil {
		executionContext = context.Background()
	}
	report := evaluateHostPreflight(executionContext, workspace, kitSlug, policy)
	printHostPreflightReport(report)
	if !report.Admitted {
		refusal := hostPreflightRefusal(report)
		rolloutFailure("preflight", refusal)
		reportApplyLedger(options, applyledger.Blocked(requirements, planHash, observedAt))
		return refusal
	}
	rolloutEvent("preflight", "succeeded", "host admitted", map[string]string{
		"policy": string(report.Policy), "status": string(report.Status),
	})
	return nil
}

// reportApplyLedger persists and emits one per-unit Apply account through the
// same path whether the host was blocked or execution stopped part-way.
func reportApplyLedger(options architectureV2ExecutionCLIOptions, ledger applyledger.Ledger) {
	persistApplyLedger(ledger)
	if options.applySink != nil {
		_ = options.applySink(architectureV2ApplyCommandResult{
			SchemaVersion: "stackkit.apply-result/v2", Status: string(ledger.Overall),
			Observations: []runtimeobservation.Observation{}, EvidenceLinks: []runtimeobservation.EvidenceLink{},
			Outcomes: &ledger,
		})
		return
	}
	printApplyLedger(ledger)
}

// reportApplyLedgerForFailure emits the per-unit account of a partial Apply
// through the machine-readable sink, and prints it for a human otherwise.
//
// It never replaces the returned error: the command still fails. What changes
// is that the caller learns which units are running on the host.
func reportApplyLedgerForFailure(
	options architectureV2ExecutionCLIOptions,
	plan generationartifact.VerifiedPlan,
	cause error,
	observedAt time.Time,
) {
	var reconcile *architecturev2.ProductApplyReconcileRequiredError
	if !errors.As(cause, &reconcile) {
		return
	}
	steps := make([]applyledger.StepOutcome, 0)
	operationID := ""
	for _, operation := range reconcile.Operations() {
		if operationID == "" {
			operationID = operation.Operation.OperationID
		}
		states := make(map[string]runtimeapply.StepSnapshot, len(operation.Snapshot.Steps))
		for _, state := range operation.Snapshot.Steps {
			states[state.StepID] = state
		}
		for _, step := range operation.Operation.Steps {
			if state, recorded := states[step.ID]; recorded {
				steps = append(steps, applyledger.StepOutcome{Step: step, State: state})
			}
		}
	}
	ledger := applyledger.FromJournal(
		plan.ApplyRequirements(), steps, plan.Binding().PlanHash, operationID, cause.Error(), observedAt,
	)
	reportApplyLedger(options, ledger)
}

// printApplyLedger renders the per-unit account for a human operator.
func printApplyLedger(ledger applyledger.Ledger) {
	if humanOutputSuppressed() {
		return
	}
	printInfo("Apply outcome: %s (%d applied, %d failed, %d not attempted)",
		ledger.Overall, ledger.Summary.Applied, ledger.Summary.Failed, ledger.Summary.Skipped+ledger.Summary.Unverified)
	for _, unit := range ledger.Units {
		label := unit.Subject.WorkloadRef
		if label == "" {
			label = unit.Subject.RequirementID
		}
		switch unit.Outcome {
		case applyledger.OutcomeApplied:
			printSuccess("  %s: applied", label)
		case applyledger.OutcomeFailed:
			detail := string(unit.Outcome)
			if unit.Failure != nil {
				detail = unit.Failure.Class
			}
			printError("  %s: failed (%s)", label, detail)
			if unit.Failure != nil {
				for _, guidance := range unit.Failure.Remediation {
					printInfo("      %s", guidance)
				}
			}
		default:
			printWarning("  %s: %s", label, unit.Outcome)
		}
	}
	if ledger.Next != nil && ledger.Next.Command != "" {
		printInfo("  Continue with: %s", ledger.Next.Command)
	}
}
