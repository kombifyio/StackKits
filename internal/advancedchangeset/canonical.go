package advancedchangeset

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

type identityPayload struct {
	SchemaVersion         string           `json:"schemaVersion"`
	CapabilityID          string           `json:"capabilityId"`
	CapabilitySHA256      string           `json:"capabilitySha256"`
	KeyID                 string           `json:"keyId"`
	StackID               string           `json:"stackId"`
	OwnerRef              string           `json:"ownerRef"`
	UIManagerRef          string           `json:"uiManagerRef"`
	RILRef                string           `json:"rilRef"`
	GenerationTarget      string           `json:"generationTarget"`
	CreatedAt             string           `json:"createdAt"`
	ExpiresAt             string           `json:"expiresAt"`
	CapabilityExpiresAt   string           `json:"capabilityExpiresAt"`
	BaselinePlanHash      string           `json:"baselinePlanHash"`
	CandidatePlanHash     string           `json:"candidatePlanHash"`
	BaselineRenderSHA256  string           `json:"baselineRenderSha256"`
	CandidateRenderSHA256 string           `json:"candidateRenderSha256"`
	Changes               []ArtifactChange `json:"changes"`
}

type unsignedRecord struct {
	SchemaVersion         string           `json:"schemaVersion"`
	ChangeSetID           string           `json:"changeSetId"`
	CapabilityID          string           `json:"capabilityId"`
	CapabilitySHA256      string           `json:"capabilitySha256"`
	KeyID                 string           `json:"keyId"`
	StackID               string           `json:"stackId"`
	OwnerRef              string           `json:"ownerRef"`
	UIManagerRef          string           `json:"uiManagerRef"`
	RILRef                string           `json:"rilRef"`
	GenerationTarget      string           `json:"generationTarget"`
	CreatedAt             string           `json:"createdAt"`
	ExpiresAt             string           `json:"expiresAt"`
	CapabilityExpiresAt   string           `json:"capabilityExpiresAt"`
	BaselinePlanHash      string           `json:"baselinePlanHash"`
	CandidatePlanHash     string           `json:"candidatePlanHash"`
	BaselineRenderSHA256  string           `json:"baselineRenderSha256"`
	CandidateRenderSHA256 string           `json:"candidateRenderSha256"`
	Changes               []ArtifactChange `json:"changes"`
}

func identityOf(record Record) identityPayload {
	return identityPayload{
		SchemaVersion: record.SchemaVersion, CapabilityID: record.CapabilityID,
		CapabilitySHA256: record.CapabilitySHA256, KeyID: record.KeyID,
		StackID: record.StackID, OwnerRef: record.OwnerRef,
		UIManagerRef: record.UIManagerRef, RILRef: record.RILRef,
		GenerationTarget: record.GenerationTarget, CreatedAt: record.CreatedAt,
		ExpiresAt: record.ExpiresAt, CapabilityExpiresAt: record.CapabilityExpiresAt,
		BaselinePlanHash: record.BaselinePlanHash, CandidatePlanHash: record.CandidatePlanHash,
		BaselineRenderSHA256:  record.BaselineRenderSHA256,
		CandidateRenderSHA256: record.CandidateRenderSHA256, Changes: cloneChanges(record.Changes),
	}
}

func unsignedOf(record Record) unsignedRecord {
	return unsignedRecord{
		SchemaVersion: record.SchemaVersion, ChangeSetID: record.ChangeSetID,
		CapabilityID: record.CapabilityID, CapabilitySHA256: record.CapabilitySHA256,
		KeyID: record.KeyID, StackID: record.StackID, OwnerRef: record.OwnerRef,
		UIManagerRef: record.UIManagerRef, RILRef: record.RILRef,
		GenerationTarget: record.GenerationTarget,
		CreatedAt:        record.CreatedAt, ExpiresAt: record.ExpiresAt,
		CapabilityExpiresAt: record.CapabilityExpiresAt,
		BaselinePlanHash:    record.BaselinePlanHash, CandidatePlanHash: record.CandidatePlanHash,
		BaselineRenderSHA256:  record.BaselineRenderSHA256,
		CandidateRenderSHA256: record.CandidateRenderSHA256, Changes: cloneChanges(record.Changes),
	}
}

func changeSetID(record Record) (string, error) {
	canonical, err := resolvedplan.CanonicalJSON(identityOf(record))
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// UnsignedCanonical returns the exact bytes authenticated by OwnerSignature.
func (record Record) UnsignedCanonical() ([]byte, error) {
	return resolvedplan.CanonicalJSON(unsignedOf(record))
}

// MarshalCanonical returns the strict persisted representation.
func (record Record) MarshalCanonical() ([]byte, error) {
	return resolvedplan.CanonicalJSON(record)
}

func cloneChanges(changes []ArtifactChange) []ArtifactChange {
	return append([]ArtifactChange(nil), changes...)
}
