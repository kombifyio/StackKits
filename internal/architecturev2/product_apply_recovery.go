package architecturev2

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeapplyv2"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productApplyRecoveryAPIVersion = "stackkits.product-apply-recovery/v1alpha1"

// ProductApplyRecoveryStore retains the StackKits-facing name for the shared
// opaque recovery-custody SPI. StackKits validates before Save and after Load;
// the store owns only atomic persistence and exact-digest lookup.
type ProductApplyRecoveryStore = runtimeapply.RecoveryStore

// ProductAppliedRuntimeCustody is an immutable recovery capsule bound to a
// verified Apply. Path and canonical bytes are projections
// for later signed custody capture; neither projection grants a new mutation.
type ProductAppliedRuntimeCustody struct {
	path      string
	canonical []byte
	request   runtimeexecutor.ExecutionRequest
}

// Path returns the exact journal-relative capsule path.
func (c ProductAppliedRuntimeCustody) Path() string { return c.path }

// Canonical returns defensive copies of the exact canonical capsule bytes.
func (c ProductAppliedRuntimeCustody) Canonical() []byte { return append([]byte(nil), c.canonical...) }

// Request returns a defensive clone of the validated shared request.
func (c ProductAppliedRuntimeCustody) Request() runtimeexecutor.ExecutionRequest {
	return runtimeexecutor.CloneExecutionRequest(c.request)
}

type productApplyRecoveryCapsule struct {
	APIVersion string                           `json:"api_version"`
	OutputRoot string                           `json:"output_root"`
	ValidUntil string                           `json:"valid_until"`
	Request    applyRuntimeExecutionRequest     `json:"request"`
	Shared     runtimeexecutor.ExecutionRequest `json:"shared_request"`
}

type productApplyRecoveryCustodian interface {
	storeProductApplyRecovery(context.Context, string, []byte) error
}

type applyRuntimeRecoveryPreparer interface {
	PrepareProductApplyRecovery(context.Context, applyRuntimeExecutionRequest, string, time.Time) error
}

func newProductApplyRecoveryCapsule(request applyRuntimeExecutionRequest, shared runtimeexecutor.ExecutionRequest, outputRoot string, validUntil time.Time) ([]byte, error) {
	_, boundShared, err := bindProductApplyRecoveryRequestChannels(request, shared)
	if err != nil {
		return nil, err
	}
	return canonicalProductApplyRecoveryCapsule(productApplyRecoveryCapsule{
		APIVersion: productApplyRecoveryAPIVersion, OutputRoot: outputRoot,
		ValidUntil: validUntil.Format(time.RFC3339Nano), Request: request, Shared: boundShared,
	})
}

func bindProductApplyRecoveryRequestChannels(
	request applyRuntimeExecutionRequest,
	shared runtimeexecutor.ExecutionRequest,
) (applyRuntimeExecutionRequest, runtimeexecutor.ExecutionRequest, error) {
	bound := request
	bound.Requirements.Hosts = append([]generationartifact.ApplyHostRequirement(nil), request.Requirements.Hosts...)
	boundShared := runtimeexecutor.CloneExecutionRequest(shared)
	hostIndexes := make(map[string]int, len(bound.Requirements.Hosts))
	for index, host := range bound.Requirements.Hosts {
		if _, duplicate := hostIndexes[host.NodeRef]; duplicate {
			return applyRuntimeExecutionRequest{}, runtimeexecutor.ExecutionRequest{}, fmt.Errorf(
				"Product Apply recovery node %q has multiple governed hosts", host.NodeRef,
			)
		}
		hostIndexes[host.NodeRef] = index
	}
	sharedChanged := false
	for index := range boundShared.RuntimeTargets {
		target := &boundShared.RuntimeTargets[index]
		if len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 {
			return applyRuntimeExecutionRequest{}, runtimeexecutor.ExecutionRequest{}, fmt.Errorf(
				"Product Apply recovery shared target %q has no exact Site/node route", target.RequirementID,
			)
		}
		hostIndex, ok := hostIndexes[target.NodeRefs[0]]
		if !ok {
			return applyRuntimeExecutionRequest{}, runtimeexecutor.ExecutionRequest{}, fmt.Errorf(
				"Product Apply recovery shared target %q has no governed host", target.RequirementID,
			)
		}
		host := &bound.Requirements.Hosts[hostIndex]
		if host.SiteRef != target.SiteRefs[0] {
			return applyRuntimeExecutionRequest{}, runtimeexecutor.ExecutionRequest{}, fmt.Errorf(
				"Product Apply recovery host %q conflicts with the sealed Site route", host.NodeRef,
			)
		}
		hostChannel := strings.TrimSpace(host.ExecutionChannelRef)
		targetChannel := strings.TrimSpace(target.ExecutionChannelRef)
		if hostChannel == "" && targetChannel == "" {
			return applyRuntimeExecutionRequest{}, runtimeexecutor.ExecutionRequest{}, fmt.Errorf(
				"Product Apply recovery shared target %q has no exact execution route", target.RequirementID,
			)
		}
		if hostChannel != "" && targetChannel != "" && hostChannel != targetChannel {
			return applyRuntimeExecutionRequest{}, runtimeexecutor.ExecutionRequest{}, fmt.Errorf(
				"Product Apply recovery host %q conflicts with the sealed execution route", host.NodeRef,
			)
		}
		if targetChannel == "" {
			target.ExecutionChannelRef = hostChannel
			targetChannel = hostChannel
			sharedChanged = true
		}
		if hostChannel == "" {
			host.ExecutionChannelRef = targetChannel
		}
	}
	if sharedChanged {
		boundShared.RequestDigest = ""
		var err error
		boundShared, err = runtimeexecutor.SealRequest(boundShared)
		if err != nil {
			return applyRuntimeExecutionRequest{}, runtimeexecutor.ExecutionRequest{}, fmt.Errorf(
				"seal Product Apply recovery shared route: %w", err,
			)
		}
	}
	return bound, boundShared, nil
}

func canonicalProductApplyRecoveryCapsule(capsule productApplyRecoveryCapsule) ([]byte, error) {
	if err := validateProductApplyRecoveryCapsule(capsule); err != nil {
		return nil, err
	}
	canonical, err := resolvedplan.CanonicalJSON(capsule)
	if err != nil {
		return nil, fmt.Errorf("canonicalize Product Apply recovery capsule: %w", err)
	}
	return canonical, nil
}

func parseProductApplyRecoveryCapsule(data []byte) (productApplyRecoveryCapsule, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var capsule productApplyRecoveryCapsule
	if err := decoder.Decode(&capsule); err != nil {
		return productApplyRecoveryCapsule{}, fmt.Errorf("decode Product Apply recovery capsule: %w", err)
	}
	sealedShared, err := runtimeexecutor.SealRequest(capsule.Shared)
	if err != nil {
		return productApplyRecoveryCapsule{}, fmt.Errorf("normalize Product Apply recovery shared request: %w", err)
	}
	if sealedShared.RequestDigest != capsule.Shared.RequestDigest {
		return productApplyRecoveryCapsule{}, errors.New("normalized Product Apply recovery request digest changed")
	}
	capsule.Shared = sealedShared
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return productApplyRecoveryCapsule{}, errors.New("Product Apply recovery capsule contains multiple JSON values")
		}
		return productApplyRecoveryCapsule{}, fmt.Errorf("decode trailing Product Apply recovery data: %w", err)
	}
	canonical, err := canonicalProductApplyRecoveryCapsule(capsule)
	if err != nil {
		return productApplyRecoveryCapsule{}, err
	}
	if subtle.ConstantTimeCompare(data, canonical) != 1 {
		return productApplyRecoveryCapsule{}, errors.New("Product Apply recovery capsule is not canonical JSON")
	}
	return capsule, nil
}

func validateProductApplyRecoveryCapsule(capsule productApplyRecoveryCapsule) error {
	if capsule.APIVersion != productApplyRecoveryAPIVersion || strings.TrimSpace(capsule.OutputRoot) == "" || capsule.OutputRoot != strings.TrimSpace(capsule.OutputRoot) {
		return errors.New("Product Apply recovery capsule identity is invalid")
	}
	if equal, err := confinedfs.OutputLockRootsEqual(capsule.OutputRoot, capsule.OutputRoot); err != nil || !equal {
		return errors.New("Product Apply recovery capsule output root is invalid")
	}
	validUntil, err := time.Parse(time.RFC3339Nano, capsule.ValidUntil)
	if err != nil || validUntil.Location() != time.UTC || validUntil.Format(time.RFC3339Nano) != capsule.ValidUntil ||
		capsule.Request.ExecutionAt.IsZero() || capsule.Request.ExecutionAt.Location() != time.UTC ||
		!capsule.Request.ExecutionAt.Before(validUntil) {
		return errors.New("Product Apply recovery capsule validity is invalid")
	}
	if err := capsule.Shared.Validate(); err != nil {
		return fmt.Errorf("validate Product Apply recovery shared request: %w", err)
	}
	boundRequest, boundShared, err := bindProductApplyRecoveryRequestChannels(capsule.Request, capsule.Shared)
	if err != nil {
		return fmt.Errorf("bind Product Apply recovery shared route: %w", err)
	}
	reconstructed, err := sharedExecutionRequest(boundRequest)
	if err != nil {
		return fmt.Errorf("reconstruct Product Apply recovery shared request: %w", err)
	}
	if !reflect.DeepEqual(boundShared, capsule.Shared) || !reflect.DeepEqual(reconstructed, capsule.Shared) ||
		capsule.Shared.Executor.ID != capsule.Request.Executor.ID ||
		capsule.Shared.Executor.Version != capsule.Request.Executor.Version || capsule.Shared.Executor.Digest != capsule.Request.Executor.Digest {
		return errors.New("Product Apply recovery capsule does not bind the exact internal and Shared request")
	}
	return nil
}

func validateProductApplyRecoveryCanonicalSize(canonical []byte) error {
	if len(canonical) > productApplyRecoveryMaxBytes {
		return fmt.Errorf("Product Apply recovery capsule exceeds %d bytes", productApplyRecoveryMaxBytes)
	}
	return nil
}

// LoadVerifiedAppliedRuntimeCustody loads the exact capsule bound to the
// verified plan and Apply result. The historical capsule expiry is deliberately
// not consulted here: expiry controls fresh Apply authorization, while this
// API only returns previously verified custody for recovery capture.
func (j *ProductApplyFileJournal) LoadVerifiedAppliedRuntimeCustody(
	ctx context.Context,
	plan generationartifact.VerifiedPlan,
	applied VerifiedApplyResult,
) (ProductAppliedRuntimeCustody, error) {
	if err := validateProductApplyJournalContext(ctx); err != nil {
		return ProductAppliedRuntimeCustody{}, err
	}
	if len(plan.Canonical()) == 0 {
		return ProductAppliedRuntimeCustody{}, errors.New("verified Apply plan is required for runtime custody")
	}
	if _, err := applied.Canonical(); err != nil {
		return ProductAppliedRuntimeCustody{}, fmt.Errorf("verified Apply result is required for runtime custody: %w", err)
	}
	if strings.TrimSpace(applied.envelope.SharedRequestDigest) == "" || !validApplySHA256(applied.envelope.SharedRequestDigest) {
		return ProductAppliedRuntimeCustody{}, errors.New("verified Apply result has no valid shared request digest")
	}
	recoveryPath, canonical, capsule, err := j.loadProductApplyRecovery(ctx, applied.envelope.SharedRequestDigest)
	if err != nil {
		return ProductAppliedRuntimeCustody{}, err
	}
	if err := validateProductAppliedRuntimeCustodyBinding(plan, applied, capsule); err != nil {
		return ProductAppliedRuntimeCustody{}, err
	}
	return ProductAppliedRuntimeCustody{
		path: recoveryPath, canonical: append([]byte(nil), canonical...),
		request: runtimeexecutor.CloneExecutionRequest(capsule.Shared),
	}, nil
}

func validateProductAppliedRuntimeCustodyBinding(
	plan generationartifact.VerifiedPlan,
	applied VerifiedApplyResult,
	capsule productApplyRecoveryCapsule,
) error {
	envelope := applied.envelope
	planBinding := plan.Binding()
	if capsule.Request.Binding != planBinding || envelope.Binding != planBinding {
		return errors.New("Product Apply recovery capsule is bound to a foreign plan")
	}
	if capsule.Request.ManifestHash != envelope.ManifestHash ||
		capsule.Request.GenerationReceiptHash != envelope.GenerationReceiptHash ||
		capsule.Request.RequirementsHash != envelope.RequirementsHash ||
		capsule.Request.EvidenceBundleHash != envelope.EvidenceBundleHash ||
		capsule.Request.ArtifactSetHash != envelope.ArtifactSetHash {
		return errors.New("Product Apply recovery capsule does not match the verified Apply envelope")
	}
	if capsule.Shared.PlanHash != envelope.Binding.PlanHash ||
		capsule.Shared.ManifestHash != envelope.ManifestHash ||
		capsule.Shared.GenerationReceiptHash != envelope.GenerationReceiptHash ||
		capsule.Shared.RequirementsHash != envelope.RequirementsHash ||
		capsule.Shared.EvidenceBundleHash != envelope.EvidenceBundleHash ||
		capsule.Shared.ArtifactSetHash != envelope.SharedArtifactSetHash ||
		capsule.Shared.RequestDigest != envelope.SharedRequestDigest {
		return errors.New("Product Apply recovery shared request does not match the verified Apply envelope")
	}
	if capsule.Request.Executor != envelope.Executor ||
		capsule.Shared.Executor.ID != envelope.Executor.ID ||
		capsule.Shared.Executor.Version != envelope.Executor.Version ||
		capsule.Shared.Executor.Digest != envelope.Executor.Digest {
		return errors.New("Product Apply recovery capsule executor does not match the verified Apply envelope")
	}
	rootEqual, err := confinedfs.OutputLockRootsEqual(capsule.OutputRoot, plan.OutputRoot())
	if err != nil || !rootEqual || capsule.OutputRoot != plan.OutputRoot() {
		return errors.New("Product Apply recovery capsule output root does not match the verified plan")
	}
	requirementsEqual, err := canonicalProductApplyRecoveryEqual(capsule.Request.Requirements, plan.ApplyRequirements())
	if err != nil || !requirementsEqual {
		return errors.New("Product Apply recovery capsule requirements do not match the verified plan")
	}
	appliedAt, err := time.Parse(time.RFC3339Nano, envelope.AppliedAt)
	if err != nil || appliedAt.Location() != time.UTC || appliedAt.Format(time.RFC3339Nano) != envelope.AppliedAt ||
		capsule.Request.ExecutionAt.Location() != time.UTC ||
		capsule.Request.ExecutionAt.Format(time.RFC3339Nano) != envelope.AppliedAt ||
		!capsule.Request.ExecutionAt.Equal(appliedAt) {
		return errors.New("Product Apply recovery capsule execution time does not match the verified Apply envelope")
	}
	return nil
}

func safeSaveProductApplyRecovery(store ProductApplyRecoveryStore, ctx context.Context, digest string, canonical []byte) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("Product Apply recovery store panicked")
		}
	}()
	if nilProductRuntimeOwnerValue(store) {
		return errors.New("Product Apply recovery store is missing")
	}
	if err := validateProductApplyRecoveryCanonicalSize(canonical); err != nil {
		return err
	}
	if _, err := parseProductApplyRecoveryCapsule(canonical); err != nil {
		return err
	}
	if err := store.SaveApplyRecovery(ctx, digest, append([]byte(nil), canonical...)); err != nil {
		return err
	}
	loaded, err := store.LoadApplyRecovery(ctx, digest)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(loaded, canonical) != 1 {
		return errors.New("Product Apply recovery store readback differs from the saved capsule")
	}
	_, err = parseProductApplyRecoveryCapsule(loaded)
	return err
}

func safeLoadProductApplyRecovery(store ProductApplyRecoveryStore, ctx context.Context, digest string) (canonical []byte, err error) {
	defer func() {
		if recover() != nil {
			canonical = nil
			err = errors.New("Product Apply recovery store panicked")
		}
	}()
	if nilProductRuntimeOwnerValue(store) {
		return nil, errors.New("Product Apply recovery store is missing")
	}
	canonical, err = store.LoadApplyRecovery(ctx, digest)
	if err != nil {
		return nil, err
	}
	if err := validateProductApplyRecoveryCanonicalSize(canonical); err != nil {
		return nil, err
	}
	capsule, err := parseProductApplyRecoveryCapsule(canonical)
	if err != nil {
		return nil, err
	}
	if capsule.Shared.RequestDigest != digest {
		return nil, errors.New("Product Apply recovery store returned a foreign request")
	}
	return append([]byte(nil), canonical...), nil
}
