// Package runtimeobservation projects the CUE-owned provider-neutral runtime
// observation contract onto the standalone Go CLI and MCP surfaces.
package runtimeobservation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

const SchemaVersionV2 = "stackkit.runtime-observation/v2"

type Phase string

const (
	PhaseApply  Phase = "apply"
	PhaseStatus Phase = "status"
	PhaseVerify Phase = "verify"
)

type Source string

const (
	SourceLocalRuntime          Source = "local-runtime"
	SourceStandardProcess       Source = "standard-process"
	SourceVerifiedApplyEvidence Source = "verified-apply-evidence"
)

type ServiceStatus string

const (
	StatusRunning  ServiceStatus = "running"
	StatusStopped  ServiceStatus = "stopped"
	StatusStarting ServiceStatus = "starting"
	StatusError    ServiceStatus = "error"
	StatusUnknown  ServiceStatus = "unknown"
)

type HealthStatus string

const (
	HealthHealthy     HealthStatus = "healthy"
	HealthUnhealthy   HealthStatus = "unhealthy"
	HealthStarting    HealthStatus = "starting"
	HealthNotRequired HealthStatus = "not-required"
	HealthUnknown     HealthStatus = "unknown"
)

type Identity struct {
	StackID             string `json:"stackId"`
	PlanHash            string `json:"planHash"`
	ApplyResultHash     string `json:"applyResultHash"`
	RunID               string `json:"runId"`
	SiteRef             string `json:"siteRef"`
	NodeRef             string `json:"nodeRef"`
	ExecutionChannelRef string `json:"executionChannelRef"`
}

type Service struct {
	Ref       string        `json:"ref"`
	URL       string        `json:"url,omitempty"`
	Status    ServiceStatus `json:"status"`
	Health    HealthStatus  `json:"health"`
	HealthRef string        `json:"healthRef,omitempty"`
}

type RuntimeEvidence struct {
	RequirementID       string `json:"requirementId"`
	InstanceRef         string `json:"instanceRef"`
	Status              string `json:"status"`
	ObservationRef      string `json:"observationRef"`
	ObservationDigest   string `json:"observationDigest"`
	SiteRef             string `json:"siteRef"`
	NodeRef             string `json:"nodeRef"`
	ExecutionChannelRef string `json:"executionChannelRef"`
}

type HealthEvidence struct {
	RequirementID     string `json:"requirementId"`
	TargetRef         string `json:"targetRef"`
	SourceRef         string `json:"sourceRef,omitempty"`
	Status            string `json:"status"`
	ObservationRef    string `json:"observationRef"`
	ObservationDigest string `json:"observationDigest"`
	SiteRef           string `json:"siteRef"`
	NodeRef           string `json:"nodeRef"`
}

type EvidenceLink struct {
	Kind   string `json:"kind"`
	Ref    string `json:"ref"`
	Digest string `json:"digest,omitempty"`
}

type Observation struct {
	SchemaVersion string            `json:"schemaVersion"`
	Phase         Phase             `json:"phase"`
	Source        Source            `json:"source"`
	ObservedAt    time.Time         `json:"observedAt"`
	Live          bool              `json:"live"`
	Identity      Identity          `json:"identity"`
	Services      []Service         `json:"services"`
	Runtime       []RuntimeEvidence `json:"runtime"`
	Health        []HealthEvidence  `json:"health"`
	EvidenceLinks []EvidenceLink    `json:"evidenceLinks"`
}

func (o Observation) Validate() error {
	if o.SchemaVersion != SchemaVersionV2 {
		return fmt.Errorf("runtime observation schemaVersion must be %q", SchemaVersionV2)
	}
	if o.Phase != PhaseApply && o.Phase != PhaseStatus && o.Phase != PhaseVerify {
		return errors.New("runtime observation phase is unsupported")
	}
	if o.Source != SourceLocalRuntime && o.Source != SourceStandardProcess && o.Source != SourceVerifiedApplyEvidence {
		return errors.New("runtime observation source is unsupported")
	}
	if o.ObservedAt.IsZero() || o.ObservedAt.Location() != time.UTC {
		return errors.New("runtime observation observedAt must be an exact UTC instant")
	}
	identity := o.Identity
	for field, value := range map[string]string{
		"stackId": identity.StackID, "runId": identity.RunID, "siteRef": identity.SiteRef,
		"nodeRef": identity.NodeRef, "executionChannelRef": identity.ExecutionChannelRef,
	} {
		if !canonicalNonEmpty(value) {
			return fmt.Errorf("runtime observation identity.%s is required and canonical", field)
		}
	}
	if !validDigest(identity.PlanHash) || !validDigest(identity.ApplyResultHash) {
		return errors.New("runtime observation identity requires exact plan and Apply result digests")
	}
	for index, service := range o.Services {
		if !canonicalNonEmpty(service.Ref) || !validServiceStatus(service.Status) || !validHealthStatus(service.Health) {
			return fmt.Errorf("runtime observation service[%d] is invalid", index)
		}
		if service.URL != "" {
			parsed, err := url.Parse(service.URL)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
				return fmt.Errorf("runtime observation service[%d].url is not a secret-free HTTP(S) URL", index)
			}
		}
	}
	for index, item := range o.Runtime {
		if !canonicalNonEmpty(item.RequirementID) || !canonicalNonEmpty(item.InstanceRef) || !canonicalNonEmpty(item.Status) ||
			!canonicalRef(item.ObservationRef) || !validDigest(item.ObservationDigest) || !canonicalNonEmpty(item.SiteRef) ||
			!canonicalNonEmpty(item.NodeRef) || !canonicalNonEmpty(item.ExecutionChannelRef) {
			return fmt.Errorf("runtime observation runtime[%d] is invalid", index)
		}
	}
	for index, item := range o.Health {
		if !canonicalNonEmpty(item.RequirementID) || !canonicalNonEmpty(item.TargetRef) || !canonicalNonEmpty(item.Status) ||
			!canonicalRef(item.ObservationRef) || !validDigest(item.ObservationDigest) || !canonicalNonEmpty(item.SiteRef) || !canonicalNonEmpty(item.NodeRef) {
			return fmt.Errorf("runtime observation health[%d] is invalid", index)
		}
	}
	for index, link := range o.EvidenceLinks {
		if !canonicalNonEmpty(link.Kind) || !canonicalRef(link.Ref) || (link.Digest != "" && !validDigest(link.Digest)) {
			return fmt.Errorf("runtime observation evidenceLinks[%d] is invalid", index)
		}
	}
	return nil
}

func (o Observation) Canonical() ([]byte, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	clone := o
	clone.Services = append([]Service{}, o.Services...)
	clone.Runtime = append([]RuntimeEvidence{}, o.Runtime...)
	clone.Health = append([]HealthEvidence{}, o.Health...)
	clone.EvidenceLinks = append([]EvidenceLink{}, o.EvidenceLinks...)
	sort.Slice(clone.Services, func(i, j int) bool { return clone.Services[i].Ref < clone.Services[j].Ref })
	sort.Slice(clone.Runtime, func(i, j int) bool { return clone.Runtime[i].RequirementID < clone.Runtime[j].RequirementID })
	sort.Slice(clone.Health, func(i, j int) bool { return clone.Health[i].RequirementID < clone.Health[j].RequirementID })
	sort.Slice(clone.EvidenceLinks, func(i, j int) bool {
		if clone.EvidenceLinks[i].Kind != clone.EvidenceLinks[j].Kind {
			return clone.EvidenceLinks[i].Kind < clone.EvidenceLinks[j].Kind
		}
		return clone.EvidenceLinks[i].Ref < clone.EvidenceLinks[j].Ref
	})
	return json.Marshal(clone)
}

func (o Observation) Digest() (string, error) {
	canonical, err := o.Canonical()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func canonicalNonEmpty(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && !strings.ContainsAny(value, "\r\n\x00")
}

func canonicalRef(value string) bool {
	return canonicalNonEmpty(value) && !strings.Contains(value, "\\") && !strings.Contains(value, "/../") && !strings.HasPrefix(value, "../")
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func validServiceStatus(value ServiceStatus) bool {
	return value == StatusRunning || value == StatusStopped || value == StatusStarting || value == StatusError || value == StatusUnknown
}

func validHealthStatus(value HealthStatus) bool {
	return value == HealthHealthy || value == HealthUnhealthy || value == HealthStarting || value == HealthNotRequired || value == HealthUnknown
}
