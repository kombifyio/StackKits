package applyevidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// NewCollectionRequest binds an exact Apply evidence request to the generated
// manifest, executor identity, and one caller-captured UTC evaluation instant.
// The request is value-only and defensively copies its expectation set.
func NewCollectionRequest(request Request, manifestHash string, executor ExecutorIdentity, evaluatedAt time.Time) (CollectionRequest, error) {
	if evaluatedAt.IsZero() || evaluatedAt.Location() != time.UTC {
		return CollectionRequest{}, errors.New("applyevidence: collection evaluatedAt must be one canonical UTC instant")
	}
	collection := CollectionRequest{
		APIVersion:   CollectionRequestAPIVersion,
		Kind:         CollectionRequestKind,
		Request:      cloneRequest(request),
		ManifestHash: manifestHash,
		Executor:     executor,
		EvaluatedAt:  evaluatedAt.Round(0),
	}
	digest, err := collectionDigest(collection)
	if err != nil {
		return CollectionRequest{}, err
	}
	collection.CollectionDigest = digest
	if err := ValidateCollectionRequest(collection); err != nil {
		return CollectionRequest{}, err
	}
	return CloneCollectionRequest(collection), nil
}

// ValidateCollectionRequest verifies the complete canonical collection
// closure. Freshness and authenticated transport remain consumer policy; an
// evaluatedAt value supplied by an unauthenticated caller is never proof of
// current time.
func ValidateCollectionRequest(collection CollectionRequest) error {
	if collection.APIVersion != CollectionRequestAPIVersion || collection.Kind != CollectionRequestKind {
		return errors.New("applyevidence: invalid collection request type")
	}
	if err := validateRequest(collection.Request); err != nil {
		return err
	}
	if !digestPattern.MatchString(collection.ManifestHash) || !validExecutor(collection.Executor) {
		return errors.New("applyevidence: collection request has invalid manifest or executor authority")
	}
	if collection.EvaluatedAt.IsZero() || collection.EvaluatedAt.Location() != time.UTC ||
		collection.EvaluatedAt != collection.EvaluatedAt.Round(0) {
		return errors.New("applyevidence: collection evaluatedAt must be canonical UTC without monotonic state")
	}
	want, err := collectionDigest(collection)
	if err != nil {
		return err
	}
	if !digestPattern.MatchString(collection.CollectionDigest) || collection.CollectionDigest != want {
		return errors.New("applyevidence: collection request digest mismatch")
	}
	return nil
}

// MarshalCollectionRequest returns the exact canonical JSON wire bytes.
func MarshalCollectionRequest(collection CollectionRequest) ([]byte, error) {
	if err := ValidateCollectionRequest(collection); err != nil {
		return nil, err
	}
	return canonicalJSON(collection)
}

// DecodeCollectionRequest strictly decodes one canonical bounded request.
func DecodeCollectionRequest(data []byte) (CollectionRequest, error) {
	if len(data) == 0 || len(data) > CollectionRequestMaxBytes {
		return CollectionRequest{}, fmt.Errorf("applyevidence: collection request size must be 1..%d bytes", CollectionRequestMaxBytes)
	}
	var collection CollectionRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&collection); err != nil {
		return CollectionRequest{}, fmt.Errorf("applyevidence: decode collection request: %w", err)
	}
	if err := requireEOF(decoder); err != nil {
		return CollectionRequest{}, err
	}
	if err := ValidateCollectionRequest(collection); err != nil {
		return CollectionRequest{}, err
	}
	canonical, err := canonicalJSON(collection)
	if err != nil {
		return CollectionRequest{}, err
	}
	if !bytes.Equal(data, canonical) {
		return CollectionRequest{}, errors.New("applyevidence: collection request must be canonical JSON")
	}
	return CloneCollectionRequest(collection), nil
}

// CloneCollectionRequest returns a defensive value copy.
func CloneCollectionRequest(collection CollectionRequest) CollectionRequest {
	result := collection
	result.Request = cloneRequest(collection.Request)
	return result
}

func cloneRequest(request Request) Request {
	result := request
	result.Expectations = append([]Expectation(nil), request.Expectations...)
	return result
}

func collectionDigest(collection CollectionRequest) (string, error) {
	unsigned := CloneCollectionRequest(collection)
	unsigned.CollectionDigest = ""
	canonical, err := canonicalJSON(unsigned)
	if err != nil {
		return "", fmt.Errorf("applyevidence: canonicalize collection request: %w", err)
	}
	return hash(canonical), nil
}
