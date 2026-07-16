package architecturev2

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/kombifyio/stackkits/internal/architecturev2/internal/execution"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// CurrentResolution is an opaque proof that one exact plan came from a fresh
// Resolve invocation owned by one governed Architecture v2 generation
// coordinator. Service value copies share that pointer-owned coordinator; its
// zero value is invalid and callers cannot compose it from persisted bytes.
//
// The public Result projection is kept separately from the immutable verified
// plan used for authorization. Mutating a Result returned by Result therefore
// cannot change the plan that may be authorized.
type CurrentResolution struct {
	owner  *generationCoordinator
	plan   generationartifact.VerifiedPlan
	result Result
	key    string
	epoch  uint64
	valid  bool
}

// ResolveCurrent resolves current StackSpec intent plus observed inventory and
// seals the exact canonical output for the later renderer-authorization gate.
// Ordinary Resolve results intentionally cannot be upgraded into this proof.
func (s *Service) ResolveCurrent(input ResolveInput) (CurrentResolution, error) {
	result, err := s.Resolve(input)
	if err != nil {
		return CurrentResolution{}, err
	}
	plan, err := s.VerifyCanonicalPlan(result.CanonicalPlan)
	if err != nil {
		return CurrentResolution{}, resolveError(ErrGenerationAuthorization, "verify the current resolver result against the service authority", err)
	}
	key, err := currentResolutionKey(result)
	if err != nil {
		return CurrentResolution{}, err
	}
	if s.generation == nil {
		return CurrentResolution{}, resolveError(ErrGenerationAuthorization, "Architecture v2 generation coordinator is not initialized", nil)
	}
	epoch := s.generation.issueCurrentGeneration(key, plan.Binding())
	return CurrentResolution{owner: s.generation, plan: plan, result: result, key: key, epoch: epoch, valid: true}, nil
}

// Result returns a defensive projection of the current resolution for plan
// display or persistence. It is not an authorization token.
func (r CurrentResolution) Result() (Result, error) {
	if !r.valid || r.owner == nil {
		return Result{}, resolveError(ErrGenerationAuthorization, "a current resolver result is required", nil)
	}
	canonical := r.plan.Canonical()
	var plan resolvedplan.ResolvedPlan
	if err := json.Unmarshal(canonical, &plan); err != nil {
		return Result{}, resolveError(ErrGenerationAuthorization, "decode the sealed current resolver result", err)
	}
	return Result{
		Plan:          plan,
		CanonicalPlan: canonical,
		PlanHash:      r.result.PlanHash,
	}, nil
}

// GenerationAuthorizationInput identifies the governed deployment workspace
// and exact component identities that must agree with a sealed current
// resolution. The service derives and reads the canonical ResolvedPlan path
// itself; callers cannot substitute another path or pass an in-memory plan as
// persistence evidence.
type GenerationAuthorizationInput struct {
	Current       CurrentResolution
	WorkspaceRoot string
	Versions      generationartifact.ComponentVersions
}

type generationAuthorizationState struct {
	mu      sync.RWMutex
	root    *confinedfs.Root
	closed  bool
	revoked bool
}

// GenerationAuthorization is an opaque Architecture v2 execution session for
// one fresh resolution and the exact workspace handle opened during service
// authorization. Its zero value is invalid, copies share close state, and no
// package outside architecturev2 can mint a non-zero value.
type GenerationAuthorization struct {
	plan       generationartifact.VerifiedPlan
	owner      *generationCoordinator
	state      *generationAuthorizationState
	key        string
	epoch      uint64
	authorized bool
}

// AuthorizeGeneration binds generation to a fresh resolver result issued by
// this service authority's exact generation coordinator. The persisted plan
// must match that result byte for byte before compatibility and readiness run.
func (s *Service) AuthorizeGeneration(input GenerationAuthorizationInput) (GenerationAuthorization, error) {
	if s == nil || s.validator == nil {
		return GenerationAuthorization{}, resolveError(ErrAuthorityLoad, "Architecture v2 plan contract validator is not initialized", nil)
	}
	current := input.Current
	if s.generation == nil || !current.valid || current.owner == nil || current.owner != s.generation {
		return GenerationAuthorization{}, resolveError(ErrGenerationAuthorization, "authorization requires a current resolution issued by this generation coordinator", nil)
	}
	if !s.generation.beginGenerationAuthorization(current.key, current.epoch, current.plan.Binding()) {
		return GenerationAuthorization{}, resolveError(ErrGenerationAuthorization, "current resolution is stale, superseded, or already consumed", nil)
	}
	completed := false
	defer func() {
		if !completed {
			s.generation.finishGenerationAuthorization(current.key, current.epoch, current.plan.Binding(), false)
		}
	}()
	if strings.TrimSpace(input.WorkspaceRoot) == "" || !filepath.IsAbs(input.WorkspaceRoot) || filepath.Clean(input.WorkspaceRoot) != input.WorkspaceRoot {
		return GenerationAuthorization{}, resolveError(ErrGenerationAuthorization, "workspaceRoot must be a non-empty canonical absolute path", nil)
	}
	workspaceRoot, err := confinedfs.Open(input.WorkspaceRoot)
	if err != nil {
		return GenerationAuthorization{}, resolveError(ErrGenerationAuthorization, "open held governed workspace root", err)
	}
	workspaceOwned := true
	defer func() {
		if workspaceOwned {
			_ = workspaceRoot.Close()
		}
	}()
	persistedPlan, err := s.readCurrentPersistedPlan(workspaceRoot, current.plan)
	if err != nil {
		return GenerationAuthorization{}, err
	}
	if err := current.plan.VerifyCurrentResolution(persistedPlan.Canonical()); err != nil {
		return GenerationAuthorization{}, resolveError(ErrGenerationAuthorization, "persisted plan does not exactly match the current resolver result", err)
	}
	if err := current.plan.VerifyCompatibility(input.Versions); err != nil {
		return GenerationAuthorization{}, resolveError(ErrGenerationAuthorization, "current plan is incompatible with the participating components", err)
	}
	if err := current.plan.RequireReady(generationartifact.ExecutionPhaseGeneration); err != nil {
		return GenerationAuthorization{}, resolveError(ErrGenerationAuthorization, "current plan is not ready for generation", err)
	}
	authorization, err := newGenerationAuthorization(current.plan, s.generation, current.key, current.epoch, workspaceRoot)
	if err != nil {
		return GenerationAuthorization{}, resolveError(ErrGenerationAuthorization, "issue held-workspace generation authorization", err)
	}
	workspaceOwned = false
	if !s.generation.finishGenerationAuthorization(current.key, current.epoch, current.plan.Binding(), true) {
		_ = authorization.Close()
		return GenerationAuthorization{}, resolveError(ErrGenerationAuthorization, "current resolution was superseded while authorization was evaluated", nil)
	}
	completed = true
	return authorization, nil
}

func newGenerationAuthorization(plan generationartifact.VerifiedPlan, owner *generationCoordinator, key string, epoch uint64, root *confinedfs.Root) (GenerationAuthorization, error) {
	if len(plan.Canonical()) == 0 || owner == nil || strings.TrimSpace(key) == "" || epoch == 0 || root == nil || root.Name() == "" {
		return GenerationAuthorization{}, generationAuthorizationError(generationartifact.ErrInvalidContract, "generation.authorization", "verified plan, coordinator identity, and held workspace root are required", nil)
	}
	return GenerationAuthorization{
		plan: plan, owner: owner, state: &generationAuthorizationState{root: root},
		key: key, epoch: epoch, authorized: true,
	}, nil
}

// Render runs the pure renderer kernel while holding the current-resolution
// lease. It is a method on the unforgeable Architecture v2 session rather than
// a free renderer function.
func (a GenerationAuthorization) Render(ctx context.Context, registry *architecturev2renderer.Registry) (architecturev2renderer.RenderResult, error) {
	plan, _, release, err := a.acquire("", false)
	if err != nil {
		return architecturev2renderer.RenderResult{}, rendererAuthorizationError("generation authorization is invalid", err)
	}
	defer release()
	return architecturev2renderer.RenderVerifiedPlan(ctx, plan, registry)
}

// RenderAndInstall holds one authorization, current-resolution, and original
// workspace-handle lease across pure rendering and the entire held-root
// stage/verify/swap/rollback transaction.
func (a GenerationAuthorization) RenderAndInstall(ctx context.Context, registry *architecturev2renderer.Registry, options architecturev2renderer.InstallOptions) (architecturev2renderer.InstallResult, error) {
	workspaceRoot, err := execution.ValidateWorkspaceRoot(options.WorkspaceRoot)
	if err != nil {
		return architecturev2renderer.InstallResult{}, err
	}
	plan, workspace, release, err := a.acquire(workspaceRoot, true)
	if err != nil {
		return architecturev2renderer.InstallResult{}, rendererAuthorizationError("generation authorization is invalid for the installation workspace", err)
	}
	defer release()
	result, err := architecturev2renderer.RenderVerifiedPlan(ctx, plan, registry)
	if err != nil {
		return architecturev2renderer.InstallResult{}, err
	}
	options.WorkspaceRoot = workspaceRoot
	return execution.InstallManagedOutput(plan, workspace, result, options)
}

// Install installs a Render-produced result through the same session. The
// result binding and exact workspace handle are revalidated before mutation.
func (a GenerationAuthorization) Install(result architecturev2renderer.RenderResult, options architecturev2renderer.InstallOptions) (architecturev2renderer.InstallResult, error) {
	workspaceRoot, err := execution.ValidateWorkspaceRoot(options.WorkspaceRoot)
	if err != nil {
		return architecturev2renderer.InstallResult{}, err
	}
	plan, workspace, release, err := a.acquire(workspaceRoot, true)
	if err != nil {
		return architecturev2renderer.InstallResult{}, rendererAuthorizationError("generation authorization is invalid for the installation workspace", err)
	}
	defer release()
	options.WorkspaceRoot = workspaceRoot
	return execution.InstallManagedOutput(plan, workspace, result, options)
}

// InstallManagedOutput preserves the explicit old operation name while the
// capability itself, not the renderer package, owns the mutating entry point.
func (a GenerationAuthorization) InstallManagedOutput(result architecturev2renderer.RenderResult, options architecturev2renderer.InstallOptions) (architecturev2renderer.InstallResult, error) {
	return a.Install(result, options)
}

func (a GenerationAuthorization) acquire(expectedWorkspace string, installation bool) (generationartifact.VerifiedPlan, *confinedfs.Transaction, func(), error) {
	if !a.authorized || a.owner == nil || a.state == nil {
		return generationartifact.VerifiedPlan{}, nil, nil, generationAuthorizationError(generationartifact.ErrInvalidContract, "generation.authorization", "valid Architecture v2 authorization is required", nil)
	}
	a.state.mu.RLock()
	if a.state.closed || a.state.root == nil {
		a.state.mu.RUnlock()
		return generationartifact.VerifiedPlan{}, nil, nil, generationAuthorizationError(generationartifact.ErrInvalidContract, "generation.authorization", "authorization is closed", nil)
	}
	releaseCurrent, ok := a.owner.acquireGenerationAuthorization(a.key, a.epoch)
	if !ok {
		a.state.mu.RUnlock()
		return generationartifact.VerifiedPlan{}, nil, nil, generationAuthorizationError(generationartifact.ErrBindingMismatch, "generation.authorization", "authorization was superseded or revoked", nil)
	}
	if err := a.state.root.VerifyPathIdentity(); err != nil {
		releaseCurrent()
		a.state.mu.RUnlock()
		return generationartifact.VerifiedPlan{}, nil, nil, generationAuthorizationError(generationartifact.ErrIO, "generation.authorization.workspaceRoot", "held workspace root identity changed", err)
	}
	if expectedWorkspace != "" && !sameCanonicalWorkspace(a.state.root.Name(), expectedWorkspace) {
		releaseCurrent()
		a.state.mu.RUnlock()
		return generationartifact.VerifiedPlan{}, nil, nil, generationAuthorizationError(generationartifact.ErrBindingMismatch, "generation.authorization.workspaceRoot", "authorization belongs to a different workspace root", nil)
	}
	var workspace *confinedfs.Transaction
	if installation {
		if expectedWorkspace == "" {
			releaseCurrent()
			a.state.mu.RUnlock()
			return generationartifact.VerifiedPlan{}, nil, nil, generationAuthorizationError(generationartifact.ErrInvalidContract, "generation.authorization.workspaceRoot", "installation requires the exact canonical workspace root", nil)
		}
		var err error
		workspace, err = a.state.root.BeginTransaction()
		if err != nil {
			releaseCurrent()
			a.state.mu.RUnlock()
			return generationartifact.VerifiedPlan{}, nil, nil, generationAuthorizationError(generationartifact.ErrIO, "generation.authorization.workspaceRoot", "borrow held workspace transaction", err)
		}
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			if workspace != nil {
				_ = workspace.Close()
			}
			releaseCurrent()
			a.state.mu.RUnlock()
		})
	}
	return a.plan, workspace, release, nil
}

// Close waits for active Render/Install leases, revokes this exact resolution,
// and releases the original held workspace. It is idempotent across copies.
func (a GenerationAuthorization) Close() error {
	if a.state == nil {
		return nil
	}
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	if a.state.closed {
		return nil
	}
	a.state.closed = true
	if !a.state.revoked && a.owner != nil {
		a.owner.revokeGenerationAuthorization(a.key, a.epoch)
		a.state.revoked = true
	}
	if a.state.root == nil {
		return nil
	}
	err := a.state.root.Close()
	a.state.root = nil
	if err != nil {
		return generationAuthorizationError(generationartifact.ErrIO, "generation.authorization.workspaceRoot", "close held workspace root", err)
	}
	return nil
}

func sameCanonicalWorkspace(bound, expected string) bool {
	if strings.TrimSpace(expected) == "" || !filepath.IsAbs(expected) || filepath.Clean(expected) != expected {
		return false
	}
	return bound == expected
}

func generationAuthorizationError(code generationartifact.ErrorCode, location, message string, err error) error {
	return &generationartifact.Error{Code: code, Path: location, Message: message, Err: err}
}

func rendererAuthorizationError(message string, err error) error {
	return &architecturev2renderer.Error{Code: architecturev2renderer.ErrAuthorization, Path: "generation.authorization", Message: message, Err: err}
}

func (s *Service) readCurrentPersistedPlan(workspaceRoot *confinedfs.Root, plan generationartifact.VerifiedPlan) (generationartifact.VerifiedPlan, error) {
	planPath, _, _ := plan.MetadataPaths(workspaceRoot.Name())
	relative, err := confinedPlanRelativePath(workspaceRoot.Name(), planPath)
	if err != nil {
		return generationartifact.VerifiedPlan{}, err
	}
	view, err := workspaceRoot.View(".")
	if err != nil {
		return generationartifact.VerifiedPlan{}, resolveError(ErrGenerationAuthorization, "open governed workspace view", err)
	}
	canonical, identity, err := readStablePersistedPlanFile(view, filepath.ToSlash(relative))
	if err != nil {
		return generationartifact.VerifiedPlan{}, err
	}
	if err := confirmPersistedPlanPathIdentity(view, filepath.ToSlash(relative), identity); err != nil {
		return generationartifact.VerifiedPlan{}, err
	}
	persisted, err := s.VerifyCanonicalPlan(canonical)
	if err != nil {
		return generationartifact.VerifiedPlan{}, resolveError(ErrGenerationAuthorization, "verify persisted governed plan", err)
	}
	return persisted, nil
}

func confinedPlanRelativePath(workspaceRoot, planPath string) (string, error) {
	relative, err := filepath.Rel(workspaceRoot, planPath)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", resolveError(ErrGenerationAuthorization, "derive confined canonical ResolvedPlan path", err)
	}
	return relative, nil
}

func readStablePersistedPlanFile(view confinedfs.View, relative string) ([]byte, os.FileInfo, error) {
	file, err := view.Open(filepath.ToSlash(relative))
	if err != nil {
		return nil, nil, resolveError(ErrGenerationAuthorization, "open persisted governed plan", err)
	}
	before, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, resolveError(ErrGenerationAuthorization, "stat persisted governed plan", err)
	}
	if runtime.GOOS != "windows" && before.Mode().Perm() != 0o600 {
		_ = file.Close()
		return nil, nil, resolveError(ErrGenerationAuthorization, "persisted governed plan must have mode 0600", nil)
	}
	canonical, readErr := io.ReadAll(file)
	after, statErr := file.Stat()
	closeErr := file.Close()
	if readErr != nil || statErr != nil || closeErr != nil {
		return nil, nil, resolveError(ErrGenerationAuthorization, "read stable persisted governed plan", firstError(readErr, statErr, closeErr))
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, nil, resolveError(ErrGenerationAuthorization, "persisted governed plan changed while read", nil)
	}
	return canonical, after, nil
}

func confirmPersistedPlanPathIdentity(view confinedfs.View, relative string, expected os.FileInfo) error {
	currentFile, err := view.Open(relative)
	if err != nil {
		return resolveError(ErrGenerationAuthorization, "reopen persisted governed plan identity", err)
	}
	currentInfo, statErr := currentFile.Stat()
	closeErr := currentFile.Close()
	if statErr != nil || closeErr != nil || !os.SameFile(expected, currentInfo) {
		return resolveError(ErrGenerationAuthorization, "persisted governed plan path changed while read", firstError(statErr, closeErr))
	}
	return nil
}

func firstError(values ...error) error {
	for _, err := range values {
		if err != nil {
			return err
		}
	}
	return nil
}

func currentResolutionKey(result Result) (string, error) {
	stackID, ok := result.Plan["stackId"].(string)
	if !ok || stackID == "" {
		return "", resolveError(ErrGenerationAuthorization, "current resolver result has no stackId identity", nil)
	}
	return stackID, nil
}

type generationResolutionState struct {
	epoch   uint64
	stage   uint8
	binding generationartifact.PlanBinding
}

const (
	generationStageIssued uint8 = iota + 1
	generationStageAuthorizing
	generationStageAuthorized
)

type generationCoordinator struct {
	mu    sync.Mutex
	slots map[string]*generationSlot
}

type generationSlot struct {
	mu       sync.RWMutex
	sequence uint64
	state    generationResolutionState
	occupied bool
}

func newGenerationCoordinator() (*generationCoordinator, error) {
	return &generationCoordinator{slots: make(map[string]*generationSlot)}, nil
}

func (c *generationCoordinator) slot(key string) *generationSlot {
	if c == nil || strings.TrimSpace(key) == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.slots == nil {
		c.slots = make(map[string]*generationSlot)
	}
	slot := c.slots[key]
	if slot == nil {
		slot = &generationSlot{}
		c.slots[key] = slot
	}
	return slot
}

func (c *generationCoordinator) existingSlot(key string) *generationSlot {
	if c == nil || strings.TrimSpace(key) == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.slots[key]
}

func (c *generationCoordinator) issueCurrentGeneration(key string, binding generationartifact.PlanBinding) uint64 {
	slot := c.slot(key)
	if slot == nil {
		return 0
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	slot.sequence++
	slot.state = generationResolutionState{epoch: slot.sequence, stage: generationStageIssued, binding: binding}
	slot.occupied = true
	return slot.sequence
}

// beginGenerationAuthorization consumes the current-resolution capability.
// A copied CurrentResolution cannot race or replay a second authorization.
func (c *generationCoordinator) beginGenerationAuthorization(key string, epoch uint64, binding generationartifact.PlanBinding) bool {
	slot := c.existingSlot(key)
	if slot == nil {
		return false
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	state := slot.state
	if !slot.occupied || state.epoch != epoch || state.stage != generationStageIssued || state.binding != binding {
		return false
	}
	state.stage = generationStageAuthorizing
	slot.state = state
	return true
}

func (c *generationCoordinator) finishGenerationAuthorization(key string, epoch uint64, binding generationartifact.PlanBinding, authorized bool) bool {
	slot := c.existingSlot(key)
	if slot == nil {
		return false
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	state := slot.state
	if !slot.occupied || state.epoch != epoch || state.stage != generationStageAuthorizing || state.binding != binding {
		return false
	}
	if !authorized {
		slot.occupied = false
		slot.state = generationResolutionState{}
		return true
	}
	state.stage = generationStageAuthorized
	slot.state = state
	return true
}

func (c *generationCoordinator) acquireGenerationAuthorization(key string, epoch uint64) (func(), bool) {
	slot := c.existingSlot(key)
	if slot == nil {
		return nil, false
	}
	slot.mu.RLock()
	state := slot.state
	if !slot.occupied || state.epoch != epoch || state.stage != generationStageAuthorized {
		slot.mu.RUnlock()
		return nil, false
	}
	return slot.mu.RUnlock, true
}

func (c *generationCoordinator) revokeGenerationAuthorization(key string, epoch uint64) {
	slot := c.existingSlot(key)
	if slot == nil {
		return
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.occupied && slot.state.epoch == epoch {
		slot.occupied = false
		slot.state = generationResolutionState{}
	}
}
