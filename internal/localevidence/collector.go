package localevidence

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/applyevidencev2"
)

// producerID is the stable provider-neutral identity of this producer. It must
// satisfy the applyevidence id pattern (lowercase, hyphenated).
const producerID = "stackkit-local-owner"

// fallbackProducerVersion is used when the binary carries no release version.
// applyevidence requires a semver-shaped producer version, so "dev" cannot be
// passed through unchanged.
const fallbackProducerVersion = "0.0.0-dev"

// observationValidity bounds how long one collected observation may be
// presented as current. applyevidence caps this at MaxValidity; staying well
// under it keeps a stalled Apply from consuming stale evidence.
const observationValidity = 5 * time.Minute

// collectionClockSkew bounds the construction-owned authority/collector clock
// handoff. The receipt uses the request's exact evaluatedAt because the same
// instant is used for immediate verification; the collector first proves that
// instant is current according to its own clock.
const collectionClockSkew = 30 * time.Second

var semverShape = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)

// Observer gathers the facts that justify one expectation on this host. It
// returns a deterministic, sorted fact set; the collector digests it into the
// typed observation reference. Returning an error fails the whole collection
// closed, which is the correct outcome for anything this host cannot prove.
type Observer interface {
	Observe(ctx context.Context, expectation applyevidence.Expectation) (map[string]string, error)
}

// ObserverFunc adapts a function to Observer.
type ObserverFunc func(context.Context, applyevidence.Expectation) (map[string]string, error)

// Observe implements Observer.
func (f ObserverFunc) Observe(ctx context.Context, expectation applyevidence.Expectation) (map[string]string, error) {
	return f(ctx, expectation)
}

// OwnerCollector is the local, owner-anchored applyevidence.Collector.
type OwnerCollector struct {
	key       OwnerKey
	version   string
	observers map[string]Observer
	now       func() time.Time
}

// CollectorConfig configures one workspace-scoped collector.
type CollectorConfig struct {
	// Key is the established local owner signing identity.
	Key OwnerKey
	// Version is the running StackKits version; normalised to semver shape.
	Version string
	// Observers is keyed by applyevidence requirement kind. A kind with no
	// registered observer fails closed.
	Observers map[string]Observer
	// Now supplies the clock; defaults to time.Now.
	Now func() time.Time
}

// NewOwnerCollector builds a collector bound to one local owner identity.
func NewOwnerCollector(config CollectorConfig) (*OwnerCollector, error) {
	if config.Key.KeyID == "" || len(config.Key.private) == 0 {
		return nil, errors.New("localevidence: collector requires an established owner evidence key")
	}
	if len(config.Observers) == 0 {
		return nil, errors.New("localevidence: collector requires at least one requirement observer")
	}
	version := strings.TrimSpace(config.Version)
	if !semverShape.MatchString(version) {
		version = fallbackProducerVersion
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	observers := make(map[string]Observer, len(config.Observers))
	for kind, observer := range config.Observers {
		if observer == nil {
			return nil, fmt.Errorf("localevidence: observer for requirement kind %q is nil", kind)
		}
		observers[kind] = observer
	}
	return &OwnerCollector{key: config.Key, version: version, observers: observers, now: now}, nil
}

// ProducerTrust returns the public half of this construction-owned producer.
// It contains no private material and is used by the product composition root
// to verify the exact collector it installs.
func (c *OwnerCollector) ProducerTrust() (applyevidence.Producer, []byte, error) {
	if c == nil || c.key.KeyID == "" || len(c.key.Public()) != ed25519.PublicKeySize {
		return applyevidence.Producer{}, nil, errors.New("localevidence: collector has no valid producer trust")
	}
	return applyevidence.Producer{ID: producerID, Version: c.version, KeyID: c.key.KeyID},
		append([]byte(nil), c.key.Public()...), nil
}

// CollectApplyEvidence implements applyevidence.Collector. It answers every
// expectation in the request from locally gathered facts, signs each receipt
// with the owner key, and returns one canonical sealed bundle.
func (c *OwnerCollector) CollectApplyEvidence(ctx context.Context, collection applyevidence.CollectionRequest) ([]byte, error) {
	if c == nil {
		return nil, errors.New("localevidence: collector is nil")
	}
	if err := applyevidence.ValidateCollectionRequest(collection); err != nil {
		return nil, fmt.Errorf("localevidence: reject collection request: %w", err)
	}

	producer := applyevidence.Producer{ID: producerID, Version: c.version, KeyID: c.key.KeyID}
	producerNow := c.now().UTC()
	if collection.EvaluatedAt.Before(producerNow.Add(-collectionClockSkew)) ||
		collection.EvaluatedAt.After(producerNow.Add(collectionClockSkew)) {
		return nil, fmt.Errorf("localevidence: collection evaluatedAt is outside the local producer clock window")
	}
	observedAt := collection.EvaluatedAt
	validUntil := observedAt.Add(observationValidity)

	receipts := make([]applyevidence.Receipt, 0, len(collection.Request.Expectations))
	for _, expectation := range collection.Request.Expectations {
		observer, known := c.observers[expectation.RequirementKind]
		if !known {
			return nil, fmt.Errorf(
				"localevidence: requirement kind %q cannot be observed on this host; refusing to sign unobserved evidence for %q",
				expectation.RequirementKind, expectation.RequirementID,
			)
		}
		facts, err := observer.Observe(ctx, expectation)
		if err != nil {
			return nil, fmt.Errorf(
				"localevidence: observe %s/%s: %w",
				expectation.RequirementKind, expectation.RequirementID, err,
			)
		}
		observationRef, err := observationReference(expectation, facts)
		if err != nil {
			return nil, err
		}
		receipt, err := applyevidence.SignReceipt(applyevidence.ReceiptInput{
			Request:        collection.Request,
			Expectation:    expectation,
			ManifestHash:   collection.ManifestHash,
			Executor:       collection.Executor,
			Producer:       producer,
			ObservationRef: observationRef,
			ObservedAt:     observedAt,
			ValidUntil:     validUntil,
		}, c.key.private)
		if err != nil {
			return nil, fmt.Errorf("localevidence: sign receipt %q: %w", expectation.ReceiptID, err)
		}
		receipts = append(receipts, receipt)
	}

	bundle, err := applyevidence.SealBundle(
		collection.Request, collection.ManifestHash, collection.Executor, receipts,
	)
	if err != nil {
		return nil, fmt.Errorf("localevidence: seal evidence bundle: %w", err)
	}
	canonical, err := applyevidence.MarshalCanonical(bundle)
	if err != nil {
		return nil, fmt.Errorf("localevidence: marshal evidence bundle: %w", err)
	}
	return canonical, nil
}

// observationPrefixes mirrors the typed reference scheme applyevidence
// enforces per requirement kind. A kind absent here cannot produce a valid
// receipt, so the collector reports it instead of emitting a rejected value.
var observationPrefixes = map[string]string{
	"workload":       "workload-observation",
	"secret":         "secret-materialization",
	"runtime":        "runtime-observation",
	"host":           "host-observation",
	"provider-owner": "provider-owner-observation",
	"evidence":       "evidence-observation",
	"health":         "health-observation",
}

// observationReference digests the gathered facts into the typed reference the
// receipt carries. The digest binds the expectation identity as well as the
// facts, so an observation cannot be replayed against a different requirement.
func observationReference(expectation applyevidence.Expectation, facts map[string]string) (string, error) {
	prefix, known := observationPrefixes[expectation.RequirementKind]
	if !known {
		return "", fmt.Errorf("localevidence: no typed observation scheme for requirement kind %q", expectation.RequirementKind)
	}
	if len(facts) == 0 {
		return "", fmt.Errorf(
			"localevidence: observer returned no facts for %s/%s; refusing to sign an empty observation",
			expectation.RequirementKind, expectation.RequirementID,
		)
	}
	keys := make([]string, 0, len(facts))
	for key := range facts {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	digest := sha256.New()
	// Bind the observation to the exact expectation it answers.
	fmt.Fprintf(digest, "%s\n%s\n%s\n", expectation.RequirementKind, expectation.RequirementID, expectation.RequirementHash)
	for _, key := range keys {
		fmt.Fprintf(digest, "%s=%s\n", key, facts[key])
	}
	return prefix + "://sha256/" + hex.EncodeToString(digest.Sum(nil)), nil
}
