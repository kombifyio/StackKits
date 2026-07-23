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
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeapplyv2"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productApplyRecoveryAPIVersion = "stackkits.product-apply-recovery/v1alpha1"

// ProductApplyRecoveryStore retains the StackKits-facing name for the shared
// opaque recovery-custody SPI. StackKits validates before Save and after Load;
// the store owns only atomic persistence and exact-digest lookup.
type ProductApplyRecoveryStore = runtimeapply.RecoveryStore

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
	return canonicalProductApplyRecoveryCapsule(productApplyRecoveryCapsule{
		APIVersion: productApplyRecoveryAPIVersion, OutputRoot: outputRoot,
		ValidUntil: validUntil.Format(time.RFC3339Nano), Request: request, Shared: shared,
	})
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
	reconstructed, err := sharedExecutionRequest(capsule.Request)
	if err != nil {
		return fmt.Errorf("reconstruct Product Apply recovery shared request: %w", err)
	}
	if !reflect.DeepEqual(reconstructed, capsule.Shared) || capsule.Shared.Executor.ID != capsule.Request.Executor.ID ||
		capsule.Shared.Executor.Version != capsule.Request.Executor.Version || capsule.Shared.Executor.Digest != capsule.Request.Executor.Digest {
		return errors.New("Product Apply recovery capsule does not bind the exact internal and Shared request")
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
	capsule, err := parseProductApplyRecoveryCapsule(canonical)
	if err != nil {
		return nil, err
	}
	if capsule.Shared.RequestDigest != digest {
		return nil, errors.New("Product Apply recovery store returned a foreign request")
	}
	return append([]byte(nil), canonical...), nil
}
