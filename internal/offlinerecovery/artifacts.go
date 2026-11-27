package offlinerecovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

func verifyArtifacts(cacheRoot string, plan Plan, now time.Time) ([]ArtifactEvidence, map[string][]byte, error) {
	root, err := confinedfs.Open(cacheRoot)
	if err != nil {
		return nil, nil, deny("artifact_cache_unavailable", "the bounded local artifact cache cannot be opened", "restore the existing local cache root; offline recovery never downloads a replacement")
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return nil, nil, deny("artifact_cache_unavailable", "the bounded local artifact cache cannot be read stably", "restore the existing local cache root before recovery")
	}
	defer func() { _ = transaction.Close() }()

	evidence := make([]ArtifactEvidence, 0, len(plan.Artifacts))
	content := make(map[string][]byte, len(plan.Artifacts))
	seenFiles := make([]os.FileInfo, 0, len(plan.Artifacts))
	var total int64
	for _, artifact := range plan.Artifacts {
		before, err := transaction.Lstat(artifact.RelativePath)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, deny("artifact_missing", fmt.Sprintf("cached artifact %q is absent", artifact.ID), "restore the exact cached artifact from existing local custody; do not fetch it from the WAN")
		}
		if err != nil {
			return nil, nil, deny("artifact_cache_invalid", fmt.Sprintf("cached artifact %q is not a safe plain file", artifact.ID), "repair the local cache entry without following links or replacing authority")
		}
		if before.Size() != artifact.SizeBytes {
			return nil, nil, deny("artifact_size_mismatch", fmt.Sprintf("cached artifact %q does not have its exact authorized size", artifact.ID), "restore the exact cached artifact bytes and obtain a new plan if the authorized size changed")
		}
		for _, previous := range seenFiles {
			if os.SameFile(previous, before) {
				return nil, nil, deny("artifact_alias", fmt.Sprintf("cached artifact %q aliases another plan artifact", artifact.ID), "store each authorized artifact as one distinct plain cache file")
			}
		}
		seenFiles = append(seenFiles, before)
		raw, info, err := transaction.ReadStableBounded(artifact.RelativePath, artifact.SizeBytes)
		if err != nil {
			return nil, nil, deny("artifact_unstable", fmt.Sprintf("cached artifact %q changed or exceeded its bound while being read", artifact.ID), "restore a stable exact cache copy and retry admission before any effect")
		}
		if info.Size() != artifact.SizeBytes || int64(len(raw)) != artifact.SizeBytes {
			return nil, nil, deny("artifact_size_mismatch", fmt.Sprintf("cached artifact %q changed size during verification", artifact.ID), "restore the exact cached artifact bytes before recovery")
		}
		sum := sha256.Sum256(raw)
		actual := "sha256:" + hex.EncodeToString(sum[:])
		if actual != artifact.SHA256 {
			return nil, nil, deny("artifact_digest_mismatch", fmt.Sprintf("cached artifact %q does not match its exact authorized SHA-256 digest", artifact.ID), "restore the exact cached artifact bytes; never authorize a substituted digest")
		}
		observedAt := info.ModTime().UTC()
		if observedAt.IsZero() || observedAt.After(now.UTC().Add(MaxClockSkew)) {
			return nil, nil, deny("artifact_clock_untrusted", fmt.Sprintf("cached artifact %q has an untrusted observation time", artifact.ID), "repair the local clock or cache metadata, then repeat admission before any effect")
		}
		age := now.UTC().Sub(observedAt)
		if age < 0 {
			age = 0
		}
		evidence = append(evidence, ArtifactEvidence{
			ID: artifact.ID, Version: artifact.Version, RelativePath: artifact.RelativePath,
			SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes,
			CacheObservedAt: observedAt, EvidenceAgeSeconds: age.Seconds(),
		})
		content[artifact.ID] = append([]byte(nil), raw...)
		total += artifact.SizeBytes
		if total > MaxTotalArtifactSize {
			return nil, nil, deny("artifacts_too_large", "the verified cache exceeds the total artifact bound", "split recovery into independently authorized bounded plans")
		}
	}
	return evidence, content, nil
}

func materializeArtifact(ctx context.Context, workspaceRoot string, step Step, artifact Artifact, content []byte) (StepResult, error) {
	if err := ctx.Err(); err != nil {
		return StepResult{Effect: EffectNotApplied}, err
	}
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return StepResult{Effect: EffectNotApplied}, err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return StepResult{Effect: EffectNotApplied}, err
	}
	if err := transaction.MkdirAll(path.Dir(step.Destination), 0o700); err != nil {
		_ = transaction.Close()
		return StepResult{Effect: EffectNotApplied}, err
	}
	if err := transaction.Close(); err != nil {
		return StepResult{Effect: EffectNotApplied}, err
	}
	view, err := root.View(path.Dir(step.Destination))
	if err != nil {
		return StepResult{Effect: EffectNotApplied}, err
	}
	result, writeErr := view.WriteAtomic0600(path.Base(step.Destination), content)
	if writeErr != nil {
		if result.Installed {
			return StepResult{Effect: EffectUnknown}, writeErr
		}
		return StepResult{Effect: EffectNotApplied}, writeErr
	}
	if !result.Installed {
		return StepResult{Effect: EffectNotApplied}, errors.New("materialize did not install the verified artifact")
	}

	readTransaction, err := root.BeginTransaction()
	if err != nil {
		return StepResult{Effect: EffectUnknown}, err
	}
	defer func() { _ = readTransaction.Close() }()
	readback, info, err := readTransaction.ReadStableBounded(step.Destination, artifact.SizeBytes)
	if err != nil || info.Size() != artifact.SizeBytes || !bytes.Equal(readback, content) {
		if err != nil {
			return StepResult{Effect: EffectUnknown}, err
		}
		return StepResult{Effect: EffectUnknown}, errors.New("materialized artifact readback differs from the verified cache bytes")
	}
	observation, err := canonicalJSON(struct {
		StepID         string `json:"stepId"`
		ArtifactID     string `json:"artifactId"`
		Destination    string `json:"destination"`
		ArtifactSHA256 string `json:"artifactSha256"`
	}{step.ID, step.ArtifactID, step.Destination, artifact.SHA256})
	if err != nil {
		return StepResult{Effect: EffectUnknown}, err
	}
	return StepResult{Effect: EffectApplied, ObservationDigest: sha256Digest(observation)}, nil
}
