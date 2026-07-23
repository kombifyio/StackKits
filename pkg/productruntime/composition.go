package productruntime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// ComponentVersions identifies the exact StackKits components participating
// in one prepared Apply. These identities are checked against the CUE-owned
// compatibility minima before an execution channel can be admitted.
type ComponentVersions struct {
	CLI       string
	Generator string
	Runtime   string
}

// SelectedPaaSOwner binds the one workload-specific owner whose exact runtime
// adapter identity is intentionally not static. The refs are catalog
// identities only; endpoints, credentials, leases, generations, and provider
// resource handles remain outside StackKits.
type SelectedPaaSOwner struct {
	RuntimeAdapterRef       string
	RuntimeAdapterModuleRef string
}

// CompositionConfig fixes the complete provider-free Product Runtime graph at
// construction. All collaborators are shared contracts; StackKits retains the
// CUE authority, target selection, workspace custody, and authorization logic.
type CompositionConfig struct {
	BuildVersion       string
	Versions           ComponentVersions
	StaticOwners       []OwnerID
	ImmichSelectedPaaS *SelectedPaaSOwner
	ExecutionChannels  ExecutionChannelFactory
	EvidenceCollector  ApplyEvidenceCollector
	Journal            Journal
	Recovery           RecoveryStore
}

// Composition is an opaque, concurrency-safe Product Runtime authority. It
// owns no provider client, transport credential, lease, generation, endpoint,
// or service database. Those concerns stay behind the supplied shared SPIs.
type Composition struct {
	service  *architecturev2.Service
	versions generationartifact.ComponentVersions
}

// PreparedRequest identifies one already-generated StackKits workspace. The
// authority scope isolates otherwise equal Stack IDs between authenticated
// tenants without entering the ResolvedPlan or generated artifact contract.
type PreparedRequest struct {
	AuthorityScope string
	WorkspaceRoot  string
	StackSpec      []byte
	Inventory      []byte
}

// ReconcileRequest resumes one exact request already held by the
// construction-owned Recovery store. Callers cannot supply recovery bytes or
// substitute evidence.
type ReconcileRequest struct {
	PreparedRequest
	RequestDigest string
}

// ApplyResult is the immutable public projection of a verified StackKits
// execution result. It contains no provider-native lifecycle receipt.
type ApplyResult struct {
	hash      string
	canonical []byte
}

// ReconcileRequiredError is the public fail-closed handoff after a partially
// executed durable Apply. Its opaque digest is the only authority accepted by
// ReconcilePrepared; internal operation steps and provider state are not
// exposed.
type ReconcileRequiredError struct {
	requestDigest string
	cause         error
}

// RequestDigest returns the exact recovery-custody key for ReconcilePrepared.
func (e *ReconcileRequiredError) RequestDigest() string {
	if e == nil {
		return ""
	}
	return e.requestDigest
}

func (e *ReconcileRequiredError) Error() string {
	if e == nil || e.cause == nil {
		return "Product Runtime reconcile is required"
	}
	return e.cause.Error()
}

// Unwrap preserves typed lower-level diagnostics without exporting their
// internal StackKits representation as part of this package's API.
func (e *ReconcileRequiredError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// ResultHash returns the canonical content address of the verified result.
func (r ApplyResult) ResultHash() string { return r.hash }

// Canonical returns a defensive copy of the verified result envelope.
func (r ApplyResult) Canonical() []byte { return append([]byte(nil), r.canonical...) }

// NewComposition constructs the external Product Runtime boundary. Every
// planned owner is remote-only at this boundary: an execution-channel
// admission must return the authenticated executor and the implicit local
// builder can never be selected.
func NewComposition(config CompositionConfig) (*Composition, error) {
	if strings.TrimSpace(config.BuildVersion) == "" || config.BuildVersion != strings.TrimSpace(config.BuildVersion) {
		return nil, errors.New("Product Runtime composition requires an exact normalized StackKits build version")
	}
	versions := generationartifact.ComponentVersions{
		CLI: strings.TrimSpace(config.Versions.CLI), Generator: strings.TrimSpace(config.Versions.Generator),
		Runtime: strings.TrimSpace(config.Versions.Runtime),
	}
	if versions.CLI == "" || versions.Generator == "" || versions.Runtime == "" ||
		versions.CLI != config.Versions.CLI || versions.Generator != config.Versions.Generator || versions.Runtime != config.Versions.Runtime {
		return nil, errors.New("Product Runtime composition requires exact normalized CLI, generator, and runtime versions")
	}
	versionChecks := []struct{ name, version string }{
		{name: "CLI", version: versions.CLI},
		{name: "generator", version: versions.Generator},
		{name: "runtime", version: versions.Runtime},
	}
	for _, check := range versionChecks {
		if _, err := resolvedplan.VersionAtLeast(check.version, "0.0.0"); err != nil {
			return nil, fmt.Errorf("Product Runtime composition %s version is invalid: %w", check.name, err)
		}
	}

	registrations := make([]architecturev2.ProductRuntimeOwnerRegistration, 0, len(config.StaticOwners)+1)
	if len(config.StaticOwners) > 0 {
		static, err := architecturev2.NewProductRemoteStaticRuntimeOwnerRegistrations(config.StaticOwners...)
		if err != nil {
			return nil, fmt.Errorf("construct static Product Runtime owners: %w", err)
		}
		registrations = append(registrations, static...)
	}
	if selected := config.ImmichSelectedPaaS; selected != nil {
		registration, err := architecturev2.NewProductRemoteImmichSelectedPaaSRegistration(
			selected.RuntimeAdapterRef,
			selected.RuntimeAdapterModuleRef,
		)
		if err != nil {
			return nil, fmt.Errorf("construct Immich selected-PaaS owner: %w", err)
		}
		registrations = append(registrations, registration)
	}
	if len(registrations) == 0 {
		return nil, errors.New("Product Runtime composition requires at least one explicit owner")
	}

	identity, err := architecturev2.NewProductRuntimeRootIdentity(versions.Runtime)
	if err != nil {
		return nil, fmt.Errorf("construct Product Runtime root identity: %w", err)
	}
	service, err := architecturev2.NewProductEmbeddedServiceWithRuntimeOwnersAndApplyEvidenceCollector(
		architecturev2.StackKitsV2Contract(config.BuildVersion),
		identity,
		registrations,
		config.ExecutionChannels,
		config.Journal,
		config.Recovery,
		config.EvidenceCollector,
	)
	if err != nil {
		return nil, fmt.Errorf("construct Product Runtime authority: %w", err)
	}
	return &Composition{service: service, versions: versions}, nil
}

// ApplyPrepared resolves current intent through embedded CUE authority, opens
// and locks the governed workspace itself, collects evidence through the
// construction-owned collector, and executes only through an admitted shared
// channel. Generated artifacts must already exist and match this exact
// resolution; no caller-provided evidence or local fallback is accepted.
func (c *Composition) ApplyPrepared(ctx context.Context, request PreparedRequest) (ApplyResult, error) {
	if ctx == nil {
		return ApplyResult{}, errors.New("Product Runtime Apply requires a context")
	}
	if err := ctx.Err(); err != nil {
		return ApplyResult{}, fmt.Errorf("Product Runtime Apply context: %w", err)
	}
	current, workspaceRoot, outputRoot, err := c.prepare(request)
	if err != nil {
		return ApplyResult{}, err
	}
	result, err := c.withOutputLock(workspaceRoot, outputRoot, func(transaction *confinedfs.Transaction, lock *confinedfs.OutputLock) (architecturev2.VerifiedApplyResult, error) {
		return c.service.ExecuteProductApply(ctx, architecturev2.ProductApplyInput{
			Current: current, Workspace: transaction, OutputLock: lock, Versions: c.versions,
		})
	})
	return result, publicRuntimeError(err)
}

// ReconcilePrepared revalidates the same CUE plan and held generated bytes
// before resuming one exact recovery digest. Access-bound recovery remains
// fail-closed until its fresh-instant continuation contract is versioned.
func (c *Composition) ReconcilePrepared(ctx context.Context, request ReconcileRequest) (ApplyResult, error) {
	if ctx == nil {
		return ApplyResult{}, errors.New("Product Runtime reconcile requires a context")
	}
	if err := ctx.Err(); err != nil {
		return ApplyResult{}, fmt.Errorf("Product Runtime reconcile context: %w", err)
	}
	if strings.TrimSpace(request.RequestDigest) == "" || request.RequestDigest != strings.TrimSpace(request.RequestDigest) {
		return ApplyResult{}, errors.New("Product Runtime reconcile requires an exact normalized request digest")
	}
	current, workspaceRoot, outputRoot, err := c.prepare(request.PreparedRequest)
	if err != nil {
		return ApplyResult{}, err
	}
	result, err := c.withOutputLock(workspaceRoot, outputRoot, func(transaction *confinedfs.Transaction, lock *confinedfs.OutputLock) (architecturev2.VerifiedApplyResult, error) {
		return c.service.ReconcileProductApply(ctx, architecturev2.ProductApplyReconcileInput{
			Current: current, Workspace: transaction, OutputLock: lock, Versions: c.versions,
			RequestDigest: request.RequestDigest,
		})
	})
	return result, publicRuntimeError(err)
}

func publicRuntimeError(err error) error {
	if err == nil {
		return nil
	}
	var reconcile *architecturev2.ProductApplyReconcileRequiredError
	if errors.As(err, &reconcile) && strings.TrimSpace(reconcile.RequestDigest()) != "" {
		return &ReconcileRequiredError{requestDigest: reconcile.RequestDigest(), cause: err}
	}
	return err
}

func (c *Composition) prepare(request PreparedRequest) (architecturev2.CurrentResolution, string, string, error) {
	if c == nil || c.service == nil {
		return architecturev2.CurrentResolution{}, "", "", errors.New("Product Runtime composition is not initialized")
	}
	if strings.TrimSpace(request.AuthorityScope) == "" || request.AuthorityScope != strings.TrimSpace(request.AuthorityScope) {
		return architecturev2.CurrentResolution{}, "", "", errors.New("Product Runtime prepared request requires an exact normalized authority scope")
	}
	if strings.TrimSpace(request.WorkspaceRoot) == "" {
		return architecturev2.CurrentResolution{}, "", "", errors.New("Product Runtime prepared request requires a workspace root")
	}
	workspaceRoot, err := filepath.Abs(request.WorkspaceRoot)
	if err != nil {
		return architecturev2.CurrentResolution{}, "", "", fmt.Errorf("resolve Product Runtime workspace root: %w", err)
	}
	workspaceRoot = filepath.Clean(workspaceRoot)
	current, err := c.service.ResolveCurrentScoped(architecturev2.ResolveInput{
		StackSpec: append([]byte(nil), request.StackSpec...), Inventory: append([]byte(nil), request.Inventory...),
	}, request.AuthorityScope)
	if err != nil {
		return architecturev2.CurrentResolution{}, "", "", err
	}
	resolved, err := current.Result()
	if err != nil {
		return architecturev2.CurrentResolution{}, "", "", err
	}
	plan, err := c.service.VerifyCanonicalPlan(resolved.CanonicalPlan)
	if err != nil {
		return architecturev2.CurrentResolution{}, "", "", err
	}
	planPath, _, _ := plan.MetadataPaths(workspaceRoot)
	persisted, err := c.service.ReadCanonicalPlan(planPath)
	if err != nil {
		return architecturev2.CurrentResolution{}, "", "", err
	}
	if err := persisted.VerifyCurrentResolution(resolved.CanonicalPlan); err != nil {
		return architecturev2.CurrentResolution{}, "", "", err
	}
	if err := persisted.VerifyCompatibility(c.versions); err != nil {
		return architecturev2.CurrentResolution{}, "", "", err
	}
	if err := persisted.RequireReady(generationartifact.ExecutionPhaseApply); err != nil {
		return architecturev2.CurrentResolution{}, "", "", err
	}
	canonicalPlan, err := resolvedplan.DecodeCanonicalPlan(persisted.Canonical())
	if err != nil {
		return architecturev2.CurrentResolution{}, "", "", fmt.Errorf("decode verified canonical plan for host-conformance freshness: %w", err)
	}
	if err := resolvedplan.ValidateHostConformanceReceiptsForApply(canonicalPlan, time.Now().UTC()); err != nil {
		return architecturev2.CurrentResolution{}, "", "", err
	}
	return current, workspaceRoot, persisted.OutputRoot(), nil
}

func (c *Composition) withOutputLock(
	workspaceRoot string,
	outputRoot string,
	execute func(*confinedfs.Transaction, *confinedfs.OutputLock) (architecturev2.VerifiedApplyResult, error),
) (result ApplyResult, returnErr error) {
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("open Product Runtime workspace: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return ApplyResult{}, fmt.Errorf("begin Product Runtime workspace transaction: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, transaction.Close()) }()
	if err := architecturev2.RequireNoPendingOutputTransaction(transaction, outputRoot); err != nil {
		return ApplyResult{}, err
	}
	lock, err := transaction.TryAcquireOutputLock(outputRoot)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("acquire Product Runtime output lock: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, lock.Release()) }()
	if execute == nil {
		return ApplyResult{}, errors.New("Product Runtime execution callback is required")
	}
	verified, err := execute(transaction, lock)
	if err != nil {
		return ApplyResult{}, err
	}
	canonical, err := verified.Canonical()
	if err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{hash: verified.ResultHash(), canonical: canonical}, nil
}
