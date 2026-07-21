package runtimeexecutor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// ComputeArtifactSetHash validates artifacts and returns the canonical digest
// of their ordered metadata. Content is bound through each artifact digest.
func ComputeArtifactSetHash(input []Artifact) (string, error) {
	artifacts := cloneArtifacts(input)
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].ID < artifacts[j].ID })
	for i := range artifacts {
		sort.Strings(artifacts[i].SiteRefs)
		sort.Strings(artifacts[i].NodeRefs)
	}
	if err := validateArtifacts(artifacts); err != nil {
		return "", err
	}
	type identity struct {
		ID, Kind, Format, Mode                                                string
		OwnerKind, OwnerRef, OwnerContractHash                                string
		ProviderRef, ProviderContractHash                                     string
		ModuleRef, ModuleContractHash, UnitRef, UnitContractHash, InstanceRef string
		OutputRef                                                             string
		SiteRefs, NodeRefs                                                    []string
		Digest                                                                string
	}
	identities := make([]identity, len(artifacts))
	for i, artifact := range artifacts {
		identities[i] = identity{
			ID: artifact.ID, Kind: artifact.Kind, Format: artifact.Format, Mode: artifact.Mode,
			OwnerKind: artifact.OwnerKind, OwnerRef: artifact.OwnerRef, OwnerContractHash: artifact.OwnerContractHash,
			ProviderRef: artifact.ProviderRef, ProviderContractHash: artifact.ProviderContractHash,
			ModuleRef: artifact.ModuleRef, ModuleContractHash: artifact.ModuleContractHash,
			UnitRef: artifact.UnitRef, UnitContractHash: artifact.UnitContractHash, InstanceRef: artifact.InstanceRef,
			OutputRef: artifact.OutputRef, SiteRefs: append([]string(nil), artifact.SiteRefs...),
			NodeRefs: append([]string(nil), artifact.NodeRefs...), Digest: artifact.Digest,
		}
	}
	data, err := canonicalJSON(identities)
	if err != nil {
		return "", wrapError(ErrorInvalidRequest, "artifacts", "canonicalize artifact set", err)
	}
	return hashBytes(data), nil
}

func computeRequestDigest(request ExecutionRequest) (string, error) {
	copy := CloneExecutionRequest(request)
	copy.RequestDigest = ""
	data, err := canonicalJSON(copy)
	if err != nil {
		return "", wrapError(ErrorInvalidRequest, "request", "canonicalize request", err)
	}
	return hashBytes(data), nil
}

func computeResultDigest(result ExecutionResult) (string, error) {
	copy := CloneExecutionResult(result)
	copy.ResultDigest = ""
	data, err := canonicalJSON(copy)
	if err != nil {
		return "", wrapError(ErrorInvalidResult, "result", "canonicalize result", err)
	}
	return hashBytes(data), nil
}

func canonicalJSON(value any) ([]byte, error) { return json.Marshal(value) }

func hashBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}
