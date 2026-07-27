package advancedchangeset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

const maxDocumentBytes = 16 * 1024 * 1024

var exactFields = map[string]struct{}{
	"schemaVersion": {}, "changeSetId": {}, "capabilityId": {}, "stackId": {},
	"capabilitySha256": {}, "keyId": {}, "uiManagerRef": {}, "rilRef": {},
	"generationTarget": {}, "baselinePlanHash": {}, "candidatePlanHash": {},
	"ownerRef": {}, "createdAt": {}, "expiresAt": {}, "capabilityExpiresAt": {},
	"baselineRenderSha256": {}, "candidateRenderSha256": {}, "changes": {},
	"ownerSignature": {},
}

// Verify rejects non-canonical, malformed, stale, scope-substituted, or
// owner-signature-invalid records without performing side effects.
func Verify(raw []byte, request VerificationRequest) (Record, error) {
	if len(raw) == 0 || len(raw) > maxDocumentBytes || !utf8.Valid(raw) {
		return Record{}, fail(ErrInvalid, "document", "must be 1 to 16777216 bytes of UTF-8 JSON")
	}
	if request.Now.IsZero() || request.Now.Location() != time.UTC || request.Now.Nanosecond() != 0 {
		return Record{}, fail(ErrInvalid, "now", "trusted UTC exact-second time is required")
	}
	if request.VerifyOwnerSignature == nil {
		return Record{}, fail(ErrInvalid, "ownerSignature", "current owner-custody verifier is required")
	}
	record, err := decodeStrict(raw)
	if err != nil {
		return Record{}, err
	}
	if err := validateClaims(record); err != nil {
		return Record{}, err
	}
	expectedID, err := changeSetID(record)
	if err != nil || record.ChangeSetID != expectedID {
		return Record{}, fail(ErrInvalid, "changeSetId", "does not match the canonical unsigned identity payload")
	}
	if request.CapabilityID == "" || record.CapabilityID != request.CapabilityID {
		return Record{}, fail(ErrInvalid, "capabilityId", "does not match the verified capability")
	}
	if request.CapabilitySHA256 == "" || record.CapabilitySHA256 != request.CapabilitySHA256 {
		return Record{}, fail(ErrInvalid, "capabilitySha256", "does not match the verified capability bytes")
	}
	if request.KeyID == "" || record.KeyID != request.KeyID {
		return Record{}, fail(ErrInvalid, "keyId", "does not match the verified capability issuer key")
	}
	if request.StackID == "" || record.StackID != request.StackID {
		return Record{}, fail(ErrInvalid, "stackId", "does not match the local stack")
	}
	if request.OwnerRef == "" || record.OwnerRef != request.OwnerRef {
		return Record{}, fail(ErrInvalid, "ownerRef", "does not match current local owner custody")
	}
	if request.UIManagerRef == "" || record.UIManagerRef != request.UIManagerRef {
		return Record{}, fail(ErrInvalid, "uiManagerRef", "does not match the verified capability")
	}
	if request.RILRef == "" || record.RILRef != request.RILRef {
		return Record{}, fail(ErrInvalid, "rilRef", "does not match the verified capability")
	}
	if request.BaselinePlanHash == "" || record.BaselinePlanHash != request.BaselinePlanHash {
		return Record{}, fail(ErrInvalid, "baselinePlanHash", "does not match the local baseline")
	}
	if request.CandidatePlanHash == "" || record.CandidatePlanHash != request.CandidatePlanHash {
		return Record{}, fail(ErrInvalid, "candidatePlanHash", "does not match the rendered candidate")
	}
	expectedCapabilityExpiry, err := canonicalTime(request.CapabilityExpiresAt, "capabilityExpiresAt")
	if err != nil || record.CapabilityExpiresAt != expectedCapabilityExpiry {
		return Record{}, fail(ErrInvalid, "capabilityExpiresAt", "does not match the verified capability")
	}
	createdAt, _ := parseCanonicalTime(record.CreatedAt, "createdAt")
	expiresAt, _ := parseCanonicalTime(record.ExpiresAt, "expiresAt")
	if request.Now.Before(createdAt) {
		return Record{}, fail(ErrInvalid, "createdAt", "is later than trusted verification time")
	}
	if !expiresAt.After(request.Now) {
		return Record{}, fail(ErrStale, "expiresAt", "change set has expired")
	}
	if err := validateOwnerSignature(record.OwnerSignature, record.OwnerRef); err != nil {
		return Record{}, err
	}
	unsigned, err := record.UnsignedCanonical()
	if err != nil {
		return Record{}, wrap(ErrInvalid, "ownerSignature", "cannot canonicalize unsigned record", err)
	}
	if err := request.VerifyOwnerSignature(unsigned, record.OwnerSignature); err != nil {
		return Record{}, wrap(ErrInvalid, "ownerSignature", "does not verify against current owner custody", err)
	}
	canonical, err := record.MarshalCanonical()
	if err != nil || !bytes.Equal(raw, canonical) {
		return Record{}, fail(ErrInvalid, "document", "must be exact canonical JSON")
	}
	return record, nil
}

func decodeStrict(raw []byte) (Record, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil {
		return Record{}, wrap(ErrInvalid, "document", "cannot decode JSON", err)
	}
	if delimiter, ok := first.(json.Delim); !ok || delimiter != '{' {
		return Record{}, fail(ErrInvalid, "document", "must be one JSON object")
	}
	fields := make(map[string]json.RawMessage, len(exactFields))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return Record{}, wrap(ErrInvalid, "document", "cannot decode field name", err)
		}
		name, ok := token.(string)
		if !ok {
			return Record{}, fail(ErrInvalid, "document", "contains a non-string field name")
		}
		if _, known := exactFields[name]; !known {
			return Record{}, fail(ErrInvalid, name, "unknown field")
		}
		if _, duplicate := fields[name]; duplicate {
			return Record{}, fail(ErrInvalid, name, "duplicate field")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return Record{}, wrap(ErrInvalid, name, "cannot decode value", err)
		}
		fields[name] = value
	}
	if _, err := decoder.Token(); err != nil {
		return Record{}, wrap(ErrInvalid, "document", "object is incomplete", err)
	}
	if len(fields) != len(exactFields) {
		for name := range exactFields {
			if _, exists := fields[name]; !exists {
				return Record{}, fail(ErrInvalid, name, "required field is missing")
			}
		}
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return Record{}, wrap(ErrInvalid, "document", "contains invalid trailing data", err)
		}
		return Record{}, fail(ErrInvalid, "document", fmt.Sprintf("contains trailing token %v", token))
	}

	// Reassemble from the duplicate-checked field map, then use the standard
	// decoder solely for type mapping.
	generic := make(map[string]json.RawMessage, len(fields))
	for name, value := range fields {
		generic[name] = value
	}
	encoded, err := json.Marshal(generic)
	if err != nil {
		return Record{}, wrap(ErrInvalid, "document", "cannot normalize fields", err)
	}
	var record Record
	typeDecoder := json.NewDecoder(bytes.NewReader(encoded))
	typeDecoder.DisallowUnknownFields()
	if err := typeDecoder.Decode(&record); err != nil {
		field := "document"
		if strings.Contains(err.Error(), "changes") {
			field = "changes"
		}
		return Record{}, wrap(ErrInvalid, field, "contains a value with the wrong JSON type", err)
	}
	return record, nil
}
