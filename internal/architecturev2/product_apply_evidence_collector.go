package architecturev2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/kombifyio/stackkits/internal/applyevidencev2"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const productApplyCollectedEvidenceMaxBytes = 4 << 20

// ProductApplyEvidenceCollectionRequest is the shared provider-free handoff to
// an integration-owned host/device collector. Private signing material, host
// inspection, transport, and credential custody remain inside the collector.
type ProductApplyEvidenceCollectionRequest = applyevidence.CollectionRequest

// ProductApplyEvidenceCollector retains the StackKits-facing name for the
// shared provider-neutral producer SPI. It is configured on the product
// service, never supplied by an Apply request. StackKits still authenticates
// every returned receipt against service-owned public trust.
type ProductApplyEvidenceCollector = applyevidence.Collector

func newProductApplyEvidenceCollectionRequest(plan generationartifact.VerifiedPlan, manifest generationartifact.ArtifactManifest, executor generationartifact.ApplyExecutorIdentity, at time.Time) (ProductApplyEvidenceCollectionRequest, error) {
	if at.IsZero() || at.Location() != time.UTC {
		return ProductApplyEvidenceCollectionRequest{}, fmt.Errorf("product Apply evidence collection requires one canonical UTC evaluation instant")
	}
	request, err := plan.ApplyEvidenceRequest()
	if err != nil {
		return ProductApplyEvidenceCollectionRequest{}, err
	}
	canonical, err := resolvedplan.CanonicalJSON(request)
	if err != nil {
		return ProductApplyEvidenceCollectionRequest{}, err
	}
	var sharedRequest applyevidence.Request
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&sharedRequest); err != nil {
		return ProductApplyEvidenceCollectionRequest{}, fmt.Errorf("project shared Apply evidence request: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return ProductApplyEvidenceCollectionRequest{}, fmt.Errorf("project shared Apply evidence request: %w", err)
	}
	manifestHash, err := manifest.Hash()
	if err != nil {
		return ProductApplyEvidenceCollectionRequest{}, err
	}
	collection, err := applyevidence.NewCollectionRequest(
		sharedRequest,
		manifestHash,
		applyevidence.ExecutorIdentity{ID: executor.ID, Version: executor.Version, Digest: executor.Digest},
		at,
	)
	if err != nil {
		return ProductApplyEvidenceCollectionRequest{}, fmt.Errorf("construct shared Apply evidence collection request: %w", err)
	}
	return collection, nil
}

func collectProductApplyEvidence(ctx context.Context, collector ProductApplyEvidenceCollector, request ProductApplyEvidenceCollectionRequest) (bundle []byte, err error) {
	if ctx == nil {
		return nil, fmt.Errorf("product Apply evidence collection requires a non-nil context")
	}
	if nilProductApplyEvidenceCollector(collector) {
		return nil, fmt.Errorf("product Apply evidence collector is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("product Apply evidence collection context: %w", err)
	}
	if err := applyevidence.ValidateCollectionRequest(request); err != nil {
		return nil, fmt.Errorf("product Apply evidence collection request: %w", err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			bundle = nil
			err = fmt.Errorf("product Apply evidence collector panic: %v", recovered)
		}
	}()
	bundle, err = collector.CollectApplyEvidence(ctx, cloneProductApplyEvidenceCollectionRequest(request))
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("product Apply evidence collection context: %w", err)
	}
	if len(bundle) == 0 || len(bundle) > productApplyCollectedEvidenceMaxBytes {
		return nil, fmt.Errorf("product Apply evidence collector returned %d bytes; require 1..%d", len(bundle), productApplyCollectedEvidenceMaxBytes)
	}
	return append([]byte(nil), bundle...), nil
}

func productApplyEvidenceBytes(ctx context.Context, collector ProductApplyEvidenceCollector, callerBundle []byte, request ProductApplyEvidenceCollectionRequest) ([]byte, error) {
	if nilProductApplyEvidenceCollector(collector) {
		return append([]byte(nil), callerBundle...), nil
	}
	if len(callerBundle) != 0 {
		return nil, fmt.Errorf("caller evidence is forbidden when the product service owns collection")
	}
	return collectProductApplyEvidence(ctx, collector, request)
}

func cloneProductApplyEvidenceCollectionRequest(input ProductApplyEvidenceCollectionRequest) ProductApplyEvidenceCollectionRequest {
	return applyevidence.CloneCollectionRequest(input)
}

func nilProductApplyEvidenceCollector(collector ProductApplyEvidenceCollector) bool {
	if collector == nil {
		return true
	}
	value := reflect.ValueOf(collector)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
