package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const (
	requestSchema    = "stackkit.standard-execution-request/v1"
	responseSchema   = "stackkit.standard-execution-result/v1"
	configSchema     = "stackkit.modern-runtime-process-config/v1"
	transcriptSchema = "stackkit.modern-runtime-process-transcript/v1"
	maxInput         = 16 << 20
)

type envelope struct {
	SchemaVersion string                           `json:"schemaVersion"`
	ChannelRef    string                           `json:"channelRef"`
	Request       runtimeexecutor.ExecutionRequest `json:"request"`
}

type response struct {
	SchemaVersion string                           `json:"schemaVersion"`
	ChannelRef    string                           `json:"channelRef"`
	Outcome       runtimeexecutor.ExecutionOutcome `json:"outcome"`
}

type source struct {
	Tag    string `json:"tag"`
	Commit string `json:"commit"`
	Digest string `json:"digest"`
}

type channel struct {
	SiteRef string `json:"siteRef"`
	NodeRef string `json:"nodeRef"`
	URL     string `json:"url"`
}

type config struct {
	APIVersion   string             `json:"apiVersion"`
	Source       source             `json:"source"`
	Channels     map[string]channel `json:"channels"`
	EvidenceRoot string             `json:"evidenceRoot"`
}

type event struct {
	ChannelRef    string                           `json:"channelRef"`
	RequestDigest string                           `json:"requestDigest"`
	Runtime       []runtimeexecutor.RuntimeOutcome `json:"runtime"`
	Health        []runtimeexecutor.HealthOutcome  `json:"health"`
	ObservedAt    string                           `json:"observedAt"`
	EventDigest   string                           `json:"eventDigest"`
}

type transcript struct {
	APIVersion  string  `json:"apiVersion"`
	Source      source  `json:"source"`
	Events      []event `json:"events"`
	FinalDigest string  `json:"finalDigest"`
}

func main() {
	if err := run(); err != nil {
		_ = atomicWrite(
			filepath.Join(".stackkit", "evidence", "modern-runtime-process", "last-error.log"),
			[]byte(err.Error()+"\n"),
			0o600,
		)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, maxInput+1))
	if err != nil || len(raw) == 0 || len(raw) > maxInput {
		return errors.New("generic runtime request is empty or exceeds the closed limit")
	}
	var request envelope
	if err := decode(raw, &request); err != nil {
		return fmt.Errorf("decode generic runtime request: %w", err)
	}
	if request.SchemaVersion != requestSchema || request.ChannelRef == "" {
		return errors.New("unsupported generic runtime request envelope")
	}
	if err := request.Request.Validate(); err != nil {
		return fmt.Errorf("validate sealed execution request: %w", err)
	}

	var cfg config
	if err := readJSON(filepath.Join(".stackkit", "custody", "modern-runtime-process.json"), &cfg); err != nil {
		return err
	}
	binding, exists := cfg.Channels[request.ChannelRef]
	if cfg.APIVersion != configSchema || !exists || binding.SiteRef == "" || binding.NodeRef == "" ||
		!filepath.IsAbs(cfg.EvidenceRoot) {
		return errors.New("generic runtime process configuration is incomplete")
	}
	for _, target := range request.Request.RuntimeTargets {
		if target.ExecutionChannelRef != request.ChannelRef ||
			len(target.SiteRefs) != 1 || target.SiteRefs[0] != binding.SiteRef ||
			len(target.NodeRefs) != 1 || target.NodeRefs[0] != binding.NodeRef {
			return fmt.Errorf("runtime target %q escaped channel custody", target.RequirementID)
		}
	}
	for _, target := range request.Request.HealthTargets {
		if len(target.SiteRefs) != 1 || target.SiteRefs[0] != binding.SiteRef ||
			len(target.NodeRefs) != 1 || target.NodeRefs[0] != binding.NodeRef {
			return fmt.Errorf("health target %q escaped channel custody", target.RequirementID)
		}
	}

	artifacts := make(map[string]runtimeexecutor.Artifact, len(request.Request.Artifacts))
	for _, artifact := range request.Request.Artifacts {
		if sha(artifact.Content) != artifact.Digest {
			return fmt.Errorf("artifact %q digest does not verify", artifact.ID)
		}
		artifacts[artifact.ID] = artifact
	}

	outcome := runtimeexecutor.ExecutionOutcome{}
	for _, target := range request.Request.RuntimeTargets {
		applied := make([]string, 0, len(target.ArtifactRefs))
		for _, ref := range target.ArtifactRefs {
			artifact, ok := artifacts[ref]
			if !ok {
				return fmt.Errorf("runtime target %q references missing artifact %q", target.RequirementID, ref)
			}
			if !artifactBoundToRuntimeInstance(artifact, target) {
				return fmt.Errorf("artifact %q escaped runtime authority", ref)
			}
			path := filepath.Join(cfg.EvidenceRoot, "applied", safe(request.ChannelRef), safe(target.RequirementID), safe(ref))
			if err := atomicWrite(path, artifact.Content, 0o600); err != nil {
				return err
			}
			applied = append(applied, artifact.Digest)
		}
		sort.Strings(applied)
		observation, _ := json.Marshal(struct {
			RequirementID string   `json:"requirementId"`
			OwnerRef      string   `json:"ownerRef"`
			ChannelRef    string   `json:"channelRef"`
			Artifacts     []string `json:"artifacts"`
		}{target.RequirementID, target.OwnerRef, request.ChannelRef, applied})
		outcome.Runtime = append(outcome.Runtime, runtimeexecutor.RuntimeOutcome{
			RequirementID: target.RequirementID, InstanceRef: target.InstanceRef,
			Status:            runtimeexecutor.RuntimeStatusApplied,
			ObservationRef:    "runtime-observation://modern-process/" + safe(target.RequirementID),
			ObservationDigest: sha(observation),
		})
	}
	for _, target := range request.Request.HealthTargets {
		if binding.URL == "" {
			return fmt.Errorf("health target %q has no channel-local probe URL", target.RequirementID)
		}
		client := &http.Client{Timeout: 5 * time.Second}
		res, err := client.Get(strings.TrimRight(binding.URL, "/") + "/healthz")
		if err != nil {
			return fmt.Errorf("probe health target %q: %w", target.RequirementID, err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("health target %q returned %d", target.RequirementID, res.StatusCode)
		}
		observation := []byte(fmt.Sprintf("%s\x00%s\x00%d", request.ChannelRef, target.RequirementID, res.StatusCode))
		outcome.Health = append(outcome.Health, runtimeexecutor.HealthOutcome{
			RequirementID: target.RequirementID, TargetRef: target.TargetRef,
			Status:            runtimeexecutor.HealthStatusHealthy,
			ObservationRef:    "health-observation://modern-process/" + safe(target.RequirementID),
			ObservationDigest: sha(observation),
		})
	}
	if err := appendTranscript(cfg, request, outcome); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(response{
		SchemaVersion: responseSchema, ChannelRef: request.ChannelRef, Outcome: outcome,
	})
}

func artifactBoundToRuntimeInstance(artifact runtimeexecutor.Artifact, target runtimeexecutor.RuntimeTarget) bool {
	return artifact.OwnerKind == "render-instance" &&
		artifact.OwnerRef == target.InstanceRef &&
		artifact.OwnerContractHash == target.UnitContractHash &&
		artifact.ProviderRef == target.ProviderRef &&
		artifact.ProviderContractHash == target.ProviderContractHash &&
		artifact.ModuleRef == target.ModuleRef &&
		artifact.ModuleContractHash == target.ModuleContractHash &&
		artifact.UnitRef == target.UnitRef &&
		artifact.UnitContractHash == target.UnitContractHash &&
		artifact.InstanceRef == target.InstanceRef &&
		slices.Equal(artifact.SiteRefs, target.SiteRefs) &&
		slices.Equal(artifact.NodeRefs, target.NodeRefs)
}

func appendTranscript(cfg config, request envelope, outcome runtimeexecutor.ExecutionOutcome) error {
	path := filepath.Join(cfg.EvidenceRoot, "transcript.json")
	lock := path + ".lock"
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := os.Mkdir(lock, 0o700); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("generic runtime transcript lock timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer os.Remove(lock)
	var value transcript
	if err := readJSON(path, &value); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if value.APIVersion == "" {
		value = transcript{APIVersion: transcriptSchema, Source: cfg.Source, Events: []event{}}
	}
	if value.APIVersion != transcriptSchema || value.Source != cfg.Source {
		return errors.New("generic runtime transcript authority differs")
	}
	next := event{
		ChannelRef: request.ChannelRef, RequestDigest: request.Request.RequestDigest,
		Runtime: outcome.Runtime, Health: outcome.Health,
		ObservedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	canonical, _ := json.Marshal(next)
	next.EventDigest = sha(canonical)
	value.Events = append(value.Events, next)
	value.FinalDigest = next.EventDigest
	raw, _ := json.Marshal(value)
	return atomicWrite(path, append(raw, '\n'), 0o600)
}

func decode(raw []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("document contains trailing JSON")
	}
	return nil
}

func readJSON(path string, target any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return decode(raw, target)
}

func atomicWrite(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp := path + fmt.Sprintf(".tmp-%d", os.Getpid())
	if err := os.WriteFile(temp, content, mode); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func sha(raw []byte) string {
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func safe(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, value)
}
