package advancedchangeset

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
)

const maxChanges = 20_000
const GenerationTargetTerramate = "terramate"

var (
	hashPattern       = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	stackIDPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	ownerRefPattern   = regexp.MustCompile(`^owner/local/[0-9a-f]{32}$`)
	capabilityPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

// Create computes a pure diff from two already-authorized RenderResults and
// obtains an owner signature through the injected custody seam.
func Create(request CreateRequest) (Record, error) {
	baselineCanonical, err := request.Baseline.MarshalCanonical()
	if err != nil {
		return Record{}, wrap(ErrInvalid, "baseline", "must be a canonical non-empty RenderResult", err)
	}
	candidateCanonical, err := request.Candidate.MarshalCanonical()
	if err != nil {
		return Record{}, wrap(ErrInvalid, "candidate", "must be a canonical non-empty RenderResult", err)
	}
	record, err := createFromSnapshots(request, renderSnapshot{
		canonical: baselineCanonical, artifacts: request.Baseline.Artifacts(),
	}, renderSnapshot{
		canonical: candidateCanonical, artifacts: request.Candidate.Artifacts(),
	})
	if err != nil {
		return Record{}, err
	}
	return record, nil
}

// RenderSHA256 exposes the exact canonical render identity used by Record.
// Apply uses it to prove that freshly rendered candidate bytes are the bytes
// the Owner approved, before any snapshot or runtime side effect is started.
func RenderSHA256(result architecturev2renderer.RenderResult) (string, error) {
	canonical, err := result.MarshalCanonical()
	if err != nil {
		return "", wrap(ErrInvalid, "renderResult", "cannot canonicalize render result", err)
	}
	return digestBytes(canonical), nil
}

type renderSnapshot struct {
	canonical []byte
	artifacts []architecturev2renderer.Artifact
}

func createFromSnapshots(request CreateRequest, baseline, candidate renderSnapshot) (Record, error) {
	if len(baseline.canonical) == 0 || len(candidate.canonical) == 0 {
		return Record{}, fail(ErrInvalid, "renderResult", "baseline and candidate canonical bytes are required")
	}
	createdAt, err := canonicalTime(request.CreatedAt, "createdAt")
	if err != nil {
		return Record{}, err
	}
	expiresAt, err := canonicalTime(request.ExpiresAt, "expiresAt")
	if err != nil {
		return Record{}, err
	}
	capabilityExpiresAt, err := canonicalTime(request.CapabilityExpiresAt, "capabilityExpiresAt")
	if err != nil {
		return Record{}, err
	}
	changes, err := diffArtifacts(baseline.artifacts, candidate.artifacts)
	if err != nil {
		return Record{}, err
	}
	record := Record{
		SchemaVersion: SchemaVersion, CapabilityID: request.CapabilityID,
		CapabilitySHA256: request.CapabilitySHA256, KeyID: request.KeyID,
		StackID: request.StackID, OwnerRef: request.OwnerRef,
		UIManagerRef: request.UIManagerRef, RILRef: request.RILRef,
		GenerationTarget: GenerationTargetTerramate,
		CreatedAt:        createdAt, ExpiresAt: expiresAt, CapabilityExpiresAt: capabilityExpiresAt,
		BaselinePlanHash: request.BaselinePlanHash, CandidatePlanHash: request.CandidatePlanHash,
		BaselineRenderSHA256:  digestBytes(baseline.canonical),
		CandidateRenderSHA256: digestBytes(candidate.canonical), Changes: changes,
	}
	if err := validateClaims(record); err != nil {
		return Record{}, err
	}
	record.ChangeSetID, err = changeSetID(record)
	if err != nil {
		return Record{}, wrap(ErrInvalid, "changeSetId", "cannot canonicalize identity payload", err)
	}
	if request.Sign == nil || request.VerifyOwnerSignature == nil {
		return Record{}, fail(ErrInvalid, "ownerSignature", "owner signer and verifier are required")
	}
	unsigned, err := record.UnsignedCanonical()
	if err != nil {
		return Record{}, wrap(ErrInvalid, "ownerSignature", "cannot canonicalize unsigned record", err)
	}
	record.OwnerSignature, err = request.Sign(unsigned)
	if err != nil {
		return Record{}, wrap(ErrInvalid, "ownerSignature", "owner signing failed", err)
	}
	if err := validateOwnerSignature(record.OwnerSignature, record.OwnerRef); err != nil {
		return Record{}, err
	}
	if err := request.VerifyOwnerSignature(unsigned, record.OwnerSignature); err != nil {
		return Record{}, wrap(ErrInvalid, "ownerSignature", "does not verify against current owner custody", err)
	}
	return record, nil
}

func diffArtifacts(baseline, candidate []architecturev2renderer.Artifact) ([]ArtifactChange, error) {
	before, err := artifactsByPath(baseline, "baseline")
	if err != nil {
		return nil, err
	}
	after, err := artifactsByPath(candidate, "candidate")
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(before)+len(after))
	for artifactPath := range before {
		paths = append(paths, artifactPath)
	}
	for artifactPath := range after {
		if _, exists := before[artifactPath]; !exists {
			paths = append(paths, artifactPath)
		}
	}
	sort.Strings(paths)
	changes := make([]ArtifactChange, 0, len(paths))
	for _, artifactPath := range paths {
		left, leftExists := before[artifactPath]
		right, rightExists := after[artifactPath]
		switch {
		case !leftExists:
			changes = append(changes, ArtifactChange{
				Path: artifactPath, Status: StatusAdded, AfterSHA256: digestBytes(right.Bytes),
			})
		case !rightExists:
			changes = append(changes, ArtifactChange{
				Path: artifactPath, Status: StatusRemoved, BeforeSHA256: digestBytes(left.Bytes),
			})
		default:
			metadataChanged := artifactMetadata(left) != artifactMetadata(right)
			if !metadataChanged && bytes.Equal(left.Bytes, right.Bytes) {
				continue
			}
			changes = append(changes, ArtifactChange{
				Path: artifactPath, Status: StatusModified,
				BeforeSHA256: digestBytes(left.Bytes), AfterSHA256: digestBytes(right.Bytes),
				MetadataChanged: metadataChanged,
			})
		}
	}
	if len(changes) == 0 {
		return nil, fail(ErrInvalid, "changes", "advanced change set must not be empty")
	}
	if len(changes) > maxChanges {
		return nil, fail(ErrInvalid, "changes", "exceeds 20000 artifact transitions")
	}
	return changes, nil
}

type metadata struct {
	ID, Kind, Format, Mode, ModuleID, UnitID, InstanceID, OutputRef string
}

func artifactMetadata(artifact architecturev2renderer.Artifact) metadata {
	return metadata{
		ID: artifact.ID, Kind: artifact.Kind, Format: artifact.Format, Mode: artifact.Mode,
		ModuleID: artifact.ModuleID, UnitID: artifact.UnitID,
		InstanceID: artifact.InstanceID, OutputRef: artifact.OutputRef,
	}
}

func artifactsByPath(artifacts []architecturev2renderer.Artifact, field string) (map[string]architecturev2renderer.Artifact, error) {
	if len(artifacts) == 0 || len(artifacts) > maxChanges {
		return nil, fail(ErrInvalid, field, "must contain 1 to 20000 artifacts")
	}
	result := make(map[string]architecturev2renderer.Artifact, len(artifacts))
	for index, artifact := range artifacts {
		if !portableArtifactPath(artifact.Path) {
			return nil, fail(ErrInvalid, fmt.Sprintf("%s[%d].path", field, index), "is not a canonical portable path")
		}
		if _, duplicate := result[artifact.Path]; duplicate {
			return nil, fail(ErrInvalid, fmt.Sprintf("%s[%d].path", field, index), "duplicates another artifact path")
		}
		result[artifact.Path] = artifact
	}
	return result, nil
}

func portableArtifactPath(value string) bool {
	return value != "" && len(value) <= 1024 && utf8.ValidString(value) &&
		value == path.Clean(value) && value != "." && !strings.HasPrefix(value, "/") &&
		!strings.Contains(value, `\`) && !strings.Contains(value, ":") &&
		!strings.HasPrefix(value, "../")
}

func canonicalTime(value time.Time, field string) (string, error) {
	if value.IsZero() || value.Location() != time.UTC || value.Nanosecond() != 0 {
		return "", fail(ErrInvalid, field, "must be UTC with exact-second precision")
	}
	return value.Format(time.RFC3339), nil
}

func parseCanonicalTime(value, field string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Nanosecond() != 0 ||
		parsed.Format(time.RFC3339) != value {
		return time.Time{}, fail(ErrInvalid, field, "must be canonical UTC RFC3339 with exact-second precision")
	}
	return parsed, nil
}

func validateClaims(record Record) error {
	if record.SchemaVersion != SchemaVersion {
		return fail(ErrInvalid, "schemaVersion", "is unsupported")
	}
	if !capabilityPattern.MatchString(record.CapabilityID) {
		return fail(ErrInvalid, "capabilityId", "must be a lowercase UUIDv7")
	}
	if !stackIDPattern.MatchString(record.StackID) {
		return fail(ErrInvalid, "stackId", "is not canonical")
	}
	if !ownerRefPattern.MatchString(record.OwnerRef) {
		return fail(ErrInvalid, "ownerRef", "must be a local owner reference")
	}
	if !hashPattern.MatchString(record.CapabilitySHA256) {
		return fail(ErrInvalid, "capabilitySha256", "must be a canonical SHA-256 digest")
	}
	if record.KeyID == "" || len(record.KeyID) > 256 {
		return fail(ErrInvalid, "keyId", "must identify the capability issuer key")
	}
	if err := validateLogicalRef(record.UIManagerRef); err != nil {
		return fail(ErrInvalid, "uiManagerRef", err.Error())
	}
	if err := validateLogicalRef(record.RILRef); err != nil {
		return fail(ErrInvalid, "rilRef", err.Error())
	}
	if record.GenerationTarget != GenerationTargetTerramate {
		return fail(ErrInvalid, "generationTarget", "must be terramate")
	}
	createdAt, err := parseCanonicalTime(record.CreatedAt, "createdAt")
	if err != nil {
		return err
	}
	expiresAt, err := parseCanonicalTime(record.ExpiresAt, "expiresAt")
	if err != nil {
		return err
	}
	capabilityExpiresAt, err := parseCanonicalTime(record.CapabilityExpiresAt, "capabilityExpiresAt")
	if err != nil {
		return err
	}
	if !expiresAt.After(createdAt) || expiresAt.Sub(createdAt) > MaxLifetime {
		return fail(ErrInvalid, "expiresAt", "must be after createdAt and no more than 24 hours later")
	}
	if expiresAt.After(capabilityExpiresAt) {
		return fail(ErrInvalid, "expiresAt", "must not outlive the authorizing capability")
	}
	if !hashPattern.MatchString(record.BaselineRenderSHA256) ||
		!hashPattern.MatchString(record.CandidateRenderSHA256) ||
		record.BaselineRenderSHA256 == record.CandidateRenderSHA256 {
		return fail(ErrInvalid, "renderSha256", "requires distinct canonical baseline and candidate SHA-256 digests")
	}
	if !hashPattern.MatchString(record.BaselinePlanHash) ||
		!hashPattern.MatchString(record.CandidatePlanHash) {
		return fail(ErrInvalid, "planHash", "requires canonical baseline and candidate plan hashes")
	}
	return validateChanges(record.Changes)
}

func validateLogicalRef(value string) error {
	if len(value) == 0 || len(value) > 256 {
		return fmt.Errorf("must contain 1 to 256 bytes")
	}
	for _, character := range []byte(value) {
		if character < 0x21 || character > 0x7e {
			return fmt.Errorf("must be printable ASCII without whitespace")
		}
	}
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, ".") ||
		strings.Contains(value, `\`) || strings.Contains(value, "://") {
		return fmt.Errorf("must be a non-network logical reference")
	}
	return nil
}

func validateChanges(changes []ArtifactChange) error {
	if len(changes) == 0 || len(changes) > maxChanges {
		return fail(ErrInvalid, "changes", "must contain 1 to 20000 transitions")
	}
	for index, change := range changes {
		field := fmt.Sprintf("changes[%d]", index)
		if !portableArtifactPath(change.Path) {
			return fail(ErrInvalid, field+".path", "is not a canonical portable path")
		}
		if index > 0 && changes[index-1].Path >= change.Path {
			return fail(ErrInvalid, "changes", "must be strictly sorted by unique path")
		}
		beforeOK := hashPattern.MatchString(change.BeforeSHA256)
		afterOK := hashPattern.MatchString(change.AfterSHA256)
		switch change.Status {
		case StatusAdded:
			if change.BeforeSHA256 != "" || !afterOK || change.MetadataChanged {
				return fail(ErrInvalid, field, "added requires only afterSha256")
			}
		case StatusRemoved:
			if !beforeOK || change.AfterSHA256 != "" || change.MetadataChanged {
				return fail(ErrInvalid, field, "removed requires only beforeSha256")
			}
		case StatusModified:
			if !beforeOK || !afterOK ||
				(change.BeforeSHA256 == change.AfterSHA256 && !change.MetadataChanged) {
				return fail(ErrInvalid, field, "modified requires changed bytes or governed metadata")
			}
		default:
			return fail(ErrInvalid, field+".status", "is unsupported")
		}
	}
	return nil
}

func validateOwnerSignature(signature OwnerSignature, ownerRef string) error {
	if signature.OwnerRef != ownerRef || signature.KeyID == "" || len(signature.KeyID) > 256 {
		return fail(ErrInvalid, "ownerSignature", "is not bound to the record owner")
	}
	for _, character := range []byte(signature.KeyID) {
		if character < 0x21 || character > 0x7e {
			return fail(ErrInvalid, "ownerSignature.keyId", "must be printable ASCII without whitespace")
		}
	}
	raw, err := base64.RawStdEncoding.Strict().DecodeString(signature.Value)
	if err != nil || len(raw) != 64 || base64.RawStdEncoding.EncodeToString(raw) != signature.Value {
		return fail(ErrInvalid, "ownerSignature.value", "must be an unpadded standard-Base64 Ed25519 signature")
	}
	return nil
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}
