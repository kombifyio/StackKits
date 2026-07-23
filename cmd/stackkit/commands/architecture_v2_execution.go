package commands

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/stackspecadmission"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
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
	architectureV2Doctor   architectureV2ExecutionMode = "doctor"
	architectureV2Validate architectureV2ExecutionMode = "validate"
	architectureV2AppAdd   architectureV2ExecutionMode = "app add"
	architectureV2AddonAdd architectureV2ExecutionMode = "addon add"
	architectureV2AddonRm  architectureV2ExecutionMode = "addon remove"
	architectureV2AddonLs  architectureV2ExecutionMode = "addon list"
)

type architectureV2ExecutionCLIOptions struct {
	inventoryPath   string
	planPath        string
	manifestPath    string
	receiptPath     string
	localSiteRef    string
	localNodeRef    string
	localChannelRef string
	outputRoot      string
	fragments       bool
	force           bool
	context         context.Context
	planOut         string
	planDestroy     bool
	inspectionSink  func(generationartifact.PlanInspection) error
	legacyPlanFile  string
}

type architectureV2ExecutionAuthority interface {
	ResolveCurrent(architecturev2.ResolveInput) (architecturev2.CurrentResolution, error)
	AuthorizeGeneration(architecturev2.GenerationAuthorizationInput) (architecturev2.GenerationAuthorization, error)
	VerifyCanonicalPlan([]byte) (generationartifact.VerifiedPlan, error)
	ReadCanonicalPlan(string) (generationartifact.VerifiedPlan, error)
}

type architectureV2ProductApplyAuthority interface {
	ExecuteProductApply(context.Context, architecturev2.ProductApplyInput) (architecturev2.VerifiedApplyResult, error)
}

type architectureV2ExecutionGate struct {
	newAuthority      func() (architectureV2ExecutionAuthority, error)
	newApplyAuthority func(string, architectureV2ExecutionCLIOptions) (architectureV2ExecutionAuthority, error)
	newRegistry       func() (*architecturev2renderer.Registry, error)
	versions          generationartifact.ComponentVersions
	rejectV1          bool
	now               func() time.Time
}

func newArchitectureV2ExecutionGate() architectureV2ExecutionGate {
	componentVersion := architectureV2ComponentVersion(version)
	return architectureV2ExecutionGate{
		newAuthority: func() (architectureV2ExecutionAuthority, error) {
			return architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
		},
		newApplyAuthority: newArchitectureV2ProductRuntimeAuthority,
		newRegistry:       architecturev2renderer.NewProductRegistry,
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
	if cmd == nil || !architectureV2RejectsV1Execution(version) || commandDisablesDeployObservability(cmd) {
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
// command creates deploy logs, rollout receipts, telemetry, or tenant events.
// A managed deployment may begin without a local file because its separately
// verified fetch flow supplies the intent; an already-present local file still
// has to cross the same native-v2 admission boundary.
func admitApplyBeforeDeployObservability(wd, requestedSpecPath string) error {
	if !architectureV2RejectsV1Execution(version) {
		return nil
	}
	if strings.TrimSpace(applyTenantDeployment) == "" {
		if err := requireNativeV2StackSpec(wd, requestedSpecPath, architectureV2Apply); err != nil {
			return err
		}
	} else {
		loader := config.NewLoader(wd)
		resolvedPath, displayPath, _, err := loader.ResolveStackSpecPathForRead(requestedSpecPath)
		if err != nil {
			return fmt.Errorf("apply: resolve local managed StackSpec before observability: %w", err)
		}
		info, err := os.Stat(resolvedPath)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("apply: inspect local managed StackSpec %s before observability: %w", displayPath, err)
		}
		if info.IsDir() {
			return fmt.Errorf("apply: local managed StackSpec %s is a directory", displayPath)
		}
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

// prefetchManagedNativeV2IntentBeforeDeployObservability performs only the
// read-only Admin fetch needed to classify a native managed job with no local
// intent. It returns admitted v2 bytes in memory; publication happens only
// after deploy observability starts. A fetched v1 document fails here.
func prefetchManagedNativeV2IntentBeforeDeployObservability(ctx context.Context, wd, requestedSpecPath string) (*tenantSpecFetchCandidate, error) {
	if !architectureV2RejectsV1Execution(version) || strings.TrimSpace(applyTenantDeployment) == "" {
		return nil, nil
	}
	loader := config.NewLoader(wd)
	resolvedPath, displayPath, _, err := loader.ResolveStackSpecPathForRead(requestedSpecPath)
	if err != nil {
		return nil, fmt.Errorf("apply: resolve managed StackSpec before admission fetch: %w", err)
	}
	info, err := os.Stat(resolvedPath)
	if err == nil {
		if info.IsDir() {
			return nil, fmt.Errorf("apply: managed StackSpec %s is a directory", displayPath)
		}
		return nil, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("apply: inspect managed StackSpec %s before admission fetch: %w", displayPath, err)
	}
	candidate, err := fetchTenantSpecCandidate(ctx, applyTenantDeployment)
	if err != nil {
		return nil, fmt.Errorf("tenant-deployment spec admission fetch: %w", err)
	}
	return &candidate, nil
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
		if mode == architectureV2Generate {
			if err := validateArchitectureV2GenerateOptions(wd, options, persisted.OutputRoot()); err != nil {
				return err
			}
		}
		if mode == architectureV2Plan {
			if err := validateArchitectureV2PlanOptions(options); err != nil {
				return err
			}
		}
		if mode == architectureV2Apply {
			if err := validateArchitectureV2ApplyOptions(options); err != nil {
				return err
			}
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
		}
		phase := architectureV2ReadinessPhase(mode)
		if err := persisted.RequireReady(phase); err != nil {
			return err
		}
		return g.continueV2Execution(wd, mode, options, authority, currentResolution, persisted, resolved.CanonicalPlan, defaultManifestPath, defaultReceiptPath, transaction, outputLock)
	}
	if mode == architectureV2Generate {
		return execute(nil, nil)
	}
	if mode == architectureV2Plan {
		return withArchitectureV2ReadOnlyOutput(wd, current.OutputRoot(), func() error { return execute(nil, nil) })
	}
	return withArchitectureV2OutputLock(wd, current.OutputRoot(), func(transaction *confinedfs.Transaction, outputLock *confinedfs.OutputLock) error {
		if err := architecturev2.RequireNoPendingOutputTransaction(transaction, current.OutputRoot()); err != nil {
			return err
		}
		return execute(transaction, outputLock)
	})
}

func (g architectureV2ExecutionGate) openV2Authority(wd string, mode architectureV2ExecutionMode, options architectureV2ExecutionCLIOptions) (architectureV2ExecutionAuthority, error) {
	newAuthority := g.newAuthority
	if mode == architectureV2Apply && g.newApplyAuthority != nil {
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
	if mode == architectureV2Apply {
		return generationartifact.ExecutionPhaseApply
	}
	return generationartifact.ExecutionPhaseGeneration
}

func (g architectureV2ExecutionGate) continueV2Execution(wd string, mode architectureV2ExecutionMode, options architectureV2ExecutionCLIOptions, authority architectureV2ExecutionAuthority, current architecturev2.CurrentResolution, persisted generationartifact.VerifiedPlan, currentCanonical []byte, defaultManifestPath, defaultReceiptPath string, transaction *confinedfs.Transaction, outputLock *confinedfs.OutputLock) error {
	switch mode {
	case architectureV2Generate:
		return g.generateV2(wd, options.context, authority, current)
	case architectureV2Plan, architectureV2Apply, architectureV2Verify:
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
	if mode == architectureV2Verify {
		return generationartifact.VerifierNotImplemented(persisted.Binding().Renderer)
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
	result, err := applyAuthority.ExecuteProductApply(executionContext, architecturev2.ProductApplyInput{
		Current: current, Workspace: transaction, OutputLock: outputLock, Versions: g.versions,
	})
	if err != nil {
		return err
	}
	resultPath, err := persistArchitectureV2ApplyResult(transaction, persisted.OutputRoot(), result)
	if err != nil {
		return err
	}
	rolloutEvent("architecture_v2.apply", "succeeded", "native Architecture v2 Apply result persisted", map[string]string{
		"result_hash": result.ResultHash(), "result_path": resultPath,
	})
	printSuccess("Architecture v2 Apply completed: %s", result.ResultHash())
	return nil
}

func persistArchitectureV2ApplyResult(transaction *confinedfs.Transaction, outputRoot string, result architecturev2.VerifiedApplyResult) (string, error) {
	canonical, err := result.Canonical()
	if err != nil {
		return "", err
	}
	hash := strings.TrimPrefix(result.ResultHash(), "sha256:")
	if len(hash) != 64 {
		return "", fmt.Errorf("persist Architecture v2 Apply result: invalid result hash")
	}
	directory := filepath.Join(filepath.FromSlash(outputRoot), ".stackkit", "apply-results")
	if err := transaction.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create Architecture v2 Apply result directory: %w", err)
	}
	path := filepath.Join(directory, hash+".json")
	if err := transaction.WriteFileExclusive(path, canonical, 0o600); err != nil {
		existing, info, readErr := transaction.ReadStable(path)
		if readErr == nil && info.Mode().IsRegular() && bytes.Equal(existing, canonical) {
			return filepath.ToSlash(path), nil
		}
		return "", fmt.Errorf("persist content-addressed Architecture v2 Apply result: %w", err)
	}
	return filepath.ToSlash(path), nil
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
